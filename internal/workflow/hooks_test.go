package workflow

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHermesBeforeRunHookAllowsCtxWorktreeWithoutCtxCommand(t *testing.T) {
	def := loadHermesWorkflowForHookTest(t)
	workspace := initGitWorkspace(t, filepath.Join(t.TempDir(), ".hermes", "worktrees", "workspace-1", "hermes-agent"), "https://github.com/taboularasa/hermes-agent.git")

	result, err := RunBeforeRunHook(context.Background(), def.Settings.Hooks, workspace, []string{"PATH=" + gitOnlyPath(t)})
	if err != nil {
		t.Fatalf("before_run hook failed: %v\noutput:\n%s", err, result.Output)
	}
	if result.TimedOut {
		t.Fatal("before_run hook unexpectedly timed out")
	}
}

func TestHermesBeforeRunHookRejectsUnexpectedOrigin(t *testing.T) {
	def := loadHermesWorkflowForHookTest(t)
	workspace := initGitWorkspace(t, filepath.Join(t.TempDir(), ".hermes", "worktrees", "workspace-1", "hermes-agent"), "https://github.com/taboularasa/de-novo.git")

	result, err := RunBeforeRunHook(context.Background(), def.Settings.Hooks, workspace, []string{"PATH=" + gitOnlyPath(t)})
	if err == nil {
		t.Fatal("expected before_run hook to reject unexpected origin")
	}
	if !strings.Contains(err.Error(), "unexpected repository origin") {
		t.Fatalf("error = %v, want unexpected origin", err)
	}
	if !strings.Contains(result.Output, "taboularasa/de-novo") {
		t.Fatalf("output = %q, want denied origin evidence", result.Output)
	}
}

func TestHermesBeforeRunHookRejectsNestedHermesWorktree(t *testing.T) {
	def := loadHermesWorkflowForHookTest(t)
	workspace := initGitWorkspace(t, filepath.Join(t.TempDir(), ".hermes", "worktrees", "workspace-1", ".hermes", "worktrees", "workspace-2", "hermes-agent"), "https://github.com/taboularasa/hermes-agent.git")

	result, err := RunBeforeRunHook(context.Background(), def.Settings.Hooks, workspace, []string{"PATH=" + gitOnlyPath(t)})
	if err == nil {
		t.Fatal("expected before_run hook to reject nested Hermes worktree")
	}
	if !strings.Contains(err.Error(), "nested Hermes worktree is not allowed") {
		t.Fatalf("error = %v, want nested worktree rejection", err)
	}
	if !strings.Contains(result.Output, "nested Hermes worktree") {
		t.Fatalf("output = %q, want nested worktree evidence", result.Output)
	}
}

func TestRunBeforeRunHookPropagatesTimeout(t *testing.T) {
	result, err := RunBeforeRunHook(context.Background(), HooksConfig{
		BeforeRun: "sleep 1",
		TimeoutMS: 10,
	}, t.TempDir(), nil)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !result.TimedOut {
		t.Fatal("result did not record timeout")
	}
	if !strings.Contains(err.Error(), "before_run hook timed out") {
		t.Fatalf("error = %v, want timeout", err)
	}
}

func TestRunBeforeRunHookPropagatesScriptError(t *testing.T) {
	result, err := RunBeforeRunHook(context.Background(), HooksConfig{
		BeforeRun: "echo hook-denied >&2; exit 7",
		TimeoutMS: int((5 * time.Second) / time.Millisecond),
	}, t.TempDir(), nil)
	if err == nil {
		t.Fatal("expected script error")
	}
	if !strings.Contains(err.Error(), "before_run hook failed") {
		t.Fatalf("error = %v, want hook failure", err)
	}
	if !strings.Contains(result.Output, "hook-denied") {
		t.Fatalf("output = %q, want script stderr", result.Output)
	}
}

func loadHermesWorkflowForHookTest(t *testing.T) Definition {
	t.Helper()
	def, err := Load(filepath.Join("..", "..", "hermes", "WORKFLOW.md"))
	if err != nil {
		t.Fatalf("load Hermes workflow: %v", err)
	}
	if strings.TrimSpace(def.Settings.Hooks.BeforeRun) == "" {
		t.Fatal("Hermes workflow missing before_run hook")
	}
	return def
}

func initGitWorkspace(t *testing.T, dir, origin string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git executable is required for hook tests")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
		}
	}
	git("init", "-q")
	git("remote", "add", "origin", origin)
	return dir
}

func gitOnlyPath(t *testing.T) string {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git executable is required for hook tests")
	}
	binDir := t.TempDir()
	link := filepath.Join(binDir, "git")
	if err := os.Symlink(gitPath, link); err != nil {
		t.Fatalf("symlink git: %v", err)
	}
	return binDir
}
