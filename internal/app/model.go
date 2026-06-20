package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/kysely/lazycaddy/internal/caddy"
	applogs "github.com/kysely/lazycaddy/internal/logs"
)

// View is the active high-level app view.
type View string

const (
	ViewServices  View = "services"
	ViewLogs      View = "logs"
	ViewLogDetail View = "logDetail"
	ViewConfig    View = "config"
	ViewSystem    View = "system"
	ViewHelp      View = "help"
)

// LogsTimeWindow is the selected access-log time filter.
type LogsTimeWindow string

const (
	LogsTimeWindowDay  LogsTimeWindow = "day"
	LogsTimeWindowWeek LogsTimeWindow = "week"
	LogsTimeWindowAll  LogsTimeWindow = "all"
)

// AccessLogStats is the per-source service-list access-log summary.
type AccessLogStats struct {
	Available bool
	OK        bool
	Count24h  int
	HasCount  bool
}

// State is the UI-independent application state model.
type State struct {
	Sources       []caddy.CaddySource
	SelectedIndex int

	ActiveView         View
	PreviousMainView   View
	PreviousHelpView   View
	PreviousConfigView View

	LogsErrorOnly        bool
	LogsSlowOnly         bool
	LogsTimeWindow       LogsTimeWindow
	SelectedAccessLogIdx int
	SlowRequestThreshold time.Duration

	LastLoad       *caddy.ConfigLoadResult
	LastLogs       *applogs.CaddyLogsResult
	LastAccessLogs *applogs.CaddyAccessLogsResult
	LastValidation *caddy.ValidationResult

	AccessLogStatsBySource map[string]AccessLogStats
	UpstreamHealthBySource map[string][]caddy.UpstreamHealthResult
	parsedAccessLogCache   parsedAccessLogCache
	visibleAccessLogCache  visibleAccessLogCache
	parsedServiceLogCache  parsedServiceLogCache
	visibleServiceLogCache visibleServiceLogCache
}

type parsedAccessLogCache struct {
	valid     bool
	available bool
	output    string
	entries   []applogs.ParsedAccessLogEntry
}

type visibleAccessLogCache struct {
	valid      bool
	available  bool
	output     string
	errorOnly  bool
	slowOnly   bool
	timeWindow LogsTimeWindow
	nowUnix    int64
	entries    []applogs.ParsedAccessLogEntry
}

type parsedServiceLogCache struct {
	valid   bool
	output  string
	error   string
	entries []applogs.ParsedServiceLogEntry
}

type visibleServiceLogCache struct {
	valid     bool
	output    string
	error     string
	errorOnly bool
	entries   []applogs.ParsedServiceLogEntry
}

// NewState returns app state with lazycaddy's default view/filter selections.
func NewState() *State {
	return &State{
		SelectedIndex:          -1,
		ActiveView:             ViewServices,
		PreviousMainView:       ViewServices,
		PreviousHelpView:       ViewServices,
		PreviousConfigView:     ViewLogs,
		LogsTimeWindow:         LogsTimeWindowDay,
		SelectedAccessLogIdx:   -1,
		SlowRequestThreshold:   time.Second,
		AccessLogStatsBySource: map[string]AccessLogStats{},
		UpstreamHealthBySource: map[string][]caddy.UpstreamHealthResult{},
	}
}

// IsMainView reports whether a view is one of the normal service navigation views.
func IsMainView(view View) bool {
	return view == ViewServices || view == ViewLogs || view == ViewLogDetail
}

// SetActiveView switches views and updates previous-view breadcrumbs/toggles.
func (s *State) SetActiveView(view View) {
	if IsMainView(view) && view != ViewLogDetail {
		s.PreviousMainView = view
	}
	s.ActiveView = view
}

