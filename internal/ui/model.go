package ui

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/kysely/lazycaddy/internal/app"
	"github.com/kysely/lazycaddy/internal/caddy"
	applogs "github.com/kysely/lazycaddy/internal/logs"
)

const (
	configFetchTimeout     = 2 * time.Second
	serviceLogsTimeout     = 2500 * time.Millisecond
	accessLogsTimeout      = 3 * time.Second
	healthCheckTimeout     = time.Second
	validationCheckTimeout = 10 * time.Second
)

type configLoadedMsg struct {
	seq    int
	result caddy.ConfigLoadResult
}

type serviceLogsLoadedMsg struct {
	seq    int
	result applogs.CaddyLogsResult
}

type allAccessLogsLoadedMsg struct {
	seq     int
	results []sourceAccessLogsResult
}

type accessLogsLoadedMsg struct {
	seq      int
	sourceID string
	result   applogs.CaddyAccessLogsResult
}

type healthLoadedMsg struct {
	seq      int
	sourceID string
	results  []caddy.UpstreamHealthResult
}

type validationLoadedMsg struct {
	result caddy.ValidationResult
}

type refreshFailedMsg struct {
	seq   int
	stage string
	err   error
}

type sourceAccessLogsResult struct {
	source caddy.CaddySource
	logs   applogs.CaddyAccessLogsResult
}

// Model is the Bubble Tea model used by the Go TUI.
type Model struct {
	State                *app.State
	Discovery            caddy.AdminAPIDiscovery
	CaddyfileCorrelation caddy.CaddyfileCorrelation

	width  int
	height int

	refreshSeq int

	configRefreshing         bool
	serviceLogsRefreshing    bool
	accessLogsRefreshing     bool
	healthRefreshing         bool
	healthRefreshingSourceID string
	validating               bool

	detailScroll int
	configScroll int
	systemScroll int
	helpScroll   int

	lastError string
}

var (
	colorTitle      = lipgloss.AdaptiveColor{Light: "#0369a1", Dark: "#38bdf8"}
	colorPrimary    = lipgloss.AdaptiveColor{Light: "#111827", Dark: "#e5e7eb"}
	colorMuted      = lipgloss.AdaptiveColor{Light: "#64748b", Dark: "#94a3b8"}
	colorActive     = lipgloss.AdaptiveColor{Light: "#2563eb", Dark: "#60a5fa"}
	colorWarn       = lipgloss.AdaptiveColor{Light: "#d97706", Dark: "#f59e0b"}
	colorOK         = lipgloss.AdaptiveColor{Light: "#16a34a", Dark: "#22c55e"}
	colorErr        = lipgloss.AdaptiveColor{Light: "#dc2626", Dark: "#f87171"}
	colorSelectedBg = lipgloss.AdaptiveColor{Light: "#dbeafe", Dark: "#1e3a5f"}
	colorSelectedFg = lipgloss.AdaptiveColor{Light: "#111827", Dark: "#f8fafc"}

	pageStyle            = lipgloss.NewStyle().Padding(1, 2)
	titleStyle           = lipgloss.NewStyle().Bold(true).Foreground(colorTitle)
	mutedStyle           = lipgloss.NewStyle().Foreground(colorMuted)
	statusWarnStyle      = lipgloss.NewStyle().Bold(true).Foreground(colorWarn)
	statusOKStyle        = lipgloss.NewStyle().Bold(true).Foreground(colorOK)
	statusErrStyle       = lipgloss.NewStyle().Bold(true).Foreground(colorErr)
	selectedServiceStyle = lipgloss.NewStyle().Background(colorSelectedBg).Foreground(colorSelectedFg)
)

// New creates a UI model and performs synchronous lightweight discovery before the TUI starts.
func New(argv []string, lookupEnv func(string) string) Model {
	if lookupEnv == nil {
		lookupEnv = os.Getenv
	}
	discovery := caddy.DiscoverAdminAPIEndpoint(context.Background(), argv, lookupEnv)
	correlation := caddy.LoadCaddyfileCorrelation(firstNonEmpty(discovery.ConfigPath, commandConfigPath(discovery.Command)), firstNonEmpty(discovery.Adapter, commandAdapter(discovery.Command)))

	return Model{
		State:                 app.NewState(),
		Discovery:             discovery,
		CaddyfileCorrelation:  correlation,
		refreshSeq:            1,
		configRefreshing:      true,
		serviceLogsRefreshing: true,
	}
}

// Init starts the first config/log/health refresh.
func (m Model) Init() tea.Cmd {
	seq := m.refreshSeq
	if seq == 0 {
		seq = 1
	}
	return tea.Batch(fetchConfigCmd(seq, m.Discovery.AdminURL), fetchServiceLogsCmd(seq))
}

