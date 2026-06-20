package caddy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const DefaultAdminAPIURL = "http://localhost:2019"

// ExplicitAdminAPIBaseURL is an Admin API override from CLI args or environment.
type ExplicitAdminAPIBaseURL struct {
	URL    string
	Source string // "cli" or "env"
}

// ConfigLoadResult is the result of fetching Caddy's active runtime config.
type ConfigLoadResult struct {
	AdminURL   string
	Endpoint   string
	OK         bool
	Config     any
	Error      string
	StatusCode int
	StatusText string
	FetchedAt  time.Time
	DurationMS int64
}

// GetExplicitAdminAPIBaseURL returns a CLI/env Admin API override, if present.
// CLI flags take precedence over environment variables.
func GetExplicitAdminAPIBaseURL(argv []string, lookupEnv func(string) string) (ExplicitAdminAPIBaseURL, bool) {
	if lookupEnv == nil {
		lookupEnv = os.Getenv
	}

	if cliURL := readCLIAdminURL(argv); cliURL != "" {
		return ExplicitAdminAPIBaseURL{URL: NormalizeAdminURL(cliURL), Source: "cli"}, true
	}

	if envURL := lookupEnv("CADDY_ADMIN_API"); envURL != "" {
		return ExplicitAdminAPIBaseURL{URL: NormalizeAdminURL(envURL), Source: "env"}, true
	}

	if envURL := lookupEnv("CADDY_ADMIN_URL"); envURL != "" {
		return ExplicitAdminAPIBaseURL{URL: NormalizeAdminURL(envURL), Source: "env"}, true
	}

	return ExplicitAdminAPIBaseURL{}, false
}

// GetAdminAPIBaseURL returns the explicit Admin API URL or Caddy's default.
func GetAdminAPIBaseURL(argv []string, lookupEnv func(string) string) string {
	if explicit, ok := GetExplicitAdminAPIBaseURL(argv, lookupEnv); ok {
		return explicit.URL
	}

	return DefaultAdminAPIURL
}

// FetchActiveConfig fetches Caddy's active runtime config from the Admin API.
func FetchActiveConfig(ctx context.Context, adminURL string) ConfigLoadResult {
	if adminURL == "" {
		adminURL = DefaultAdminAPIURL
	}

	normalizedAdminURL := NormalizeAdminURL(adminURL)
	endpoint := normalizedAdminURL + "/config/"
	startedAt := time.Now()
	fetchedAt := startedAt

	result := ConfigLoadResult{
		AdminURL:  normalizedAdminURL,
		Endpoint:  endpoint,
		FetchedAt: fetchedAt,
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		result.DurationMS = durationMS(startedAt)
		result.Error = fmt.Sprintf("Could not create request for Caddy Admin API at %s: %v", endpoint, err)
		return result
	}
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		result.DurationMS = durationMS(startedAt)
		if ctxErr := ctx.Err(); ctxErr != nil {
			result.Error = fmt.Sprintf("Timed out connecting to Caddy Admin API at %s: %v", endpoint, ctxErr)
		} else {
			result.Error = fmt.Sprintf("Could not connect to Caddy Admin API at %s: %v", endpoint, err)
		}
		return result
	}
	defer resp.Body.Close()

	result.DurationMS = durationMS(startedAt)
	result.StatusCode = resp.StatusCode
	result.StatusText = resp.Status

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body := safeReadBody(resp.Body)
		result.Error = fmt.Sprintf("GET %s returned HTTP %d %s", endpoint, resp.StatusCode, resp.Status)
		if body != "" {
			result.Error += ": " + body
		}
		return result
	}

	var config any
	if err := json.NewDecoder(resp.Body).Decode(&config); err != nil {
		result.Error = fmt.Sprintf("GET %s succeeded, but the response was not valid JSON: %v", endpoint, err)
		return result
	}

	result.OK = true
	result.Config = config
	return result
}

// NormalizeAdminURL ensures a URL has an HTTP scheme and no trailing slash.
func NormalizeAdminURL(rawURL string) string {
	trimmed := strings.TrimSpace(rawURL)
	if !strings.HasPrefix(trimmed, "http://") && !strings.HasPrefix(trimmed, "https://") {
		trimmed = "http://" + trimmed
	}

	return strings.TrimRight(trimmed, "/")
}

func readCLIAdminURL(argv []string) string {
	for index := 1; index < len(argv); index++ {
		arg := argv[index]
		if arg == "" {
			continue
		}

		switch arg {
		case "--admin", "--admin-url":
			if index+1 < len(argv) {
				return argv[index+1]
			}
		case "--admin=":
			return ""
		case "--admin-url=":
			return ""
		}

		if strings.HasPrefix(arg, "--admin=") {
			return strings.TrimPrefix(arg, "--admin=")
		}

		if strings.HasPrefix(arg, "--admin-url=") {
			return strings.TrimPrefix(arg, "--admin-url=")
		}
	}

	return ""
}

func safeReadBody(reader io.Reader) string {
	limited := io.LimitReader(reader, 501)
	body, err := io.ReadAll(limited)
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(body))[:min(len(strings.TrimSpace(string(body))), 500)]
}

func durationMS(startedAt time.Time) int64 {
	return time.Since(startedAt).Milliseconds()
}
