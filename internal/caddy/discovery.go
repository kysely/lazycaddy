package caddy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type adminConfigDiscovery struct {
	AdminURL string
	Disabled bool
	Listen   string
	Source   string
}

// ParsedCaddyRunCommand is a parsed `caddy run ...` process command.
type ParsedCaddyRunCommand struct {
	Argv       []string
	ConfigPath string
	Adapter    string
	Resume     bool
}

// SystemdServiceInfo contains inspected caddy.service metadata.
type SystemdServiceInfo struct {
	Exists       bool
	LoadState    string
	ActiveState  string
	SubState     string
	MainPID      int
	FragmentPath string
	ExecStart    string
	Argv         []string
	ArgvSource   string // "proc" or "systemd"
	Error        string
}

// AdminAPIDiscovery describes the discovered Caddy Admin API endpoint and context.
type AdminAPIDiscovery struct {
	AdminURL    string
	Source      string
	SourceLabel string
	Service     *SystemdServiceInfo
	Command     *ParsedCaddyRunCommand
	ConfigPath  string
	Adapter     string
	Disabled    bool
	Notes       []string
}

// DiscoverAdminAPIEndpoint discovers Caddy's Admin API endpoint using the same priority as lazycaddy TS.
func DiscoverAdminAPIEndpoint(ctx context.Context, argv []string, lookupEnv func(string) string) AdminAPIDiscovery {
	return DiscoverAdminAPIEndpointWithConfig(ctx, argv, lookupEnv, "")
}

// DiscoverAdminAPIEndpointWithConfig discovers Caddy's Admin API endpoint with
// an optional config-file Admin API URL. Precedence is CLI > environment >
// config file > Caddy/system discovery > default.
func DiscoverAdminAPIEndpointWithConfig(ctx context.Context, argv []string, lookupEnv func(string) string, configAdminURL string) AdminAPIDiscovery {
	notes := []string{}
	explicit, hasExplicit := GetExplicitAdminAPIBaseURLWithConfig(argv, lookupEnv, configAdminURL)
	service := inspectSystemdCaddyService(ctx)

	if hasExplicit {
		explicitResult := explicitDiscovery(explicit)
		var serviceCommand *ParsedCaddyRunCommand
		if service != nil {
			serviceCommand = parseCaddyRunCommand(service.Argv)
		}

		result := explicitResult
		result.Service = service
		result.Command = serviceCommand
		if serviceCommand != nil {
			result.ConfigPath = serviceCommand.ConfigPath
			result.Adapter = serviceCommand.Adapter
		}
		if service != nil {
			result.Notes = append(result.Notes, formatServiceNote(*service))
		} else {
			result.Notes = append(result.Notes, "systemd caddy.service was not found or could not be inspected.")
		}
		return result
	}

	if service != nil {
		notes = append(notes, formatServiceNote(*service))
		serviceCommand := parseCaddyRunCommand(service.Argv)
		if serviceCommand != nil {
			if discovered, ok := discoverFromCommand(serviceCommand, &notes, "systemd"); ok {
				discovered.Service = service
				discovered.Command = serviceCommand
				return discovered
			}
		} else if service.Exists {
			notes = append(notes, "Could not parse a caddy run command from systemd's caddy.service.")
		}

		if service.Exists {
			notes = append(notes, "No admin endpoint was found in the service command/config; assuming Caddy's default admin endpoint.")
			return AdminAPIDiscovery{
				AdminURL:    DefaultAdminAPIURL,
				Source:      "systemd-default",
				SourceLabel: "systemd caddy.service default",
				Service:     service,
				Notes:       notes,
			}
		}
	}

	processCommand := inspectCaddyProcess(ctx)
	if processCommand != nil {
		notes = append(notes, "Found a running caddy process with pgrep.")
		if discovered, ok := discoverFromCommand(processCommand, &notes, "process"); ok {
			discovered.Command = processCommand
			return discovered
		}

		notes = append(notes, "No admin endpoint was found in the process command/config; assuming Caddy's default admin endpoint.")
		return AdminAPIDiscovery{
			AdminURL:    DefaultAdminAPIURL,
			Source:      "process-default",
			SourceLabel: "caddy process default",
			Command:     processCommand,
			Notes:       notes,
		}
	}

	notes = append(notes, "No explicit endpoint, systemd caddy.service, or caddy process was discovered; using Caddy's default endpoint.")
	return AdminAPIDiscovery{
		AdminURL:    DefaultAdminAPIURL,
		Source:      "default",
		SourceLabel: "Caddy default",
		Notes:       notes,
	}
}

