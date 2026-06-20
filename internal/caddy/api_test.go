package caddy

import "testing"

func TestNormalizeAdminURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "adds http scheme", in: "localhost:2019", want: "http://localhost:2019"},
		{name: "preserves http scheme", in: "http://localhost:2019", want: "http://localhost:2019"},
		{name: "preserves https scheme", in: "https://caddy.example.test/admin", want: "https://caddy.example.test/admin"},
		{name: "trims whitespace and slash", in: "  localhost:2019///  ", want: "http://localhost:2019"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeAdminURL(tt.in); got != tt.want {
				t.Fatalf("NormalizeAdminURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestGetExplicitAdminAPIBaseURLPriority(t *testing.T) {
	env := map[string]string{
		"CADDY_ADMIN_API": "env-api:2019",
		"CADDY_ADMIN_URL": "env-url:2019",
	}
	lookup := func(key string) string { return env[key] }

	explicit, ok := GetExplicitAdminAPIBaseURL([]string{"lazycaddy", "--admin-url", "cli:2019"}, lookup)
	if !ok {
		t.Fatal("expected explicit admin URL")
	}
	if explicit.Source != "cli" {
		t.Fatalf("source = %q, want cli", explicit.Source)
	}
	if explicit.URL != "http://cli:2019" {
		t.Fatalf("url = %q, want http://cli:2019", explicit.URL)
	}
}

func TestGetExplicitAdminAPIBaseURLEnvFallback(t *testing.T) {
	env := map[string]string{"CADDY_ADMIN_URL": "env-url:2019"}
	lookup := func(key string) string { return env[key] }

	explicit, ok := GetExplicitAdminAPIBaseURL([]string{"lazycaddy"}, lookup)
	if !ok {
		t.Fatal("expected explicit admin URL")
	}
	if explicit.Source != "env" {
		t.Fatalf("source = %q, want env", explicit.Source)
	}
	if explicit.URL != "http://env-url:2019" {
		t.Fatalf("url = %q, want http://env-url:2019", explicit.URL)
	}
}

func TestGetAdminAPIBaseURLDefault(t *testing.T) {
	lookup := func(string) string { return "" }
	if got := GetAdminAPIBaseURL([]string{"lazycaddy"}, lookup); got != DefaultAdminAPIURL {
		t.Fatalf("GetAdminAPIBaseURL default = %q, want %q", got, DefaultAdminAPIURL)
	}
}
