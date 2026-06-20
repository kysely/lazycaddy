package ui

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kysely/lazycaddy/internal/app"
	"github.com/kysely/lazycaddy/internal/caddy"
	applogs "github.com/kysely/lazycaddy/internal/logs"
)

func TestConfigAndServiceLogsMessagesStartDependentRefreshes(t *testing.T) {
	model := Model{State: app.NewState(), refreshSeq: 1, configRefreshing: true, serviceLogsRefreshing: true}
	config := decodeJSON(t, `{"apps":{"http":{"servers":{"srv0":{"routes":[{"match":[{"host":["example.com"]}],"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"127.0.0.1:1"}]}]}]}}}}}`)

	updated, cmd := model.Update(configLoadedMsg{seq: 1, result: caddy.ConfigLoadResult{OK: true, Config: config, FetchedAt: time.Now()}})
	model = updated.(Model)
	if model.configRefreshing {
		t.Fatal("config refresh flag should be cleared")
	}
	if !model.healthRefreshing {
		t.Fatal("config load with selected source should start health refresh")
	}
	if model.accessLogsRefreshing {
		t.Fatal("access logs should wait until service logs are loaded")
	}
	if len(model.State.Sources) != 1 || model.State.SelectedSource() == nil {
		t.Fatalf("expected one selected source, got %+v", model.State.Sources)
	}
	if cmd == nil {
		t.Fatal("expected health command")
	}

	updated, cmd = model.Update(serviceLogsLoadedMsg{seq: 1, result: applogs.CaddyLogsResult{OK: true, Output: "logs", FetchedAt: time.Now()}})
	model = updated.(Model)
	if model.serviceLogsRefreshing {
		t.Fatal("service log refresh flag should be cleared")
	}
	if !model.accessLogsRefreshing {
		t.Fatal("service logs loaded after config should start access log refresh")
	}
	if cmd == nil {
		t.Fatal("expected access log command")
	}
}

func TestStaleMessagesAreIgnored(t *testing.T) {
	model := Model{State: app.NewState(), refreshSeq: 2, configRefreshing: true}
	updated, _ := model.Update(configLoadedMsg{seq: 1, result: caddy.ConfigLoadResult{OK: true, Config: map[string]any{}}})
	model = updated.(Model)
	if !model.configRefreshing {
		t.Fatal("stale config message should not clear refresh flag")
	}
	if model.State.LastLoad != nil {
		t.Fatal("stale config message should not update state")
	}
}

func TestRefreshKeyIgnoredWhileRefreshing(t *testing.T) {
	model := Model{State: app.NewState(), refreshSeq: 1, configRefreshing: true}
	updated, cmd := model.Update(keyMsg("r"))
	model = updated.(Model)
	if model.refreshSeq != 1 {
		t.Fatalf("refresh seq changed while in flight: %d", model.refreshSeq)
	}
	if cmd != nil {
		t.Fatal("expected no command while refresh is in flight")
	}
}

func TestRefreshKeyStartsFullRefresh(t *testing.T) {
	model := Model{State: app.NewState(), refreshSeq: 1}
	updated, cmd := model.Update(keyMsg("r"))
	model = updated.(Model)
	if model.refreshSeq != 2 || !model.configRefreshing || !model.serviceLogsRefreshing {
		t.Fatalf("refresh was not started correctly: %+v", model)
	}
	if cmd == nil {
		t.Fatal("expected refresh command")
	}
}

