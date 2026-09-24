//go:build unix

package run

import (
	"os/exec"
	"syscall"
)

// isolate starts cmd in a session of its own, and makes a timeout kill the
// whole session rather than just cmd.
//
// Two things follow. The kill reaches cmd's children — ssh under git fetch —
// which otherwise live on holding the output pipe. And with no controlling
// terminal, nothing it starts can open /dev/tty to prompt for a passphrase or a
// host key: that prompt used to appear over the TUI and compete with it for
// keystrokes. Now it fails fast with an error the screen can show.
func isolate(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error {
		// A new session is also a new process group with the same id.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