// Update handles Bubble Tea messages and key input.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.MouseMsg:
		return m.handleMouse(msg)
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "r":
			if m.isRefreshing() {
				return m, nil
			}
			return m, m.startFullRefresh()
		case "?":
			if m.State.ActiveView != app.ViewHelp {
				m.helpScroll = 0
			}
			m.State.ToggleHelpView()
			return m, nil
		case "S":
			if m.State.ActiveView != app.ViewSystem {
				m.systemScroll = 0
			}
			m.State.ToggleSystemView()
			return m, nil
		case "c":
			if m.State.ActiveView != app.ViewConfig {
				m.configScroll = 0
			}
			m.State.ToggleConfigView()
			return m, nil
		case "enter", "l", "right":
			if m.State.ActiveView == app.ViewServices && m.State.SelectedSource() != nil {
				m.State.SetActiveView(app.ViewLogs)
				m.State.ClampSelectedAccessLogIndex(m.State.VisibleAccessLogEntries(time.Now()))
			} else if m.State.ActiveView == app.ViewLogs && len(m.State.VisibleAccessLogEntries(time.Now())) > 0 {
				m.detailScroll = 0
				m.State.SetActiveView(app.ViewLogDetail)
			}
			return m, nil
		case "esc", "h", "left":
			m.goBack()
			return m, nil
		case "j", "down":
			if m.State.ActiveView == app.ViewServices {
				before := selectedSourceID(m.State)
				m.State.MoveServiceSelection(1)
				return m, m.startSelectedSourceRefreshIfChanged(before)
			}
			if m.State.ActiveView == app.ViewLogs {
				m.State.MoveAccessLogSelection(1, time.Now())
			} else if m.State.ActiveView == app.ViewLogDetail {
				m.detailScroll++
			} else if m.State.ActiveView == app.ViewConfig {
				m.configScroll++
			} else if m.State.ActiveView == app.ViewSystem {
				m.systemScroll++
			} else if m.State.ActiveView == app.ViewHelp {
				m.helpScroll++
			}
			return m, nil
		case "k", "up":
			if m.State.ActiveView == app.ViewServices {
				before := selectedSourceID(m.State)
				m.State.MoveServiceSelection(-1)
				return m, m.startSelectedSourceRefreshIfChanged(before)
			}
			if m.State.ActiveView == app.ViewLogs {
				m.State.MoveAccessLogSelection(-1, time.Now())
			} else if m.State.ActiveView == app.ViewLogDetail {
				m.detailScroll = max(0, m.detailScroll-1)
			} else if m.State.ActiveView == app.ViewConfig {
				m.configScroll = max(0, m.configScroll-1)
			} else if m.State.ActiveView == app.ViewSystem {
				m.systemScroll = max(0, m.systemScroll-1)
			} else if m.State.ActiveView == app.ViewHelp {
				m.helpScroll = max(0, m.helpScroll-1)
			}
			return m, nil
		case "e":
			if m.State.ActiveView == app.ViewLogs || m.State.ActiveView == app.ViewLogDetail {
				m.State.SetLogFilters(!m.State.LogsErrorOnly, m.State.LogsSlowOnly, m.State.LogsTimeWindow)
				m.State.ClampSelectedAccessLogIndex(m.State.VisibleAccessLogEntries(time.Now()))
			} else if m.State.ActiveView == app.ViewSystem {
				m.State.SetLogFilters(!m.State.LogsErrorOnly, m.State.LogsSlowOnly, m.State.LogsTimeWindow)
			}
			return m, nil
		case "s":
			if m.State.ActiveView == app.ViewLogs || m.State.ActiveView == app.ViewLogDetail {
				m.State.SetLogFilters(m.State.LogsErrorOnly, !m.State.LogsSlowOnly, m.State.LogsTimeWindow)
				m.State.ClampSelectedAccessLogIndex(m.State.VisibleAccessLogEntries(time.Now()))
			}
			return m, nil
		case "d", "w", "a":
			if m.State.ActiveView == app.ViewLogs || m.State.ActiveView == app.ViewLogDetail {
				m.State.SetLogFilters(m.State.LogsErrorOnly, m.State.LogsSlowOnly, logsTimeWindowForKey(msg.String()))
				m.State.SelectedAccessLogIdx = 0
				m.State.ClampSelectedAccessLogIndex(m.State.VisibleAccessLogEntries(time.Now()))
			}
			return m, nil
		case "v":
			if m.validating {
				return m, nil
			}
			m.validating = true
			return m, validateConfigCmd(m.Discovery)
		}
	case configLoadedMsg:
		if msg.seq != m.refreshSeq {
			return m, nil
		}
		m.configRefreshing = false
		m.State.LastLoad = &msg.result
		fallbackIndex := m.State.SelectedIndex
		preferredID := ""
		if selected := m.State.SelectedSource(); selected != nil {
			preferredID = selected.ID
		}
		if msg.result.Config != nil {
			m.State.SetSources(caddy.ExtractCaddySources(msg.result.Config), preferredID, fallbackIndex)
		} else {
			m.State.SetSources(nil, preferredID, fallbackIndex)
		}
		cmds := []tea.Cmd{}
		if selected := m.State.SelectedSource(); selected != nil && !m.healthRefreshing {
			source := *selected
			m.healthRefreshing = true
			m.healthRefreshingSourceID = source.ID
			cmds = append(cmds, checkHealthCmd(msg.seq, source))
		}
		if !m.serviceLogsRefreshing && m.State.LastLogs != nil && len(m.State.Sources) > 0 && !m.accessLogsRefreshing {
			m.accessLogsRefreshing = true
			cmds = append(cmds, fetchAllAccessLogsCmd(msg.seq, m.State.Sources, m.State.LastLogs))
		}
		return m, tea.Batch(cmds...)
	case serviceLogsLoadedMsg:
		if msg.seq != m.refreshSeq {
			return m, nil
		}
		m.serviceLogsRefreshing = false
		m.State.SetServiceLogs(&msg.result)
		if !m.configRefreshing && len(m.State.Sources) > 0 && !m.accessLogsRefreshing {
			m.accessLogsRefreshing = true
			return m, fetchAllAccessLogsCmd(msg.seq, m.State.Sources, &msg.result)
		}
		return m, nil
	case allAccessLogsLoadedMsg:
		if msg.seq != m.refreshSeq {
			return m, nil
		}
		m.accessLogsRefreshing = false
		selectedID := ""
		if selected := m.State.SelectedSource(); selected != nil {
			selectedID = selected.ID
		}
		now := time.Now()
		for _, result := range msg.results {
			source := result.source
			logs := result.logs
			m.State.UpdateAccessLogStats(&source, &logs, now)
			if source.ID == selectedID {
				m.State.SetAccessLogs(&logs)
			}
		}
		if selectedID == "" {
			m.State.SetAccessLogs(nil)
		}
		if m.State.ActiveView == app.ViewLogs {
			m.State.ClampSelectedAccessLogIndex(m.State.VisibleAccessLogEntries(now))
		}
		return m, nil
	case accessLogsLoadedMsg:
		if msg.seq != m.refreshSeq {
			return m, nil
		}
		m.accessLogsRefreshing = false
		if selected := m.State.SelectedSource(); selected != nil && selected.ID == msg.sourceID {
			result := msg.result
			now := time.Now()
			m.State.SetAccessLogs(&result)
			m.State.UpdateAccessLogStats(selected, &result, now)
			if m.State.ActiveView == app.ViewLogs {
				m.State.ClampSelectedAccessLogIndex(m.State.VisibleAccessLogEntries(now))
			}
		}
		return m, nil
	case healthLoadedMsg:
		if msg.seq != m.refreshSeq {
			return m, nil
		}
		m.healthRefreshing = false
		m.healthRefreshingSourceID = ""
		m.State.UpstreamHealthBySource[msg.sourceID] = msg.results
		return m, nil
	case validationLoadedMsg:
		m.validating = false
		m.State.LastValidation = &msg.result
		return m, nil
	case refreshFailedMsg:
		if msg.seq != m.refreshSeq {
			return m, nil
		}
		m.lastError = fmt.Sprintf("%s: %v", msg.stage, msg.err)
		switch msg.stage {
		case "config":
			m.configRefreshing = false
		case "service_logs":
			m.serviceLogsRefreshing = false
		case "access_logs":
			m.accessLogsRefreshing = false
		case "health":
			m.healthRefreshing = false
			m.healthRefreshingSourceID = ""
		}
		return m, nil
	}
	return m, nil
}

// View renders the current screen.
func (m Model) View() string {
	width := m.width
	if width <= 0 {
		width = 80
	}
	contentWidth := max(1, width-4)

	lines := []string{}
	if m.State.ActiveView != app.ViewServices && m.State.ActiveView != app.ViewLogs && m.State.ActiveView != app.ViewLogDetail && m.State.ActiveView != app.ViewConfig && m.State.ActiveView != app.ViewSystem {
		lines = append(lines, m.headerLine(), "")
	}
	switch m.State.ActiveView {
	case app.ViewServices:
		lines = append(lines, m.servicesViewLines(contentWidth)...)
	case app.ViewLogs:
		lines = append(lines, m.logsViewLines(contentWidth)...)
	case app.ViewLogDetail:
		lines = append(lines, m.requestDetailViewLines(contentWidth)...)
	case app.ViewConfig:
		lines = append(lines, m.configViewLines(contentWidth)...)
	case app.ViewSystem:
		lines = append(lines, m.systemViewLines(contentWidth)...)
	case app.ViewHelp:
		lines = append(lines, m.helpViewLines(contentWidth)...)
	default:
		lines = append(lines, m.servicesViewLines(contentWidth)...)
	}
	if m.lastError != "" {
		lines = append(lines, "", statusErrStyle.Render(m.lastError))
	}

	return pageStyle.Width(width).Render(m.fillScreen(lines, mutedStyle.Render(m.footerLine()), contentWidth))
}

func (m Model) headerLine() string {
	return titleStyle.Render("lazycaddy")
}

func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch msg.Button {
	case tea.MouseButtonWheelDown:
		return m.scrollActiveView(1)
	case tea.MouseButtonWheelUp:
		return m.scrollActiveView(-1)
	default:
		return m, nil
	}
}

func (m Model) scrollActiveView(delta int) (tea.Model, tea.Cmd) {
	switch m.State.ActiveView {
	case app.ViewServices:
		before := selectedSourceID(m.State)
		m.State.MoveServiceSelection(delta)
		return m, m.startSelectedSourceRefreshIfChanged(before)
	case app.ViewLogs:
		m.State.MoveAccessLogSelection(delta, time.Now())
	case app.ViewLogDetail:
		m.detailScroll = max(0, m.detailScroll+delta)
	case app.ViewConfig:
		m.configScroll = max(0, m.configScroll+delta)
	case app.ViewSystem:
		m.systemScroll = max(0, m.systemScroll+delta)
	case app.ViewHelp:
		m.helpScroll = max(0, m.helpScroll+delta)
	}
	return m, nil
}

func (m Model) fillScreen(lines []string, footer string, width int) string {
	if m.height > 0 {
		// Account for pageStyle's top and bottom padding. Keeping the footer on a
		// stable last line also clears content left over from the previous screen.
		contentHeight := max(1, m.height-2)
		bodyHeight := max(0, contentHeight-1)
		if len(lines) > bodyHeight {
			lines = lines[:bodyHeight]
		}
		for len(lines) < bodyHeight {
			lines = append(lines, "")
		}
		lines = append(lines, footer)
	} else {
		lines = append(lines, "", footer)
	}

	padded := make([]string, len(lines))
	lineStyle := lipgloss.NewStyle().Width(width)
	for index, line := range lines {
		padded[index] = lineStyle.Render(line)
	}
	return strings.Join(padded, "\n")
}

