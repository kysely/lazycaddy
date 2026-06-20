package app

import (
	"strconv"
	"testing"
	"time"

	"github.com/kysely/lazycaddy/internal/caddy"
	applogs "github.com/kysely/lazycaddy/internal/logs"
)

func TestNewStateDefaults(t *testing.T) {
	state := NewState()
	if state.SelectedIndex != -1 || state.SelectedAccessLogIdx != -1 {
		t.Fatalf("unexpected selected defaults: service=%d access=%d", state.SelectedIndex, state.SelectedAccessLogIdx)
	}
	if state.ActiveView != ViewServices || state.PreviousMainView != ViewServices || state.PreviousConfigView != ViewLogs {
		t.Fatalf("unexpected view defaults: %+v", state)
	}
	if state.LogsTimeWindow != LogsTimeWindowDay || state.SlowRequestThreshold != time.Second {
		t.Fatalf("unexpected log defaults: %+v", state)
	}
	if state.AccessLogStatsBySource == nil || state.UpstreamHealthBySource == nil {
		t.Fatal("expected initialized maps")
	}
}

func TestSetSourcesRestoresSelection(t *testing.T) {
	state := NewState()
	sources := []caddy.CaddySource{{ID: "one"}, {ID: "two"}, {ID: "three"}}
	state.SetSources(sources, "two", 0)
	if state.SelectedIndex != 1 || state.SelectedSource().ID != "two" {
		t.Fatalf("selection not restored by id: index=%d source=%+v", state.SelectedIndex, state.SelectedSource())
	}

	state.SetSources(sources[:2], "missing", 9)
	if state.SelectedIndex != 1 {
		t.Fatalf("fallback should clamp to last index, got %d", state.SelectedIndex)
	}

	state.SetSources(nil, "two", 0)
	if state.SelectedIndex != -1 || state.SelectedSource() != nil {
		t.Fatalf("empty sources should clear selection, got %d", state.SelectedIndex)
	}
}

func TestMoveServiceSelection(t *testing.T) {
	state := NewState()
	state.Sources = []caddy.CaddySource{{ID: "one"}, {ID: "two"}}
	state.MoveServiceSelection(1)
	if state.SelectedIndex != 0 {
		t.Fatalf("negative selection should move to 0, got %d", state.SelectedIndex)
	}
	state.MoveServiceSelection(10)
	if state.SelectedIndex != 1 {
		t.Fatalf("selection should clamp to 1, got %d", state.SelectedIndex)
	}
	state.MoveServiceSelection(-10)
	if state.SelectedIndex != 0 {
		t.Fatalf("selection should clamp to 0, got %d", state.SelectedIndex)
	}
}

func TestViewToggles(t *testing.T) {
	state := NewState()
	state.SetActiveView(ViewLogs)
	state.ToggleSystemView()
	if state.ActiveView != ViewSystem || state.PreviousMainView != ViewLogs {
		t.Fatalf("system toggle did not preserve previous main view: %+v", state)
	}
	state.ToggleSystemView()
	if state.ActiveView != ViewLogs {
		t.Fatalf("system toggle should return to logs, got %s", state.ActiveView)
	}

	state.ToggleConfigView()
	if state.ActiveView != ViewConfig || state.PreviousConfigView != ViewLogs {
		t.Fatalf("config toggle did not preserve previous view: %+v", state)
	}
	state.ToggleConfigView()
	if state.ActiveView != ViewLogs {
		t.Fatalf("config toggle should return to logs, got %s", state.ActiveView)
	}

	state.ToggleHelpView()
	if state.ActiveView != ViewHelp || state.PreviousHelpView != ViewLogs {
		t.Fatalf("help toggle did not preserve previous view: %+v", state)
	}
	state.ToggleHelpView()
	if state.ActiveView != ViewLogs {
		t.Fatalf("help toggle should return to logs, got %s", state.ActiveView)
	}
}

func TestVisibleAccessLogEntriesFiltersWindowAndReverses(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	recentOK := now.Add(-time.Hour).Unix()
	recentSlowError := now.Add(-2 * time.Hour).Unix()
	oldError := now.Add(-48 * time.Hour).Unix()

	state := NewState()
	state.SetAccessLogs(&applogs.CaddyAccessLogsResult{Available: true, OK: true, Output: joinLines(
		jsonAccessLine(recentOK, "GET", "/ok", 200, 10),
		jsonAccessLine(recentSlowError, "POST", "/slow-error", 500, 1500),
		jsonAccessLine(oldError, "GET", "/old-error", 500, 1500),
	)})

	visible := state.VisibleAccessLogEntries(now)
	if len(visible) != 2 {
		t.Fatalf("day window visible len=%d, want 2: %+v", len(visible), visible)
	}
	if visible[0].URI != "/slow-error" || visible[1].URI != "/ok" {
		t.Fatalf("entries should be newest-first within day window: %+v", visible)
	}

	state.SetLogFilters(true, false, LogsTimeWindowDay)
	visible = state.VisibleAccessLogEntries(now)
	if len(visible) != 1 || visible[0].URI != "/slow-error" {
		t.Fatalf("error-only day visible=%+v", visible)
	}

	state.SetLogFilters(false, true, LogsTimeWindowAll)
	visible = state.VisibleAccessLogEntries(now)
	if len(visible) != 2 || visible[0].URI != "/old-error" || visible[1].URI != "/slow-error" {
		t.Fatalf("slow all visible=%+v", visible)
	}
}