// ToggleSystemView opens or returns from the System view.
func (s *State) ToggleSystemView() {
	if s.ActiveView == ViewSystem {
		s.SetActiveView(s.PreviousMainView)
		return
	}
	if IsMainView(s.ActiveView) {
		s.PreviousMainView = s.ActiveView
	}
	if s.ActiveView != ViewHelp {
		s.PreviousHelpView = ViewSystem
	}
	s.SetActiveView(ViewSystem)
}

// ToggleConfigView opens or returns from the Config view.
func (s *State) ToggleConfigView() {
	if s.ActiveView == ViewConfig {
		s.SetActiveView(s.PreviousConfigView)
		return
	}
	if s.ActiveView == ViewServices || s.ActiveView == ViewLogs || s.ActiveView == ViewLogDetail {
		s.PreviousConfigView = s.ActiveView
	}
	s.SetActiveView(ViewConfig)
}

// ToggleHelpView opens or returns from the Help view.
func (s *State) ToggleHelpView() {
	if s.ActiveView == ViewHelp {
		s.SetActiveView(s.PreviousHelpView)
		return
	}
	s.PreviousHelpView = s.ActiveView
	s.SetActiveView(ViewHelp)
}

// SetSources replaces the source list and restores selection when possible.
func (s *State) SetSources(sources []caddy.CaddySource, preferredSourceID string, fallbackIndex int) {
	s.Sources = append([]caddy.CaddySource{}, sources...)
	s.SelectedIndex = s.RestoredSelectedSourceIndex(preferredSourceID, fallbackIndex)
}

// SelectedSource returns the selected source, if any.
func (s *State) SelectedSource() *caddy.CaddySource {
	if s.SelectedIndex < 0 || s.SelectedIndex >= len(s.Sources) {
		return nil
	}
	return &s.Sources[s.SelectedIndex]
}

// RestoredSelectedSourceIndex picks a stable selection after source refresh.
func (s *State) RestoredSelectedSourceIndex(preferredSourceID string, fallbackIndex int) int {
	if len(s.Sources) == 0 {
		return -1
	}
	if preferredSourceID != "" {
		for index, source := range s.Sources {
			if source.ID == preferredSourceID {
				return index
			}
		}
	}
	return clamp(fallbackIndex, 0, len(s.Sources)-1)
}

// MoveServiceSelection moves the selected service by delta and clamps it.
func (s *State) MoveServiceSelection(delta int) {
	if len(s.Sources) == 0 {
		s.SelectedIndex = -1
		return
	}
	if s.SelectedIndex < 0 {
		s.SelectedIndex = 0
		return
	}
	s.SelectedIndex = clamp(s.SelectedIndex+delta, 0, len(s.Sources)-1)
}

// SetServiceLogs stores service logs and invalidates parser caches.
func (s *State) SetServiceLogs(result *applogs.CaddyLogsResult) {
	s.LastLogs = result
	s.invalidateServiceLogCaches()
}

// SetAccessLogs stores the current selected-source access logs and invalidates parser caches.
func (s *State) SetAccessLogs(result *applogs.CaddyAccessLogsResult) {
	s.LastAccessLogs = result
	s.invalidateAccessLogCaches()
}

// SetLogFilters updates visible access/service-log filters.
func (s *State) SetLogFilters(errorOnly bool, slowOnly bool, window LogsTimeWindow) {
	s.LogsErrorOnly = errorOnly
	s.LogsSlowOnly = slowOnly
	if window == "" {
		window = LogsTimeWindowDay
	}
	s.LogsTimeWindow = window
	s.visibleAccessLogCache.valid = false
	s.visibleServiceLogCache.valid = false
}

