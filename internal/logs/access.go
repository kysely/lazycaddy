package logs

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ParsedAccessLogEntry is a normalized Caddy access log entry.
type ParsedAccessLogEntry struct {
	Raw             string
	Parsed          bool
	Timestamp       *time.Time
	Level           string
	Logger          string
	Message         string
	Method          string
	Host            string
	URI             string
	Status          int
	DurationMS      float64
	SizeBytes       int64
	RemoteIP        string
	RemotePort      string
	ClientIP        string
	Protocol        string
	Scheme          string
	UserAgent       string
	Referer         string
	RequestHeaders  map[string][]string
	ResponseHeaders map[string][]string
	TLS             AccessLogTLS
	Error           string
}

// AccessLogTLS contains TLS metadata from an access log request.
type AccessLogTLS struct {
	Version     string
	CipherSuite string
	ServerName  string
	Protocol    string
}

// ParseAccessLogOutput parses multiline access log output.
func ParseAccessLogOutput(output string) []ParsedAccessLogEntry {
	lines := splitLines(output)
	entries := make([]ParsedAccessLogEntry, 0, len(lines))
	for _, line := range lines {
		entry := ParseAccessLogLine(line)
		if strings.TrimSpace(entry.Raw) != "" || entry.Message != "" {
			entries = append(entries, entry)
		}
	}
	return entries
}

// ParseAccessLogLine parses one access log line.
func ParseAccessLogLine(line string) ParsedAccessLogEntry {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return ParsedAccessLogEntry{Raw: line}
	}

	if strings.HasPrefix(trimmed, "---") && strings.HasSuffix(trimmed, "---") {
		return ParsedAccessLogEntry{Raw: line, Message: trimmed}
	}

	if entry, ok := parseJSONAccessLog(trimmed, line); ok {
		return entry
	}
	if entry, ok := parseConsoleAccessLog(trimmed, line); ok {
		return entry
	}

	return ParsedAccessLogEntry{Raw: line}
}

// FormatAccessLogOutput formats multiline access log output as a compact table.
func FormatAccessLogOutput(output string, errorOnly bool) string {
	entries := ParseAccessLogOutput(output)
	parsedCount := 0
	for _, entry := range entries {
		if entry.Parsed {
			parsedCount++
		}
	}

	if parsedCount == 0 {
		rawLines := make([]string, 0, len(entries))
		for _, entry := range entries {
			if entry.Message != "" {
				rawLines = append(rawLines, entry.Message)
			} else {
				rawLines = append(rawLines, entry.Raw)
			}
		}
		raw := strings.TrimSpace(strings.Join(rawLines, "\n"))
		if !errorOnly {
			if raw == "" {
				return "No access log entries."
			}
			return raw
		}

		important := []string{}
		for _, line := range splitLines(raw) {
			if isImportantRawLine(line) {
				important = append(important, line)
			}
		}
		if len(important) > 0 {
			return strings.Join(important, "\n")
		}
		if raw != "" {
			return raw
		}
		return "No error/warning access log entries found."
	}

	formatted := []string{AccessLogListHeader()}
	for _, entry := range entries {
		if errorOnly && !IsImportantAccessLogEntry(entry) {
			continue
		}
		if entry.Parsed {
			formatted = append(formatted, FormatAccessLogEntry(entry))
		} else if entry.Message != "" {
			formatted = append(formatted, entry.Message)
		} else {
			formatted = append(formatted, entry.Raw)
		}
	}

	if len(formatted) == 1 {
		return "No error/warning access log entries found."
	}
	return strings.Join(formatted, "\n")
}

// AccessLogListHeader is the compact access log table header.
func AccessLogListHeader() string {
	return "TYPE CODE METHOD URI                                      TIME     SIZE REMOTE"
}

// FormatAccessLogEntry formats one access log entry as a compact row.
func FormatAccessLogEntry(entry ParsedAccessLogEntry) string {
	kind := padRight(StatusKind(entry.Status, entry.Error, entry.Level), 4)[:4]
	status := formatStatus(entry.Status)
	method := padRight(defaultString(entry.Method, "-"), 6)[:6]
	uri := padRight(truncateMiddle(defaultString(firstNonEmpty(entry.URI, entry.Message), "-"), 38), 38)
	duration := padLeft(formatDuration(entry.DurationMS), 8)
	size := padLeft(formatBytes(entry.SizeBytes), 8)
	remote := padRight(truncateMiddle(defaultString(entry.RemoteIP, "-"), 16), 16)
	suffix := ""
	if entry.Error != "" {
		suffix = "  " + entry.Error
	}

	return fmt.Sprintf("%s %s %s %s %s %s %s%s", kind, status, method, uri, duration, size, remote, suffix)
}

// IsImportantAccessLogEntry reports whether an entry is an error/warning or otherwise noteworthy.
func IsImportantAccessLogEntry(entry ParsedAccessLogEntry) bool {
	if entry.Parsed {
		kind := StatusKind(entry.Status, entry.Error, entry.Level)
		if kind == "WARN" || kind == "ERR" {
			return true
		}
	}

	return isImportantRawLine(entry.Raw)
}