func (m Model) servicesViewLines(width int) []string {
	if width <= 0 {
		width = 80
	}
	lines := []string{}
	if m.configRefreshing && len(m.State.Sources) == 0 {
		return append(lines,
			"Loading active config",
			mutedStyle.Render("Fetching active config from the running Caddy Admin API"),
		)
	}
	if m.State.LastLoad != nil && !m.State.LastLoad.OK {
		return append(lines,
			statusErrStyle.Render("Caddy API cannot be reached"),
			mutedStyle.Render(adminRetryHint(m.Discovery.AdminURL)),
		)
	}
	if len(m.State.Sources) == 0 {
		return append(lines,
			"No proxied sources found",
			mutedStyle.Render("Active config loaded, but no sources with reverse_proxy routes were detected"),
		)
	}

	for index, source := range m.State.Sources {
		selected := index == m.State.SelectedIndex
		title := caddy.SourceLabel(source)
		if selected {
			lines = append(lines, selectedLine(title, width), m.selectedServiceMetaLine(source, width))
		} else {
			meta := fmt.Sprintf("  ≡ %s  ⇄ %s  %s %s",
				m.serviceLogsLabel(source),
				serviceRouteCountLabel(source),
				m.serviceReachabilityDot(source),
				truncateOneLine(caddy.SourceProxySummary(source), max(12, width-32)),
			)
			lines = append(lines, title, mutedStyle.Render(meta))
		}
	}
	return lines
}

func (m Model) logsViewLines(width int) []string {
	if width <= 0 {
		width = 80
	}
	serviceLabel := "selected source"
	if selected := m.State.SelectedSource(); selected != nil {
		serviceLabel = caddy.SourceLabel(*selected)
	}
	lines := []string{m.logsBreadcrumbLine(width), ""}
	lines = append(lines, m.accessLogsFilterLine(width), "")

	if m.accessLogsRefreshing && m.State.LastAccessLogs == nil {
		return append(lines, "Checking selected source access logs...")
	}
	if m.State.LastAccessLogs == nil {
		return append(lines, "No source access logs loaded yet.")
	}
	if !m.State.LastAccessLogs.Available {
		return append(lines, fmt.Sprintf("Access logs off for %s.", serviceLabel))
	}
	if !m.State.LastAccessLogs.OK && m.State.LastAccessLogs.Output == "" {
		message := m.State.LastAccessLogs.Error
		if message == "" {
			message = "Access log read error."
		}
		return append(lines, statusErrStyle.Render(message))
	}

	now := time.Now()
	entries := m.State.VisibleAccessLogEntries(now)
	if len(entries) == 0 {
		if m.State.LogsErrorOnly {
			return append(lines, "No error/warning requests found.")
		}
		return append(lines, "No parsed requests found.")
	}
	window := m.accessLogWindow(entries)
	lines = append(lines, requestTableHeader(width), mutedStyle.Render(strings.Repeat("─", min(width, len(requestTableHeader(width))))))
	for index, entry := range window.entries {
		selected := window.start+index == m.State.SelectedAccessLogIdx
		lines = append(lines, m.requestTableRow(entry, selected, width))
	}
	return m.appendLogsPageSummaryAtBottom(lines, mutedStyle.Render(m.accessLogsPageSummary(entries, window.start, window.end, now)))
}

type accessLogWindow struct {
	start   int
	end     int
	entries []applogs.ParsedAccessLogEntry
}

func (m Model) appendLogsPageSummaryAtBottom(lines []string, summary string) []string {
	if m.height <= 0 {
		return append(lines, summary)
	}

	// View() reserves one line for the footer and pageStyle adds top/bottom
	// padding. Keep pagination on the final body line, directly above footer,
	// even when the request table has only a few rows.
	bodyHeight := max(1, m.height-3)
	for len(lines) < bodyHeight-1 {
		lines = append(lines, "")
	}
	return append(lines, summary)
}

func (m Model) logsBreadcrumbLine(width int) string {
	serviceLabel := m.breadcrumbServiceLabel(width)
	return serviceLabel + mutedStyle.Render(" › ") + breadcrumbActiveStyle().Render("logs")
}

func (m Model) configBreadcrumbLine(width int) string {
	serviceLabel := m.breadcrumbServiceLabel(width)
	return serviceLabel + mutedStyle.Render(" › ") + breadcrumbActiveStyle().Render("config")
}

func (m Model) systemBreadcrumbLine() string {
	return "caddy" + mutedStyle.Render(" › ") + breadcrumbActiveStyle().Render("system")
}

func (m Model) breadcrumbServiceLabel(width int) string {
	serviceLabel := "selected source"
	if selected := m.State.SelectedSource(); selected != nil {
		serviceLabel = caddy.SourceLabel(*selected)
	}
	return truncateOneLine(serviceLabel, max(12, min(36, width/2)))
}

func breadcrumbActiveStyle() lipgloss.Style {
	return lipgloss.NewStyle().Bold(true).Foreground(colorActive)
}

func (m Model) accessLogWindow(entries []applogs.ParsedAccessLogEntry) accessLogWindow {
	if len(entries) == 0 {
		return accessLogWindow{}
	}
	selected := clamp(m.State.SelectedAccessLogIdx, 0, len(entries)-1)
	visibleRows := m.accessLogVisibleRows()
	before := min(12, max(1, (visibleRows-1)/3))
	after := min(28, max(1, visibleRows-before-1))
	start := max(0, selected-before)
	end := min(len(entries), selected+after+1)
	if end-start < visibleRows {
		start = max(0, min(start, end-visibleRows))
		end = min(len(entries), max(end, start+visibleRows))
	}
	return accessLogWindow{start: start, end: end, entries: append([]applogs.ParsedAccessLogEntry{}, entries[start:end]...)}
}

func (m Model) accessLogVisibleRows() int {
	if m.height <= 0 {
		return 8
	}
	return max(8, m.height-11)
}

func (m Model) accessLogsFilterLine(width int) string {
	left := strings.Join([]string{
		filterLabel("day", "d", m.State.LogsTimeWindow == app.LogsTimeWindowDay),
		filterLabel("week", "w", m.State.LogsTimeWindow == app.LogsTimeWindowWeek),
		filterLabel("all", "a", m.State.LogsTimeWindow == app.LogsTimeWindowAll),
	}, "  ")
	right := strings.Join([]string{
		filterLabel("slow", "s", m.State.LogsSlowOnly),
		filterLabel("error", "e", m.State.LogsErrorOnly),
	}, "  ")
	padding := strings.Repeat(" ", max(1, width-lipgloss.Width(left)-lipgloss.Width(right)))
	return left + padding + right
}

func filterLabel(label string, key string, active bool) string {
	dot := mutedStyle.Render("○ ")
	rest := mutedStyle.Render(label[1:])
	if active {
		dot = statusWarnStyle.Render("● ")
		rest = lipgloss.NewStyle().Foreground(colorPrimary).Bold(true).Render(label[1:])
	}

	return dot + statusWarnStyle.Render(key) + rest
}

func (m Model) accessLogsPageSummary(entries []applogs.ParsedAccessLogEntry, start int, end int, now time.Time) string {
	if len(entries) == 0 {
		return "0/0"
	}
	slow := 0
	for _, entry := range m.State.AccessLogEntriesForTimeWindow(m.State.ParsedAccessLogEntries(), now) {
		if m.State.IsSlowAccessLogEntry(entry) {
			slow++
		}
	}
	return fmt.Sprintf("%d-%d of %d · %d slow", start+1, end, len(entries), slow)
}

func requestTableHeader(width int) string {
	pathWidth := requestTablePathWidth(width)
	return strings.Join([]string{padRight("TIME", 8), padRight("ST", 4), padRight("METHOD", 6), padRight("PATH", pathWidth), padLeft("LATENCY", 8)}, " ")
}