// DiscoverySummary returns a compact human-readable Admin API summary.
func DiscoverySummary(discovery AdminAPIDiscovery) string {
	if discovery.Disabled {
		return fmt.Sprintf("Admin API disabled by %s", discovery.SourceLabel)
	}
	adminURL := discovery.AdminURL
	if adminURL == "" {
		adminURL = "not discovered"
	}
	return fmt.Sprintf("%s (%s)", adminURL, discovery.SourceLabel)
}

func explicitDiscovery(explicit ExplicitAdminAPIBaseURL) AdminAPIDiscovery {
	note := "Using CADDY_ADMIN_API/CADDY_ADMIN_URL from the environment."
	label := "environment override"
	switch explicit.Source {
	case "cli":
		note = "Using --admin-url/--admin from the lazycaddy command line."
		label = "CLI override"
	case "config":
		note = "Using admin_url from the lazycaddy config file."
		label = "lazycaddy config"
	}

	return AdminAPIDiscovery{
		AdminURL:    explicit.URL,
		Source:      explicit.Source,
		SourceLabel: label,
		Notes:       []string{note},
	}
}

func discoverFromCommand(command *ParsedCaddyRunCommand, notes *[]string, origin string) (AdminAPIDiscovery, bool) {
	if command == nil {
		return AdminAPIDiscovery{}, false
	}

	if command.ConfigPath != "" {
		note := fmt.Sprintf("Caddy command uses config: %s", command.ConfigPath)
		if command.Adapter != "" {
			note += fmt.Sprintf(" (%s)", command.Adapter)
		}
		note += "."
		*notes = append(*notes, note)

		configDiscovery := discoverFromConfig(command.ConfigPath, command.Adapter)
		*notes = append(*notes, configDiscovery.Source)

		if configDiscovery.Disabled {
			return AdminAPIDiscovery{
				Source:      "disabled",
				SourceLabel: fmt.Sprintf("%s config %s", origin, command.ConfigPath),
				ConfigPath:  command.ConfigPath,
				Adapter:     command.Adapter,
				Disabled:    true,
				Notes:       append([]string{}, (*notes)...),
			}, true
		}

		if configDiscovery.AdminURL != "" {
			source := "systemd-config"
			if origin == "process" {
				source = "process-config"
			}
			return AdminAPIDiscovery{
				AdminURL:    configDiscovery.AdminURL,
				Source:      source,
				SourceLabel: fmt.Sprintf("%s config %s", origin, command.ConfigPath),
				ConfigPath:  command.ConfigPath,
				Adapter:     command.Adapter,
				Notes:       append([]string{}, (*notes)...),
			}, true
		}

		if configDiscovery.Listen != "" {
			return AdminAPIDiscovery{
				Source:      "unsupported",
				SourceLabel: fmt.Sprintf("%s config %s", origin, command.ConfigPath),
				ConfigPath:  command.ConfigPath,
				Adapter:     command.Adapter,
				Notes:       append([]string{}, (*notes)...),
			}, true
		}
	}

	if command.Resume {
		*notes = append(*notes, "Caddy was started with --resume; the resumed config cannot be inspected before reaching the Admin API.")
	}

	return AdminAPIDiscovery{}, false
}

func discoverFromConfig(configPath string, adapter string) adminConfigDiscovery {
	content, err := os.ReadFile(configPath)
	if err != nil {
		return adminConfigDiscovery{
			Source:   fmt.Sprintf("Could not read %s: %v. Falling back to default endpoint.", configPath, err),
			AdminURL: DefaultAdminAPIURL,
		}
	}

	if inferConfigFormat(configPath, adapter) == "json" {
		return discoverFromJSONConfig(string(content))
	}
	return discoverFromCaddyfile(string(content))
}