func TestAccessLogSelectionRestore(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	state := NewState()
	state.SetAccessLogs(&applogs.CaddyAccessLogsResult{Available: true, OK: true, Output: joinLines(
		jsonAccessLine(now.Add(-3*time.Hour).Unix(), "GET", "/one", 200, 10),
		jsonAccessLine(now.Add(-2*time.Hour).Unix(), "GET", "/two", 200, 10),
		jsonAccessLine(now.Add(-time.Hour).Unix(), "GET", "/three", 200, 10),
	)})

	state.MoveAccessLogSelection(1, now)
	state.MoveAccessLogSelection(1, now)
	if selected := state.SelectedAccessLogEntry(now); selected == nil || selected.URI != "/two" {
		t.Fatalf("selected=%+v, want /two", selected)
	}
	key := state.SelectedAccessLogEntryKey(now)

	state.SetAccessLogs(&applogs.CaddyAccessLogsResult{Available: true, OK: true, Output: joinLines(
		jsonAccessLine(now.Add(-4*time.Hour).Unix(), "GET", "/zero", 200, 10),
		jsonAccessLine(now.Add(-2*time.Hour).Unix(), "GET", "/two", 200, 10),
		jsonAccessLine(now.Add(-time.Hour).Unix(), "GET", "/three", 200, 10),
	)})
	state.RestoreAccessLogSelection(key, 0, now)
	if selected := state.SelectedAccessLogEntry(now); selected == nil || selected.URI != "/two" {
		t.Fatalf("restored selected=%+v, want /two", selected)
	}
}

func TestCountAccessLogsInLast24Hours(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	result := &applogs.CaddyAccessLogsResult{Available: true, OK: true, Output: joinLines(
		jsonAccessLine(now.Add(-time.Hour).Unix(), "GET", "/recent", 200, 10),
		jsonAccessLine(now.Add(-48*time.Hour).Unix(), "GET", "/old", 200, 10),
	)}
	count, ok := CountAccessLogsInLast24Hours(result, now)
	if !ok || count != 1 {
		t.Fatalf("count=%d ok=%v, want 1 true", count, ok)
	}

	state := NewState()
	source := &caddy.CaddySource{ID: "source"}
	state.UpdateAccessLogStats(source, result, now)
	stats := state.AccessLogStatsBySource["source"]
	if !stats.Available || !stats.OK || !stats.HasCount || stats.Count24h != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestTimeWindowFallsBackWhenNoTimestamps(t *testing.T) {
	state := NewState()
	state.SetAccessLogs(&applogs.CaddyAccessLogsResult{Available: true, OK: true, Output: "method=GET uri=/no-time status=200"})
	entries := state.VisibleAccessLogEntries(time.Now())
	if len(entries) != 1 || entries[0].URI != "/no-time" {
		t.Fatalf("entries without timestamps should not be hidden by day filter: %+v", entries)
	}
}

func TestVisibleServiceLogEntriesCachesAndFilters(t *testing.T) {
	state := NewState()
	state.SetServiceLogs(&applogs.CaddyLogsResult{OK: true, Output: joinLines(
		`2026-06-17T22:10:00Z dev caddy[123]: INFO admin started`,
		`2026-06-17T22:10:01Z dev caddy[123]: ERROR http failed err=boom`,
	)})

	entries := state.VisibleServiceLogEntries()
	if len(entries) != 2 || entries[0].Error != "boom" || entries[1].Message != "started" {
		t.Fatalf("unexpected visible service logs: %+v", entries)
	}
	if !state.parsedServiceLogCache.valid || !state.visibleServiceLogCache.valid {
		t.Fatal("expected service log caches to be valid")
	}

	state.SetLogFilters(true, false, state.LogsTimeWindow)
	entries = state.VisibleServiceLogEntries()
	if len(entries) != 1 || entries[0].Error != "boom" {
		t.Fatalf("unexpected error-only service logs: %+v", entries)
	}

	state.SetServiceLogs(&applogs.CaddyLogsResult{OK: true, Output: `2026-06-17T22:10:02Z dev caddy[123]: INFO admin reloaded`})
	if state.parsedServiceLogCache.valid || state.visibleServiceLogCache.valid {
		t.Fatal("SetServiceLogs should invalidate service log caches")
	}
}

func joinLines(values ...string) string {
	result := ""
	for index, value := range values {
		if index > 0 {
			result += "\n"
		}
		result += value
	}
	return result
}

func jsonAccessLine(ts int64, method string, uri string, status int, durationMS int) string {
	return `{"ts":` + strconv.FormatInt(ts, 10) + `,"request":{"method":"` + method + `","uri":"` + uri + `"},"status":` + strconv.Itoa(status) + `,"duration_ms":` + strconv.Itoa(durationMS) + `}`
}
