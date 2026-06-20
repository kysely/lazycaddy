package caddy

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// UpstreamHealthStatus describes a TCP reachability check result.
type UpstreamHealthStatus string

const (
	UpstreamHealthOK          UpstreamHealthStatus = "ok"
	UpstreamHealthDown        UpstreamHealthStatus = "down"
	UpstreamHealthUnsupported UpstreamHealthStatus = "unsupported"
)

// UpstreamHealthResult is one upstream reachability result.
type UpstreamHealthResult struct {
	Upstream  string
	Status    UpstreamHealthStatus
	Target    string
	LatencyMS int64
	Error     string
	CheckedAt time.Time
}

type dialTarget struct {
	host  string
	port  int
	label string
}

// CheckSourceUpstreams checks every unique static upstream for a source.
func CheckSourceUpstreams(ctx context.Context, source *CaddySource, timeout time.Duration) []UpstreamHealthResult {
	if source == nil {
		return nil
	}

	upstreams := uniqueUpstreams(source)
	results := make([]UpstreamHealthResult, len(upstreams))
	var wg sync.WaitGroup

	for index, upstream := range upstreams {
		index := index
		upstream := upstream
		wg.Add(1)
		go func() {
			defer wg.Done()

			dial := upstream.Dial
			if dial == "" {
				dial = upstream.Label
			}

			target, ok := parseDialTarget(dial)
			if !ok {
				errorMessage := fmt.Sprintf("unsupported dial address: %s", dial)
				if upstream.Dynamic {
					errorMessage = "dynamic upstream"
				}

				results[index] = UpstreamHealthResult{
					Upstream:  upstream.Label,
					Status:    UpstreamHealthUnsupported,
					Error:     errorMessage,
					CheckedAt: time.Now(),
				}
				return
			}

			results[index] = checkTCPTarget(ctx, upstream.Label, target, timeout)
		}()
	}

	wg.Wait()
	return results
}

type upstreamRef struct {
	Label   string
	Dial    string
	Dynamic bool
}

func uniqueUpstreams(source *CaddySource) []upstreamRef {
	seen := map[string]bool{}
	upstreams := []upstreamRef{}

	for _, route := range source.Routes {
		for _, action := range route.Actions {
			for _, upstream := range action.Upstreams {
				if seen[upstream.Label] {
					continue
				}

				seen[upstream.Label] = true
				upstreams = append(upstreams, upstreamRef{
					Label:   upstream.Label,
					Dial:    upstream.Dial,
					Dynamic: upstream.Dynamic,
				})
			}
		}
	}

	return upstreams
}

func parseDialTarget(dial string) (dialTarget, bool) {
	trimmed := strings.TrimSpace(dial)
	if trimmed == "" || strings.HasPrefix(trimmed, "unix/") || strings.HasPrefix(trimmed, "unix:") {
		return dialTarget{}, false
	}

	value := trimmed
	if !hasURLScheme(value) {
		if strings.HasPrefix(value, ":") {
			value = "localhost" + value
		}
		value = "tcp://" + value
	}

	parsed, err := url.Parse(value)
	if err != nil {
		return dialTarget{}, false
	}

	protocol := strings.TrimSuffix(parsed.Scheme, ":")
	portText := parsed.Port()
	if portText == "" {
		portText = defaultPort(protocol)
	}

	port, err := strconv.Atoi(portText)
	if err != nil || port <= 0 {
		return dialTarget{}, false
	}

	host := parsed.Hostname()
	if host == "" {
		return dialTarget{}, false
	}

	return dialTarget{
		host:  host,
		port:  port,
		label: net.JoinHostPort(host, strconv.Itoa(port)),
	}, true
}

func hasURLScheme(value string) bool {
	separator := strings.Index(value, "://")
	if separator <= 0 {
		return false
	}

	for index, char := range value[:separator] {
		if index == 0 && !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z')) {
			return false
		}
		if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '+' || char == '.' || char == '-') {
			return false
		}
	}

	return true
}

func defaultPort(protocol string) string {
	switch protocol {
	case "http", "h2c":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}

func checkTCPTarget(ctx context.Context, upstream string, target dialTarget, timeout time.Duration) UpstreamHealthResult {
	if timeout <= 0 {
		timeout = time.Second
	}

	checkedAt := time.Now()
	startedAt := checkedAt
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	conn, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", net.JoinHostPort(target.host, strconv.Itoa(target.port)))
	latency := durationMS(startedAt)
	if err != nil {
		return UpstreamHealthResult{
			Upstream:  upstream,
			Status:    UpstreamHealthDown,
			Target:    target.label,
			LatencyMS: latency,
			Error:     err.Error(),
			CheckedAt: checkedAt,
		}
	}
	defer conn.Close()

	return UpstreamHealthResult{
		Upstream:  upstream,
		Status:    UpstreamHealthOK,
		Target:    target.label,
		LatencyMS: latency,
		CheckedAt: checkedAt,
	}
}