func (m Model) requestTableRow(entry applogs.ParsedAccessLogEntry, selected bool, width int) string {
	pathWidth := requestTablePathWidth(width)
	status := "-"
	if entry.Status != 0 {
		status = fmt.Sprint(entry.Status)
	}

	left := padRight(formatRequestTime(entry.Timestamp), 8) + " "
	statusPart := padRight(status, 4)
	path := truncateOneLine(firstNonEmpty(accessLogPath(entry.URI), entry.URI, entry.Message, "-"), pathWidth)
	right := " " + strings.Join([]string{
		padRight(firstNonEmpty(entry.Method, "-"), 6),
		padRight(path, pathWidth),
		padLeft(formatRequestLatency(entry.DurationMS), 8),
	}, " ")

	if selected {
		padding := strings.Repeat(" ", max(0, width-lipgloss.Width(left)-lipgloss.Width(statusPart)-lipgloss.Width(right)))
		return selectedServiceStyle.Render(left) + statusCodeStyle(entry.Status, true).Render(statusPart) + selectedServiceStyle.Render(right+padding)
	}

	return left + statusCodeStyle(entry.Status, false).Render(statusPart) + right
}

func statusCodeStyle(status int, selected bool) lipgloss.Style {
	style := lipgloss.NewStyle()
	if selected {
		style = style.Background(colorSelectedBg)
	}
	switch {
	case status >= 500:
		return style.Foreground(colorErr)
	case status >= 400:
		return style.Foreground(colorWarn)
	case status >= 300:
		return style.Foreground(colorActive)
	case status >= 200:
		return style.Foreground(colorOK)
	default:
		return style.Foreground(colorMuted)
	}
}

func requestTablePathWidth(width int) int {
	return max(16, width-32)
}

func formatRequestTime(value *time.Time) string {
	if value == nil {
		return "-"
	}
	return value.Local().Format("15:04:05")
}

func formatRequestLatency(durationMS float64) string {
	if durationMS <= 0 {
		return "-"
	}
	if durationMS < 1000 {
		return fmt.Sprintf("%.0fms", durationMS)
	}
	return fmt.Sprintf("%.2fs", durationMS/1000)
}

func accessLogPath(rawURI string) string {
	if rawURI == "" {
		return ""
	}
	parsed, err := url.Parse(rawURI)
	if err != nil || parsed.Path == "" {
		if index := strings.Index(rawURI, "?"); index >= 0 {
			return rawURI[:index]
		}
		return rawURI
	}
	return parsed.Path
}

func logsTimeWindowForKey(key string) app.LogsTimeWindow {
	switch key {
	case "w":
		return app.LogsTimeWindowWeek
	case "a":
		return app.LogsTimeWindowAll
	default:
		return app.LogsTimeWindowDay
	}
}

func (m Model) helpViewLines(width int) []string {
	content := helpContentLines()
	visibleRows := m.helpVisibleRows()
	maxScroll := max(0, len(content)-visibleRows)
	scroll := clamp(m.helpScroll, 0, maxScroll)
	end := min(len(content), scroll+visibleRows)
	if scroll > 0 || end < len(content) {
		prefix := mutedStyle.Render(fmt.Sprintf("%d-%d of %d", scroll+1, end, len(content)))
		return append([]string{prefix, ""}, content[scroll:end]...)
	}
	return content
}

func (m Model) helpVisibleRows() int {
	if m.height <= 0 {
		return 22
	}
	return max(8, m.height-6)
}

func helpContentLines() []string {
	return []string{
		"Shortcuts",
		"",
		"?        help",
		"S        system",
		"r        refresh",
		"q        quit",
		"",
		"↑/↓ j/k  move selection or scroll",
		"→/enter  open",
		"←/esc/h  back",
		"c        config",
		"",
		"Logs",
		"s        slow filter",
		"e        error filter",
		"d/w/a    day/week/all",
		"",
		"System",
		"v        validate config",
	}
}

func (m Model) systemViewLines(width int) []string {
	if width <= 0 {
		width = 80
	}
	content := m.systemContentLines(width)
	visibleRows := max(1, m.systemVisibleRows()-2)
	maxScroll := max(0, len(content)-visibleRows)
	scroll := clamp(m.systemScroll, 0, maxScroll)
	end := min(len(content), scroll+visibleRows)
	body := content[scroll:end]
	if scroll > 0 || end < len(content) {
		prefix := mutedStyle.Render(fmt.Sprintf("%d-%d of %d", scroll+1, end, len(content)))
		body = append([]string{prefix, ""}, body...)
	}
	return append([]string{m.systemBreadcrumbLine(), ""}, body...)
}

func (m Model) systemContentLines(width int) []string {
	lines := []string{}
	lines = append(lines, m.serviceStatusLines()...)
	lines = append(lines, "")
	lines = append(lines, m.startupConfigLines()...)
	lines = append(lines, "")
	lines = append(lines, m.validationLines()...)
	lines = append(lines, "")
	lines = append(lines, m.adminAPILines()...)
	lines = append(lines, "")
	lines = append(lines, m.discoveryLines(width)...)
	lines = append(lines, "")
	lines = append(lines, m.serviceLogsLines(width)...)
	return lines
}

func (m Model) serviceStatusLines() []string {
	service := m.Discovery.Service
	if service == nil {
		return appendDetailSection([]string{}, "Caddy service", compactStrings([]string{
			"○ caddy.service not inspected",
			fieldLine("Reason", m.Discovery.SourceLabel),
		}))
	}
	state := strings.Join(compactStrings([]string{service.ActiveState, service.SubState}), "/")
	if state == "" {
		state = "unknown"
	}
	return appendDetailSection([]string{}, "Caddy service", compactStrings([]string{
		m.serviceStateDot(service.ActiveState) + " " + serviceStateText(state),
		fieldLine("Loaded", firstNonEmpty(service.LoadState, "unknown")),
		fieldLine("PID", pidLabel(service.MainPID)),
		fieldLine("Unit", firstNonEmpty(service.FragmentPath, "n/a")),
	}))
}

func (m Model) startupConfigLines() []string {
	command := m.Discovery.Command
	executable := ""
	if command != nil && len(command.Argv) > 0 {
		executable = command.Argv[0]
	} else if m.Discovery.Service != nil && len(m.Discovery.Service.Argv) > 0 {
		executable = m.Discovery.Service.Argv[0]
	}
	resume := "no"
	if command != nil && command.Resume {
		resume = "yes"
	}
	argsSource := "not available"
	if m.Discovery.Service != nil && m.Discovery.Service.ArgvSource != "" {
		argsSource = m.Discovery.Service.ArgvSource
	} else if command != nil {
		argsSource = "process"
	}
	return appendDetailSection([]string{}, "Startup config", compactStrings([]string{
		fieldLine("Config", firstNonEmpty(m.Discovery.ConfigPath, commandConfigPath(command), "not discovered")),
		fieldLine("Adapter", firstNonEmpty(m.Discovery.Adapter, commandAdapter(command), "caddyfile/default")),
		fieldLine("Resume", resume),
		fieldLine("Command", firstNonEmpty(executable, "not available")),
		fieldLine("Args", argsSource),
	}))
}

func (m Model) validationLines() []string {
	if m.validating {
		return appendDetailSection([]string{}, "Validation", []string{"● Validating Caddy config..."})
	}
	if m.State.LastValidation == nil {
		return appendDetailSection([]string{}, "Validation", compactStrings([]string{
			"○ Not run yet. Press v to validate.",
			fieldLine("Config", firstNonEmpty(m.Discovery.ConfigPath, commandConfigPath(m.Discovery.Command), "not discovered")),
		}))
	}
	validation := m.State.LastValidation
	status := "FAILED"
	if validation.OK {
		status = "OK"
	} else if validation.Skipped {
		status = "SKIPPED"
	}
	lines := appendDetailSection([]string{}, "Validation", compactStrings([]string{
		validationDot(validation.OK) + " " + status,
		fieldLine("Command", commandLineLabel(validation.Command)),
		fieldLine("Ran", fmt.Sprintf("%s in %dms", timeLabel(validation.RanAt), validation.DurationMS)),
	}))
	for _, line := range validationOutputLines(validation.Output, validation.OK) {
		lines = append(lines, "  "+line)
	}
	return lines
}

