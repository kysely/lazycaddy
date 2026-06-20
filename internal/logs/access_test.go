package logs

import (
	"strings"
	"testing"
)

func TestParseJSONAccessLogLine(t *testing.T) {
	line := `{"level":"info","ts":1716050000.5,"logger":"http.log.access","msg":"handled request","request":{"method":"GET","host":"example.com","uri":"/api/users","remote_ip":"203.0.113.10","remote_port":"52341","proto":"HTTP/2.0","headers":{"User-Agent":["curl/8"],"Referer":["https://example.com/"]},"tls":{"server_name":"example.com","proto":"h2"}},"status":502,"duration":0.123,"size":2048,"resp_headers":{"Content-Type":["application/json"]},"error":"dial tcp refused"}`

	entry := ParseAccessLogLine(line)
	if !entry.Parsed {
		t.Fatal("expected parsed entry")
	}
	if entry.Method != "GET" || entry.Host != "example.com" || entry.URI != "/api/users" {
		t.Fatalf("unexpected request fields: method=%q host=%q uri=%q", entry.Method, entry.Host, entry.URI)
	}
	if entry.Status != 502 {
		t.Fatalf("status=%d, want 502", entry.Status)
	}
	if entry.DurationMS != 123 {
		t.Fatalf("durationMS=%v, want 123", entry.DurationMS)
	}
	if entry.SizeBytes != 2048 {
		t.Fatalf("size=%d, want 2048", entry.SizeBytes)
	}
	if entry.UserAgent != "curl/8" || entry.Referer != "https://example.com/" {
		t.Fatalf("unexpected headers: user-agent=%q referer=%q", entry.UserAgent, entry.Referer)
	}
	if entry.TLS.ServerName != "example.com" || entry.TLS.Protocol != "h2" {
		t.Fatalf("unexpected tls: %+v", entry.TLS)
	}
	if !IsImportantAccessLogEntry(entry) {
		t.Fatal("expected 502 entry to be important")
	}
}

func TestParseConsoleAccessLogLine(t *testing.T) {
	line := `2026-06-17T22:10:00Z INFO http.log.access handled request method=POST uri=/submit status=201 duration_ms=12 size=42 remote_ip=127.0.0.1 request={"headers":{"User-Agent":["curl"]}}`

	entry := ParseAccessLogLine(line)
	if !entry.Parsed {
		t.Fatal("expected parsed entry")
	}
	if entry.Level != "info" || entry.Logger != "http.log.access" || entry.Message != "handled" {
		t.Fatalf("unexpected prefix: level=%q logger=%q message=%q", entry.Level, entry.Logger, entry.Message)
	}
	if entry.Method != "POST" || entry.URI != "/submit" || entry.Status != 201 {
		t.Fatalf("unexpected request fields: method=%q uri=%q status=%d", entry.Method, entry.URI, entry.Status)
	}
	if entry.DurationMS != 12 || entry.SizeBytes != 42 || entry.RemoteIP != "127.0.0.1" {
		t.Fatalf("unexpected metrics: duration=%v size=%d remote=%q", entry.DurationMS, entry.SizeBytes, entry.RemoteIP)
	}
}

func TestFormatAccessLogOutput(t *testing.T) {
	output := `{"request":{"method":"GET","uri":"/ok"},"status":200,"duration":0.005,"size":10}
{"request":{"method":"GET","uri":"/missing"},"status":404,"duration":0.002,"size":20}`

	formatted := FormatAccessLogOutput(output, true)
	if !containsAll(formatted, "TYPE CODE", "WARN", "404", "/missing") {
		t.Fatalf("formatted output did not contain expected important row:\n%s", formatted)
	}
	if containsAll(formatted, "/ok") {
		t.Fatalf("error-only output should not contain /ok:\n%s", formatted)
	}
}

func containsAll(value string, needles ...string) bool {
	for _, needle := range needles {
		if !strings.Contains(value, needle) {
			return false
		}
	}
	return true
}