func TestAllAccessLogsLoadedUpdatesSelectedLogsAndStats(t *testing.T) {
	state := app.NewState()
	source := caddy.CaddySource{ID: "srv0:example.com", Hosts: []string{"example.com"}}
	state.SetSources([]caddy.CaddySource{source}, source.ID, 0)
	model := Model{State: state, refreshSeq: 1, accessLogsRefreshing: true}
	logs := applogs.CaddyAccessLogsResult{OK: true, Available: true, Output: `{"ts":1716050000,"request":{"method":"GET","uri":"/"},"status":200}`}

	updated, _ := model.Update(allAccessLogsLoadedMsg{seq: 1, results: []sourceAccessLogsResult{{source: source, logs: logs}}})
	model = updated.(Model)
	if model.accessLogsRefreshing {
		t.Fatal("access log refresh flag should be cleared")
	}
	if model.State.LastAccessLogs == nil || model.State.LastAccessLogs.Output != logs.Output {
		t.Fatalf("selected access logs not updated: %+v", model.State.LastAccessLogs)
	}
	if stats, ok := model.State.AccessLogStatsBySource[source.ID]; !ok || !stats.Available || !stats.OK {
		t.Fatalf("stats not updated: %+v ok=%v", stats, ok)
	}
}

func TestServicesHomeDoesNotRenderHeadlineOrEndpoint(t *testing.T) {
	state := app.NewState()
	state.LastLoad = &caddy.ConfigLoadResult{OK: true, StatusCode: 200}
	model := Model{State: state, Discovery: caddy.AdminAPIDiscovery{AdminURL: caddy.DefaultAdminAPIURL, SourceLabel: "Caddy default"}, width: 80, height: 20}

	view := stripANSI(model.View())
	if strings.Contains(view, "lazycaddy") || strings.Contains(view, caddy.DefaultAdminAPIURL) || strings.Contains(view, "Caddy default") {
		t.Fatalf("services home should not render headline or endpoint/discovery summary:\n%s", view)
	}
}

func TestViewFillsScreenToClearPreviousContent(t *testing.T) {
	state := app.NewState()
	model := Model{State: state, width: 80, height: 20}
	lines := strings.Split(model.View(), "\n")
	if len(lines) < 20 {
		t.Fatalf("view rendered %d lines, want at least terminal height", len(lines))
	}
}

func TestNonHomeHeaderDoesNotIncludeGlobalAdminStatus(t *testing.T) {
	state := app.NewState()
	state.SetActiveView(app.ViewConfig)
	state.LastLoad = &caddy.ConfigLoadResult{OK: true, StatusCode: 200}
	model := Model{State: state, width: 80, height: 20}

	firstLine := strings.Split(stripANSI(model.View()), "\n")[1]
	if strings.Contains(firstLine, "admin") || strings.Contains(firstLine, "●") {
		t.Fatalf("header should only contain title, got %q", firstLine)
	}
}

func TestServicesViewRendersRowsAndStatus(t *testing.T) {
	state := app.NewState()
	sourceOne := caddy.CaddySource{
		ID: "srv0:example.com", ServerName: "srv0", Hosts: []string{"example.com"},
		Routes:     []caddy.CaddyRouteRule{{Actions: []caddy.CaddyRouteAction{{Kind: "reverse_proxy", Upstreams: []caddy.CaddyUpstream{{Label: "localhost:8080"}}}}}},
		ProxyCount: 1,
		AccessLogs: []caddy.CaddyAccessLog{{LoggerName: "access", WriterOutput: "file"}},
	}
	sourceTwo := caddy.CaddySource{
		ID: "srv0:api.example.com", ServerName: "srv0", Hosts: []string{"api.example.com"},
		Routes: []caddy.CaddyRouteRule{{}, {}}, ProxyCount: 1,
	}
	state.SetSources([]caddy.CaddySource{sourceOne, sourceTwo}, sourceOne.ID, 0)
	state.LastLoad = &caddy.ConfigLoadResult{OK: true}
	state.AccessLogStatsBySource[sourceOne.ID] = app.AccessLogStats{Available: true, OK: true, Count24h: 42, HasCount: true}
	state.UpstreamHealthBySource[sourceOne.ID] = []caddy.UpstreamHealthResult{{Status: caddy.UpstreamHealthOK}}
	model := Model{State: state, Discovery: caddy.AdminAPIDiscovery{AdminURL: caddy.DefaultAdminAPIURL, SourceLabel: "test"}, width: 100}

	view := stripANSI(model.View())
	for _, want := range []string{"example.com", "≡ 42 reqs", "1 route", "localhost:8080", "api.example.com", "≡ logs off", "2 routes", "→ logs"} {
		if !strings.Contains(view, want) {
			t.Fatalf("services view missing %q:\n%s", want, view)
		}
	}
	for _, unwanted := range []string{"Services", "› example.com"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("services view should not contain %q:\n%s", unwanted, view)
		}
	}
}