func discoverFromJSONConfig(content string) adminConfigDiscovery {
	var config map[string]any
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.UseNumber()
	if err := decoder.Decode(&config); err != nil {
		return adminConfigDiscovery{Source: fmt.Sprintf("Could not parse JSON config: %v. Falling back to default endpoint.", err), AdminURL: DefaultAdminAPIURL}
	}
	if config == nil {
		return adminConfigDiscovery{Source: "JSON config is not an object. Falling back to default endpoint.", AdminURL: DefaultAdminAPIURL}
	}

	admin, ok := config["admin"].(map[string]any)
	if !ok || admin == nil {
		return adminConfigDiscovery{Source: "JSON config has no admin block; using Caddy's default endpoint.", AdminURL: DefaultAdminAPIURL}
	}
	if disabled, ok := admin["disabled"].(bool); ok && disabled {
		return adminConfigDiscovery{Source: "JSON config sets admin.disabled=true.", Disabled: true}
	}
	if listen, ok := admin["listen"].(string); ok {
		return listenToAdminURL(listen, "JSON config admin.listen")
	}
	return adminConfigDiscovery{Source: "JSON config admin block has no listen address; using Caddy's default endpoint.", AdminURL: DefaultAdminAPIURL}
}

func discoverFromCaddyfile(content string) adminConfigDiscovery {
	globalBlock, ok := extractCaddyfileGlobalBlock(content)
	if !ok {
		return adminConfigDiscovery{Source: "Caddyfile has no global options block; using Caddy's default endpoint.", AdminURL: DefaultAdminAPIURL}
	}

	for _, rawLine := range globalBlock {
		line := strings.TrimSpace(stripComment(rawLine))
		if line == "" || line == "{" || line == "}" {
			continue
		}
		tokens := strings.Fields(line)
		if len(tokens) == 0 || tokens[0] != "admin" {
			continue
		}
		value := ""
		if len(tokens) > 1 {
			value = tokens[1]
		}
		if value == "off" {
			return adminConfigDiscovery{Source: "Caddyfile global options set `admin off`.", Disabled: true}
		}
		if value != "" && value != "{" {
			return listenToAdminURL(value, "Caddyfile global admin option")
		}
		return adminConfigDiscovery{Source: "Caddyfile global admin option has no explicit address; using Caddy's default endpoint.", AdminURL: DefaultAdminAPIURL}
	}

	return adminConfigDiscovery{Source: "Caddyfile global options do not override admin; using Caddy's default endpoint.", AdminURL: DefaultAdminAPIURL}
}

func inspectSystemdCaddyService(ctx context.Context) *SystemdServiceInfo {
	command := []string{
		"systemctl", "show", "caddy", "--no-pager",
		"--property=LoadState",
		"--property=ActiveState",
		"--property=SubState",
		"--property=MainPID",
		"--property=FragmentPath",
		"--property=ExecStart",
	}
	result := runDiscoveryCommand(ctx, command, 1500*time.Millisecond)
	if !result.ok && result.stdout == "" {
		return nil
	}

	properties := parseProperties(result.stdout)
	loadState := properties["LoadState"]
	exists := loadState != "" && loadState != "not-found"
	mainPID := numberOrZero(properties["MainPID"])
	procArgv := []string(nil)
	if mainPID > 0 {
		procArgv = readProcCmdline(mainPID)
	}
	systemdArgv := extractSystemdArgv(properties["ExecStart"])
	argv := procArgv
	argvSource := ""
	if len(argv) > 0 {
		argvSource = "proc"
	} else if len(systemdArgv) > 0 {
		argv = systemdArgv
		argvSource = "systemd"
	}

	service := &SystemdServiceInfo{
		Exists:       exists,
		LoadState:    loadState,
		ActiveState:  properties["ActiveState"],
		SubState:     properties["SubState"],
		MainPID:      mainPID,
		FragmentPath: properties["FragmentPath"],
		ExecStart:    properties["ExecStart"],
		Argv:         argv,
		ArgvSource:   argvSource,
	}
	if !result.ok {
		service.Error = firstNonEmpty(strings.TrimSpace(result.stderr), strings.TrimSpace(result.stdout))
	}
	return service
}

