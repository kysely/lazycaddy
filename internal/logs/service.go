package logs

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// ParsedServiceLogEntry is a normalized caddy.service log entry.
type ParsedServiceLogEntry struct {
	Raw       string
	Parsed    bool
	Timestamp *time.Time
	Unit      string
	PID       string
	Level     string
	Logger    string
	Message   string
	Error     string
	File      string
	Line      int
}

// ParseServiceLogOutput parses multiline caddy.service log output.
func ParseServiceLogOutput(output string) []ParsedServiceLogEntry {
	lines := splitLines(output)
	entries := make([]ParsedServiceLogEntry, 0, len(lines))
	for _, line := range lines {
		entry := ParseServiceLogLine(line)
		if strings.TrimSpace(entry.Raw) != "" || entry.Message != "" {
			entries = append(entries, entry)
		}
	}
	return entries
}

// ParseServiceLogLine parses one caddy.service log line.
func ParseServiceLogLine(line string) ParsedServiceLogEntry {
	journal := parseJournalPrefix(line)
	messageText := journal.message
	if messageText == "" {
		messageText = line
	}

	if object, ok := parseEmbeddedJSONObject(messageText); ok {
		lineNumber, _ := intValue(object["line"])
		return ParsedServiceLogEntry{
			Raw:       line,
			Parsed:    true,
			Timestamp: firstTime(timestampValue(object["ts"]), journal.timestamp),
			Unit:      journal.unit,
			PID:       journal.pid,
			Level:     stringValue(object["level"]),
			Logger:    stringValue(object["logger"]),
			Message:   firstNonEmpty(stringValue(object["msg"]), stringValue(object["message"])),
			Error:     firstNonEmpty(stringValue(object["error"]), stringValue(object["err"])),
			File:      stringValue(object["file"]),
			Line:      lineNumber,
		}
	}

	plain := parsePlainCaddyMessage(messageText)
	return ParsedServiceLogEntry{
		Raw:       line,
		Parsed:    journal.timestamp != nil || plain.level != "" || journal.unit != "",
		Timestamp: journal.timestamp,
		Unit:      journal.unit,
		PID:       journal.pid,
		Level:     plain.level,
		Logger:    plain.logger,
		Message:   firstNonEmpty(plain.message, messageText),
		Error:     plain.error,
	}
}

// FormatServiceLogOutput formats service logs as a compact table.
func FormatServiceLogOutput(output string, errorOnly bool) string {
	entries := ParseServiceLogOutput(output)
	visible := make([]ParsedServiceLogEntry, 0, len(entries))
	for _, entry := range entries {
		if !errorOnly || IsImportantServiceLogEntry(entry) {
			visible = append(visible, entry)
		}
	}

	if len(visible) == 0 {
		if errorOnly {
			return "No error/warning service log entries found."
		}
		return "No service log entries."
	}

	hasParsed := false
	for _, entry := range visible {
		if entry.Parsed {
			hasParsed = true
			break
		}
	}
	if !hasParsed {
		raw := make([]string, 0, len(visible))
		for _, entry := range visible {
			raw = append(raw, entry.Raw)
		}
		return strings.Join(raw, "\n")
	}

	lines := []string{serviceLogListHeader()}
	for _, entry := range visible {
		lines = append(lines, formatServiceLogEntry(entry))
	}
	return strings.Join(lines, "\n")
}

// IsImportantServiceLogEntry reports whether a service log entry is an error/warning or otherwise noteworthy.
func IsImportantServiceLogEntry(entry ParsedServiceLogEntry) bool {
	kind := ServiceLogKind(entry)
	if kind == "ERR" || kind == "WARN" {
		return true
	}
	return regexp.MustCompile(`(?i)\b(error|err|warn|warning|failed|failure|panic|fatal|unhealthy|timeout|refused)\b`).MatchString(entry.Raw)
}

