package caddy

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// CaddyfileDirective is a route-related directive found inside a Caddyfile site block.
type CaddyfileDirective struct {
	Line        int
	Text        string
	Name        string
	Args        []string
	Matcher     string
	MatcherLine int
}

// CaddyfileSourceBlock is a Caddyfile site block correlated with a Caddy source.
type CaddyfileSourceBlock struct {
	Path       string
	Line       int
	EndLine    int
	Address    string
	Addresses  []string
	Directives []CaddyfileDirective
}

// CaddyfileCorrelation contains parsed Caddyfile source-correlation data.
type CaddyfileCorrelation struct {
	Path         string
	Available    bool
	Error        string
	ContentLines []string
	Sources      []CaddyfileSourceBlock
}

type caddyfileStackItem struct {
	typeName    string
	source      *CaddyfileSourceBlock
	matcher     string
	matcherLine int
}

var routeDirectiveNames = map[string]bool{
	"reverse_proxy": true,
	"file_server":   true,
	"respond":       true,
	"redir":         true,
	"rewrite":       true,
	"header":        true,
	"headers":       true,
	"encode":        true,
	"php_fastcgi":   true,
	"request_body":  true,
	"basic_auth":    true,
	"basicauth":     true,
	"templates":     true,
	"try_files":     true,
	"uri":           true,
	"map":           true,
	"root":          true,
}

// LoadCaddyfileCorrelation loads and parses a Caddyfile config for source correlation.
func LoadCaddyfileCorrelation(configPath string, adapter string) CaddyfileCorrelation {
	if configPath == "" {
		return CaddyfileCorrelation{Available: false, Error: "No config path discovered.", Sources: []CaddyfileSourceBlock{}}
	}
	if adapter != "" && adapter != "caddyfile" {
		return CaddyfileCorrelation{Path: configPath, Available: false, Error: fmt.Sprintf("Config adapter is %s; Caddyfile correlation only supports Caddyfile configs for now.", adapter), Sources: []CaddyfileSourceBlock{}}
	}
	if strings.HasSuffix(configPath, ".json") {
		return CaddyfileCorrelation{Path: configPath, Available: false, Error: "Config appears to be JSON; Caddyfile correlation is not available.", Sources: []CaddyfileSourceBlock{}}
	}

	content, err := os.ReadFile(configPath)
	if err != nil {
		return CaddyfileCorrelation{Path: configPath, Available: false, Error: fmt.Sprintf("Could not read %s: %v", configPath, err), Sources: []CaddyfileSourceBlock{}}
	}

	return CaddyfileCorrelation{
		Path:         configPath,
		Available:    true,
		ContentLines: splitCaddyfileLines(string(content)),
		Sources:      parseCaddyfile(configPath, string(content)),
	}
}

// FindCaddyfileSource returns the Caddyfile site block best matching a Caddy source.
func FindCaddyfileSource(correlation CaddyfileCorrelation, source *CaddySource) *CaddyfileSourceBlock {
	if source == nil || !correlation.Available {
		return nil
	}
	sourceHosts := []string{}
	for _, host := range source.Hosts {
		if normalized := normalizeAddressForHost(host); normalized != "" {
			sourceHosts = append(sourceHosts, normalized)
		}
	}
	sourceListeners := []string{}
	for _, listener := range source.Listen {
		if normalized := normalizeAddressForListener(listener); normalized != "" {
			sourceListeners = append(sourceListeners, normalized)
		}
	}

	for index := range correlation.Sources {
		block := &correlation.Sources[index]
		blockHosts := []string{}
		for _, address := range block.Addresses {
			if normalized := normalizeAddressForHost(address); normalized != "" {
				blockHosts = append(blockHosts, normalized)
			}
		}
		for _, host := range sourceHosts {
			if containsString(blockHosts, host) {
				return block
			}
		}
	}

	for index := range correlation.Sources {
		block := &correlation.Sources[index]
		blockListeners := []string{}
		for _, address := range block.Addresses {
			if normalized := normalizeAddressForListener(address); normalized != "" {
				blockListeners = append(blockListeners, normalized)
			}
		}
		for _, listener := range sourceListeners {
			if containsString(blockListeners, listener) {
				return block
			}
		}
	}

	return nil
}

