package main

import (
	"cmp"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/onlyilkr/claude-migrate/internal/account"
	"github.com/onlyilkr/claude-migrate/internal/guard"
	"github.com/onlyilkr/claude-migrate/internal/manifest"
	"github.com/onlyilkr/claude-migrate/internal/paths"
	"github.com/onlyilkr/claude-migrate/internal/store"
)

const (
	exitOK             = 0
	exitError          = 1
	exitUsage          = 2
	exitDesktopRunning = 3
	exitLayout         = 4
)

const usageText = `claude-migrate: move Claude Desktop Code sessions between accounts on this Mac.
` + disclaimer + `

Usage:
  claude-migrate [plan] [--from <acct>/<org>] [--to <acct>/<org>] [--include-orphans] [--json]
  claude-migrate apply   --from <acct>/<org>  [--to <acct>/<org>] [--include-orphans] [--json]
  claude-migrate undo   [--force] [--json] [manifest]
  claude-migrate pins   [--from <acct>/<org>] [--to <acct>/<org>] [--json]
  claude-migrate doctor [--json]
  claude-migrate version

A store is <account-uuid>/<org-uuid>; unique prefixes work (e.g. 1a2b3c4d/5e6f7a8b).
--to defaults to the account Claude Code is signed in to (~/.claude.json).
Sessions whose transcript is missing are skipped; --include-orphans copies them anyway.
Nothing is written unless you run apply or undo. Quit Claude Desktop (Cmd+Q) first.
`

type App struct {
	In    io.Reader
	Out   io.Writer
	Err   io.Writer
	Home  string
	IsTTY bool
	Guard guard.Checker
	Now   func() time.Time // nil: time.Now
}

type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func usagef(format string, a ...any) error { return usageError{fmt.Sprintf(format, a...)} }

func (a *App) Run(args []string) int {
	cmd := "plan"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "plan":
		err = a.cmdPlan(args, false)
	case "apply":
		err = a.cmdPlan(args, true)
	case "undo":
		err = a.cmdUndo(args)
	case "pins":
		err = a.cmdPins(args)
	case "doctor":
		err = a.cmdDoctor(args)
	case "version":
		fmt.Fprintf(a.Out, "claude-migrate %s\n%s\n", version, disclaimer)
	case "help":
		fmt.Fprint(a.Out, usageText)
	default:
		err = usagef("unknown command %q (see claude-migrate help)", cmd)
	}
	return a.exitCode(err)
}

func (a *App) exitCode(err error) int {
	if err == nil || errors.Is(err, flag.ErrHelp) {
		return exitOK
	}
	fmt.Fprintln(a.Err, "error:", err)
	var ue usageError
	switch {
	case errors.As(err, &ue), errors.Is(err, account.ErrNoAccount), errors.Is(err, account.ErrInvalid), errors.Is(err, store.ErrNoMatch),
		errors.Is(err, store.ErrAmbiguous), errors.Is(err, manifest.ErrNoRuns):
		return exitUsage
	case errors.Is(err, guard.ErrDesktopRunning), errors.Is(err, guard.ErrRemoteSession):
		return exitDesktopRunning
	case errors.Is(err, store.ErrLayout), errors.Is(err, store.ErrStoreMissing):
		return exitLayout
	}
	return exitError
}

type options struct {
	from, to, home string
	json, force    bool
	includeOrphans bool
}

// flagSet selects which flags a subcommand accepts.
type flagSet uint8

const (
	flagStores flagSet = 1 << iota // --from, --to
	flagForce
	flagJSON
	flagOrphans // --include-orphans
)

// parse handles one subcommand's flags. --home is hidden; it overrides $HOME for tests.
func (a *App) parse(name string, args []string, accept flagSet) (options, []string, error) {
	var o options
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() { fmt.Fprint(a.Err, usageText) }
	if accept&flagStores != 0 {
		fs.StringVar(&o.from, "from", "", "")
		fs.StringVar(&o.to, "to", "", "")
	}
	if accept&flagForce != 0 {
		fs.BoolVar(&o.force, "force", false, "")
	}
	if accept&flagOrphans != 0 {
		fs.BoolVar(&o.includeOrphans, "include-orphans", false, "")
	}
	if accept&flagJSON != 0 {
		fs.BoolVar(&o.json, "json", false, "")
	}
	fs.StringVar(&o.home, "home", "", "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return o, nil, err
		}
		return o, nil, usageError{err.Error()}
	}
	return o, fs.Args(), nil
}

func (a *App) pathsFor(o options) paths.Paths { return paths.New(cmp.Or(o.home, a.Home)) }

// ensureDesktopClosed runs the Desktop guard when targetDir may be a store
// Desktop reads. Without --home it always is. With --home, the target is
// compared by file identity against every store under the real home ($HOME
// and the user database), so ".", case variants, firmlinks and symlinked
// trees are all recognized; anything that cannot be inspected is guarded.
func (a *App) ensureDesktopClosed(o options, targetDir string) error {
	if o.home == "" || a.desktopStore(targetDir) {
		return guard.EnsureDesktopClosed(a.Guard)
	}
	return nil
}

func (a *App) desktopStore(targetDir string) bool {
	target, err := os.Stat(targetDir)
	if err != nil {
		return true
	}
	homes := []string{a.Home}
	if u, err := user.Current(); err == nil && u.HomeDir != "" && u.HomeDir != a.Home {
		homes = append(homes, u.HomeDir)
	}
	for _, h := range homes {
		root := paths.New(h).SessionsRoot()
		accounts, err := os.ReadDir(root)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return true
		}
		for _, acc := range accounts {
			orgs, err := os.ReadDir(filepath.Join(root, acc.Name()))
			if err != nil {
				if acc.IsDir() {
					return true
				}
				continue
			}
			for _, org := range orgs {
				if fi, err := os.Stat(filepath.Join(root, acc.Name(), org.Name())); err == nil && os.SameFile(target, fi) {
					return true
				}
			}
		}
	}
	return false
}

func (a *App) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}