// ParsedAccessLogEntries returns parsed entries from LastAccessLogs, cached by raw output.
func (s *State) ParsedAccessLogEntries() []applogs.ParsedAccessLogEntry {
	if s.LastAccessLogs == nil || !s.LastAccessLogs.Available {
		return nil
	}
	if s.parsedAccessLogCache.valid && s.parsedAccessLogCache.available == s.LastAccessLogs.Available && s.parsedAccessLogCache.output == s.LastAccessLogs.Output {
		return append([]applogs.ParsedAccessLogEntry{}, s.parsedAccessLogCache.entries...)
	}

	allEntries := applogs.ParseAccessLogOutput(s.LastAccessLogs.Output)
	entries := make([]applogs.ParsedAccessLogEntry, 0, len(allEntries))
	for _, entry := range allEntries {
		if entry.Parsed {
			entries = append(entries, entry)
		}
	}
	s.parsedAccessLogCache = parsedAccessLogCache{valid: true, available: s.LastAccessLogs.Available, output: s.LastAccessLogs.Output, entries: entries}
	return append([]applogs.ParsedAccessLogEntry{}, entries...)
}

// ParsedServiceLogEntries returns parsed caddy.service log entries, cached by raw output/error.
func (s *State) ParsedServiceLogEntries() []applogs.ParsedServiceLogEntry {
	if s.LastLogs == nil {
		return nil
	}
	output := s.LastLogs.Output
	errorText := s.LastLogs.Error
	if s.parsedServiceLogCache.valid && s.parsedServiceLogCache.output == output && s.parsedServiceLogCache.error == errorText {
		return append([]applogs.ParsedServiceLogEntry{}, s.parsedServiceLogCache.entries...)
	}
	entries := applogs.ParseServiceLogOutput(firstNonEmpty(output, errorText))
	s.parsedServiceLogCache = parsedServiceLogCache{valid: true, output: output, error: errorText, entries: entries}
	return append([]applogs.ParsedServiceLogEntry{}, entries...)
}

// VisibleServiceLogEntries returns service log entries after the error-only filter, newest-first.
func (s *State) VisibleServiceLogEntries() []applogs.ParsedServiceLogEntry {
	if s.LastLogs == nil {
		return nil
	}
	output := s.LastLogs.Output
	errorText := s.LastLogs.Error
	if s.visibleServiceLogCache.valid && s.visibleServiceLogCache.output == output && s.visibleServiceLogCache.error == errorText && s.visibleServiceLogCache.errorOnly == s.LogsErrorOnly {
		return append([]applogs.ParsedServiceLogEntry{}, s.visibleServiceLogCache.entries...)
	}
	parsed := s.ParsedServiceLogEntries()
	visible := make([]applogs.ParsedServiceLogEntry, 0, len(parsed))
	for _, entry := range parsed {
		if !s.LogsErrorOnly || applogs.IsImportantServiceLogEntry(entry) {
			visible = append(visible, entry)
		}
	}
	reverseServiceEntries(visible)
	s.visibleServiceLogCache = visibleServiceLogCache{valid: true, output: output, error: errorText, errorOnly: s.LogsErrorOnly, entries: visible}
	return append([]applogs.ParsedServiceLogEntry{}, visible...)
}

// VisibleAccessLogEntries returns parsed access-log entries after time/error/slow filters.
// Entries are returned newest-first for request-table display.
func (s *State) VisibleAccessLogEntries(now time.Time) []applogs.ParsedAccessLogEntry {
	if s.LastAccessLogs == nil || !s.LastAccessLogs.Available {
		return nil
	}
	nowUnix := now.Unix()
	if s.visibleAccessLogCache.valid &&
		s.visibleAccessLogCache.available == s.LastAccessLogs.Available &&
		s.visibleAccessLogCache.output == s.LastAccessLogs.Output &&
		s.visibleAccessLogCache.errorOnly == s.LogsErrorOnly &&
		s.visibleAccessLogCache.slowOnly == s.LogsSlowOnly &&
		s.visibleAccessLogCache.timeWindow == s.LogsTimeWindow &&
		s.visibleAccessLogCache.nowUnix == nowUnix {
		return append([]applogs.ParsedAccessLogEntry{}, s.visibleAccessLogCache.entries...)
	}

	entries := s.AccessLogEntriesForTimeWindow(s.ParsedAccessLogEntries(), now)
	visible := make([]applogs.ParsedAccessLogEntry, 0, len(entries))
	for _, entry := range entries {
		if s.LogsErrorOnly && !applogs.IsImportantAccessLogEntry(entry) {
			continue
		}
		if s.LogsSlowOnly && !s.IsSlowAccessLogEntry(entry) {
			continue
		}
		visible = append(visible, entry)
	}
	reverseAccessEntries(visible)
	s.visibleAccessLogCache = visibleAccessLogCache{valid: true, available: s.LastAccessLogs.Available, output: s.LastAccessLogs.Output, errorOnly: s.LogsErrorOnly, slowOnly: s.LogsSlowOnly, timeWindow: s.LogsTimeWindow, nowUnix: nowUnix, entries: visible}
	return append([]applogs.ParsedAccessLogEntry{}, visible...)
}