func (m Model) adminAPILines() []string {
	proxyCount := 0
	for _, source := range m.State.Sources {
		proxyCount += source.ProxyCount
	}
	active := "n/a"
	fetched := "n/a"
	if m.State.LastLoad != nil && m.State.LastLoad.OK {
		active = fmt.Sprintf("%d sources · %d proxies", len(m.State.Sources), proxyCount)
		fetched = fmt.Sprintf("%s in %dms", timeLabel(m.State.LastLoad.FetchedAt), m.State.LastLoad.DurationMS)
	}
	return appendDetailSection([]string{}, "Admin API", compactStrings([]string{
		m.adminStatusDot() + " " + m.adminStatusLabel(),
		fieldLine("Endpoint", firstNonEmpty(m.Discovery.AdminURL, "none")),
		fieldLine("Active", active),
		fieldLine("Fetched", fetched),
		fieldLine("Source", m.Discovery.SourceLabel),
	}))
}

func (m Model) discoveryLines(width int) []string {
	notes := m.Discovery.Notes
	if len(notes) == 0 {
		notes = []string{"No discovery notes."}
	}
	lines := []string{titleStyle.Render("Discovery")}
	for _, note := range notes {
		for index, wrapped := range wrapText(note, max(12, width-2)) {
			prefix := "• "
			if index > 0 {
				prefix = "  "
			}
			lines = append(lines, mutedStyle.Render(prefix)+wrapped)
		}
	}
	return lines
}

func (m Model) serviceLogsLines(width int) []string {
	lines := []string{titleStyle.Render("Service logs")}
	if m.serviceLogsRefreshing && m.State.LastLogs == nil {
		return append(lines, "● Fetching caddy.service logs...")
	}
	if m.State.LastLogs == nil {
		return append(lines, "○ No logs loaded yet.")
	}
	logs := m.State.LastLogs
	sourceText := logs.Error
	if logs.OK {
		sourceText = fmt.Sprintf("%s in %dms", timeLabel(logs.FetchedAt), logs.DurationMS)
	} else if sourceText == "" {
		sourceText = "journalctl failed"
	}
	lines = append(lines, serviceLogStatusDot(logs.OK)+" "+sourceText)
	visible := m.State.VisibleServiceLogEntries()
	if len(visible) == 0 {
		if m.State.LogsErrorOnly {
			return append(lines, "No error/warning service log entries found.")
		}
		return append(lines, "No service log entries.")
	}
	lines = append(lines, mutedStyle.Render(serviceLogTableHeader(width)))
	for _, entry := range visible {
		lines = append(lines, serviceLogRow(entry, width))
	}
	suffix := fmt.Sprintf("%d entries", len(visible))
	if m.State.LogsErrorOnly {
		suffix += " · errors only"
	}
	return append(lines, mutedStyle.Render(suffix))
}

func (m Model) systemVisibleRows() int {
	if m.height <= 0 {
		return 22
	}
	return max(8, m.height-6)
}

func (m Model) serviceStateDot(activeState string) string {
	if activeState == "active" {
		return statusOKStyle.Render("●")
	}
	if activeState == "activating" || activeState == "reloading" {
		return statusWarnStyle.Render("●")
	}
	if activeState == "failed" || activeState == "inactive" {
		return statusErrStyle.Render("●")
	}
	return mutedStyle.Render("●")
}

func serviceStateText(state string) string {
	return state
}

func pidLabel(pid int) string {
	if pid > 0 {
		return fmt.Sprint(pid)
	}
	return "n/a"
}

func validationDot(ok bool) string {
	if ok {
		return statusOKStyle.Render("●")
	}
	return statusErrStyle.Render("●")
}

func commandLineLabel(command []string) string {
	if len(command) == 0 {
		return "not run"
	}
	return strings.Join(command, " ")
}

func validationOutputLines(output string, ok bool) []string {
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	result := []string{}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			result = append(result, line)
		}
		if len(result) >= 3 {
			break
		}
	}
	if len(result) == 0 && ok {
		return []string{"Config is valid."}
	}
	return result
}

func (m Model) adminStatusDot() string {
	if m.isRefreshing() {
		return statusWarnStyle.Render("●")
	}
	if m.State.LastLoad != nil && m.State.LastLoad.OK {
		return statusOKStyle.Render("●")
	}
	if m.Discovery.Disabled {
		return mutedStyle.Render("●")
	}
	return statusErrStyle.Render("●")
}

func (m Model) adminStatusLabel() string {
	if m.isRefreshing() {
		return "refreshing"
	}
	if m.State.LastLoad != nil && m.State.LastLoad.OK {
		if m.State.LastLoad.StatusCode != 0 {
			return fmt.Sprintf("OK %d", m.State.LastLoad.StatusCode)
		}
		return "OK"
	}
	if m.Discovery.Disabled {
		return "disabled"
	}
	if m.State.LastLoad != nil && m.State.LastLoad.Error != "" {
		return m.State.LastLoad.Error
	}
	return "unavailable"
}

func timeLabel(value time.Time) string {
	if value.IsZero() {
		return "n/a"
	}
	return value.Local().Format("15:04:05")
}

func serviceLogStatusDot(ok bool) string {
	if ok {
		return statusOKStyle.Render("●")
	}
	return statusErrStyle.Render("●")
}

func serviceLogTableHeader(width int) string {
	loggerWidth := serviceLogLoggerWidth(width)
	return strings.Join([]string{padRight("TYPE", 4), padRight("TIME", 8), padRight("LOGGER", loggerWidth), "MESSAGE"}, " ")
}

func serviceLogRow(entry applogs.ParsedServiceLogEntry, width int) string {
	kind := applogs.ServiceLogKind(entry)
	timeText := "-"
	if entry.Timestamp != nil {
		timeText = formatRequestTime(entry.Timestamp)
	}
	loggerWidth := serviceLogLoggerWidth(width)
	messageWidth := max(20, width-loggerWidth-16)
	logger := truncateOneLine(firstNonEmpty(entry.Logger, entry.Unit, "caddy"), loggerWidth)
	suffix := ""
	if entry.Error != "" {
		suffix = "  " + entry.Error
	} else if entry.File != "" {
		suffix = "  " + entry.File
		if entry.Line != 0 {
			suffix += fmt.Sprintf(":%d", entry.Line)
		}
	}
	message := truncateOneLine(firstNonEmpty(entry.Message, entry.Error, entry.Raw, "-")+suffix, messageWidth)
	row := strings.Join([]string{padRight(kind, 4), padRight(timeText, 8), padRight(logger, loggerWidth), message}, " ")
	if kind == "ERR" {
		return statusErrStyle.Render(row)
	}
	if kind == "WARN" {
		return statusWarnStyle.Render(row)
	}
	return row
}

func serviceLogLoggerWidth(width int) int {
	return min(24, max(12, width*22/100))
}

func (m Model) configViewLines(width int) []string {
	if width <= 0 {
		width = 80
	}
	content := m.configContentLines(width)
	visibleRows := max(1, m.configVisibleRows()-2)
	maxScroll := max(0, len(content)-visibleRows)
	scroll := clamp(m.configScroll, 0, maxScroll)
	end := min(len(content), scroll+visibleRows)
	body := content[scroll:end]
	if scroll > 0 || end < len(content) {
		prefix := mutedStyle.Render(fmt.Sprintf("%d-%d of %d", scroll+1, end, len(content)))
		body = append([]string{prefix, ""}, body...)
	}
	return append([]string{m.configBreadcrumbLine(width), ""}, body...)
}

func (m Model) configContentLines(width int) []string {
	source := m.State.SelectedSource()
	block := caddy.FindCaddyfileSource(m.CaddyfileCorrelation, source)
	lines := []string{}
	lines = append(lines, m.configSummaryLines(source, block)...)
	lines = append(lines, "")
	lines = append(lines, titleStyle.Render("Caddyfile block"))
	for _, line := range caddy.FormatCaddyfileBlock(m.CaddyfileCorrelation, block) {
		lines = append(lines, truncateOneLine(line, width))
	}
	lines = append(lines, "")
	lines = append(lines, m.activeConfigSummaryLines(source, width)...)
	return lines
}

