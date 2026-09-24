package run

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Killing only the command left its children holding the output pipe, and
// CombinedOutput waited for them: a 1s deadline on `sleep 6 & wait` came back
// after 6s. The real case is git fetch starting ssh, which then hangs on a dead
// network or a prompt, freezing the TUI long after the 30s deadline.
func TestDeadlineHoldsWhenTheCommandHasChildren(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process groups are unix-only")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}

	start := time.Now()
	_, err := Combined(300*time.Millisecond, "", "sh", "-c", "sleep 10 & wait")
	elapsed := time.Since(start)

	if !IsTimeout(err) {
		t.Errorf("err = %v, want a TimeoutError", err)
	}
	// Under waitDelay, so this is the child being killed, not the backstop.
	if elapsed > waitDelay-500*time.Millisecond {
		t.Errorf("returned after %s; the deadline was 300ms", elapsed.Round(time.Millisecond))
	}
}

// git asks for credentials on the terminal when an HTTPS remote needs them,
// which is the TUI's terminal. It must fail instead.
func TestCommandsCannotPromptForGitCredentials(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	out, err := Output(Local, "", "sh", "-c", "echo $GIT_TERMINAL_PROMPT")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(out)); got != "0" {
		t.Errorf("GIT_TERMINAL_PROMPT = %q, want 0", got)
	}
}

// waitDelay also applies when a command exits normally but leaves something in
// the background holding its output. Wait then reports exec.ErrWaitDelay, which
// must not turn a successful command into a failure.
func TestBackgroundChildDoesNotFailAFinishedCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	defer func(d time.Duration) { waitDelay = d }(waitDelay)
	waitDelay = 200 * time.Millisecond

	out, err := Combined(Local, "", "sh", "-c", "sleep 10 & echo done")
	if err != nil {
		t.Errorf("err = %v, want nil: the command itself succeeded", err)
	}
	if strings.TrimSpace(string(out)) != "done" {
		t.Errorf("out = %q, want %q", out, "done")
	}
}