// AccessLogEntriesForTimeWindow applies the selected day/week/all time window.
func (s *State) AccessLogEntriesForTimeWindow(entries []applogs.ParsedAccessLogEntry, now time.Time) []applogs.ParsedAccessLogEntry {
	switch s.LogsTimeWindow {
	case LogsTimeWindowDay:
		return accessLogEntriesSince(entries, now, 24*time.Hour)
	case LogsTimeWindowWeek:
		return accessLogEntriesSince(entries, now, 7*24*time.Hour)
	case LogsTimeWindowAll:
		return append([]applogs.ParsedAccessLogEntry{}, entries...)
	default:
		return accessLogEntriesSince(entries, now, 24*time.Hour)
	}
}

// IsSlowAccessLogEntry reports whether an entry meets the configured slow threshold.
func (s *State) IsSlowAccessLogEntry(entry applogs.ParsedAccessLogEntry) bool {
	thresholdMS := float64(s.SlowRequestThreshold.Milliseconds())
	return entry.DurationMS >= thresholdMS && entry.DurationMS > 0
}

// MoveAccessLogSelection moves selected request by delta among visible entries.
func (s *State) MoveAccessLogSelection(delta int, now time.Time) {
	entries := s.VisibleAccessLogEntries(now)
	if len(entries) == 0 {
		s.SelectedAccessLogIdx = -1
		return
	}
	if s.SelectedAccessLogIdx < 0 {
		s.SelectedAccessLogIdx = 0
		return
	}
	s.SelectedAccessLogIdx = clamp(s.SelectedAccessLogIdx+delta, 0, len(entries)-1)
}

// ClampSelectedAccessLogIndex clamps request selection against provided visible entries.
func (s *State) ClampSelectedAccessLogIndex(entries []applogs.ParsedAccessLogEntry) {
	if len(entries) == 0 {
		s.SelectedAccessLogIdx = -1
		return
	}
	if s.SelectedAccessLogIdx < 0 {
		s.SelectedAccessLogIdx = 0
		return
	}
	s.SelectedAccessLogIdx = clamp(s.SelectedAccessLogIdx, 0, len(entries)-1)
}

// SelectedAccessLogEntry returns the selected visible request entry.
func (s *State) SelectedAccessLogEntry(now time.Time) *applogs.ParsedAccessLogEntry {
	entries := s.VisibleAccessLogEntries(now)
	s.ClampSelectedAccessLogIndex(entries)
	if s.SelectedAccessLogIdx < 0 || s.SelectedAccessLogIdx >= len(entries) {
		return nil
	}
	entry := entries[s.SelectedAccessLogIdx]
	return &entry
}

// SelectedAccessLogEntryKey returns a stable key for the selected visible request.
func (s *State) SelectedAccessLogEntryKey(now time.Time) string {
	entry := s.SelectedAccessLogEntry(now)
	if entry == nil {
		return ""
	}
	return AccessLogEntryKey(*entry)
}

