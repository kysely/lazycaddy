package logs

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/kysely/lazycaddy/internal/caddy"
)

// CaddyLogsResult is the result of fetching caddy.service logs.
type CaddyLogsResult struct {
	OK         bool
	Command    []string
	Output     string
	Error      string
	FetchedAt  time.Time
	DurationMS int64
	ExitCode   int
}

// CaddyAccessLogsResult is the result of fetching access logs for a source.
type CaddyAccessLogsResult struct {
	OK          bool
	Available   bool
	SourceLabel string
	Output      string
	Error       string
	FetchedAt   time.Time
	DurationMS  int64
	Details     []string
}

// FetchCaddyLogs fetches recent caddy.service logs using journalctl.
func FetchCaddyLogs(ctx context.Context, lines int) CaddyLogsResult {
	if lines <= 0 {
		lines = 100
	}

	command := []string{"journalctl", "-u", "caddy", "-n", fmt.Sprint(lines), "--no-pager", "-o", "short-iso"}
	fetchedAt := time.Now()
	startedAt := fetchedAt
	stdout, stderr, exitCode, err := runCommand(ctx, command)
	duration := readerDurationMS(startedAt)

	result := CaddyLogsResult{
		OK:         err == nil && exitCode == 0,
		Command:    command,
		Output:     strings.TrimSpace(stdout),
		FetchedAt:  fetchedAt,
		DurationMS: duration,
		ExitCode:   exitCode,
	}

	if result.OK {
		if result.Output == "" {
			result.Output = "No caddy.service logs found."
		}
		return result
	}

	if ctxErr := ctx.Err(); ctxErr != nil {
		result.Error = ctxErr.Error()
	} else if strings.TrimSpace(stderr) != "" {
		result.Error = strings.TrimSpace(stderr)
	} else if strings.TrimSpace(stdout) != "" {
		result.Error = strings.TrimSpace(stdout)
	} else if err != nil {
		result.Error = fmt.Sprintf("Could not run journalctl: %v", err)
	} else {
		result.Error = fmt.Sprintf("journalctl exited with code %d", exitCode)
	}

	return result
}

// FetchAccessLogsForSource fetches configured access logs for a source.
func FetchAccessLogsForSource(ctx context.Context, source *caddy.CaddySource, serviceLogs *CaddyLogsResult, lines int) CaddyAccessLogsResult {
	if lines <= 0 {
		lines = 100
	}

	fetchedAt := time.Now()
	startedAt := fetchedAt

	if source == nil {
		return CaddyAccessLogsResult{
			Output:     "No source selected.",
			FetchedAt:  fetchedAt,
			DurationMS: 0,
			Details:    []string{},
		}
	}

	label := sourceLabel(source)
	if len(source.AccessLogs) == 0 {
		return CaddyAccessLogsResult{
			SourceLabel: label,
			Output:      fmt.Sprintf("Access logs are not configured for %s.", label),
			Error:       "No access log writer is configured for this source in the active Caddy config.",
			FetchedAt:   fetchedAt,
			DurationMS:  readerDurationMS(startedAt),
			Details:     []string{"No access log writer found in apps.http.servers.*.logs for this source."},
		}
	}

	chunks := []string{}
	errors := []string{}
	details := make([]string, 0, len(source.AccessLogs))

	for _, accessLog := range source.AccessLogs {
		target := accessLog.WriterOutput
		if accessLog.Filename != "" {
			target += ":" + accessLog.Filename
		}
		detail := fmt.Sprintf("%s -> %s (%s", accessLog.Source, accessLog.LoggerName, target)
		if accessLog.Encoder != "" {
			detail += ", " + accessLog.Encoder
		}
		detail += ")"
		details = append(details, detail)

		switch {
		case accessLog.WriterOutput == "file" && accessLog.Filename != "":
			fileResult := tailFile(ctx, accessLog.Filename, lines)
			if fileResult.ok {
				chunks = append(chunks, accessLogHeader(accessLog.Filename), fileResult.output)
			} else {
				errors = append(errors, firstNonEmpty(fileResult.err, fmt.Sprintf("Could not read %s", accessLog.Filename)))
			}
		case accessLog.WriterOutput == "stdout" || accessLog.WriterOutput == "stderr" || accessLog.WriterOutput == "default":
			serviceOutput := ""
			if serviceLogs != nil {
				serviceOutput = serviceLogs.Output
			}
			filtered := filterServiceLogsForSource(serviceOutput, source, accessLog)
			if filtered == "" {
				filtered = fmt.Sprintf("Access log is configured for %s, but no recent journal entries matched %s.", accessLog.WriterOutput, label)
			}
			chunks = append(chunks, accessLogHeader(fmt.Sprintf("%s via caddy.service journal", accessLog.WriterOutput)), filtered)
		default:
			errors = append(errors, fmt.Sprintf("Access log writer %s is configured, but lazycaddy cannot read it yet.", accessLog.WriterOutput))
		}
	}

	output := strings.TrimSpace(strings.Join(nonEmptyStrings(chunks...), "\n"))
	errorText := strings.Join(errors, "\n")

	return CaddyAccessLogsResult{
		OK:          len(errors) == 0 || output != "",
		Available:   true,
		SourceLabel: label,
		Output:      firstNonEmpty(output, errorText),
		Error:       errorText,
		FetchedAt:   fetchedAt,
		DurationMS:  readerDurationMS(startedAt),
		Details:     details,
	}
}