func TestServiceSelectionKeysStartSelectedRefresh(t *testing.T) {
	state := app.NewState()
	sources := []caddy.CaddySource{
		{ID: "one", Hosts: []string{"one.test"}, Routes: []caddy.CaddyRouteRule{{Actions: []caddy.CaddyRouteAction{{Kind: "reverse_proxy"}}}}, ProxyCount: 1},
		{ID: "two", Hosts: []string{"two.test"}, Routes: []caddy.CaddyRouteRule{{Actions: []caddy.CaddyRouteAction{{Kind: "reverse_proxy"}}}}, ProxyCount: 1},
	}
	state.SetSources(sources, "one", 0)
	state.LastLogs = &applogs.CaddyLogsResult{OK: true, Output: ""}
	model := Model{State: state, refreshSeq: 3}

	updated, cmd := model.Update(keyMsg("j"))
	model = updated.(Model)
	if model.State.SelectedSource().ID != "two" {
		t.Fatalf("selected source=%+v, want two", model.State.SelectedSource())
	}
	if !model.healthRefreshing || model.healthRefreshingSourceID != "two" || !model.accessLogsRefreshing {
		t.Fatalf("expected health/access refresh for selected source: %+v", model)
	}
	if cmd == nil {
		t.Fatal("expected selection refresh command")
	}
}

func TestServicesEnterOpensLogsAndBackReturns(t *testing.T) {
	state := app.NewState()
	state.SetSources([]caddy.CaddySource{{ID: "one", Hosts: []string{"one.test"}}}, "one", 0)
	model := Model{State: state}

	updated, _ := model.Update(keyMsg("l"))
	model = updated.(Model)
	if model.State.ActiveView != app.ViewLogs {
		t.Fatalf("active view=%s, want logs", model.State.ActiveView)
	}
	updated, _ = model.Update(keyMsg("h"))
	model = updated.(Model)
	if model.State.ActiveView != app.ViewServices {
		t.Fatalf("active view=%s, want services", model.State.ActiveView)
	}
}

