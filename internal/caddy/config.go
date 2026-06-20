package caddy

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type routeContext struct {
	serverName    string
	listen        []string
	hosts         []string
	matchers      []string
	fallbackAfter []string
}

type sourceRouteContext struct {
	serverName        string
	listen            []string
	hosts             []string
	matchers          []string
	matcherOverride   string
	accessLogResolver func([]string) []CaddyAccessLog
}

// ReverseProxyRoute is a summarized reverse_proxy handler from the active config.
type ReverseProxyRoute struct {
	ID            string
	ServerName    string
	Listen        []string
	Hosts         []string
	Matchers      []string
	FallbackAfter []string
	Upstreams     []CaddyUpstream
	RoutePath     string
	Handler       map[string]any
	Transport     string
	LoadBalancing string
}

// ExtractReverseProxies extracts reverse_proxy handlers from Caddy's active JSON config.
func ExtractReverseProxies(config any) []ReverseProxyRoute {
	proxies := []ReverseProxyRoute{}
	servers := getObject(config, "apps", "http", "servers")
	if servers == nil {
		return proxies
	}

	for _, serverName := range sortedKeys(servers) {
		serverValue := objectValue(servers[serverName])
		if serverValue == nil {
			continue
		}
		context := routeContext{
			serverName: serverName,
			listen:     stringArray(serverValue["listen"]),
		}
		traverseRoutes(arrayValue(serverValue["routes"]), context, fmt.Sprintf("apps.http.servers.%s.routes", serverName), &proxies)
		if errors := objectValue(serverValue["errors"]); errors != nil {
			traverseRoutes(arrayValue(errors["routes"]), context, fmt.Sprintf("apps.http.servers.%s.errors.routes", serverName), &proxies)
		}
	}

	return proxies
}

