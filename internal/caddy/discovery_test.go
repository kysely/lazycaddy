package caddy

import (
	"reflect"
	"testing"
)

func TestSplitShellWords(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{name: "simple", in: `caddy run --config /etc/caddy/Caddyfile`, want: []string{"caddy", "run", "--config", "/etc/caddy/Caddyfile"}},
		{name: "double quotes", in: `caddy run --config "/path with spaces/Caddyfile"`, want: []string{"caddy", "run", "--config", "/path with spaces/Caddyfile"}},
		{name: "single quotes", in: `caddy run --adapter 'caddyfile'`, want: []string{"caddy", "run", "--adapter", "caddyfile"}},
		{name: "escaped space", in: `caddy run --config /path/with\ space/Caddyfile`, want: []string{"caddy", "run", "--config", "/path/with space/Caddyfile"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := splitShellWords(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("splitShellWords(%q) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseProperties(t *testing.T) {
	got := parseProperties("LoadState=loaded\nActiveState=active\nExecStart={ path=/usr/bin/caddy ; argv[]=/usr/bin/caddy run --config /etc/caddy/Caddyfile ; }\n")
	if got["LoadState"] != "loaded" || got["ActiveState"] != "active" {
		t.Fatalf("unexpected states: %#v", got)
	}
	if got["ExecStart"] == "" {
		t.Fatalf("expected ExecStart property: %#v", got)
	}
}

func TestExtractSystemdArgv(t *testing.T) {
	execStart := `{ path=/usr/bin/caddy ; argv[]=/usr/bin/caddy run --environ --config /etc/caddy/Caddyfile --adapter caddyfile ; ignore_errors=no ; }`
	got := extractSystemdArgv(execStart)
	want := []string{"/usr/bin/caddy", "run", "--environ", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("extractSystemdArgv = %#v, want %#v", got, want)
	}
}

func TestParseCaddyRunCommand(t *testing.T) {
	tests := []struct {
		name       string
		argv       []string
		wantConfig string
		wantAdapt  string
		wantResume bool
		wantNil    bool
	}{
		{name: "long flags", argv: []string{"/usr/bin/caddy", "run", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile"}, wantConfig: "/etc/caddy/Caddyfile", wantAdapt: "caddyfile"},
		{name: "equals flags", argv: []string{"caddy", "run", "--config=/etc/caddy/config.json", "--adapter=json", "--resume"}, wantConfig: "/etc/caddy/config.json", wantAdapt: "json", wantResume: true},
		{name: "short-ish flags", argv: []string{"caddy", "run", "-config", "/tmp/Caddyfile", "-adapter", "caddyfile"}, wantConfig: "/tmp/Caddyfile", wantAdapt: "caddyfile"},
		{name: "not caddy", argv: []string{"node", "server.js"}, wantNil: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseCaddyRunCommand(tt.argv)
			if tt.wantNil {
				if got != nil {
					t.Fatalf("expected nil, got %#v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected command, got nil")
			}
			if got.ConfigPath != tt.wantConfig || got.Adapter != tt.wantAdapt || got.Resume != tt.wantResume {
				t.Fatalf("command = %+v, want config=%q adapter=%q resume=%v", got, tt.wantConfig, tt.wantAdapt, tt.wantResume)
			}
		})
	}
}

func TestDiscoverFromJSONConfig(t *testing.T) {
	tests := []struct {
		name         string
		content      string
		wantURL      string
		wantDisabled bool
		wantListen   string
	}{
		{name: "default without admin", content: `{}`, wantURL: DefaultAdminAPIURL},
		{name: "listen", content: `{"admin":{"listen":"localhost:2020"}}`, wantURL: "http://localhost:2020", wantListen: "localhost:2020"},
		{name: "disabled", content: `{"admin":{"disabled":true}}`, wantDisabled: true},
		{name: "unix unsupported", content: `{"admin":{"listen":"unix//run/caddy-admin.sock"}}`, wantListen: "unix//run/caddy-admin.sock"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := discoverFromJSONConfig(tt.content)
			if got.AdminURL != tt.wantURL || got.Disabled != tt.wantDisabled || got.Listen != tt.wantListen {
				t.Fatalf("discoverFromJSONConfig = %+v, want url=%q disabled=%v listen=%q", got, tt.wantURL, tt.wantDisabled, tt.wantListen)
			}
		})
	}
}

func TestDiscoverFromCaddyfile(t *testing.T) {
	tests := []struct {
		name         string
		content      string
		wantURL      string
		wantDisabled bool
		wantListen   string
	}{
		{name: "no global", content: "example.com {\n respond ok\n}\n", wantURL: DefaultAdminAPIURL},
		{name: "admin address", content: "{\n admin :2020\n}\nexample.com { respond ok }", wantURL: "http://localhost:2020", wantListen: ":2020"},
		{name: "admin off", content: "{\n admin off\n}\n", wantDisabled: true},
		{name: "admin default", content: "{\n admin\n}\n", wantURL: DefaultAdminAPIURL},
		{name: "comment ignored", content: "{\n # admin off\n admin localhost:2021 # comment\n}\n", wantURL: "http://localhost:2021", wantListen: "localhost:2021"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := discoverFromCaddyfile(tt.content)
			if got.AdminURL != tt.wantURL || got.Disabled != tt.wantDisabled || got.Listen != tt.wantListen {
				t.Fatalf("discoverFromCaddyfile = %+v, want url=%q disabled=%v listen=%q", got, tt.wantURL, tt.wantDisabled, tt.wantListen)
			}
		})
	}
}

func TestListenToAdminURL(t *testing.T) {
	tests := []struct {
		listen       string
		wantURL      string
		wantDisabled bool
		wantListen   string
	}{
		{listen: "off", wantDisabled: true},
		{listen: ":2020", wantURL: "http://localhost:2020", wantListen: ":2020"},
		{listen: "tcp/127.0.0.1:2020", wantURL: "http://127.0.0.1:2020", wantListen: "tcp/127.0.0.1:2020"},
		{listen: "unix//run/caddy.sock", wantListen: "unix//run/caddy.sock"},
	}

	for _, tt := range tests {
		t.Run(tt.listen, func(t *testing.T) {
			got := listenToAdminURL(tt.listen, "test")
			if got.AdminURL != tt.wantURL || got.Disabled != tt.wantDisabled || got.Listen != tt.wantListen {
				t.Fatalf("listenToAdminURL = %+v, want url=%q disabled=%v listen=%q", got, tt.wantURL, tt.wantDisabled, tt.wantListen)
			}
		})
	}
}
