// Command claude-migrate moves Claude Desktop Code session records between
// accounts on one Mac. Unofficial; see README.
package main

import (
	"fmt"
	"os"
	"runtime/debug"

	"github.com/onlyilkr/claude-migrate/internal/guard"
)

// version is set by GoReleaser through -ldflags; "dev" marks any other build.
var version = "dev"

func init() {
	bi, ok := debug.ReadBuildInfo()
	version = resolveVersion(version, bi, ok)
}

// resolveVersion keeps an ldflags-injected version. A plain `go install
// module@vX.Y.Z` build has no ldflags but records the module version, which is
// used instead of "dev"; local builds report "(devel)" and stay "dev".
func resolveVersion(v string, bi *debug.BuildInfo, ok bool) string {
	if v != "dev" || !ok || bi == nil {
		return v
	}
	if mv := bi.Main.Version; mv != "" && mv != "(devel)" {
		return mv
	}
	return v
}

const disclaimer = "Unofficial community tool. Not affiliated with or endorsed by Anthropic."

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(exitError)
	}
	fi, _ := os.Stdin.Stat()
	app := &App{
		In: os.Stdin, Out: os.Stdout, Err: os.Stderr, Home: home,
		IsTTY: fi != nil && fi.Mode()&os.ModeCharDevice != 0,
		Guard: guard.LSAppInfo{},
	}
	os.Exit(app.Run(os.Args[1:]))
}