// ExtractCaddySources extracts UI services/sources from Caddy's active JSON config.
func ExtractCaddySources(config any) []CaddySource {
	sources := map[string]*CaddySource{}
	servers := getObject(config, "apps", "http", "servers")
	loggingLogs := getObject(config, "logging", "logs")
	if servers == nil {
		return nil
	}

	for _, serverName := range sortedKeys(servers) {
		serverValue := objectValue(servers[serverName])
		if serverValue == nil {
			continue
		}
		serverLogs := objectValue(serverValue["logs"])
		context := sourceRouteContext{
			serverName: serverName,
			listen:     stringArray(serverValue["listen"]),
			accessLogResolver: func(hosts []string) []CaddyAccessLog {
				return resolveAccessLogs(serverLogs, loggingLogs, hosts)
			},
		}
		traverseSourceRoutes(arrayValue(serverValue["routes"]), context, fmt.Sprintf("apps.http.servers.%s.routes", serverName), sources)
	}

	keys := make([]string, 0, len(sources))
	for key, source := range sources {
		if source.ProxyCount > 0 {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	result := make([]CaddySource, 0, len(keys))
	for _, key := range keys {
		result = append(result, *sources[key])
	}
	return result
}

// SourceLabel returns the user-facing label for a source.
func SourceLabel(source CaddySource) string {
	if len(source.Hosts) > 0 {
		return strings.Join(source.Hosts, ", ")
	}
	if len(source.Listen) > 0 {
		return strings.Join(source.Listen, ", ")
	}
	return source.ServerName
}

// SourceProxySummary returns a compact list of unique upstream labels for a source.
func SourceProxySummary(source CaddySource) string {
	upstreams := []string{}
	for _, route := range source.Routes {
		for _, action := range route.Actions {
			for _, upstream := range action.Upstreams {
				upstreams = append(upstreams, upstream.Label)
			}
		}
	}
	upstreams = uniqueStrings(upstreams)
	if len(upstreams) == 0 {
		return "(no upstreams)"
	}
	return strings.Join(upstreams, ", ")
}

func traverseRoutes(routes []any, parent routeContext, routesPath string, proxies *[]ReverseProxyRoute) {
	priorMatchersByGroup := map[string][]string{}
	for index, rawRoute := range routes {
		route := objectValue(rawRoute)
		if route == nil {
			continue
		}
		group := stringValue(route["group"])
		fallbackAfter := []string{}
		if group != "" {
			fallbackAfter = priorMatchersByGroup[group]
		}
		traverseRoute(route, parent, fmt.Sprintf("%s[%d]", routesPath, index), proxies, fallbackAfter)
		if group != "" {
			if currentMatcher := routeMatcherLabel(route["match"]); currentMatcher != "" {
				priorMatchersByGroup[group] = append(fallbackAfter, currentMatcher)
			}
		}
	}
}

func traverseRoute(route map[string]any, parent routeContext, routePath string, proxies *[]ReverseProxyRoute, fallbackAfter []string) {
	matchSummary := summarizeRouteMatch(route["match"])
	context := routeContext{
		serverName:    parent.serverName,
		listen:        parent.listen,
		hosts:         uniqueStrings(append(append([]string{}, parent.hosts...), matchSummary.hosts...)),
		matchers:      uniqueStrings(append(append([]string{}, parent.matchers...), matchSummary.matchers...)),
		fallbackAfter: uniqueStrings(append(append([]string{}, parent.fallbackAfter...), fallbackAfter...)),
	}
	for index, rawHandler := range arrayValue(route["handle"]) {
		traverseHandler(rawHandler, context, fmt.Sprintf("%s.handle[%d]", routePath, index), proxies)
	}
	traverseRoutes(arrayValue(route["routes"]), context, routePath+".routes", proxies)
}

func traverseHandler(rawHandler any, context routeContext, handlerPath string, proxies *[]ReverseProxyRoute) {
	handler := objectValue(rawHandler)
	if handler == nil {
		return
	}
	if stringValue(handler["handler"]) == "reverse_proxy" {
		*proxies = append(*proxies, ReverseProxyRoute{
			ID:            fmt.Sprintf("%s:%d", context.serverName, len(*proxies)),
			ServerName:    context.serverName,
			Listen:        context.listen,
			Hosts:         context.hosts,
			Matchers:      context.matchers,
			FallbackAfter: context.fallbackAfter,
			Upstreams:     extractUpstreams(handler),
			RoutePath:     handlerPath,
			Handler:       handler,
			Transport:     summarizeTransport(handler["transport"]),
			LoadBalancing: summarizeLoadBalancing(handler),
		})
	}
	traverseRoutes(arrayValue(handler["routes"]), context, handlerPath+".routes", proxies)
	for index, nestedHandler := range arrayValue(handler["handle"]) {
		traverseHandler(nestedHandler, context, fmt.Sprintf("%s.handle[%d]", handlerPath, index), proxies)
	}
}

func traverseSourceRoutes(routes []any, parent sourceRouteContext, routesPath string, sources map[string]*CaddySource) {
	priorMatchersByGroup := map[string][]string{}
	for index, rawRoute := range routes {
		route := objectValue(rawRoute)
		if route == nil {
			continue
		}
		group := stringValue(route["group"])
		ownMatchSummary := summarizeRouteMatch(route["match"])
		priorMatchers := []string{}
		if group != "" {
			priorMatchers = priorMatchersByGroup[group]
		}
		isFallbackRoute := len(priorMatchers) > 0 && len(ownMatchSummary.matchers) == 0
		traverseSourceRoute(route, parent, fmt.Sprintf("%s[%d]", routesPath, index), sources, isFallbackRoute)
		if group != "" && len(ownMatchSummary.matchers) > 0 {
			priorMatchersByGroup[group] = append(priorMatchers, strings.Join(ownMatchSummary.matchers, "; "))
		}
	}
}

func traverseSourceRoute(route map[string]any, parent sourceRouteContext, routePath string, sources map[string]*CaddySource, isFallbackRoute bool) {
	matchSummary := summarizeRouteMatch(route["match"])
	context := sourceRouteContext{
		serverName:        parent.serverName,
		listen:            parent.listen,
		hosts:             uniqueStrings(append(append([]string{}, parent.hosts...), matchSummary.hosts...)),
		matchers:          uniqueStrings(append(append([]string{}, parent.matchers...), matchSummary.matchers...)),
		matcherOverride:   parent.matcherOverride,
		accessLogResolver: parent.accessLogResolver,
	}
	if isFallbackRoute {
		context.matcherOverride = "everything else"
	}

	handlers := arrayValue(route["handle"])
	actions := summarizeDirectRouteActions(handlers)
	if len(actions) > 0 {
		addSourceRoute(sources, context, CaddyRouteRule{
			Matcher:   sourceRouteMatcherLabel(context.matchers, context.matcherOverride),
			Actions:   actions,
			RoutePath: routePath,
		})
	}

	for index, rawHandler := range handlers {
		handler := objectValue(rawHandler)
		if handler == nil {
			continue
		}
		traverseSourceRoutes(arrayValue(handler["routes"]), context, fmt.Sprintf("%s.handle[%d].routes", routePath, index), sources)
		for nestedIndex, nestedRawHandler := range arrayValue(handler["handle"]) {
			if objectValue(nestedRawHandler) == nil {
				continue
			}
			traverseSourceRoutes(arrayValue(objectValue(nestedRawHandler)["routes"]), context, fmt.Sprintf("%s.handle[%d].handle[%d].routes", routePath, index, nestedIndex), sources)
		}
	}
	traverseSourceRoutes(arrayValue(route["routes"]), context, routePath+".routes", sources)
}

func summarizeDirectRouteActions(handlers []any) []CaddyRouteAction {
	actions := []CaddyRouteAction{}
	root := ""
	for _, rawHandler := range handlers {
		handler := objectValue(rawHandler)
		if handler == nil {
			continue
		}
		handlerName := stringValue(handler["handler"])
		switch handlerName {
		case "vars":
			if value := stringValue(handler["root"]); value != "" {
				root = value
			} else {
				actions = append(actions, CaddyRouteAction{Kind: "vars", Label: summarizeVarsHandler(handler), Raw: handler})
			}
		case "map":
			actions = append(actions, CaddyRouteAction{Kind: "map", Label: summarizeMapHandler(handler), Raw: handler})
		case "file_server":
			actions = append(actions, CaddyRouteAction{Kind: "file_server", Label: summarizeFileServerHandler(handler, root), Raw: handler})
		case "reverse_proxy":
			actions = append(actions, summarizeReverseProxyAction(handler))
		case "static_response", "respond":
			actions = append(actions, summarizeStaticResponseAction(handler))
		case "rewrite":
			actions = append(actions, CaddyRouteAction{Kind: "rewrite", Label: summarizeRewriteHandler(handler), Raw: handler})
		case "headers":
			actions = append(actions, CaddyRouteAction{Kind: "headers", Label: summarizeHeadersHandler(handler), Raw: handler})
		case "encode":
			actions = append(actions, CaddyRouteAction{Kind: "encode", Label: summarizeEncodeHandler(handler), Raw: handler})
		case "authentication":
			actions = append(actions, CaddyRouteAction{Kind: "authentication", Label: summarizeAuthenticationHandler(handler), Raw: handler})
		case "request_body":
			actions = append(actions, CaddyRouteAction{Kind: "request_body", Label: summarizeRequestBodyHandler(handler), Raw: handler})
		case "templates":
			actions = append(actions, CaddyRouteAction{Kind: "templates", Label: summarizeTemplatesHandler(handler), Raw: handler})
		case "error":
			actions = append(actions, CaddyRouteAction{Kind: "error", Label: summarizeErrorHandler(handler), Raw: handler})
		case "metrics", "acme_server":
			actions = append(actions, CaddyRouteAction{Kind: handlerName, Label: handlerName, Raw: handler})
		case "subroute", "":
			continue
		default:
			actions = append(actions, CaddyRouteAction{Kind: handlerName, Label: summarizeUnknownHandler(handlerName, handler), Raw: handler})
		}
	}
	return actions
}

func addSourceRoute(sources map[string]*CaddySource, context sourceRouteContext, route CaddyRouteRule) {
	key := sourceKey(context)
	resolvedAccessLogs := []CaddyAccessLog{}
	if context.accessLogResolver != nil {
		resolvedAccessLogs = context.accessLogResolver(context.hosts)
	}
	source := sources[key]
	if source == nil {
		source = &CaddySource{ID: key, ServerName: context.serverName, Listen: context.listen, Hosts: context.hosts, AccessLogs: resolvedAccessLogs}
	}
	source.AccessLogs = mergeAccessLogs(source.AccessLogs, resolvedAccessLogs)
	source.Routes = append(source.Routes, route)
	for _, action := range route.Actions {
		if action.Kind == "reverse_proxy" {
			source.ProxyCount++
		}
	}
	sources[key] = source
}

func resolveAccessLogs(serverLogs map[string]any, loggingLogs map[string]any, hosts []string) []CaddyAccessLog {
	if serverLogs == nil {
		return nil
	}
	skippedHosts := stringArray(serverLogs["skip_hosts"])
	loggerNames := objectValue(serverLogs["logger_names"])
	defaultLoggerName := stringValue(serverLogs["default_logger_name"])
	type resolvedLogger struct{ loggerName, source string }
	resolved := []resolvedLogger{}
	for _, host := range hosts {
		if containsString(skippedHosts, host) {
			continue
		}
		if loggerName := stringValue(loggerNames[host]); loggerName != "" {
			resolved = append(resolved, resolvedLogger{loggerName, "host " + host})
		}
	}
	if len(resolved) == 0 && defaultLoggerName != "" {
		resolved = append(resolved, resolvedLogger{defaultLoggerName, "server default"})
	}
	logs := []CaddyAccessLog{}
	for _, item := range resolved {
		var logConfig any
		if loggingLogs != nil {
			logConfig = loggingLogs[item.loggerName]
		}
		logs = append(logs, buildAccessLog(item.loggerName, item.source, logConfig))
	}
	return mergeAccessLogs(logs, nil)
}

func buildAccessLog(loggerName, source string, logConfig any) CaddyAccessLog {
	config := objectValue(logConfig)
	writer := objectValue(config["writer"])
	encoder := objectValue(config["encoder"])
	include := stringArray(config["include"])
	loggerID := ""
	for _, entry := range include {
		if strings.HasPrefix(entry, "http.log.access.") {
			loggerID = entry
			break
		}
	}
	if loggerID == "" && loggerName != "" {
		loggerID = "http.log.access." + loggerName
	}
	if loggerName == "" {
		loggerName = "default"
	}
	writerOutput := stringValue(writer["output"])
	if writerOutput == "" {
		writerOutput = "default"
	}
	return CaddyAccessLog{LoggerName: loggerName, LoggerID: loggerID, WriterOutput: writerOutput, Filename: stringValue(writer["filename"]), Encoder: stringValue(encoder["format"]), Source: source}
}

func mergeAccessLogs(left, right []CaddyAccessLog) []CaddyAccessLog {
	merged := map[string]CaddyAccessLog{}
	order := []string{}
	for _, log := range append(append([]CaddyAccessLog{}, left...), right...) {
		key := log.LoggerName + ":" + log.WriterOutput + ":" + log.Filename
		if _, exists := merged[key]; !exists {
			order = append(order, key)
		}
		merged[key] = log
	}
	result := []CaddyAccessLog{}
	for _, key := range order {
		result = append(result, merged[key])
	}
	return result
}

func sourceKey(context sourceRouteContext) string {
	address := strings.Join(context.hosts, ",")
	if address == "" {
		address = strings.Join(context.listen, ",")
	}
	if address == "" {
		address = context.serverName
	}
	return context.serverName + ":" + address
}

func sourceRouteMatcherLabel(matchers []string, matcherOverride string) string {
	if matcherOverride != "" {
		return matcherOverride
	}
	if len(matchers) > 0 {
		return strings.Join(matchers, "; ")
	}
	return "all requests"
}

func summarizeReverseProxyAction(handler map[string]any) CaddyRouteAction {
	upstreams := extractUpstreams(handler)
	upstreamLabel := "(no upstreams)"
	if len(upstreams) > 0 {
		labels := make([]string, 0, len(upstreams))
		for _, upstream := range upstreams {
			labels = append(labels, upstream.Label)
		}
		upstreamLabel = strings.Join(labels, ", ")
	}
	transport := summarizeTransport(handler["transport"])
	label := "reverse_proxy " + upstreamLabel
	if transportObject := objectValue(handler["transport"]); stringValue(transportObject["protocol"]) == "fastcgi" {
		label = "php_fastcgi " + upstreamLabel
	}
	return CaddyRouteAction{Kind: "reverse_proxy", Label: label, Upstreams: upstreams, Transport: transport, LoadBalancing: summarizeLoadBalancing(handler), Raw: handler}
}

func summarizeStaticResponseAction(handler map[string]any) CaddyRouteAction {
	statusCode := numberOrUndefined(handler["status_code"])
	location := firstHeaderValue(handler["headers"], "Location")
	if location != "" && statusCode >= 300 && statusCode < 400 {
		return CaddyRouteAction{Kind: "redir", Label: fmt.Sprintf("redir %s %.0f", location, statusCode), Raw: handler}
	}
	body := ""
	if value := stringValue(handler["body"]); value != "" {
		body = " " + compactJSON(truncate(value, 40))
	}
	status := ""
	if statusCode != 0 {
		status = fmt.Sprintf(" %.0f", statusCode)
	}
	return CaddyRouteAction{Kind: "respond", Label: "respond" + status + body, Raw: handler}
}

func summarizeFileServerHandler(handler map[string]any, root string) string {
	parts := []string{"file_server"}
	if root != "" {
		parts = append(parts, root)
	}
	if boolValue(handler["browse"]) {
		parts = append(parts, "browse")
	}
	if boolValue(handler["pass_thru"]) {
		parts = append(parts, "pass_thru")
	}
	if indexNames := stringArray(handler["index_names"]); len(indexNames) > 0 {
		parts = append(parts, "index "+strings.Join(indexNames, ","))
	}
	return strings.Join(parts, " ")
}

func summarizeRewriteHandler(handler map[string]any) string {
	if uri := stringValue(handler["uri"]); uri != "" {
		return "rewrite " + uri
	}
	if value := stringValue(handler["strip_path_prefix"]); value != "" {
		return "uri strip_prefix " + value
	}
	if value := stringValue(handler["strip_path_suffix"]); value != "" {
		return "uri strip_suffix " + value
	}
	return "rewrite " + compactJSON(omitHandlerName(handler))
}

func summarizeHeadersHandler(handler map[string]any) string {
	operations := []string{}
	collectHeaderOperations(&operations, "request", objectValue(handler["request"]))
	collectHeaderOperations(&operations, "response", objectValue(handler["response"]))
	if len(operations) == 0 {
		return "headers"
	}
	return "headers " + strings.Join(operations, "; ")
}

func collectHeaderOperations(operations *[]string, scope string, config map[string]any) {
	if config == nil {
		return
	}
	for _, operation := range []string{"set", "add", "delete", "replace", "defer"} {
		value, ok := config[operation]
		if !ok {
			continue
		}
		if object := objectValue(value); object != nil {
			*operations = append(*operations, scope+"."+operation+" "+strings.Join(sortedKeys(object), ","))
		} else if values := stringArray(value); len(values) > 0 {
			*operations = append(*operations, scope+"."+operation+" "+strings.Join(values, ","))
		} else {
			*operations = append(*operations, scope+"."+operation)
		}
	}
}

func summarizeEncodeHandler(handler map[string]any) string {
	encodingsObject := objectValue(handler["encodings"])
	encodings := sortedKeys(encodingsObject)
	prefer := stringArray(handler["prefer"])
	labels := encodings
	if len(prefer) > 0 {
		labels = prefer
	}
	if len(labels) == 0 {
		return "encode"
	}
	return "encode " + strings.Join(labels, ", ")
}

func summarizeAuthenticationHandler(handler map[string]any) string {
	providers := objectValue(handler["providers"])
	if providers == nil {
		return "authentication"
	}
	if httpBasic := objectValue(providers["http_basic"]); httpBasic != nil {
		accounts := arrayValue(httpBasic["accounts"])
		realm := ""
		if value := stringValue(httpBasic["realm"]); value != "" {
			realm = " realm=" + value
		}
		plural := "s"
		if len(accounts) == 1 {
			plural = ""
		}
		return fmt.Sprintf("basic_auth %d user%s%s", len(accounts), plural, realm)
	}
	return "authentication " + strings.Join(sortedKeys(providers), ", ")
}

func summarizeRequestBodyHandler(handler map[string]any) string {
	parts := []string{"request_body"}
	if value, exists := handler["max_size"]; exists {
		parts = append(parts, "max_size "+formatByteLimit(value))
	}
	return strings.Join(parts, " ")
}

func summarizeTemplatesHandler(handler map[string]any) string {
	mimeTypes := stringArray(handler["mime_types"])
	if len(mimeTypes) == 0 {
		return "templates"
	}
	return "templates " + strings.Join(mimeTypes, ", ")
}

func summarizeMapHandler(handler map[string]any) string {
	source := stringValue(handler["source"])
	if source == "" {
		source = "source"
	}
	destinations := stringArray(handler["destinations"])
	destination := "destination"
	if len(destinations) > 0 {
		destination = strings.Join(destinations, ",")
	}
	defaults := []string{}
	for _, value := range arrayValue(handler["defaults"]) {
		defaults = append(defaults, fmt.Sprint(value))
	}
	suffix := ""
	if len(defaults) > 0 {
		suffix = " default " + strings.Join(defaults, ",")
	}
	return fmt.Sprintf("map %s → %s%s", source, destination, suffix)
}

func summarizeVarsHandler(handler map[string]any) string {
	omitted := omitHandlerName(handler)
	parts := []string{}
	for _, key := range sortedKeys(omitted) {
		parts = append(parts, key+"="+compactJSON(omitted[key]))
	}
	if len(parts) == 0 {
		return "vars"
	}
	return "vars " + strings.Join(parts, " ")
}

func summarizeErrorHandler(handler map[string]any) string {
	status := numberOrUndefined(handler["status_code"])
	message := ""
	if value := stringValue(handler["message"]); value != "" {
		message = " " + compactJSON(value)
	}
	if status == 0 {
		return "error" + message
	}
	return fmt.Sprintf("error %.0f%s", status, message)
}

func summarizeUnknownHandler(handlerName string, handler map[string]any) string {
	if handlerName == "" {
		handlerName = "handler"
	}
	summary := compactJSON(omitHandlerName(handler))
	if summary == "{}" {
		return handlerName
	}
	return handlerName + " " + summary
}

func extractUpstreams(handler map[string]any) []CaddyUpstream {
	upstreams := []CaddyUpstream{}
	for _, rawUpstream := range arrayValue(handler["upstreams"]) {
		if text := stringValue(rawUpstream); text != "" {
			upstreams = append(upstreams, CaddyUpstream{Label: text, Dial: text, Raw: rawUpstream})
			continue
		}
		upstreamObject := objectValue(rawUpstream)
		if upstreamObject == nil {
			upstreams = append(upstreams, CaddyUpstream{Label: compactJSON(rawUpstream), Raw: rawUpstream})
			continue
		}
		dial := stringValue(upstreamObject["dial"])
		label := dial
		if label == "" {
			label = compactJSON(upstreamObject)
		}
		upstreams = append(upstreams, CaddyUpstream{Label: label, Dial: dial, Raw: rawUpstream})
	}
	if dynamicUpstreams, exists := handler["dynamic_upstreams"]; exists {
		upstreams = append(upstreams, CaddyUpstream{Label: "dynamic " + summarizeDynamicUpstreams(dynamicUpstreams), Dynamic: true, Raw: dynamicUpstreams})
	}
	return upstreams
}

func routeMatcherLabel(matchValue any) string {
	summary := summarizeRouteMatch(matchValue)
	labels := []string{}
	for _, host := range summary.hosts {
		labels = append(labels, "host "+host)
	}
	labels = append(labels, summary.matchers...)
	return strings.Join(labels, "; ")
}

type routeMatchSummary struct{ hosts, matchers []string }

func summarizeRouteMatch(matchValue any) routeMatchSummary {
	matchSets := []any{}
	if values := arrayValue(matchValue); values != nil {
		matchSets = values
	} else if objectValue(matchValue) != nil {
		matchSets = []any{matchValue}
	}
	hosts := []string{}
	matchers := []string{}
	for _, rawMatchSet := range matchSets {
		matchSet := objectValue(rawMatchSet)
		if matchSet == nil {
			continue
		}
		parts := []string{}
		for _, key := range sortedKeys(matchSet) {
			value := matchSet[key]
			switch key {
			case "host":
				hosts = append(hosts, stringArray(value)...)
			case "path", "method", "remote_ip":
				values := stringArray(value)
				if len(values) > 0 {
					parts = append(parts, key+" "+strings.Join(values, ", "))
				} else {
					parts = append(parts, key+" "+compactJSON(value))
				}
			case "path_regexp":
				parts = append(parts, "path_regexp "+summarizeRegexpMatcher(value))
			case "header", "query", "vars":
				parts = append(parts, key+" "+summarizeObjectMatcher(value))
			case "file":
				parts = append(parts, "file "+summarizeFileMatcher(value))
			case "not":
				parts = append(parts, "not "+summarizeNotMatcher(value))
			case "protocol":
				if text := stringValue(value); text != "" {
					parts = append(parts, "protocol "+text)
				} else {
					parts = append(parts, "protocol "+compactJSON(value))
				}
			case "expression":
				if text := stringValue(value); text != "" {
					parts = append(parts, "expr "+text)
				} else {
					parts = append(parts, "expr "+compactJSON(value))
				}
			default:
				parts = append(parts, key+" "+compactJSON(value))
			}
		}
		if len(parts) > 0 {
			matchers = append(matchers, strings.Join(parts, " + "))
		}
	}
	return routeMatchSummary{hosts: uniqueStrings(hosts), matchers: uniqueStrings(matchers)}
}

func summarizeTransport(transport any) string {
	object := objectValue(transport)
	if object == nil {
		return ""
	}
	protocol := stringValue(object["protocol"])
	tls := ""
	if boolValue(object["tls"]) {
		tls = " + tls"
	}
	if protocol != "" {
		return protocol + tls
	}
	return compactJSON(object)
}

func summarizeLoadBalancing(handler map[string]any) string {
	policy := getObject(handler, "load_balancing", "selection_policy")
	if policy == nil {
		return ""
	}
	if policyName := stringValue(policy["policy"]); policyName != "" {
		return policyName
	}
	return compactJSON(policy)
}

func summarizeFileMatcher(value any) string {
	object := objectValue(value)
	if object == nil {
		return compactJSON(value)
	}
	parts := []string{}
	if values := stringArray(object["try_files"]); len(values) > 0 {
		parts = append(parts, "try_files "+strings.Join(values, ", "))
	}
	if values := stringArray(object["split_path"]); len(values) > 0 {
		parts = append(parts, "split_path "+strings.Join(values, ", "))
	}
	if root, ok := object["root"]; ok {
		parts = append(parts, "root "+compactJSON(root))
	}
	if len(parts) == 0 {
		return compactJSON(object)
	}
	return strings.Join(parts, "; ")
}

func summarizeNotMatcher(value any) string {
	matchSets := arrayValue(value)
	if matchSets == nil && objectValue(value) != nil {
		matchSets = []any{value}
	}
	if len(matchSets) == 0 {
		return compactJSON(value)
	}
	parts := []string{}
	for _, matchSet := range matchSets {
		matchers := summarizeRouteMatch(matchSet).matchers
		if len(matchers) > 0 {
			parts = append(parts, strings.Join(matchers, " + "))
		} else {
			parts = append(parts, compactJSON(matchSet))
		}
	}
	return strings.Join(parts, "; ")
}

func summarizeRegexpMatcher(value any) string {
	object := objectValue(value)
	if object == nil {
		return compactJSON(value)
	}
	name := ""
	if value := stringValue(object["name"]); value != "" {
		name = value + ": "
	}
	pattern := stringValue(object["pattern"])
	if pattern == "" {
		pattern = compactJSON(object)
	}
	return name + pattern
}

func summarizeObjectMatcher(value any) string {
	object := objectValue(value)
	if object == nil {
		return compactJSON(value)
	}
	parts := []string{}
	for _, key := range sortedKeys(object) {
		matcherValue := object[key]
		if values := stringArray(matcherValue); len(values) > 0 {
			parts = append(parts, key+"="+strings.Join(values, ","))
		} else {
			parts = append(parts, key+"="+compactJSON(matcherValue))
		}
	}
	return strings.Join(parts, "; ")
}

func summarizeDynamicUpstreams(value any) string {
	if values := arrayValue(value); values != nil {
		parts := []string{}
		for _, item := range values {
			parts = append(parts, summarizeDynamicUpstreams(item))
		}
		return strings.Join(parts, ", ")
	}
	object := objectValue(value)
	if object == nil {
		return compactJSON(value)
	}
	if source := stringValue(object["source"]); source != "" {
		return source
	}
	if name := stringValue(object["name"]); name != "" {
		return name
	}
	return compactJSON(object)
}

func getObject(value any, path ...string) map[string]any {
	current := value
	for _, segment := range path {
		object := objectValue(current)
		if object == nil {
			return nil
		}
		current = object[segment]
	}
	return objectValue(current)
}

func objectValue(value any) map[string]any {
	if object, ok := value.(map[string]any); ok && object != nil {
		return object
	}
	return nil
}

func arrayValue(value any) []any {
	if values, ok := value.([]any); ok {
		return values
	}
	return nil
}

func stringArray(value any) []string {
	if value == nil {
		return nil
	}
	if text, ok := value.(string); ok {
		return []string{text}
	}
	values, ok := value.([]any)
	if !ok {
		if strings, ok := value.([]string); ok {
			return append([]string{}, strings...)
		}
		return nil
	}
	result := []string{}
	for _, item := range values {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func numberOrUndefined(value any) float64 {
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Float64()
		if err == nil {
			return parsed
		}
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case string:
		parsed, err := strconv.ParseFloat(typed, 64)
		if err == nil {
			return parsed
		}
	}
	return 0
}

func firstHeaderValue(headers any, name string) string {
	object := objectValue(headers)
	if object == nil {
		return ""
	}
	for key, value := range object {
		if !strings.EqualFold(key, name) {
			continue
		}
		if text := stringValue(value); text != "" {
			return text
		}
		for _, item := range arrayValue(value) {
			if text := stringValue(item); text != "" {
				return text
			}
		}
	}
	return ""
}

func omitHandlerName(handler map[string]any) map[string]any {
	result := map[string]any{}
	for key, value := range handler {
		if key != "handler" {
			result[key] = value
		}
	}
	return result
}

func truncate(value string, maxLength int) string {
	if len(value) <= maxLength {
		return value
	}
	if maxLength <= 1 {
		return "…"
	}
	return value[:maxLength-1] + "…"
}

func formatByteLimit(value any) string {
	bytes := numberOrUndefined(value)
	if bytes == 0 {
		return compactJSON(value)
	}
	if bytes < 1000 {
		return fmt.Sprintf("%.0fB", bytes)
	}
	if bytes < 1000*1000 {
		return fmt.Sprintf("%.1fKB", bytes/1000)
	}
	if bytes < 1000*1000*1000 {
		return fmt.Sprintf("%.1fMB", bytes/1000/1000)
	}
	return fmt.Sprintf("%.1fGB", bytes/1000/1000/1000)
}

func compactJSON(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	if value == nil {
		return "undefined"
	}
	bytes, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(bytes)
}

func boolValue(value any) bool {
	if typed, ok := value.(bool); ok {
		return typed
	}
	return false
}

func stringValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func sortedKeys(object map[string]any) []string {
	if object == nil {
		return nil
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
