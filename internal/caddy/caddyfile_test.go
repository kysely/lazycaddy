package caddy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadCaddyfileCorrelationUnavailable(t *testing.T) {
	if got := LoadCaddyfileCorrelation("", ""); got.Available || got.Error == "" {
		t.Fatalf("empty config path should be unavailable: %+v", got)
	}
	if got := LoadCaddyfileCorrelation("/tmp/config.json", ""); got.Available || !strings.Contains(got.Error, "JSON") {
		t.Fatalf("json config should be unavailable: %+v", got)
	}
	if got := LoadCaddyfileCorrelation("/tmp/Caddyfile", "json"); got.Available || !strings.Contains(got.Error, "adapter is json") {
		t.Fatalf("json adapter should be unavailable: %+v", got)
	}
}

func TestParseCaddyfileSiteBlocksDirectivesAndMatchers(t *testing.T) {
	content := `{
	admin :2020
}

example.com, www.example.com {
	encode gzip
	handle /api/* {
		reverse_proxy localhost:8080
	}
	handle {
		file_server browse
	}
}

:8081 {
	respond "ok"
}
`

	blocks := parseCaddyfile("/etc/caddy/Caddyfile", content)
	if len(blocks) != 2 {
		t.Fatalf("len(blocks)=%d, want 2: %+v", len(blocks), blocks)
	}
	first := blocks[0]
	if first.Line != 5 || first.EndLine != 13 || first.Address != "example.com, www.example.com" {
		t.Fatalf("unexpected first block: %+v", first)
	}
	if len(first.Directives) != 3 {
		t.Fatalf("directives=%+v, want 3", first.Directives)
	}
	if first.Directives[0].Name != "encode" || first.Directives[0].Matcher != "" {
		t.Fatalf("unexpected encode directive: %+v", first.Directives[0])
	}
	if first.Directives[1].Name != "reverse_proxy" || first.Directives[1].Matcher != "path /api/*" || first.Directives[1].Line != 8 {
		t.Fatalf("unexpected reverse_proxy directive: %+v", first.Directives[1])
	}
	if first.Directives[2].Name != "file_server" || first.Directives[2].Matcher != "everything else" {
		t.Fatalf("unexpected file_server directive: %+v", first.Directives[2])
	}
	if blocks[1].Address != ":8081" || blocks[1].Directives[0].Name != "respond" {
		t.Fatalf("unexpected second block: %+v", blocks[1])
	}
}

func TestFindCaddyfileSourceByHostAndListener(t *testing.T) {
	correlation := CaddyfileCorrelation{Available: true, Sources: []CaddyfileSourceBlock{
		{Path: "/etc/caddy/Caddyfile", Line: 1, Addresses: []string{"example.com", "www.example.com"}},
		{Path: "/etc/caddy/Caddyfile", Line: 5, Addresses: []string{":8080"}},
	}}

	byHost := FindCaddyfileSource(correlation, &CaddySource{Hosts: []string{"www.example.com"}})
	if byHost == nil || byHost.Line != 1 {
		t.Fatalf("host match failed: %+v", byHost)
	}
	byListener := FindCaddyfileSource(correlation, &CaddySource{Listen: []string{"0.0.0.0:8080"}})
	if byListener == nil || byListener.Line != 5 {
		t.Fatalf("listener match failed: %+v", byListener)
	}
}

func TestFindCaddyfileRouteLineScoresMatcherAndArgs(t *testing.T) {
	block := &CaddyfileSourceBlock{Directives: []CaddyfileDirective{
		{Line: 3, Name: "reverse_proxy", Args: []string{"localhost:3000"}, Matcher: "everything else"},
		{Line: 7, Name: "reverse_proxy", Args: []string{"localhost:8080"}, Matcher: "path /api/*"},
	}}
	route := CaddyRouteRule{
		Matcher: "path /api/*",
		Actions: []CaddyRouteAction{{Kind: "reverse_proxy", Label: "reverse_proxy localhost:8080", Upstreams: []CaddyUpstream{{Label: "localhost:8080"}}}},
	}
	if line := FindCaddyfileRouteLine(block, route); line != 7 {
		t.Fatalf("line=%d, want 7", line)
	}
}

func TestFormatCaddyfileBlockAndLocation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	content := "example.com {\n\treverse_proxy localhost:8080\n}\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	correlation := LoadCaddyfileCorrelation(path, "")
	if !correlation.Available {
		t.Fatalf("expected available correlation: %+v", correlation)
	}
	block := FindCaddyfileSource(correlation, &CaddySource{Hosts: []string{"example.com"}})
	if block == nil {
		t.Fatal("expected source block")
	}
	if location := CaddyfileLocation(correlation, block, 0); location != path+":1" {
		t.Fatalf("location=%q", location)
	}
	formatted := strings.Join(FormatCaddyfileBlock(correlation, block), "\n")
	if !strings.Contains(formatted, "1 │ example.com {") || !strings.Contains(formatted, "2 │ \treverse_proxy localhost:8080") {
		t.Fatalf("unexpected formatted block:\n%s", formatted)
	}
}

func TestDirectiveMatchesActionAliases(t *testing.T) {
	tests := []struct{ directive, action string }{
		{directive: "php_fastcgi", action: "reverse_proxy"},
		{directive: "basic_auth", action: "authentication"},
		{directive: "redir", action: "respond"},
		{directive: "header", action: "headers"},
	}
	for _, tt := range tests {
		if !directiveMatchesAction(CaddyfileDirective{Name: tt.directive}, tt.action) {
			t.Fatalf("expected directive %q to match action %q", tt.directive, tt.action)
		}
	}
}