func TestLogsViewRendersRequestTableAndFilters(t *testing.T) {
	state := app.NewState()
	state.SetSources([]caddy.CaddySource{{ID: "one", Hosts: []string{"one.test"}}}, "one", 0)
	state.SetActiveView(app.ViewLogs)
	now := time.Now().Unix()
	state.SetAccessLogs(&applogs.CaddyAccessLogsResult{OK: true, Available: true, Output: joinLines(
		fmt.Sprintf(`{"ts":%d,"request":{"method":"GET","uri":"/ok"},"status":200,"duration_ms":25}`, now-60),
		fmt.Sprintf(`{"ts":%d,"request":{"method":"POST","uri":"/bad"},"status":500,"duration_ms":1500}`, now),
	)})
	model := Model{State: state, width: 100, height: 30}

	view := stripANSI(model.View())
	if strings.Contains(view, "lazycaddy") {
		t.Fatalf("logs page should not render lazycaddy heading:\n%s", view)
	}
	for _, want := range []string{"one.test › logs", "TIME", "METHOD", "PATH", "LATENCY", "/bad", "/ok", "1 slow"} {
		if !strings.Contains(view, want) {
			t.Fatalf("logs view missing %q:\n%s", want, view)
		}
	}
	viewLines := strings.Split(view, "\n")
	filterLine := findLineContaining(viewLines, "day")
	if !strings.Contains(filterLine, "slow") || strings.Index(filterLine, "slow") < 40 {
		t.Fatalf("slow/error filters should be right-aligned, filter line=%q\n%s", filterLine, view)
	}
	pageInfoIndex := lineIndexContaining(viewLines, "1 slow")
	headerIndex := lineIndexContaining(viewLines, "TIME")
	badIndex := lineIndexContaining(viewLines, "/bad")
	footerIndex := lineIndexContaining(viewLines, "← back")
	if !(pageInfoIndex > headerIndex && pageInfoIndex > badIndex) {
		t.Fatalf("page info should be below table content; header=%d bad=%d page=%d\n%s", headerIndex, badIndex, pageInfoIndex, view)
	}
	if footerIndex == -1 || pageInfoIndex != footerIndex-1 {
		t.Fatalf("page info should be pinned directly above footer; page=%d footer=%d\n%s", pageInfoIndex, footerIndex, view)
	}

	updated, _ := model.Update(keyMsg("e"))
	model = updated.(Model)
	view = stripANSI(model.View())
	if !model.State.LogsErrorOnly || !strings.Contains(view, "/bad") || strings.Contains(view, "/ok") {
		t.Fatalf("error filter failed; errorOnly=%v view=\n%s", model.State.LogsErrorOnly, view)
	}

	updated, _ = model.Update(keyMsg("a"))
	model = updated.(Model)
	if model.State.LogsTimeWindow != app.LogsTimeWindowAll {
		t.Fatalf("time window=%s, want all", model.State.LogsTimeWindow)
	}
}

func TestEnteringLogsSelectsFirstRequestAndFooterOmitsFilterHints(t *testing.T) {
	state := app.NewState()
	state.SetSources([]caddy.CaddySource{{ID: "one", Hosts: []string{"one.test"}}}, "one", 0)
	state.SetAccessLogs(&applogs.CaddyAccessLogsResult{OK: true, Available: true, Output: joinLines(
		`{"request":{"method":"GET","uri":"/one"},"status":200,"duration_ms":25}`,
		`{"request":{"method":"GET","uri":"/two"},"status":200,"duration_ms":25}`,
	)})
	model := Model{State: state, width: 100, height: 30}

	updated, _ := model.Update(keyMsg("l"))
	model = updated.(Model)
	if model.State.ActiveView != app.ViewLogs || model.State.SelectedAccessLogIdx != 0 {
		t.Fatalf("expected logs view with first request selected, view=%s selected=%d", model.State.ActiveView, model.State.SelectedAccessLogIdx)
	}
	view := stripANSI(model.View())
	if strings.Contains(view, "s slow") || strings.Contains(view, "e errors") || strings.Contains(view, "d/w/a") {
		t.Fatalf("logs footer should not include filter key hints:\n%s", view)
	}
}

func TestLogsSelectionAndDetailNavigation(t *testing.T) {
	state := app.NewState()
	state.SetSources([]caddy.CaddySource{{ID: "one", Hosts: []string{"one.test"}}}, "one", 0)
	state.SetActiveView(app.ViewLogs)
	state.SetAccessLogs(&applogs.CaddyAccessLogsResult{OK: true, Available: true, Output: joinLines(
		`{"request":{"method":"GET","uri":"/one"},"status":200,"duration_ms":25}`,
		`{"request":{"method":"GET","uri":"/two"},"status":200,"duration_ms":25}`,
	)})
	state.SelectedAccessLogIdx = 0
	model := Model{State: state, width: 100, height: 30}

	updated, _ := model.Update(keyMsg("j"))
	model = updated.(Model)
	if model.State.SelectedAccessLogIdx != 1 {
		t.Fatalf("selected access index=%d, want 1", model.State.SelectedAccessLogIdx)
	}
	updated, _ = model.Update(keyMsg("l"))
	model = updated.(Model)
	if model.State.ActiveView != app.ViewLogDetail {
		t.Fatalf("active view=%s, want log detail", model.State.ActiveView)
	}
	updated, _ = model.Update(keyMsg("h"))
	model = updated.(Model)
	if model.State.ActiveView != app.ViewLogs {
		t.Fatalf("active view=%s, want logs", model.State.ActiveView)
	}
}