func inspectCaddyProcess(ctx context.Context) *ParsedCaddyRunCommand {
	result := runDiscoveryCommand(ctx, []string{"pgrep", "-a", "caddy"}, 1500*time.Millisecond)
	if !result.ok || strings.TrimSpace(result.stdout) == "" {
		return nil
	}

	for _, line := range regexp.MustCompile(`\r?\n`).Split(strings.TrimSpace(result.stdout), -1) {
		commandLine := regexp.MustCompile(`^\d+\s+`).ReplaceAllString(line, "")
		argv := splitShellWords(commandLine)
		command := parseCaddyRunCommand(argv)
		if command != nil {
			return command
		}
	}
	return nil
}

func parseCaddyRunCommand(argv []string) *ParsedCaddyRunCommand {
	if len(argv) == 0 {
		return nil
	}
	executable := filepath.Base(argv[0])
	if executable != "caddy" && !strings.HasSuffix(argv[0], "/caddy") {
		return nil
	}

	command := &ParsedCaddyRunCommand{Argv: append([]string{}, argv...)}
	for index := 1; index < len(argv); index++ {
		arg := argv[index]
		switch {
		case arg == "--config" || arg == "-config":
			if index+1 < len(argv) {
				command.ConfigPath = argv[index+1]
				index++
			}
		case strings.HasPrefix(arg, "--config="):
			command.ConfigPath = strings.TrimPrefix(arg, "--config=")
		case arg == "--adapter" || arg == "-adapter":
			if index+1 < len(argv) {
				command.Adapter = argv[index+1]
				index++
			}
		case strings.HasPrefix(arg, "--adapter="):
			command.Adapter = strings.TrimPrefix(arg, "--adapter=")
		case arg == "--resume":
			command.Resume = true
		}
	}
	return command
}

func listenToAdminURL(listen string, source string) adminConfigDiscovery {
	if listen == "off" {
		return adminConfigDiscovery{Source: source + " is off.", Disabled: true}
	}
	if strings.HasPrefix(listen, "unix/") || strings.HasPrefix(listen, "unix:") {
		return adminConfigDiscovery{
			Source: fmt.Sprintf("%s uses a Unix socket (%s); lazycaddy cannot query Unix-socket admin endpoints yet.", source, listen),
			Listen: listen,
		}
	}

	normalizedListen := listen
	if strings.HasPrefix(normalizedListen, "tcp/") {
		normalizedListen = strings.TrimPrefix(normalizedListen, "tcp/")
	}
	if strings.HasPrefix(normalizedListen, ":") {
		normalizedListen = "localhost" + normalizedListen
	}
	return adminConfigDiscovery{
		Source:   fmt.Sprintf("%s is %s.", source, listen),
		Listen:   listen,
		AdminURL: NormalizeAdminURL(normalizedListen),
	}
}

func extractCaddyfileGlobalBlock(content string) ([]string, bool) {
	lines := regexp.MustCompile(`\r?\n`).Split(content, -1)
	firstMeaningful := -1
	for index, line := range lines {
		if strings.TrimSpace(stripComment(line)) != "" {
			firstMeaningful = index
			break
		}
	}
	if firstMeaningful == -1 {
		return nil, false
	}
	if strings.TrimSpace(stripComment(lines[firstMeaningful])) != "{" {
		return nil, false
	}

	block := []string{}
	depth := 0
	for index := firstMeaningful; index < len(lines); index++ {
		line := lines[index]
		clean := stripComment(line)
		for _, char := range clean {
			if char == '{' {
				depth++
			}
			if char == '}' {
				depth--
			}
		}
		block = append(block, line)
		if index > firstMeaningful && depth <= 0 {
			return block, true
		}
	}
	return block, true
}