func (m Model) configSummaryLines(source *caddy.CaddySource, block *caddy.CaddyfileSourceBlock) []string {
	configPath := firstNonEmpty(m.Discovery.ConfigPath, commandConfigPath(m.Discovery.Command), "not discovered")
	adapter := firstNonEmpty(m.Discovery.Adapter, commandAdapter(m.Discovery.Command), "caddyfile/default")
	if source == nil {
		return appendDetailSection([]string{}, "Config", compactStrings([]string{
			fieldLine("Config", configPath),
			fieldLine("Adapter", adapter),
		}))
	}
	location := caddy.CaddyfileLocation(m.CaddyfileCorrelation, block, 0)
	if location == "" {
		location = firstNonEmpty(m.CaddyfileCorrelation.Error, "not correlated")
	}
	blockText := "not correlated"
	if block != nil {
		end := "?"
		if block.EndLine != 0 {
			end = fmt.Sprint(block.EndLine)
		}
		blockText = fmt.Sprintf("lines %d-%s", block.Line, end)
	}
	return appendDetailSection([]string{}, "Config source", compactStrings([]string{
		fieldLine("Host", caddy.SourceLabel(*source)),
		fieldLine("Caddyfile", location),
		fieldLine("Block", blockText),
		fieldLine("Adapter", adapter),
		fieldLine("Config", configPath),
	}))
}

func (m Model) activeConfigSummaryLines(source *caddy.CaddySource, width int) []string {
	if source == nil {
		return appendDetailSection([]string{}, "Active config", []string{"No source selected."})
	}
	lines := appendDetailSection([]string{}, "Active runtime config", compactStrings([]string{
		fieldLine("Server", source.ServerName),
		fieldLine("Listener", firstNonEmpty(strings.Join(source.Listen, ", "), "default")),
		fieldLine("Routes", fmt.Sprint(len(source.Routes))),
		fieldLine("Proxies", fmt.Sprint(source.ProxyCount)),
	}))
	if len(source.Routes) == 0 {
		return lines
	}
	if len(lines) > 0 && lines[len(lines)-1] != "" {
		lines = append(lines, "")
	}
	lines = append(lines, titleStyle.Render("Routes"))
	for index, route := range source.Routes {
		prefix := fmt.Sprintf("%2d. ", index+1)
		lines = append(lines, mutedStyle.Render(prefix)+truncateOneLine(route.Matcher, max(12, width-len(prefix))))
		actions := []string{}
		for _, action := range route.Actions {
			actions = append(actions, action.Label)
		}
		label := strings.Join(actions, "; ")
		if label == "" {
			label = "no direct handlers"
		}
		for _, wrapped := range wrapText("↳ "+label, max(12, width-3)) {
			lines = append(lines, mutedStyle.Render("   ")+wrapped)
		}
	}
	return lines
}

func (m Model) configVisibleRows() int {
	if m.height <= 0 {
		return 18
	}
	return max(8, m.height-6)
}

func (m Model) requestDetailViewLines(width int) []string {
	if width <= 0 {
		width = 80
	}
	breadcrumb := m.requestDetailBreadcrumbLine(nil, width)
	if m.accessLogsRefreshing && m.State.LastAccessLogs == nil {
		return []string{breadcrumb, "", "Waiting for access logs..."}
	}
	if m.State.LastAccessLogs == nil {
		return []string{breadcrumb, "", "No access log entry selected."}
	}
	if !m.State.LastAccessLogs.Available {
		return []string{breadcrumb, "", accessLogHelper(m.State.SelectedSource())}
	}

	entries := m.State.VisibleAccessLogEntries(time.Now())
	if len(entries) == 0 {
		return []string{breadcrumb, "", "No access log entry selected."}
	}
	selectedIndex := clamp(m.State.SelectedAccessLogIdx, 0, len(entries)-1)
	entry := entries[selectedIndex]
	breadcrumb = m.requestDetailBreadcrumbLine(&entry, width)

	content := m.requestDetailContentLines(entry, width)
	visibleRows := max(1, m.detailVisibleRows()-2)
	maxScroll := max(0, len(content)-visibleRows)
	scroll := clamp(m.detailScroll, 0, maxScroll)
	end := min(len(content), scroll+visibleRows)
	body := content[scroll:end]
	if scroll > 0 || end < len(content) {
		prefix := mutedStyle.Render(fmt.Sprintf("%d-%d of %d", scroll+1, end, len(content)))
		body = append([]string{prefix, ""}, body...)
	}
	return append([]string{breadcrumb, ""}, body...)
}

func (m Model) requestDetailBreadcrumbLine(entry *applogs.ParsedAccessLogEntry, width int) string {
	serviceLabel := m.breadcrumbServiceLabel(width)
	requestLabel := "request"
	if entry != nil {
		status := "-"
		if entry.Status != 0 {
			status = fmt.Sprint(entry.Status)
		}
		requestLabel = strings.Join([]string{status, firstNonEmpty(entry.Method, "-"), firstNonEmpty(accessLogPath(entry.URI), entry.URI, entry.Message, "-")}, " ")
	}
	maxRequestLen := max(12, width-lipgloss.Width(serviceLabel)-lipgloss.Width(" › logs › "))
	requestLabel = truncateOneLine(requestLabel, maxRequestLen)
	return serviceLabel + mutedStyle.Render(" › ") + mutedStyle.Render("logs") + mutedStyle.Render(" › ") + breadcrumbActiveStyle().Render(requestLabel)
}

func (m Model) requestDetailContentLines(entry applogs.ParsedAccessLogEntry, width int) []string {
	path := firstNonEmpty(accessLogPath(entry.URI), entry.URI, entry.Message, "-")
	lines := []string{titleStyle.Render(truncateOneLine(firstNonEmpty(entry.Method, "-")+" "+path, width))}
	status := "-"
	if entry.Status != 0 {
		status = fmt.Sprint(entry.Status)
	}
	lines = append(lines,
		fmt.Sprintf("%s  %s  %s  %s", colorStatus(status, entry.Status), formatRequestLatency(entry.DurationMS), formatSizeBytes(entry.SizeBytes), formatRequestTime(entry.Timestamp)),
		"",
	)
	lines = appendDetailSection(lines, "Matched route", m.matchedRouteLines(entry))
	lines = appendDetailSection(lines, "Request", compactStrings([]string{
		fieldLine("Host", entry.Host),
		fieldLine("Method", entry.Method),
		fieldLine("URI", entry.URI),
		fieldLine("Remote", remoteAddressLabel(entry)),
		fieldLine("Client IP", entry.ClientIP),
		fieldLine("Protocol", entry.Protocol),
		fieldLine("Agent", entry.UserAgent),
		fieldLine("Referer", entry.Referer),
	}))
	lines = appendDetailSection(lines, "Response", compactStrings([]string{
		fieldLine("Status", status),
		fieldLine("Duration", formatRequestLatency(entry.DurationMS)),
		fieldLine("Size", formatSizeBytes(entry.SizeBytes)),
		fieldLine("Error", entry.Error),
	}))
	headerLines := []string{}
	headerLines = append(headerLines, preferredHeaderLines("Request headers", entry.RequestHeaders, []string{"User-Agent", "Accept", "Content-Type", "Content-Length", "X-Forwarded-For", "X-Real-IP"})...)
	headerLines = append(headerLines, preferredHeaderLines("Response headers", entry.ResponseHeaders, []string{"Content-Type", "Content-Length", "Location", "Server", "Cache-Control"})...)
	lines = appendDetailSection(lines, "Headers", headerLines)
	lines = appendDetailSection(lines, "Metadata", compactStrings([]string{
		fieldLine("Logger", entry.Logger),
		fieldLine("Level", entry.Level),
		fieldLine("Message", entry.Message),
		fieldLine("TLS", tlsSummary(entry)),
	}))
	if entry.Raw != "" {
		lines = appendDetailSection(lines, "Raw", wrapText(entry.Raw, max(24, width-2)))
	}
	return lines
}