// StatusKind classifies an access log status/error/level.
func StatusKind(status int, errorText string, level string) string {
	if errorText != "" || regexp.MustCompile(`(?i)^(error|fatal|panic)$`).MatchString(level) {
		return "ERR"
	}
	if regexp.MustCompile(`(?i)^warn`).MatchString(level) {
		return "WARN"
	}
	if status == 0 {
		return "LOG"
	}
	if status >= 500 {
		return "ERR"
	}
	if status >= 400 {
		return "WARN"
	}
	return "OK"
}

func parseJSONAccessLog(trimmed string, raw string) (ParsedAccessLogEntry, bool) {
	var value map[string]any
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return ParsedAccessLogEntry{}, false
	}
	if value == nil {
		return ParsedAccessLogEntry{}, false
	}
	return entryFromAccessObject(value, raw), true
}

func parseConsoleAccessLog(trimmed string, raw string) (ParsedAccessLogEntry, bool) {
	prefix := parseAccessConsolePrefix(trimmed)
	fields := parseConsoleFields(trimmed)
	if len(fields) > 0 {
		object := map[string]any{}
		for key, value := range fields {
			object[key] = parseConsoleValue(value)
		}
		entry := entryFromAccessObject(object, raw)
		applyAccessConsolePrefix(&entry, prefix)
		if entry.Parsed {
			return entry, true
		}
	}

	if object, ok := parseEmbeddedJSONObject(trimmed); ok {
		entry := entryFromAccessObject(object, raw)
		applyAccessConsolePrefix(&entry, prefix)
		if entry.Parsed {
			return entry, true
		}
	}

	return ParsedAccessLogEntry{}, false
}

func entryFromAccessObject(value map[string]any, raw string) ParsedAccessLogEntry {
	request := objectValue(value["request"])
	requestHeaders := normalizeHeaders(request["headers"])
	response := objectValue(value["response"])
	responseHeaders := firstHeaders(
		normalizeHeaders(value["resp_headers"]),
		normalizeHeaders(value["response_headers"]),
		normalizeHeaders(response["headers"]),
	)
	tls := objectValue(request["tls"])
	durationSeconds, hasDurationSeconds := numberValue(value["duration"])
	durationMS, hasDurationMS := numberValue(value["duration_ms"])
	if !hasDurationMS && hasDurationSeconds {
		durationMS = durationSeconds * 1000
		hasDurationMS = true
	}
	status, _ := intValue(value["status"])
	size, _ := int64Value(firstAny(value["size"], value["resp_headers_size"], value["bytes_written"]))
	message := firstNonEmpty(stringValue(value["msg"]), stringValue(value["message"]))
	method := firstNonEmpty(stringValue(request["method"]), stringValue(value["method"]))
	uri := firstNonEmpty(stringValue(request["uri"]), stringValue(value["uri"]), stringValue(value["path"]))
	timestamp := firstTime(timestampValue(value["ts"]), timestampValue(value["time"]), timestampValue(value["timestamp"]))

	entry := ParsedAccessLogEntry{
		Raw:             raw,
		Parsed:          status != 0 || method != "" || uri != "" || objectExists(value["request"]),
		Timestamp:       timestamp,
		Level:           stringValue(value["level"]),
		Logger:          stringValue(value["logger"]),
		Message:         message,
		Method:          method,
		Host:            firstNonEmpty(stringValue(request["host"]), stringValue(value["host"])),
		URI:             uri,
		Status:          status,
		SizeBytes:       size,
		RemoteIP:        firstNonEmpty(stringValue(request["remote_ip"]), stringValue(value["remote_ip"]), stringValue(value["remote"]), stringValue(value["client_ip"])),
		RemotePort:      firstNonEmpty(stringValue(request["remote_port"]), stringValue(value["remote_port"])),
		ClientIP:        firstNonEmpty(stringValue(request["client_ip"]), stringValue(value["client_ip"])),
		Protocol:        firstNonEmpty(stringValue(request["proto"]), stringValue(value["proto"])),
		Scheme:          firstNonEmpty(stringValue(request["scheme"]), stringValue(value["scheme"])),
		RequestHeaders:  requestHeaders,
		ResponseHeaders: responseHeaders,
		TLS: AccessLogTLS{
			Version:     stringValue(tls["version"]),
			CipherSuite: firstNonEmpty(stringValue(tls["cipher_suite"]), stringValue(tls["cipherSuite"])),
			ServerName:  firstNonEmpty(stringValue(tls["server_name"]), stringValue(tls["serverName"])),
			Protocol:    firstNonEmpty(stringValue(tls["proto"]), stringValue(tls["protocol"])),
		},
		Error: firstNonEmpty(stringValue(value["error"]), stringValue(value["err"])),
	}
	if hasDurationMS {
		entry.DurationMS = durationMS
	}
	entry.UserAgent = firstNonEmpty(headerValue(requestHeaders, "User-Agent"), stringValue(value["user_agent"]))
	entry.Referer = firstNonEmpty(headerValue(requestHeaders, "Referer"), stringValue(value["referer"]))
	return entry
}

type accessConsolePrefix struct {
	Timestamp *time.Time
	Level     string
	Logger    string
	Message   string
}

