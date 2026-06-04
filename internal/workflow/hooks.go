package workflow

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

type HookResult struct {
	Name      string
	Output    string
	Duration  time.Duration
	TimedOut  bool
	Workspace string
}

func RunBeforeRunHook(ctx context.Context, hooks HooksConfig, workspace string, env []string) (HookResult, error) {
	return runShellHook(ctx, "before_run", hooks.BeforeRun, workspace, hooks.TimeoutDuration(), env)
}

func runShellHook(ctx context.Context, name, script, workspace string, timeout time.Duration, env []string) (HookResult, error) {
	result := HookResult{Name: name, Workspace: workspace}
	if strings.TrimSpace(script) == "" {
		return result, nil
	}
	if strings.TrimSpace(workspace) == "" {
		return result, fmt.Errorf("%s hook workspace is required", name)
	}
	if timeout <= 0 {
		return result, fmt.Errorf("%s hook timeout must be positive", name)
	}

	startedAt := time.Now()
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "bash", "-lc", script)
	cmd.Dir = workspace
	cmd.Env = append(os.Environ(), env...)

	output, err := cmd.CombinedOutput()
	result.Duration = time.Since(startedAt)
	result.Output = string(output)
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		result.TimedOut = true
		return result, fmt.Errorf("%s hook timed out after %s", name, timeout)
	}
	if err != nil {
		trimmed := strings.TrimSpace(result.Output)
		if trimmed == "" {
			return result, fmt.Errorf("%s hook failed: %w", name, err)
		}
		return result, fmt.Errorf("%s hook failed: %w: %s", name, err, trimmed)
	}
	return result, nil
}
