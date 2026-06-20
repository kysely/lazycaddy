package caddy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ValidationResult is the result of running `caddy validate`.
type ValidationResult struct {
	OK         bool
	Skipped    bool
	Command    []string
	Stdout     string
	Stderr     string
	Output     string
	Error      string
	ExitCode   int
	RanAt      time.Time
	DurationMS int64
}

// ValidateConfig runs `caddy validate --config <path> [--adapter <adapter>]`.
func ValidateConfig(ctx context.Context, configPath string, adapter string) ValidationResult {
	ranAt := time.Now()
	startedAt := ranAt

	if strings.TrimSpace(configPath) == "" {
		return ValidationResult{
			Skipped:    true,
			Command:    []string{},
			Output:     "No config path was discovered for the running Caddy service.",
			Error:      "No config path discovered.",
			RanAt:      ranAt,
			DurationMS: 0,
		}
	}

	command := []string{"caddy", "validate", "--config", configPath}
	if strings.TrimSpace(adapter) != "" {
		command = append(command, "--adapter", adapter)
	}

	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	duration := durationMS(startedAt)
	stdoutText := strings.TrimSpace(stdout.String())
	stderrText := strings.TrimSpace(stderr.String())
	output := strings.TrimSpace(strings.Join(nonEmpty(stdoutText, stderrText), "\n"))

	result := ValidationResult{
		OK:         err == nil,
		Command:    command,
		Stdout:     stdoutText,
		Stderr:     stderrText,
		Output:     output,
		RanAt:      ranAt,
		DurationMS: duration,
	}

	if err == nil {
		if result.Output == "" {
			result.Output = "Config is valid."
		}
		return result
	}

	if exitErr := (&exec.ExitError{}); errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
	} else {
		result.ExitCode = -1
	}

	if ctxErr := ctx.Err(); ctxErr != nil {
		result.Error = ctxErr.Error()
		if result.Output == "" {
			result.Output = fmt.Sprintf("Could not run caddy validate: %s", ctxErr)
		}
		return result
	}

	if stderrText != "" {
		result.Error = stderrText
	} else if stdoutText != "" {
		result.Error = stdoutText
	} else {
		result.Error = err.Error()
	}

	if result.Output == "" {
		if result.ExitCode >= 0 {
			result.Output = fmt.Sprintf("caddy validate exited with code %d", result.ExitCode)
		} else {
			result.Output = fmt.Sprintf("Could not run caddy validate: %s", err)
		}
	}

	return result
}

func nonEmpty(values ...string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}