func applyAccessConsolePrefix(entry *ParsedAccessLogEntry, prefix accessConsolePrefix) {
	if prefix.Timestamp != nil && entry.Timestamp == nil {
		entry.Timestamp = prefix.Timestamp
	}
	if prefix.Level != "" && entry.Level == "" {
		entry.Level = prefix.Level
	}
	if prefix.Logger != "" && entry.Logger == "" {
		entry.Logger = prefix.Logger
	}
	if prefix.Message != "" && entry.Message == "" {
		entry.Message = prefix.Message
	}
}

func parseAccessConsolePrefix(line string) accessConsolePrefix {
	parts := strings.Fields(strings.TrimSpace(line))
	var timestamp *time.Time
	consumedTimestampParts := 0

	if len(parts) >= 2 {
		if parsed := timestampValue(parts[0] + " " + parts[1]); parsed != nil {
			timestamp = parsed
			consumedTimestampParts = 2
		} else if parsed := timestampValue(parts[0]); parsed != nil {
			timestamp = parsed
			consumedTimestampParts = 1
		}
	} else if len(parts) == 1 {
		if parsed := timestampValue(parts[0]); parsed != nil {
			timestamp = parsed
			consumedTimestampParts = 1
		}
	}

	levelIndex := -1
	levelRe := regexp.MustCompile(`(?i)^(DEBUG|INFO|WARN|WARNING|ERROR|FATAL|PANIC)$`)
	for index := consumedTimestampParts; index < len(parts); index++ {
		if levelRe.MatchString(parts[index]) {
			levelIndex = index
			break
		}
	}
	if levelIndex < 0 {
		return accessConsolePrefix{Timestamp: timestamp}
	}

	prefix := accessConsolePrefix{Timestamp: timestamp, Level: strings.ToLower(parts[levelIndex])}
	if levelIndex+1 < len(parts) {
		prefix.Logger = parts[levelIndex+1]
	}
	for _, part := range parts[levelIndex+2:] {
		if !strings.Contains(part, "=") && !strings.HasPrefix(part, "{") {
			prefix.Message = part
			break
		}
	}
	return prefix
}

func parseConsoleFields(line string) map[string]string {
	fields := map[string]string{}
	keyRegex := regexp.MustCompile(`(?:^|\s)([A-Za-z_][\w.-]*)=`)
	matches := keyRegex.FindAllStringSubmatchIndex(line, -1)
	for index, match := range matches {
		if len(match) < 4 || match[2] < 0 || match[3] < 0 {
			continue
		}
		key := line[match[2]:match[3]]
		valueStart := match[1]
		valueEnd := len(line)
		if index+1 < len(matches) {
			valueEnd = matches[index+1][0]
		}
		fields[key] = strings.TrimSuffix(strings.TrimSpace(line[valueStart:valueEnd]), ",")
	}
	return fields
}

func parseConsoleValue(value string) any {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if (strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}")) || (strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")) {
		var parsed any
		decoder := json.NewDecoder(strings.NewReader(trimmed))
		decoder.UseNumber()
		if err := decoder.Decode(&parsed); err == nil {
			return parsed
		}
		return trimmed
	}
	if (strings.HasPrefix(trimmed, `"`) && strings.HasSuffix(trimmed, `"`)) || (strings.HasPrefix(trimmed, `'`) && strings.HasSuffix(trimmed, `'`)) {
		if unquoted, err := strconv.Unquote(trimmed); err == nil {
			return unquoted
		}
		return strings.Trim(trimmed, `"'`)
	}
	if number, err := strconv.ParseFloat(trimmed, 64); err == nil && !math.IsNaN(number) && !math.IsInf(number, 0) {
		return number
	}
	return trimmed
}

func isImportantRawLine(line string) bool {
	return regexp.MustCompile(`(?i)\b(error|err|warn|warning|failed|failure|panic|fatal|unhealthy|timeout|refused)\b`).MatchString(line) ||
		regexp.MustCompile(`(?i)\bstatus[=: ]+5\d\d\b`).MatchString(line) ||
		regexp.MustCompile(`\s[45]\d\d\s`).MatchString(line)
}

func formatStatus(status int) string {
	if status == 0 {
		return "---"
	}
	return padLeft(strconv.Itoa(status), 3)
}

func formatDuration(durationMS float64) string {
	if durationMS == 0 || math.IsNaN(durationMS) || math.IsInf(durationMS, 0) {
		return "-"
	}
	if durationMS < 1 {
		return fmt.Sprintf("%.1fms", durationMS)
	}
	if durationMS < 1000 {
		return fmt.Sprintf("%.0fms", durationMS)
	}
	return fmt.Sprintf("%.2fs", durationMS/1000)
}

func formatBytes(bytes int64) string {
	if bytes == 0 {
		return "-"
	}
	if bytes < 1024 {
		return fmt.Sprintf("%dB", bytes)
	}
	if bytes < 1024*1024 {
		return fmt.Sprintf("%.1fKB", float64(bytes)/1024)
	}
	return fmt.Sprintf("%.1fMB", float64(bytes)/(1024*1024))
}
