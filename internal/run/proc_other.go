//go:build !unix

package run

import "os/exec"

// isolate is a no-op where process groups are unavailable; waitDelay still
// bounds how long a timed-out command's children can hold its output.
func isolate(cmd *exec.Cmd) {}