// FindCaddyfileRouteLine returns the likely Caddyfile line for a route.
func FindCaddyfileRouteLine(block *CaddyfileSourceBlock, route CaddyRouteRule) int {
	if directive := findCaddyfileDirective(block, route); directive != nil {
		return directive.Line
	}
	return 0
}

// CaddyfileLocation formats a path:line location for a Caddyfile block or route.
func CaddyfileLocation(correlation CaddyfileCorrelation, block *CaddyfileSourceBlock, line int) string {
	path := correlation.Path
	if path == "" && block != nil {
		path = block.Path
	}
	if path == "" || block == nil {
		return ""
	}
	if line == 0 {
		line = block.Line
	}
	return fmt.Sprintf("%s:%d", path, line)
}

// FormatCaddyfileBlock formats a Caddyfile source block with line numbers.
func FormatCaddyfileBlock(correlation CaddyfileCorrelation, block *CaddyfileSourceBlock) []string {
	if block == nil {
		if correlation.Error != "" {
			return []string{correlation.Error}
		}
		return []string{"No correlated Caddyfile block found."}
	}
	if len(correlation.ContentLines) == 0 {
		return []string{"Caddyfile contents are not available."}
	}
	start := max(1, block.Line)
	end := block.EndLine
	if end == 0 {
		end = block.Line
	}
	end = min(len(correlation.ContentLines), end)
	width := len(fmt.Sprint(end))
	formatted := []string{}
	for index, line := range correlation.ContentLines[start-1 : end] {
		formatted = append(formatted, fmt.Sprintf("%*d │ %s", width, start+index, line))
	}
	return formatted
}

func parseCaddyfile(path string, content string) []CaddyfileSourceBlock {
	sources := []CaddyfileSourceBlock{}
	stack := []caddyfileStackItem{}
	lines := splitCaddyfileLines(content)

	for index, line := range lines {
		lineNumber := index + 1
		cleaned := strings.TrimSpace(stripComment(line))
		if cleaned == "" {
			continue
		}

		closeCount := countChar(cleaned, '}')
		for count := 0; count < closeCount && strings.HasPrefix(cleaned, "}"); count++ {
			closeCaddyfileStack(&stack, lineNumber)
		}

		withoutLeadingClosers := regexp.MustCompile(`^}+\s*`).ReplaceAllString(cleaned, "")
		withoutLeadingClosers = strings.TrimSpace(withoutLeadingClosers)
		if withoutLeadingClosers == "" {
			continue
		}

		opensBlock := strings.Contains(withoutLeadingClosers, "{")
		beforeBrace := withoutLeadingClosers
		if opensBlock {
			beforeBrace = strings.TrimSpace(withoutLeadingClosers[:strings.Index(withoutLeadingClosers, "{")])
		}
		tokens := splitCaddyfileWords(beforeBrace)
		if len(tokens) == 0 {
			continue
		}

		currentSource := findCurrentCaddyfileSource(stack)
		if currentSource != nil && isRouteDirective(tokens[0]) {
			matcher, matcherLine := currentCaddyfileMatcher(stack)
			currentSource.Directives = append(currentSource.Directives, CaddyfileDirective{
				Line:        lineNumber,
				Text:        beforeBrace,
				Name:        normalizeDirectiveName(tokens[0]),
				Args:        append([]string{}, tokens[1:]...),
				Matcher:     matcher,
				MatcherLine: matcherLine,
			})
		}

		if opensBlock {
			if currentSource == nil && isSiteBlockHeader(beforeBrace) {
				addresses := parseSiteAddresses(beforeBrace)
				sources = append(sources, CaddyfileSourceBlock{Path: path, Line: lineNumber, Address: strings.Join(addresses, ", "), Addresses: addresses, Directives: []CaddyfileDirective{}})
				stack = append(stack, caddyfileStackItem{typeName: "site", source: &sources[len(sources)-1]})
			} else if currentSource != nil {
				matcher, matcherLine, ok := matcherFromBlockHeader(tokens, lineNumber)
				typeName := "other"
				if ok {
					typeName = "matcher"
				}
				stack = append(stack, caddyfileStackItem{typeName: typeName, source: currentSource, matcher: matcher, matcherLine: matcherLine})
			} else {
				stack = append(stack, caddyfileStackItem{typeName: "other"})
			}
		}

		nonLeadingCloseCount := closeCount
		if strings.HasPrefix(cleaned, "}") {
			nonLeadingCloseCount = max(0, closeCount-1)
		}
		for count := 0; count < nonLeadingCloseCount; count++ {
			closeCaddyfileStack(&stack, lineNumber)
		}
	}

	return sources
}