type tailFileResult struct {
	ok     bool
	output string
	err    string
}

func tailFile(ctx context.Context, path string, lines int) tailFileResult {
	stdout, stderr, exitCode, err := runCommand(ctx, []string{"tail", "-n", fmt.Sprint(lines), path})
	stdout = strings.TrimSpace(stdout)
	stderr = strings.TrimSpace(stderr)
	if err == nil && exitCode == 0 {
		if stdout == "" {
			stdout = fmt.Sprintf("No recent entries in %s.", path)
		}
		return tailFileResult{ok: true, output: stdout}
	}

	if ctxErr := ctx.Err(); ctxErr != nil {
		return tailFileResult{err: ctxErr.Error()}
	}
	if stderr != "" {
		return tailFileResult{output: stdout, err: stderr}
	}
	if err != nil {
		return tailFileResult{output: stdout, err: fmt.Sprintf("Could not read %s: %v", path, err)}
	}
	return tailFileResult{output: stdout, err: fmt.Sprintf("tail exited with code %d", exitCode)}
}

func filterServiceLogsForSource(serviceLogOutput string, source *caddy.CaddySource, accessLog caddy.CaddyAccessLog) string {
	needles := append([]string{}, source.Hosts...)
	if accessLog.LoggerID != "" {
		needles = append(needles, accessLog.LoggerID)
	}
	if accessLog.LoggerName != "" && accessLog.LoggerName != "default" {
		needles = append(needles, accessLog.LoggerName)
	}
	needles = nonEmptyStrings(needles...)

	if len(needles) == 0 {
		return serviceLogOutput
	}

	filtered := []string{}
	for _, line := range splitLines(serviceLogOutput) {
		for _, needle := range needles {
			if strings.Contains(line, needle) {
				filtered = append(filtered, line)
				break
			}
		}
	}
	return strings.Join(filtered, "\n")
}

func runCommand(ctx context.Context, command []string) (stdout string, stderr string, exitCode int, err error) {
	if len(command) == 0 {
		return "", "", -1, fmt.Errorf("empty command")
	}

	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	var stdoutBuffer bytes.Buffer
	var stderrBuffer bytes.Buffer
	cmd.Stdout = &stdoutBuffer
	cmd.Stderr = &stderrBuffer

	err = cmd.Run()
	exitCode = 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	} else if err != nil {
		exitCode = -1
	}

	return stdoutBuffer.String(), stderrBuffer.String(), exitCode, err
}

func accessLogHeader(label string) string {
	return fmt.Sprintf("--- %s ---", label)
}

func sourceLabel(source *caddy.CaddySource) string {
	if source == nil {
		return ""
	}
	if len(source.Hosts) > 0 {
		return strings.Join(source.Hosts, ", ")
	}
	if len(source.Listen) > 0 {
		return strings.Join(source.Listen, ", ")
	}
	return source.ServerName
}

func nonEmptyStrings(values ...string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}

func readerDurationMS(startedAt time.Time) int64 {
	return time.Since(startedAt).Milliseconds()
}
