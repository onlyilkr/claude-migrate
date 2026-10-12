// Command claude-migrate moves Claude Desktop Code session records between
// accounts on one Mac. Unofficial; see README.
package main

import (
	"fmt"
	"os"

	"github.com/onlyilkr/claude-migrate/internal/guard"
)

var version = "dev"

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
