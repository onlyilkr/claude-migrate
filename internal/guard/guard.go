// Package guard refuses writes while Claude Desktop is running, because Desktop
// keeps its session list in memory and may write over changes made under it.
package guard

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// lsappinfoPath is absolute so a hostile $PATH cannot substitute the check.
const lsappinfoPath = "/usr/bin/lsappinfo"

const BundleID = "com.anthropic.claudefordesktop"

var ErrDesktopRunning = errors.New("the Claude Desktop app is running: quit it (Cmd+Q) and try again")

// ErrRemoteSession means Desktop's state cannot be known: over SSH, Launch
// Services does not see the Mac's GUI session, so an empty answer proves nothing.
var ErrRemoteSession = errors.New("running over SSH, where Claude Desktop cannot be detected: run claude-migrate in Terminal on the Mac itself")

type Checker interface {
	DesktopRunning() (bool, error)
}

// LSAppInfo asks macOS Launch Services whether the Desktop bundle has a process.
type LSAppInfo struct {
	Run    func(name string, args ...string) ([]byte, error) // nil: run the real command
	Getenv func(key string) string                           // nil: os.Getenv
}

func (c LSAppInfo) DesktopRunning() (bool, error) {
	getenv := c.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	if getenv("SSH_CONNECTION") != "" || getenv("SSH_TTY") != "" {
		return false, ErrRemoteSession
	}
	run := c.Run
	if run == nil {
		run = func(name string, args ...string) ([]byte, error) { return exec.Command(name, args...).Output() }
	}
	out, err := run(lsappinfoPath, "info", "-only", "pid", "-app", BundleID)
	if err != nil {
		return false, fmt.Errorf("check whether Claude Desktop is running (lsappinfo): %w", err)
	}
	return bytes.Contains(out, []byte(`"pid"=`)), nil
}

// EnsureDesktopClosed returns nil only when Desktop is confirmed not running.
func EnsureDesktopClosed(c Checker) error {
	running, err := c.DesktopRunning()
	if err != nil {
		return err
	}
	if running {
		return ErrDesktopRunning
	}
	return nil
}