// ServiceLogKind classifies a service log entry.
func ServiceLogKind(entry ParsedServiceLogEntry) string {
	if entry.Error != "" || regexp.MustCompile(`(?i)^(error|fatal|panic)$`).MatchString(entry.Level) {
		return "ERR"
	}
	if regexp.MustCompile(`(?i)^warn`).MatchString(entry.Level) {
		return "WARN"
	}
	if regexp.MustCompile(`(?i)^info$`).MatchString(entry.Level) {
		return "OK"
	}
	return "LOG"
}

func serviceLogListHeader() string {
	return "TYPE TIME     LOGGER                 MESSAGE"
}

func formatServiceLogEntry(entry ParsedServiceLogEntry) string {
	if !entry.Parsed {
		return entry.Raw
	}

	kind := padRight(ServiceLogKind(entry), 4)[:4]
	timeText := padRight("-", 8)
	if entry.Timestamp != nil {
		timeText = padRight(formatServiceTime(*entry.Timestamp), 8)
	}
	logger := padRight(truncateMiddle(defaultString(firstNonEmpty(entry.Logger, entry.Unit), "caddy"), 22), 22)
	message := truncateMiddle(defaultString(firstNonEmpty(entry.Message, entry.Error), entry.Raw), 78)
	suffix := ""
	if entry.Error != "" {
		suffix = "  " + entry.Error
	} else if entry.File != "" {
		suffix = "  " + entry.File
		if entry.Line != 0 {
			suffix += fmt.Sprintf(":%d", entry.Line)
		}
	}

	return fmt.Sprintf("%s %s %s %s%s", kind, timeText, logger, message, suffix)
}

type journalPrefix struct {
	timestamp *time.Time
	unit      string
	pid       string
	message   string
}

func parseJournalPrefix(line string) journalPrefix {
	shortISO := regexp.MustCompile(`^(\S+)\s+\S+\s+([^\s:]+?)(?:\[(\d+)\])?:\s?(.*)$`)
	if match := shortISO.FindStringSubmatch(line); match != nil && timestampValue(match[1]) != nil {
		return journalPrefix{
			timestamp: timestampValue(match[1]),
			unit:      match[2],
			pid:       match[3],
			message:   match[4],
		}
	}

	spacedTimestamp := regexp.MustCompile(`^(\S+\s+\S+)\s+\S+\s+([^\s:]+?)(?:\[(\d+)\])?:\s?(.*)$`)
	match := spacedTimestamp.FindStringSubmatch(line)
	if match == nil {
		return journalPrefix{message: line}
	}
	return journalPrefix{
		timestamp: timestampValue(match[1]),
		unit:      match[2],
		pid:       match[3],
		message:   match[4],
	}
}

type plainCaddyMessage struct {
	level   string
	logger  string
	message string
	error   string
}

func parsePlainCaddyMessage(message string) plainCaddyMessage {
	parts := strings.Fields(strings.TrimSpace(message))
	levelRe := regexp.MustCompile(`(?i)^(DEBUG|INFO|WARN|WARNING|ERROR|FATAL|PANIC)$`)
	levelIndex := -1
	for index, part := range parts {
		if levelRe.MatchString(part) {
			levelIndex = index
			break
		}
	}
	if levelIndex == -1 {
		return plainCaddyMessage{message: message}
	}

	result := plainCaddyMessage{level: strings.ToLower(parts[levelIndex])}
	if levelIndex+1 < len(parts) && !strings.Contains(parts[levelIndex+1], "=") {
		result.logger = parts[levelIndex+1]
	}
	restStart := levelIndex + 1
	if result.logger != "" {
		restStart = levelIndex + 2
	}
	if restStart < len(parts) {
		result.message = strings.Join(parts[restStart:], " ")
	}
	if match := regexp.MustCompile(`(?i)(?:error|err)=([^\s]+)`).FindStringSubmatch(result.message); match != nil {
		result.error = match[1]
	}
	return result
}

func formatServiceTime(value time.Time) string {
	return value.Format("15:04:05")
}