func stripComment(line string) string {
	var quote rune
	previous := rune(0)
	for index, char := range line {
		if (char == '"' || char == '\'') && previous != '\\' {
			if quote == char {
				quote = 0
			} else if quote == 0 {
				quote = char
			}
		}
		if char == '#' && quote == 0 {
			return line[:index]
		}
		previous = char
	}
	return line
}

func inferConfigFormat(configPath string, adapter string) string {
	if adapter == "json" {
		return "json"
	}
	if adapter == "caddyfile" {
		return "caddyfile"
	}
	if strings.HasSuffix(configPath, ".json") {
		return "json"
	}
	return "caddyfile"
}

func extractSystemdArgv(execStart string) []string {
	match := regexp.MustCompile(`argv\[\]=(.*?)(?:\s;\s|$)`).FindStringSubmatch(execStart)
	if match == nil || len(match) < 2 {
		return nil
	}
	return splitShellWords(match[1])
}

func splitShellWords(input string) []string {
	words := []string{}
	current := strings.Builder{}
	var quote rune
	escaping := false

	for _, char := range strings.TrimSpace(input) {
		if escaping {
			current.WriteRune(char)
			escaping = false
			continue
		}
		if char == '\\' && quote != '\'' {
			escaping = true
			continue
		}
		if (char == '"' || char == '\'') && quote == 0 {
			quote = char
			continue
		}
		if char == quote {
			quote = 0
			continue
		}
		if (char == ' ' || char == '\t' || char == '\n') && quote == 0 {
			if current.Len() > 0 {
				words = append(words, current.String())
				current.Reset()
			}
			continue
		}
		current.WriteRune(char)
	}
	if current.Len() > 0 {
		words = append(words, current.String())
	}
	return words
}

func parseProperties(output string) map[string]string {
	properties := map[string]string{}
	for _, line := range regexp.MustCompile(`\r?\n`).Split(output, -1) {
		separator := strings.Index(line, "=")
		if separator == -1 {
			continue
		}
		properties[line[:separator]] = line[separator+1:]
	}
	return properties
}

func readProcCmdline(pid int) []string {
	content, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return nil
	}
	parts := strings.Split(string(content), "\x00")
	argv := []string{}
	for _, part := range parts {
		if part != "" {
			argv = append(argv, part)
		}
	}
	if len(argv) == 0 {
		return nil
	}
	return argv
}

type discoveryCommandResult struct {
	ok       bool
	stdout   string
	stderr   string
	exitCode int
}

func runDiscoveryCommand(ctx context.Context, args []string, timeout time.Duration) discoveryCommandResult {
	if len(args) == 0 {
		return discoveryCommandResult{stderr: "empty command", exitCode: -1}
	}
	commandCtx := ctx
	cancel := func() {}
	if timeout > 0 {
		commandCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	cmd := exec.CommandContext(commandCtx, args[0], args[1:]...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	} else if err != nil {
		exitCode = -1
	}
	if err != nil && stderr.Len() == 0 {
		stderr.WriteString(err.Error())
	}
	return discoveryCommandResult{
		ok:       err == nil && exitCode == 0,
		stdout:   stdout.String(),
		stderr:   stderr.String(),
		exitCode: exitCode,
	}
}

func formatServiceNote(service SystemdServiceInfo) string {
	if !service.Exists {
		state := service.LoadState
		if state == "" {
			state = "unknown"
		}
		return fmt.Sprintf("systemd caddy.service not found (%s).", state)
	}
	state := strings.Join(nonEmpty(service.LoadState, service.ActiveState, service.SubState), "/")
	if state == "" {
		state = "unknown"
	}
	pid := ""
	if service.MainPID > 0 {
		pid = fmt.Sprintf(" pid %d", service.MainPID)
	}
	argvSource := ""
	if service.ArgvSource != "" {
		argvSource = fmt.Sprintf(" command from %s", service.ArgvSource)
	}
	return fmt.Sprintf("Found systemd caddy.service: %s%s%s.", state, pid, argvSource)
}

func numberOrZero(value string) int {
	if value == "" {
		return 0
	}
	number, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return number
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