func TestRequestDetailFooterOmitsRefreshHint(t *testing.T) {
	state := app.NewState()
	state.SetSources([]caddy.CaddySource{{ID: "one", Hosts: []string{"one.test"}}}, "one", 0)
	state.SetActiveView(app.ViewLogDetail)
	state.SetAccessLogs(&applogs.CaddyAccessLogsResult{OK: true, Available: true, Output: `{"request":{"method":"GET","uri":"/one"},"status":200,"duration_ms":25}`})
	model := Model{State: state, width: 100, height: 30}

	view := stripANSI(model.View())
	if strings.Contains(view, "r refresh") {
		t.Fatalf("request detail footer should not include refresh hint:\n%s", view)
	}
}

func TestRequestDetailViewRendersSelectedRequest(t *testing.T) {
	state := app.NewState()
	source := caddy.CaddySource{
		ID: "one", Hosts: []string{"one.test"},
		Routes: []caddy.CaddyRouteRule{{
			Matcher: "path /api/*",
			Actions: []caddy.CaddyRouteAction{{Kind: "reverse_proxy", Label: "reverse_proxy localhost:8080", Upstreams: []caddy.CaddyUpstream{{Label: "localhost:8080"}}}},
		}},
	}
	state.SetSources([]caddy.CaddySource{source}, "one", 0)
	state.SetActiveView(app.ViewLogDetail)
	state.SetAccessLogs(&applogs.CaddyAccessLogsResult{OK: true, Available: true, Output: `{"request":{"method":"GET","host":"one.test","uri":"/api/users?active=1","remote_ip":"127.0.0.1","remote_port":"5555","headers":{"User-Agent":["curl"],"Accept":["application/json"]}},"resp_headers":{"Content-Type":["application/json"]},"status":502,"duration_ms":1500,"size":2048,"error":"bad gateway","logger":"access"}`})
	model := Model{State: state, width: 120, height: 40}

	view := stripANSI(model.View())
	if strings.Contains(view, "lazycaddy") {
		t.Fatalf("request detail should not render lazycaddy heading:\n%s", view)
	}
	for _, want := range []string{"one.test › logs › 502 GET /api/users", "GET /api/users", "1.50s", "2.0KB", "Matched route", "path /api/* → reverse_proxy localhost:8080", "Remote: 127.0.0.1:5555", "User-Agent: curl", "Content-Type: application/json", "Error: bad gateway", "Raw"} {
		if !strings.Contains(view, want) {
			t.Fatalf("detail view missing %q:\n%s", want, view)
		}
	}
}

func TestRequestDetailScrollKeys(t *testing.T) {
	state := app.NewState()
	state.SetSources([]caddy.CaddySource{{ID: "one", Hosts: []string{"one.test"}}}, "one", 0)
	state.SetActiveView(app.ViewLogDetail)
	state.SetAccessLogs(&applogs.CaddyAccessLogsResult{OK: true, Available: true, Output: `{"request":{"method":"GET","uri":"/very-long"},"status":200,"duration_ms":25,"size":10,"message":"ok"}`})
	model := Model{State: state, width: 40, height: 8}

	updated, _ := model.Update(keyMsg("j"))
	model = updated.(Model)
	if model.detailScroll != 1 {
		t.Fatalf("detailScroll=%d, want 1", model.detailScroll)
	}
	updated, _ = model.Update(keyMsg("k"))
	model = updated.(Model)
	if model.detailScroll != 0 {
		t.Fatalf("detailScroll=%d, want 0", model.detailScroll)
	}
}

