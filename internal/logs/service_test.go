package logs

import "testing"

func TestParseJournalJSONServiceLogLine(t *testing.T) {
	line := `2026-06-17T22:10:00Z dev caddy[123]: {"level":"error","ts":1716050000,"logger":"http.log.error","msg":"request failed","error":"dial tcp refused","file":"reverseproxy.go","line":123}`

	entry := ParseServiceLogLine(line)
	if !entry.Parsed {
		t.Fatal("expected parsed entry")
	}
	if entry.Unit != "caddy" || entry.PID != "123" {
		t.Fatalf("unexpected journal prefix: unit=%q pid=%q", entry.Unit, entry.PID)
	}
	if entry.Level != "error" || entry.Logger != "http.log.error" || entry.Message != "request failed" {
		t.Fatalf("unexpected json fields: level=%q logger=%q message=%q", entry.Level, entry.Logger, entry.Message)
	}
	if entry.Error != "dial tcp refused" || entry.File != "reverseproxy.go" || entry.Line != 123 {
		t.Fatalf("unexpected error fields: error=%q file=%q line=%d", entry.Error, entry.File, entry.Line)
	}
	if ServiceLogKind(entry) != "ERR" || !IsImportantServiceLogEntry(entry) {
		t.Fatal("expected error entry to be important")
	}
}

func TestParsePlainServiceLogLine(t *testing.T) {
	line := `2026-06-17T22:10:00Z dev caddy[123]: WARN admin admin endpoint disabled err=disabled`

	entry := ParseServiceLogLine(line)
	if !entry.Parsed {
		t.Fatal("expected parsed entry")
	}
	if entry.Level != "warn" || entry.Logger != "admin" || entry.Error != "disabled" {
		t.Fatalf("unexpected plain fields: level=%q logger=%q error=%q", entry.Level, entry.Logger, entry.Error)
	}
	if ServiceLogKind(entry) != "ERR" {
		t.Fatalf("kind=%q, want ERR due to err=", ServiceLogKind(entry))
	}
}

func TestFormatServiceLogOutputErrorOnly(t *testing.T) {
	output := `2026-06-17T22:10:00Z dev caddy[123]: INFO admin started
2026-06-17T22:10:01Z dev caddy[123]: ERROR http failed err=boom`

	formatted := FormatServiceLogOutput(output, true)
	if !containsAll(formatted, "TYPE TIME", "ERR", "boom") {
		t.Fatalf("formatted output missing expected error row:\n%s", formatted)
	}
	if containsAll(formatted, "started") {
		t.Fatalf("error-only output should not contain info line:\n%s", formatted)
	}
}
