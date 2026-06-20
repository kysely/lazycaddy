package logs

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func splitLines(value string) []string {
	if value == "" {
		return []string{""}
	}
	return regexp.MustCompile(`\r?\n`).Split(value, -1)
}

func objectValue(value any) map[string]any {
	if object, ok := value.(map[string]any); ok && object != nil {
		return object
	}
	return map[string]any{}
}

func objectExists(value any) bool {
	object, ok := value.(map[string]any)
	return ok && object != nil
}

func normalizeHeaders(value any) map[string][]string {
	object, ok := value.(map[string]any)
	if !ok || object == nil {
		return nil
	}

	headers := map[string][]string{}
	for name, rawValue := range object {
		switch typed := rawValue.(type) {
		case []any:
			values := make([]string, 0, len(typed))
			for _, item := range typed {
				text := fmt.Sprint(item)
				if text != "" {
					values = append(values, text)
				}
			}
			if len(values) > 0 {
				headers[name] = values
			}
		case []string:
			values := make([]string, 0, len(typed))
			for _, item := range typed {
				if item != "" {
					values = append(values, item)
				}
			}
			if len(values) > 0 {
				headers[name] = values
			}
		case nil:
			continue
		default:
			headers[name] = []string{fmt.Sprint(rawValue)}
		}
	}
	if len(headers) == 0 {
		return nil
	}
	return headers
}

func firstHeaders(headers ...map[string][]string) map[string][]string {
	for _, value := range headers {
		if len(value) > 0 {
			return value
		}
	}
	return nil
}

func headerValue(headers map[string][]string, name string) string {
	for key, values := range headers {
		if strings.EqualFold(key, name) && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

func parseEmbeddedJSONObject(line string) (map[string]any, bool) {
	start := strings.Index(line, "{")
	if start == -1 {
		return nil, false
	}
	for end := len(line); end > start; end-- {
		candidate := strings.TrimSpace(line[start:end])
		if !strings.HasSuffix(candidate, "}") {
			continue
		}
		var parsed map[string]any
		decoder := json.NewDecoder(strings.NewReader(candidate))
		decoder.UseNumber()
		if err := decoder.Decode(&parsed); err == nil && parsed != nil {
			return parsed, true
		}
	}
	return nil, false
}

func timestampValue(value any) *time.Time {
	switch typed := value.(type) {
	case time.Time:
		return &typed
	case json.Number:
		if number, err := typed.Float64(); err == nil {
			return timestampFromNumber(number)
		}
	case float64:
		return timestampFromNumber(typed)
	case float32:
		return timestampFromNumber(float64(typed))
	case int:
		return timestampFromNumber(float64(typed))
	case int64:
		return timestampFromNumber(float64(typed))
	case string:
		return timestampFromString(typed)
	}
	return nil
}

func timestampFromNumber(value float64) *time.Time {
	if value == 0 {
		return nil
	}
	millis := value * 1000
	if value > 1_000_000_000_000 {
		millis = value
	}
	seconds := int64(millis / 1000)
	nanos := int64((millis - float64(seconds*1000)) * 1_000_000)
	parsed := time.Unix(seconds, nanos)
	return &parsed
}

func timestampFromString(value string) *time.Time {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	if strings.Contains(trimmed, "/") {
		parts := strings.SplitN(trimmed, " ", 2)
		parts[0] = strings.ReplaceAll(parts[0], "/", "-")
		trimmed = strings.Join(parts, " ")
	}

	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05.999999999",
		"2006-01-02T15:04:05",
		"Jan 2 15:04:05",
		"Jan _2 15:04:05",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, trimmed); err == nil {
			return &parsed
		}
	}
	return nil
}

func numberValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case json.Number:
		number, err := typed.Float64()
		return number, err == nil
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case string:
		number, err := strconv.ParseFloat(typed, 64)
		return number, err == nil
	default:
		return 0, false
	}
}

func intValue(value any) (int, bool) {
	number, ok := numberValue(value)
	if !ok {
		return 0, false
	}
	return int(number), true
}

func int64Value(value any) (int64, bool) {
	number, ok := numberValue(value)
	if !ok {
		return 0, false
	}
	return int64(number), true
}

func stringValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func firstAny(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func firstTime(values ...*time.Time) *time.Time {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func defaultString(value string, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func padRight(value string, width int) string {
	if len(value) >= width {
		return value[:width]
	}
	return value + strings.Repeat(" ", width-len(value))
}

func padLeft(value string, width int) string {
	if len(value) >= width {
		return value
	}
	return strings.Repeat(" ", width-len(value)) + value
}

func truncateMiddle(value string, maxLength int) string {
	if len(value) <= maxLength {
		return value
	}
	if maxLength <= 1 {
		return "…"
	}
	head := (maxLength) / 2
	tail := maxLength - head - len("…")
	if tail < 0 {
		tail = 0
	}
	return value[:head] + "…" + value[len(value)-tail:]
}
