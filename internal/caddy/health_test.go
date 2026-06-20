package caddy

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestParseDialTarget(t *testing.T) {
	tests := []struct {
		name      string
		dial      string
		wantOK    bool
		wantHost  string
		wantPort  int
		wantLabel string
	}{
		{name: "host port", dial: "localhost:8080", wantOK: true, wantHost: "localhost", wantPort: 8080, wantLabel: "localhost:8080"},
		{name: "bare port", dial: ":8080", wantOK: true, wantHost: "localhost", wantPort: 8080, wantLabel: "localhost:8080"},
		{name: "http default port", dial: "http://example.com", wantOK: true, wantHost: "example.com", wantPort: 80, wantLabel: "example.com:80"},
		{name: "https default port", dial: "https://example.com", wantOK: true, wantHost: "example.com", wantPort: 443, wantLabel: "example.com:443"},
		{name: "h2c default port", dial: "h2c://example.com", wantOK: true, wantHost: "example.com", wantPort: 80, wantLabel: "example.com:80"},
		{name: "bare host unsupported", dial: "example.com", wantOK: false},
		{name: "unix unsupported", dial: "unix//var/run/app.sock", wantOK: false},
		{name: "empty unsupported", dial: "", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseDialTarget(tt.dial)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if got.host != tt.wantHost || got.port != tt.wantPort || got.label != tt.wantLabel {
				t.Fatalf("target = {%q %d %q}, want {%q %d %q}", got.host, got.port, got.label, tt.wantHost, tt.wantPort, tt.wantLabel)
			}
		})
	}
}

func TestCheckSourceUpstreams(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	accepted := make(chan struct{})
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			conn.Close()
		}
		close(accepted)
	}()

	source := &CaddySource{
		Routes: []CaddyRouteRule{{
			Actions: []CaddyRouteAction{{
				Upstreams: []CaddyUpstream{{Label: listener.Addr().String(), Dial: listener.Addr().String()}},
			}},
		}},
	}

	results := CheckSourceUpstreams(context.Background(), source, time.Second)
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].Status != UpstreamHealthOK {
		t.Fatalf("status = %q, want %q; error=%q", results[0].Status, UpstreamHealthOK, results[0].Error)
	}

	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("listener did not accept health-check connection")
	}
}