// RestoreAccessLogSelection restores request selection by key, falling back to previous index.
func (s *State) RestoreAccessLogSelection(previousKey string, previousIndex int, now time.Time) {
	entries := s.VisibleAccessLogEntries(now)
	if len(entries) == 0 {
		s.SelectedAccessLogIdx = -1
		return
	}
	if previousKey != "" {
		for index, entry := range entries {
			if AccessLogEntryKey(entry) == previousKey {
				s.SelectedAccessLogIdx = index
				return
			}
		}
	}
	if previousIndex >= 0 {
		s.SelectedAccessLogIdx = clamp(previousIndex, 0, len(entries)-1)
	} else {
		s.SelectedAccessLogIdx = 0
	}
}

// AccessLogEntryKey returns a stable key for an access-log entry.
func AccessLogEntryKey(entry applogs.ParsedAccessLogEntry) string {
	timestamp := ""
	if entry.Timestamp != nil {
		timestamp = entry.Timestamp.UTC().Format(time.RFC3339Nano)
	}
	return strings.Join([]string{
		timestamp,
		entry.Method,
		entry.URI,
		fmt.Sprint(entry.Status),
		fmt.Sprintf("%g", entry.DurationMS),
		entry.Raw,
	}, "\x00")
}

// UpdateAccessLogStats updates source-list log stats for a source.
func (s *State) UpdateAccessLogStats(source *caddy.CaddySource, result *applogs.CaddyAccessLogsResult, now time.Time) {
	if source == nil || result == nil {
		return
	}
	count, ok := CountAccessLogsInLast24Hours(result, now)
	s.AccessLogStatsBySource[source.ID] = AccessLogStats{Available: result.Available, OK: result.OK, Count24h: count, HasCount: ok}
}

// CountAccessLogsInLast24Hours counts parsed timestamped access logs in the last 24h.
func CountAccessLogsInLast24Hours(result *applogs.CaddyAccessLogsResult, now time.Time) (int, bool) {
	if result == nil || !result.Available || !result.OK {
		return 0, false
	}
	parsed := applogs.ParseAccessLogOutput(result.Output)
	parsedEntries := make([]applogs.ParsedAccessLogEntry, 0, len(parsed))
	for _, entry := range parsed {
		if entry.Parsed {
			parsedEntries = append(parsedEntries, entry)
		}
	}
	if len(parsedEntries) == 0 {
		return 0, true
	}

	timestamped := make([]applogs.ParsedAccessLogEntry, 0, len(parsedEntries))
	for _, entry := range parsedEntries {
		if entry.Timestamp != nil {
			timestamped = append(timestamped, entry)
		}
	}
	if len(timestamped) == 0 {
		return 0, false
	}

	since := now.Add(-24 * time.Hour)
	count := 0
	for _, entry := range timestamped {
		if !entry.Timestamp.Before(since) {
			count++
		}
	}
	return count, true
}

func (s *State) invalidateAccessLogCaches() {
	s.parsedAccessLogCache.valid = false
	s.visibleAccessLogCache.valid = false
}

func (s *State) invalidateServiceLogCaches() {
	s.parsedServiceLogCache.valid = false
	s.visibleServiceLogCache.valid = false
}

func accessLogEntriesSince(entries []applogs.ParsedAccessLogEntry, now time.Time, duration time.Duration) []applogs.ParsedAccessLogEntry {
	timestamped := false
	for _, entry := range entries {
		if entry.Timestamp != nil {
			timestamped = true
			break
		}
	}
	if !timestamped {
		return append([]applogs.ParsedAccessLogEntry{}, entries...)
	}
	since := now.Add(-duration)
	filtered := make([]applogs.ParsedAccessLogEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Timestamp != nil && !entry.Timestamp.Before(since) {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func reverseAccessEntries(entries []applogs.ParsedAccessLogEntry) {
	for left, right := 0, len(entries)-1; left < right; left, right = left+1, right-1 {
		entries[left], entries[right] = entries[right], entries[left]
	}
}

func reverseServiceEntries(entries []applogs.ParsedServiceLogEntry) {
	for left, right := 0, len(entries)-1; left < right; left, right = left+1, right-1 {
		entries[left], entries[right] = entries[right], entries[left]
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
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