func closeCaddyfileStack(stack *[]caddyfileStackItem, lineNumber int) {
	if len(*stack) == 0 {
		return
	}
	closed := (*stack)[len(*stack)-1]
	*stack = (*stack)[:len(*stack)-1]
	if closed.typeName == "site" && closed.source != nil {
		closed.source.EndLine = lineNumber
	}
}

func isSiteBlockHeader(header string) bool {
	if header == "" || header == "{" || strings.HasPrefix(header, "(") {
		return false
	}
	words := splitCaddyfileWords(header)
	if len(words) == 0 {
		return false
	}
	nonSite := map[string]bool{"admin": true, "email": true, "debug": true, "log": true, "storage": true, "acme_ca": true, "auto_https": true, "servers": true, "order": true, "skip_install_trust": true}
	return !nonSite[words[0]]
}

func parseSiteAddresses(header string) []string {
	fields := regexp.MustCompile(`[,\s]+`).Split(header, -1)
	addresses := []string{}
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field != "" {
			addresses = append(addresses, field)
		}
	}
	return addresses
}

func matcherFromBlockHeader(tokens []string, lineNumber int) (string, int, bool) {
	if len(tokens) == 0 {
		return "", 0, false
	}
	name := tokens[0]
	args := tokens[1:]
	switch name {
	case "handle":
		if len(args) > 0 {
			return matcherFromArgs(args), lineNumber, true
		}
		return "everything else", lineNumber, true
	case "handle_path":
		if len(args) > 0 {
			return matcherFromArgs(args), lineNumber, true
		}
		return "path *", lineNumber, true
	case "route":
		if len(args) > 0 {
			return matcherFromArgs(args), lineNumber, true
		}
		return "", 0, false
	case "handle_errors":
		return "handle_errors", lineNumber, true
	default:
		return "", 0, false
	}
}

func matcherFromArgs(args []string) string {
	if len(args) == 0 {
		return "all requests"
	}
	if strings.HasPrefix(args[0], "@") {
		return args[0]
	}
	parts := []string{}
	for _, arg := range args {
		if strings.HasPrefix(arg, "/") {
			parts = append(parts, "path "+arg)
		} else {
			parts = append(parts, arg)
		}
	}
	return strings.Join(parts, " ")
}

func findCaddyfileDirective(block *CaddyfileSourceBlock, route CaddyRouteRule) *CaddyfileDirective {
	if block == nil {
		return nil
	}
	actionKinds := []string{}
	for _, action := range route.Actions {
		actionKinds = append(actionKinds, action.Kind)
	}
	candidates := []CaddyfileDirective{}
	for _, directive := range block.Directives {
		for _, kind := range actionKinds {
			if directiveMatchesAction(directive, kind) {
				candidates = append(candidates, directive)
				break
			}
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		left := scoreDirective(candidates[i], route)
		right := scoreDirective(candidates[j], route)
		if left == right {
			return candidates[i].Line < candidates[j].Line
		}
		return left > right
	})
	return &candidates[0]
}

func scoreDirective(directive CaddyfileDirective, route CaddyRouteRule) int {
	score := 0
	if route.Matcher == "everything else" && directive.Matcher == "everything else" {
		score += 20
	}
	if route.Matcher == "all requests" && directive.Matcher == "" {
		score += 20
	}
	if directive.Matcher != "" && route.Matcher != "all requests" && matcherOverlaps(route.Matcher, directive.Matcher) {
		score += 20
	}
	for _, action := range route.Actions {
		if directiveMatchesAction(directive, action.Kind) {
			score += 5
		}
		if actionMentionsDirectiveArgs(action, directive) {
			score += 5
		}
	}
	return score
}

