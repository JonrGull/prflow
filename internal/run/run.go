// Package run executes external commands with a deadline.
//
// Every subprocess in this app (gh, git, clipboard and browser helpers) used
// to be launched with a bare exec.Command and no timeout. Because the TUI
// blocks on these calls, a single hung `gh` — an expired credential helper
// prompting on stdin, a wedged network connection — froze the whole app with
// no way out but Ctrl+C. Routing them through here bounds that.
package run

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const (
	// Network is for commands that talk to a remote: gh, git fetch/pull.
	Network = 30 * time.Second
	// Local is for fast local helpers: clipboard, browser launchers.
	Local = 5 * time.Second
	// Slow is for commands that legitimately take a while, such as
	// downloading a release binary.
	Slow = 5 * time.Minute
)

// TimeoutError reports a command killed by its deadline. It is distinguished
// from an ordinary non-zero exit so callers can tell the user the difference
// between "the command failed" and "the command never came back".
type TimeoutError struct {
	Name    string
	Timeout time.Duration
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("%s timed out after %s", e.Name, e.Timeout)
}

// IsTimeout reports whether err is (or wraps) a TimeoutError.
func IsTimeout(err error) bool {
	var te *TimeoutError
	return errors.As(err, &te)
}

// Combined runs name with args in dir and returns combined stdout+stderr.
// An empty dir means the current working directory.
func Combined(timeout time.Duration, dir, name string, args ...string) ([]byte, error) {
	return exec_(timeout, dir, "", true, name, args...)
}

// Output runs name with args in dir and returns stdout only.
func Output(timeout time.Duration, dir, name string, args ...string) ([]byte, error) {
	return exec_(timeout, dir, "", false, name, args...)
}

// Input runs name with args, writing stdin to the process, and returns
// combined output. Used for the clipboard helpers.
func Input(timeout time.Duration, stdin, name string, args ...string) error {
	_, err := exec_(timeout, "", stdin, true, name, args...)
	return err
}

func exec_(timeout time.Duration, dir, stdin string, combined bool, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}

	var out []byte
	var err error
	if combined {
		out, err = cmd.CombinedOutput()
	} else {
		out, err = cmd.Output()
	}

	// A killed process reports a generic exec error, so check the context to
	// find out whether the deadline was the real cause.
	if ctx.Err() == context.DeadlineExceeded {
		return out, &TimeoutError{Name: describe(name, args), Timeout: timeout}
	}
	return out, err
}

// Detached starts a command without waiting for it. Used for launching a
// browser, where blocking on the child would hang the UI for as long as the
// browser stays open.
func Detached(name string, args ...string) error {
	return exec.Command(name, args...).Start()
}

// LookPath reports whether a binary is on PATH.
func LookPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// describe builds a short command label for error messages, e.g. "gh pr list".
func describe(name string, args []string) string {
	label := name
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			break
		}
		label += " " + a
	}
	return label
}
