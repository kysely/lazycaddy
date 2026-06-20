package caddy

// CaddyUpstream describes an upstream target referenced by a route action.
type CaddyUpstream struct {
	Label   string
	Dial    string
	Dynamic bool
	Raw     any
}

// CaddyRouteAction describes one summarized Caddy HTTP handler/action.
type CaddyRouteAction struct {
	Kind          string
	Label         string
	Upstreams     []CaddyUpstream
	Transport     string
	LoadBalancing string
	Raw           map[string]any
}

// CaddyRouteRule describes one summarized route for a source/service.
type CaddyRouteRule struct {
	Matcher   string
	Actions   []CaddyRouteAction
	RoutePath string
}

// CaddyAccessLog describes an access-log writer associated with a source/service.
type CaddyAccessLog struct {
	LoggerName   string
	LoggerID     string
	WriterOutput string
	Filename     string
	Encoder      string
	Source       string
}

// CaddySource describes one service/source shown by lazycaddy.
type CaddySource struct {
	ID         string
	ServerName string
	Listen     []string
	Hosts      []string
	Routes     []CaddyRouteRule
	ProxyCount int
	AccessLogs []CaddyAccessLog
}