func (m Model) detailVisibleRows() int {
	if m.height <= 0 {
		return 16
	}
	return max(8, m.height-6)
}

func (m Model) matchedRouteLines(entry applogs.ParsedAccessLogEntry) []string {
	source := m.State.SelectedSource()
	if source == nil {
		return nil
	}
	route := findMatchedRoute(*source, entry)
	if route == nil {
		return []string{"No route match inferred."}
	}
	actions := []string{}
	for _, action := range route.Actions {
		actions = append(actions, action.Label)
	}
	return []string{route.Matcher + " → " + strings.Join(actions, "; ")}
}

func findMatchedRoute(source caddy.CaddySource, entry applogs.ParsedAccessLogEntry) *caddy.CaddyRouteRule {
	var fallback *caddy.CaddyRouteRule
	for index := range source.Routes {
		if source.Routes[index].Matcher == "everything else" || source.Routes[index].Matcher == "all requests" {
			fallback = &source.Routes[index]
			break
		}
	}
	for index := range source.Routes {
		if fallback != nil && &source.Routes[index] == fallback {
			continue
		}
		if routeMatchesAccessLog(source.Routes[index], entry) {
			return &source.Routes[index]
		}
	}
	return fallback
}

func routeMatchesAccessLog(route caddy.CaddyRouteRule, entry applogs.ParsedAccessLogEntry) bool {
	path := accessLogPath(entry.URI)
	if path == "" {
		return false
	}
	matcher := route.Matcher
	if strings.Contains(matcher, "method ") && entry.Method != "" {
		methods := matcherValues(matcher, `method\s+([^;+]+)`)
		if len(methods) > 0 && !containsString(methods, entry.Method) {
			return false
		}
	}
	pathMatchers := matcherValues(matcher, `path\s+([^;+]+)`)
	if len(pathMatchers) == 0 {
		return false
	}
	for _, pathMatcher := range pathMatchers {
		if pathMatches(path, pathMatcher) {
			return true
		}
	}
	return false
}

func matcherValues(value string, pattern string) []string {
	matches := regexpMustCompile(pattern).FindAllStringSubmatch(value, -1)
	result := []string{}
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		for _, part := range strings.FieldsFunc(match[1], func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
			part = strings.TrimSpace(part)
			if part != "" {
				result = append(result, part)
			}
		}
	}
	return result
}

func pathMatches(path string, matcher string) bool {
	if matcher == "*" || matcher == "/*" {
		return true
	}
	if strings.HasSuffix(matcher, "*") {
		return strings.HasPrefix(path, strings.TrimSuffix(matcher, "*"))
	}
	return path == matcher
}

func appendDetailSection(lines []string, title string, rows []string) []string {
	if len(rows) == 0 {
		return lines
	}
	if len(lines) > 0 && lines[len(lines)-1] != "" {
		lines = append(lines, "")
	}
	lines = append(lines, titleStyle.Render(title))
	for _, row := range rows {
		lines = append(lines, "  "+row)
	}
	return lines
}

func fieldLine(name string, value string) string {
	if value == "" || value == "-" {
		return ""
	}
	return name + ": " + value
}