func TestConfigViewRendersCaddyfileAndActiveConfig(t *testing.T) {
	state := app.NewState()
	source := caddy.CaddySource{
		ID: "srv0:one.test", ServerName: "srv0", Listen: []string{":443"}, Hosts: []string{"one.test"}, ProxyCount: 1,
		Routes: []caddy.CaddyRouteRule{{Matcher: "path /api/*", Actions: []caddy.CaddyRouteAction{{Kind: "reverse_proxy", Label: "reverse_proxy localhost:8080"}}}},
	}
	state.SetSources([]caddy.CaddySource{source}, source.ID, 0)
	state.SetActiveView(app.ViewConfig)
	correlation := caddy.CaddyfileCorrelation{
		Path: "/etc/caddy/Caddyfile", Available: true,
		ContentLines: []string{"one.test {", "\treverse_proxy localhost:8080", "}"},
		Sources:      []caddy.CaddyfileSourceBlock{{Path: "/etc/caddy/Caddyfile", Line: 1, EndLine: 3, Address: "one.test", Addresses: []string{"one.test"}}},
	}
	model := Model{State: state, CaddyfileCorrelation: correlation, Discovery: caddy.AdminAPIDiscovery{ConfigPath: "/etc/caddy/Caddyfile"}, width: 120, height: 40}

	view := stripANSI(model.View())
	if strings.Contains(view, "lazycaddy") {
		t.Fatalf("config view should not render lazycaddy heading:\n%s", view)
	}
	for _, want := range []string{"one.test › config", "Config source", "Host: one.test", "Caddyfile: /etc/caddy/Caddyfile:1", "Block: lines 1-3", "Caddyfile block", "1 │ one.test {", "2 │     reverse_proxy localhost:8080", "Active runtime config", "Server: srv0", "Routes", "path /api/*", "↳ reverse_proxy localhost:8080"} {
		if !strings.Contains(view, want) {
			t.Fatalf("config view missing %q:\n%s", want, view)
		}
	}
}

func TestConfigToggleAndScrollKeys(t *testing.T) {
	state := app.NewState()
	state.SetSources([]caddy.CaddySource{{ID: "one", Hosts: []string{"one.test"}}}, "one", 0)
	model := Model{State: state, width: 80, height: 8}

	updated, _ := model.Update(keyMsg("c"))
	model = updated.(Model)
	if model.State.ActiveView != app.ViewConfig || model.configScroll != 0 {
		t.Fatalf("expected config view at scroll 0, got view=%s scroll=%d", model.State.ActiveView, model.configScroll)
	}
	updated, _ = model.Update(keyMsg("j"))
	model = updated.(Model)
	if model.configScroll != 1 {
		t.Fatalf("configScroll=%d, want 1", model.configScroll)
	}
	updated, _ = model.Update(keyMsg("k"))
	model = updated.(Model)
	if model.configScroll != 0 {
		t.Fatalf("configScroll=%d, want 0", model.configScroll)
	}
	updated, _ = model.Update(keyMsg("c"))
	model = updated.(Model)
	if model.State.ActiveView != app.ViewServices {
		t.Fatalf("config close should return to services, got %s", model.State.ActiveView)
	}
}

