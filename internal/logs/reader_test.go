package logs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kysely/lazycaddy/internal/caddy"
)

func TestFetchAccessLogsForSourceNoSource(t *testing.T) {
	result := FetchAccessLogsForSource(context.Background(), nil, nil, 100)
	if result.OK || result.Available {
		t.Fatalf("expected unavailable failed result, got %+v", result)
	}
	if result.Output != "No source selected." {
		t.Fatalf("output=%q", result.Output)
	}
}

func TestFetchAccessLogsForSourceNoAccessLogs(t *testing.T) {
	source := &caddy.CaddySource{Hosts: []string{"example.com"}}
	result := FetchAccessLogsForSource(context.Background(), source, nil, 100)
	if result.OK || result.Available {
		t.Fatalf("expected unavailable failed result, got %+v", result)
	}
	if !strings.Contains(result.Output, "Access logs are not configured for example.com") {
		t.Fatalf("unexpected output: %q", result.Output)
	}
	if len(result.Details) != 1 {
		t.Fatalf("details=%v", result.Details)
	}
}

func TestFetchAccessLogsForSourceFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "access.log")
	content := strings.Join([]string{
		`{"request":{"method":"GET","uri":"/one"},"status":200}`,
		`{"request":{"method":"GET","uri":"/two"},"status":404}`,
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	source := &caddy.CaddySource{
		Hosts: []string{"example.com"},
		AccessLogs: []caddy.CaddyAccessLog{{
			LoggerName:   "log0",
			WriterOutput: "file",
			Filename:     path,
			Source:       "example.com",
		}},
	}

	result := FetchAccessLogsForSource(context.Background(), source, nil, 1)
	if !result.OK || !result.Available {
		t.Fatalf("expected available ok result, got %+v", result)
	}
	if !strings.Contains(result.Output, accessLogHeader(path)) || !strings.Contains(result.Output, "/two") {
		t.Fatalf("unexpected output: %q", result.Output)
	}
	if strings.Contains(result.Output, "/one") {
		t.Fatalf("tail -n 1 should not include first line: %q", result.Output)
	}
}

func TestFetchAccessLogsForSourceJournalFilter(t *testing.T) {
	serviceLogs := &CaddyLogsResult{Output: strings.Join([]string{
		`2026-06-17T22:10:00Z dev caddy[123]: unrelated host other.example`,
		`2026-06-17T22:10:01Z dev caddy[123]: example.com GET /ok status=200`,
		`2026-06-17T22:10:02Z dev caddy[123]: logger_id_abc GET /by-logger status=200`,
	}, "\n")}
	source := &caddy.CaddySource{
		Hosts: []string{"example.com"},
		AccessLogs: []caddy.CaddyAccessLog{{
			LoggerName:   "default",
			LoggerID:     "logger_id_abc",
			WriterOutput: "stdout",
			Source:       "example.com",
		}},
	}

	result := FetchAccessLogsForSource(context.Background(), source, serviceLogs, 100)
	if !result.OK || !result.Available {
		t.Fatalf("expected available ok result, got %+v", result)
	}
	if !strings.Contains(result.Output, "example.com GET /ok") || !strings.Contains(result.Output, "logger_id_abc GET /by-logger") {
		t.Fatalf("missing filtered journal lines: %q", result.Output)
	}
	if strings.Contains(result.Output, "unrelated host") {
		t.Fatalf("unrelated line should have been filtered out: %q", result.Output)
	}
}