func compactStrings(values []string) []string {
	result := []string{}
	for _, value := range values {
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}

func preferredHeaderLines(title string, headers map[string][]string, names []string) []string {
	if len(headers) == 0 {
		return nil
	}
	lines := []string{title}
	for _, name := range names {
		if value := headerValue(headers, name); value != "" {
			lines = append(lines, "  "+name+": "+value)
		}
	}
	if len(lines) == 1 {
		return nil
	}
	return lines
}

func headerValue(headers map[string][]string, name string) string {
	for key, values := range headers {
		if strings.EqualFold(key, name) && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

func remoteAddressLabel(entry applogs.ParsedAccessLogEntry) string {
	if entry.RemoteIP == "" {
		return ""
	}
	if entry.RemotePort != "" {
		return entry.RemoteIP + ":" + entry.RemotePort
	}
	return entry.RemoteIP
}

func tlsSummary(entry applogs.ParsedAccessLogEntry) string {
	parts := compactStrings([]string{entry.TLS.Version, entry.TLS.CipherSuite, entry.TLS.ServerName, entry.TLS.Protocol})
	return strings.Join(parts, " · ")
}

func formatSizeBytes(bytes int64) string {
	if bytes <= 0 {
		return "-"
	}
	if bytes < 1024 {
		return fmt.Sprintf("%dB", bytes)
	}
	if bytes < 1024*1024 {
		return fmt.Sprintf("%.1fKB", float64(bytes)/1024)
	}
	return fmt.Sprintf("%.1fMB", float64(bytes)/(1024*1024))
}

func colorStatus(value string, status int) string {
	if status >= 500 {
		return statusErrStyle.Render(value)
	}
	if status >= 400 {
		return statusWarnStyle.Render(value)
	}
	if status >= 200 && status < 300 {
		return statusOKStyle.Render(value)
	}
	return mutedStyle.Render(value)
}

func accessLogHelper(source *caddy.CaddySource) string {
	label := "selected source"
	if source != nil {
		label = caddy.SourceLabel(*source)
	}
	return "Access logs are not configured or readable for " + label + "."
}

func wrapText(value string, width int) []string {
	width = max(1, width)
	text := strings.TrimSpace(strings.ReplaceAll(value, "\n", " "))
	if text == "" {
		return []string{""}
	}
	lines := []string{}
	for len(text) > width {
		split := strings.LastIndex(text[:width], " ")
		if split <= 0 {
			split = width
		}
		lines = append(lines, strings.TrimRight(text[:split], " "))
		text = strings.TrimLeft(text[split:], " ")
	}
	return append(lines, text)
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func regexpMustCompile(pattern string) *regexp.Regexp {
	return regexp.MustCompile(pattern)
}

func placeholderViewLines(title string, message string) []string {
	return []string{title, "", mutedStyle.Render(message)}
}

func (m Model) serviceLogsLabel(source caddy.CaddySource) string {
	stats, hasStats := m.State.AccessLogStatsBySource[source.ID]
	disabled := len(source.AccessLogs) == 0 || (hasStats && !stats.Available)
	if disabled {
		return padRight("logs off", len("99999 reqs"))
	}
	if !hasStats || !stats.HasCount {
		return padRight("- reqs", len("99999 reqs"))
	}
	return padRight(formatRequestCountLabel(stats.Count24h), len("99999 reqs"))
}

func (m Model) selectedServiceMetaLine(source caddy.CaddySource, width int) string {
	left := fmt.Sprintf("  ≡ %s  ⇄ %s  ", m.serviceLogsLabel(source), serviceRouteCountLabel(source))
	right := " " + truncateOneLine(caddy.SourceProxySummary(source), max(12, width-32))
	rawWidth := lipgloss.Width(left) + 1 + lipgloss.Width(right)
	padding := strings.Repeat(" ", max(0, width-rawWidth))

	return selectedServiceStyle.Render(left) + m.selectedServiceReachabilityDot(source) + selectedServiceStyle.Render(right+padding)
}

func selectedLine(value string, width int) string {
	padding := strings.Repeat(" ", max(0, width-lipgloss.Width(value)))
	return selectedServiceStyle.Render(value + padding)
}

func (m Model) selectedServiceReachabilityDot(source caddy.CaddySource) string {
	return lipgloss.NewStyle().
		Background(colorSelectedBg).
		Foreground(m.serviceReachabilityColor(source)).
		Render("●")
}

func (m Model) serviceReachabilityDot(source caddy.CaddySource) string {
	return lipgloss.NewStyle().Foreground(m.serviceReachabilityColor(source)).Render("●")
}

func (m Model) serviceReachabilityColor(source caddy.CaddySource) lipgloss.TerminalColor {
	switch m.serviceReachabilityStatus(source) {
	case "REACHABLE":
		return colorOK
	case "UNREACHABLE":
		return colorErr
	case "CHECKING":
		return colorWarn
	default:
		return colorMuted
	}
}

func (m Model) serviceReachabilityStatus(source caddy.CaddySource) string {
	if m.healthRefreshing && m.healthRefreshingSourceID == source.ID {
		return "CHECKING"
	}
	results, ok := m.State.UpstreamHealthBySource[source.ID]
	if !ok {
		return "UNKNOWN"
	}
	hasOK := false
	for _, result := range results {
		if result.Status == caddy.UpstreamHealthDown {
			return "UNREACHABLE"
		}
		if result.Status == caddy.UpstreamHealthOK {
			hasOK = true
		}
	}
	if hasOK {
		return "REACHABLE"
	}
	return "UNKNOWN"
}

func serviceRouteCountLabel(source caddy.CaddySource) string {
	label := fmt.Sprintf("%d route", len(source.Routes))
	if len(source.Routes) != 1 {
		label += "s"
	}
	return padRight(label, len("99 routes"))
}

func formatRequestCountLabel(count int) string {
	if count >= 100000 {
		return fmt.Sprintf("%dk reqs", int(float64(count)/1000+0.5))
	}
	label := fmt.Sprintf("%d req", count)
	if count != 1 {
		label += "s"
	}
	return label
}

func (m Model) footerLine() string {
	base := "? help  S system  r refresh  q quit"
	switch m.State.ActiveView {
	case app.ViewServices:
		return "↑/↓ select  → logs  c config  " + base
	case app.ViewLogs:
		return "← back  → detail  " + base
	case app.ViewLogDetail:
		return "← back  ? help  S system  q quit"
	case app.ViewConfig:
		return "←/c back  " + base
	case app.ViewSystem:
		return "←/S return  ? help  r refresh  v validate  q quit"
	case app.ViewHelp:
		return "←/? return  S system  r refresh  q quit"
	default:
		return base
	}
}

func (m *Model) goBack() {
	switch m.State.ActiveView {
	case app.ViewLogs:
		m.State.SetActiveView(app.ViewServices)
	case app.ViewLogDetail:
		m.State.SetActiveView(app.ViewLogs)
	case app.ViewConfig:
		m.State.SetActiveView(m.State.PreviousConfigView)
	case app.ViewSystem:
		m.State.SetActiveView(m.State.PreviousMainView)
	case app.ViewHelp:
		m.State.SetActiveView(m.State.PreviousHelpView)
	}
}

func (m *Model) startSelectedSourceRefreshIfChanged(previousSourceID string) tea.Cmd {
	selected := m.State.SelectedSource()
	if selected == nil || selected.ID == previousSourceID {
		return nil
	}
	m.State.SetAccessLogs(nil)
	m.State.SelectedAccessLogIdx = -1
	cmds := []tea.Cmd{}
	if !m.healthRefreshing {
		source := *selected
		m.healthRefreshing = true
		m.healthRefreshingSourceID = source.ID
		cmds = append(cmds, checkHealthCmd(m.refreshSeq, source))
	}
	if !m.accessLogsRefreshing && m.State.LastLogs != nil {
		source := *selected
		m.accessLogsRefreshing = true
		cmds = append(cmds, fetchAccessLogsCmd(m.refreshSeq, source, m.State.LastLogs))
	}
	return tea.Batch(cmds...)
}

func selectedSourceID(state *app.State) string {
	if selected := state.SelectedSource(); selected != nil {
		return selected.ID
	}
	return ""
}

func adminRetryHint(adminURL string) string {
	if adminURL == "" {
		return "Make sure a Caddy service is running with its Admin API enabled, or use --admin-url to override"
	}
	return "Make sure the Caddy service is running and reachable, then press r to retry"
}

func padRight(value string, width int) string {
	if len(value) >= width {
		return value[:width]
	}
	return value + strings.Repeat(" ", width-len(value))
}

func padLeft(value string, width int) string {
	if len(value) >= width {
		return value
	}
	return strings.Repeat(" ", width-len(value)) + value
}

func truncateOneLine(value string, maxLength int) string {
	if len(value) <= maxLength {
		return value
	}
	if maxLength <= 1 {
		return "…"
	}
	return value[:maxLength-1] + "…"
}

func max(left int, right int) int {
	if left > right {
		return left
	}
	return right
}

func min(left int, right int) int {
	if left < right {
		return left
	}
	return right
}

func clamp(value int, minValue int, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func (m *Model) startFullRefresh() tea.Cmd {
	m.refreshSeq++
	m.configRefreshing = true
	m.serviceLogsRefreshing = true
	m.accessLogsRefreshing = false
	m.healthRefreshing = false
	m.healthRefreshingSourceID = ""
	m.lastError = ""
	return tea.Batch(fetchConfigCmd(m.refreshSeq, m.Discovery.AdminURL), fetchServiceLogsCmd(m.refreshSeq))
}

func (m Model) isRefreshing() bool {
	return m.configRefreshing || m.serviceLogsRefreshing || m.accessLogsRefreshing || m.healthRefreshing
}

func fetchConfigCmd(seq int, adminURL string) tea.Cmd {
	return func() tea.Msg {
		if adminURL == "" {
			return configLoadedMsg{seq: seq, result: caddy.ConfigLoadResult{AdminURL: "(not discovered)", Endpoint: "(admin API disabled or unsupported)", OK: false, Error: "Admin API disabled or unsupported.", FetchedAt: time.Now()}}
		}
		ctx, cancel := context.WithTimeout(context.Background(), configFetchTimeout)
		defer cancel()
		return configLoadedMsg{seq: seq, result: caddy.FetchActiveConfig(ctx, adminURL)}
	}
}

func fetchServiceLogsCmd(seq int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), serviceLogsTimeout)
		defer cancel()
		return serviceLogsLoadedMsg{seq: seq, result: applogs.FetchCaddyLogs(ctx, 100)}
	}
}

func fetchAllAccessLogsCmd(seq int, sources []caddy.CaddySource, serviceLogs *applogs.CaddyLogsResult) tea.Cmd {
	sourcesCopy := append([]caddy.CaddySource{}, sources...)
	var serviceLogsCopy *applogs.CaddyLogsResult
	if serviceLogs != nil {
		copyValue := *serviceLogs
		serviceLogsCopy = &copyValue
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), accessLogsTimeout)
		defer cancel()
		results := make([]sourceAccessLogsResult, 0, len(sourcesCopy))
		for index := range sourcesCopy {
			source := sourcesCopy[index]
			logs := applogs.FetchAccessLogsForSource(ctx, &source, serviceLogsCopy, 5000)
			results = append(results, sourceAccessLogsResult{source: source, logs: logs})
		}
		return allAccessLogsLoadedMsg{seq: seq, results: results}
	}
}

func fetchAccessLogsCmd(seq int, source caddy.CaddySource, serviceLogs *applogs.CaddyLogsResult) tea.Cmd {
	var serviceLogsCopy *applogs.CaddyLogsResult
	if serviceLogs != nil {
		copyValue := *serviceLogs
		serviceLogsCopy = &copyValue
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), accessLogsTimeout)
		defer cancel()
		return accessLogsLoadedMsg{seq: seq, sourceID: source.ID, result: applogs.FetchAccessLogsForSource(ctx, &source, serviceLogsCopy, 5000)}
	}
}

func checkHealthCmd(seq int, source caddy.CaddySource) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), healthCheckTimeout)
		defer cancel()
		return healthLoadedMsg{seq: seq, sourceID: source.ID, results: caddy.CheckSourceUpstreams(ctx, &source, healthCheckTimeout)}
	}
}

func validateConfigCmd(discovery caddy.AdminAPIDiscovery) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), validationCheckTimeout)
		defer cancel()
		configPath := firstNonEmpty(discovery.ConfigPath, commandConfigPath(discovery.Command))
		adapter := firstNonEmpty(discovery.Adapter, commandAdapter(discovery.Command))
		return validationLoadedMsg{result: caddy.ValidateConfig(ctx, configPath, adapter)}
	}
}

func commandConfigPath(command *caddy.ParsedCaddyRunCommand) string {
	if command == nil {
		return ""
	}
	return command.ConfigPath
}

func commandAdapter(command *caddy.ParsedCaddyRunCommand) string {
	if command == nil {
		return ""
	}
	return command.Adapter
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

var _ = fetchAccessLogsCmd