func TestSystemViewRendersStatusValidationAdminDiscoveryAndLogs(t *testing.T) {
	now := time.Now()
	state := app.NewState()
	state.SetSources([]caddy.CaddySource{{ID: "one", ProxyCount: 2}}, "one", 0)
	state.SetActiveView(app.ViewSystem)
	state.LastLoad = &caddy.ConfigLoadResult{OK: true, StatusCode: 200, FetchedAt: now, DurationMS: 12}
	state.LastValidation = &caddy.ValidationResult{OK: true, Command: []string{"caddy", "validate", "--config", "/etc/caddy/Caddyfile"}, Output: "Config is valid.", RanAt: now, DurationMS: 34}
	state.LastLogs = &applogs.CaddyLogsResult{OK: true, Output: joinLines(
		`2026-06-17T22:10:00Z dev caddy[123]: INFO admin started`,
		`2026-06-17T22:10:01Z dev caddy[123]: ERROR http failed err=boom`,
	), FetchedAt: now, DurationMS: 56}
	model := Model{
		State: state,
		Discovery: caddy.AdminAPIDiscovery{
			AdminURL: "http://localhost:2019", SourceLabel: "systemd config /etc/caddy/Caddyfile", Source: "systemd-config", ConfigPath: "/etc/caddy/Caddyfile",
			Service: &caddy.SystemdServiceInfo{Exists: true, LoadState: "loaded", ActiveState: "active", SubState: "running", MainPID: 123, FragmentPath: "/lib/systemd/system/caddy.service", ArgvSource: "proc"},
			Command: &caddy.ParsedCaddyRunCommand{Argv: []string{"/usr/bin/caddy", "run"}, ConfigPath: "/etc/caddy/Caddyfile"},
			Notes:   []string{"Found systemd caddy.service."},
		},
		width: 120, height: 50,
	}

	view := stripANSI(model.View())
	if strings.Contains(view, "lazycaddy") {
		t.Fatalf("system view should not render lazycaddy heading:\n%s", view)
	}
	for _, want := range []string{"caddy › system", "Caddy service", "active/running", "PID: 123", "Startup config", "Config: /etc/caddy/Caddyfile", "Validation", "OK", "Admin API", "Endpoint: http://localhost:2019", "Active: 1 sources · 2 proxies", "Discovery", "Found systemd caddy.service", "Service logs", "ERR", "boom"} {
		if !strings.Contains(view, want) {
			t.Fatalf("system view missing %q:\n%s", want, view)
		}
	}
}

func TestSystemKeysToggleErrorsValidateAndScroll(t *testing.T) {
	state := app.NewState()
	state.SetActiveView(app.ViewSystem)
	state.LastLogs = &applogs.CaddyLogsResult{OK: true, Output: joinLines(
		`2026-06-17T22:10:00Z dev caddy[123]: INFO admin started`,
		`2026-06-17T22:10:01Z dev caddy[123]: ERROR http failed err=boom`,
	)}
	model := Model{State: state, width: 80, height: 50}

	updated, _ := model.Update(keyMsg("e"))
	model = updated.(Model)
	if !model.State.LogsErrorOnly {
		t.Fatal("expected system e to toggle error-only logs")
	}
	view := stripANSI(model.View())
	if strings.Contains(view, "started") || !strings.Contains(view, "boom") {
		t.Fatalf("system error filter failed:\n%s", view)
	}
	updated, _ = model.Update(keyMsg("j"))
	model = updated.(Model)
	if model.systemScroll != 1 {
		t.Fatalf("systemScroll=%d, want 1", model.systemScroll)
	}
	updated, _ = model.Update(keyMsg("k"))
	model = updated.(Model)
	if model.systemScroll != 0 {
		t.Fatalf("systemScroll=%d, want 0", model.systemScroll)
	}
	updated, cmd := model.Update(keyMsg("v"))
	model = updated.(Model)
	if !model.validating || cmd == nil {
		t.Fatalf("expected validation command and flag, validating=%v cmd=%v", model.validating, cmd)
	}
}