func directiveMatchesAction(directive CaddyfileDirective, actionKind string) bool {
	directiveName := normalizeDirectiveName(directive.Name)
	normalizedAction := normalizeDirectiveName(actionKind)
	if directiveName == normalizedAction {
		return true
	}
	if normalizedAction == "headers" && directiveName == "headers" {
		return true
	}
	if normalizedAction == "reverse_proxy" && directiveName == "php_fastcgi" {
		return true
	}
	if normalizedAction == "authentication" && directiveName == "basic_auth" {
		return true
	}
	if normalizedAction == "respond" && directiveName == "redir" {
		return true
	}
	return false
}

func actionMentionsDirectiveArgs(action CaddyRouteAction, directive CaddyfileDirective) bool {
	actionParts := []string{action.Label}
	for _, upstream := range action.Upstreams {
		actionParts = append(actionParts, upstream.Label)
	}
	actionText := strings.Join(actionParts, " ")
	for _, arg := range directive.Args {
		if strings.Contains(actionText, arg) {
			return true
		}
	}
	return false
}

func matcherOverlaps(routeMatcher string, directiveMatcher string) bool {
	if routeMatcher == directiveMatcher {
		return true
	}
	routePaths := regexp.MustCompile(`path\s+([^;]+)`).FindAllStringSubmatch(routeMatcher, -1)
	directivePaths := regexp.MustCompile(`path\s+([^;]+)`).FindAllStringSubmatch(directiveMatcher, -1)
	directiveSet := map[string]bool{}
	for _, match := range directivePaths {
		if len(match) > 1 {
			directiveSet[strings.TrimSpace(match[1])] = true
		}
	}
	for _, match := range routePaths {
		if len(match) > 1 && directiveSet[strings.TrimSpace(match[1])] {
			return true
		}
	}
	return false
}

func findCurrentCaddyfileSource(stack []caddyfileStackItem) *CaddyfileSourceBlock {
	for index := len(stack) - 1; index >= 0; index-- {
		if stack[index].source != nil {
			return stack[index].source
		}
	}
	return nil
}

func currentCaddyfileMatcher(stack []caddyfileStackItem) (string, int) {
	for index := len(stack) - 1; index >= 0; index-- {
		if stack[index].matcher != "" {
			return stack[index].matcher, stack[index].matcherLine
		}
	}
	return "", 0
}

func isRouteDirective(name string) bool {
	return name != "" && routeDirectiveNames[normalizeDirectiveName(name)]
}

func normalizeDirectiveName(name string) string {
	if name == "header" {
		return "headers"
	}
	if name == "basicauth" {
		return "basic_auth"
	}
	return name
}

func splitCaddyfileWords(input string) []string {
	return splitShellWords(input)
}

func countChar(value string, expected rune) int {
	count := 0
	for _, char := range value {
		if char == expected {
			count++
		}
	}
	return count
}

func normalizeAddressForHost(address string) string {
	normalized := strings.TrimSpace(strings.ToLower(address))
	normalized = regexp.MustCompile(`^https?://`).ReplaceAllString(normalized, "")
	if slash := strings.Index(normalized, "/"); slash >= 0 {
		normalized = normalized[:slash]
	}
	if strings.HasPrefix(normalized, ":") {
		return ""
	}
	normalized = strings.TrimPrefix(normalized, "*.")
	normalized = regexp.MustCompile(`:\d+$`).ReplaceAllString(normalized, "")
	return normalized
}

func normalizeAddressForListener(address string) string {
	trimmed := strings.TrimSpace(strings.ToLower(address))
	if strings.HasPrefix(trimmed, ":") {
		return trimmed
	}
	match := regexp.MustCompile(`:(\d+)$`).FindStringSubmatch(trimmed)
	if len(match) > 1 {
		return ":" + match[1]
	}
	return ""
}

func splitCaddyfileLines(content string) []string {
	return regexp.MustCompile(`\r?\n`).Split(content, -1)
}
