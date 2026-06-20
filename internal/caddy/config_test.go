package caddy

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExtractCaddySourcesReverseProxyAndAccessLogs(t *testing.T) {
	config := mustDecodeJSON(t, `{
		"apps": {"http": {"servers": {
			"srv0": {
				"listen": [":443"],
				"logs": {
					"logger_names": {"example.com": "example_access"},
					"default_logger_name": "default_access"
				},
				"routes": [{
					"match": [{"host": ["example.com"], "path": ["/api/*"]}],
					"handle": [{
						"handler": "reverse_proxy",
						"upstreams": [{"dial": "127.0.0.1:8080"}],
						"transport": {"protocol": "http"},
						"load_balancing": {"selection_policy": {"policy": "round_robin"}}
					}]
				}]
			}
		}}},
		"logging": {"logs": {
			"example_access": {
				"writer": {"output": "file", "filename": "/var/log/caddy/example.access.log"},
				"encoder": {"format": "console"},
				"include": ["http.log.access.example_access"]
			}
		}}
	}`)

	sources := ExtractCaddySources(config)
	if len(sources) != 1 {
		t.Fatalf("len(sources)=%d, want 1: %+v", len(sources), sources)
	}
	source := sources[0]
	if SourceLabel(source) != "example.com" {
		t.Fatalf("label=%q, want example.com", SourceLabel(source))
	}
	if source.ID != "srv0:example.com" || source.ServerName != "srv0" {
		t.Fatalf("unexpected source identity: %+v", source)
	}
	if source.ProxyCount != 1 || len(source.Routes) != 1 {
		t.Fatalf("unexpected routes/proxies: %+v", source)
	}
	route := source.Routes[0]
	if route.Matcher != "path /api/*" {
		t.Fatalf("matcher=%q, want path /api/*", route.Matcher)
	}
	if len(route.Actions) != 1 || route.Actions[0].Kind != "reverse_proxy" {
		t.Fatalf("unexpected actions: %+v", route.Actions)
	}
	action := route.Actions[0]
	if action.Label != "reverse_proxy 127.0.0.1:8080" || action.Transport != "http" || action.LoadBalancing != "round_robin" {
		t.Fatalf("unexpected action summary: %+v", action)
	}
	if SourceProxySummary(source) != "127.0.0.1:8080" {
		t.Fatalf("proxy summary=%q", SourceProxySummary(source))
	}
	if len(source.AccessLogs) != 1 {
		t.Fatalf("access logs=%+v", source.AccessLogs)
	}
	log := source.AccessLogs[0]
	if log.LoggerName != "example_access" || log.LoggerID != "http.log.access.example_access" || log.WriterOutput != "file" || log.Filename != "/var/log/caddy/example.access.log" || log.Encoder != "console" {
		t.Fatalf("unexpected access log: %+v", log)
	}
}

func TestExtractCaddySourcesNestedSubrouteAndFallback(t *testing.T) {
	config := mustDecodeJSON(t, `{
		"apps": {"http": {"servers": {
			"srv0": {
				"listen": [":80"],
				"routes": [{
					"match": [{"host": ["example.com"]}],
					"handle": [{"handler": "subroute", "routes": [
						{"group": "group0", "match": [{"path": ["/static/*"]}], "handle": [
							{"handler": "vars", "root": "/srv/www"},
							{"handler": "file_server", "browse": true}
						]},
						{"group": "group0", "handle": [
							{"handler": "reverse_proxy", "upstreams": [{"dial": "localhost:3000"}]}
						]}
					]}]
				}]
			}
		}}}
	}`)

	sources := ExtractCaddySources(config)
	if len(sources) != 1 {
		t.Fatalf("len(sources)=%d, want 1", len(sources))
	}
	source := sources[0]
	if len(source.Routes) != 2 {
		t.Fatalf("routes=%+v, want 2", source.Routes)
	}
	if source.Routes[0].Matcher != "path /static/*" || source.Routes[0].Actions[0].Kind != "file_server" || !strings.Contains(source.Routes[0].Actions[0].Label, "/srv/www") {
		t.Fatalf("unexpected first route: %+v", source.Routes[0])
	}
	if source.Routes[1].Matcher != "everything else" || source.Routes[1].Actions[0].Kind != "reverse_proxy" {
		t.Fatalf("unexpected fallback route: %+v", source.Routes[1])
	}
	if source.ProxyCount != 1 {
		t.Fatalf("proxy count=%d, want 1", source.ProxyCount)
	}
}

func TestExtractCaddySourcesSkipsNonProxyOnlySources(t *testing.T) {
	config := mustDecodeJSON(t, `{
		"apps": {"http": {"servers": {"srv0": {"routes": [{"handle": [{"handler": "static_response", "status_code": 200, "body": "ok"}]}]}}}}
	}`)

	if sources := ExtractCaddySources(config); len(sources) != 0 {
		t.Fatalf("sources=%+v, want none because there are no reverse_proxy actions", sources)
	}
}

func TestExtractReverseProxiesIncludesErrorsRoutes(t *testing.T) {
	config := mustDecodeJSON(t, `{
		"apps": {"http": {"servers": {"srv0": {
			"listen": [":443"],
			"routes": [{"match": [{"host": ["example.com"]}], "handle": [{"handler": "reverse_proxy", "upstreams": [{"dial": "localhost:8080"}]}]}],
			"errors": {"routes": [{"handle": [{"handler": "reverse_proxy", "upstreams": [{"dial": "localhost:9000"}]}]}]}
		}}}}
	}`)

	proxies := ExtractReverseProxies(config)
	if len(proxies) != 2 {
		t.Fatalf("len(proxies)=%d, want 2: %+v", len(proxies), proxies)
	}
	if proxies[0].Hosts[0] != "example.com" || proxies[0].Upstreams[0].Dial != "localhost:8080" {
		t.Fatalf("unexpected first proxy: %+v", proxies[0])
	}
	if proxies[1].Upstreams[0].Dial != "localhost:9000" {
		t.Fatalf("unexpected error proxy: %+v", proxies[1])
	}
}

func TestResolveAccessLogsDefaultAndSkipHosts(t *testing.T) {
	serverLogs := map[string]any{
		"skip_hosts":          []any{"skip.example.com"},
		"default_logger_name": "default_access",
	}
	loggingLogs := map[string]any{
		"default_access": map[string]any{"writer": map[string]any{"output": "stdout"}},
	}

	logs := resolveAccessLogs(serverLogs, loggingLogs, []string{"skip.example.com"})
	if len(logs) != 1 {
		t.Fatalf("default logger should still be used when all hosts are skipped and no host logger resolved: %+v", logs)
	}
	if logs[0].WriterOutput != "stdout" || logs[0].Source != "server default" {
		t.Fatalf("unexpected log: %+v", logs[0])
	}
}

func mustDecodeJSON(t *testing.T, value string) any {
	t.Helper()
	var decoded any
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}