func TestHelpViewRendersAndTogglesBack(t *testing.T) {
	state := app.NewState()
	state.SetActiveView(app.ViewLogs)
	model := Model{State: state, width: 100, height: 50}

	updated, _ := model.Update(keyMsg("?"))
	model = updated.(Model)
	if model.State.ActiveView != app.ViewHelp || model.State.PreviousHelpView != app.ViewLogs || model.helpScroll != 0 {
		t.Fatalf("expected help from logs, got view=%s previous=%s scroll=%d", model.State.ActiveView, model.State.PreviousHelpView, model.helpScroll)
	}
	view := stripANSI(model.View())
	for _, want := range []string{"Shortcuts", "?        help", "S        system", "↑/↓ j/k", "←/esc/h", "Logs", "d/w/a", "System", "v        validate"} {
		if !strings.Contains(view, want) {
			t.Fatalf("help view missing %q:\n%s", want, view)
		}
	}

	updated, _ = model.Update(keyMsg("?"))
	model = updated.(Model)
	if model.State.ActiveView != app.ViewLogs {
		t.Fatalf("help toggle should return to logs, got %s", model.State.ActiveView)
	}
}

func TestHelpScrollKeys(t *testing.T) {
	state := app.NewState()
	state.SetActiveView(app.ViewHelp)
	model := Model{State: state, width: 80, height: 8}

	updated, _ := model.Update(keyMsg("j"))
	model = updated.(Model)
	if model.helpScroll != 1 {
		t.Fatalf("helpScroll=%d, want 1", model.helpScroll)
	}
	updated, _ = model.Update(keyMsg("k"))
	model = updated.(Model)
	if model.helpScroll != 0 {
		t.Fatalf("helpScroll=%d, want 0", model.helpScroll)
	}
}

func TestMouseWheelScrollsActiveView(t *testing.T) {
	state := app.NewState()
	state.SetActiveView(app.ViewConfig)
	model := Model{State: state, width: 80, height: 8}

	updated, _ := model.Update(mouseWheelDownMsg())
	model = updated.(Model)
	if model.configScroll != 1 {
		t.Fatalf("configScroll=%d, want 1", model.configScroll)
	}
	updated, _ = model.Update(mouseWheelUpMsg())
	model = updated.(Model)
	if model.configScroll != 0 {
		t.Fatalf("configScroll=%d, want 0", model.configScroll)
	}
}

func TestMouseWheelMovesLogsSelection(t *testing.T) {
	state := app.NewState()
	state.SetSources([]caddy.CaddySource{{ID: "one", Hosts: []string{"one.test"}}}, "one", 0)
	state.SetActiveView(app.ViewLogs)
	state.SetAccessLogs(&applogs.CaddyAccessLogsResult{OK: true, Available: true, Output: joinLines(
		`{"request":{"method":"GET","uri":"/one"},"status":200,"duration_ms":25}`,
		`{"request":{"method":"GET","uri":"/two"},"status":200,"duration_ms":25}`,
	)})
	state.SelectedAccessLogIdx = 0
	model := Model{State: state, width: 80, height: 20}

	updated, _ := model.Update(mouseWheelDownMsg())
	model = updated.(Model)
	if model.State.SelectedAccessLogIdx != 1 {
		t.Fatalf("selected access index=%d, want 1", model.State.SelectedAccessLogIdx)
	}
}

func keyMsg(value string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(value)}
}

func mouseWheelDownMsg() tea.MouseMsg {
	return tea.MouseMsg{Button: tea.MouseButtonWheelDown, Type: tea.MouseWheelDown}
}

func mouseWheelUpMsg() tea.MouseMsg {
	return tea.MouseMsg{Button: tea.MouseButtonWheelUp, Type: tea.MouseWheelUp}
}

func stripANSI(value string) string {
	return regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(value, "")
}

func findLineContaining(lines []string, needle string) string {
	index := lineIndexContaining(lines, needle)
	if index == -1 {
		return ""
	}
	return lines[index]
}

func lineIndexContaining(lines []string, needle string) int {
	for index, line := range lines {
		if strings.Contains(line, needle) {
			return index
		}
	}
	return -1
}

func joinLines(values ...string) string {
	return strings.Join(values, "\n")
}

func decodeJSON(t *testing.T, value string) any {
	t.Helper()
	var decoded any
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}
