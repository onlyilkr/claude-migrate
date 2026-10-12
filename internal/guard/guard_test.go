package guard_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/onlyilkr/claude-migrate/internal/guard"
)

func fake(out string, err error, gotArgs *[]string) guard.LSAppInfo {
	return guard.LSAppInfo{
		Run: func(name string, args ...string) ([]byte, error) {
			*gotArgs = append([]string{name}, args...)
			return []byte(out), err
		},
		Getenv: func(string) string { return "" },
	}
}

func TestLSAppInfo(t *testing.T) {
	var args []string
	running, err := fake("\"pid\"=7374\n", nil, &args).DesktopRunning()
	if err != nil || !running {
		t.Fatalf("running = %v, err = %v", running, err)
	}
	want := []string{"/usr/bin/lsappinfo", "info", "-only", "pid", "-app", "com.anthropic.claudefordesktop"}
	if !slices.Equal(args, want) {
		t.Fatalf("args = %v", args)
	}
	// Desktop closed -> lsappinfo prints nothing.
	if running, err := fake("", nil, &args).DesktopRunning(); err != nil || running {
		t.Fatalf("empty output: running = %v, err = %v", running, err)
	}
	if _, err := fake("", errors.New("boom"), &args).DesktopRunning(); err == nil {
		t.Fatal("command failure must be an error")
	}
}

type stub struct {
	running bool
	err     error
}

func (s stub) DesktopRunning() (bool, error) { return s.running, s.err }

func TestEnsureDesktopClosed(t *testing.T) {
	if err := guard.EnsureDesktopClosed(stub{}); err != nil {
		t.Fatal(err)
	}
	if err := guard.EnsureDesktopClosed(stub{running: true}); !errors.Is(err, guard.ErrDesktopRunning) {
		t.Fatalf("err = %v", err)
	}
	err := guard.EnsureDesktopClosed(stub{err: errors.New("boom")})
	if err == nil || errors.Is(err, guard.ErrDesktopRunning) {
		t.Fatalf("check failure must refuse with its own error, got %v", err)
	}
}

// Over SSH, Launch Services cannot see the GUI session, so "no pid" proves nothing.
func TestLSAppInfoRefusesOverSSH(t *testing.T) {
	for _, key := range []string{"SSH_CONNECTION", "SSH_TTY"} {
		called := false
		c := guard.LSAppInfo{
			Run: func(string, ...string) ([]byte, error) { called = true; return nil, nil },
			Getenv: func(k string) string {
				if k == key {
					return "x"
				}
				return ""
			},
		}
		if _, err := c.DesktopRunning(); !errors.Is(err, guard.ErrRemoteSession) {
			t.Fatalf("%s set: err = %v, want ErrRemoteSession", key, err)
		}
		if called {
			t.Fatalf("%s set: lsappinfo must not be consulted", key)
		}
	}
}
