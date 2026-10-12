package main

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"

	"github.com/onlyilkr/claude-migrate/internal/account"
	"github.com/onlyilkr/claude-migrate/internal/paths"
	"github.com/onlyilkr/claude-migrate/internal/store"
)

type resolved struct {
	p           paths.Paths
	source      store.ID
	target      store.ID
	targetLabel string
}

// resolve picks the target (--to or the signed-in account) and the source
// (--from, the only candidate, or an interactive choice on a TTY). When the
// target comes from ~/.claude.json and Claude Desktop last used another
// account, strict (apply) refuses and the rest warn on stderr.
func (a *App) resolve(o options, strict bool) (resolved, error) {
	r := resolved{p: a.pathsFor(o)}
	infos, err := store.Discover(r.p)
	if err != nil {
		return r, err
	}
	if o.to != "" {
		if r.target, err = store.Resolve(infos, o.to); err != nil {
			return r, err
		}
		r.targetLabel = r.target.Short()
	} else {
		acc, err := account.Current(r.p)
		if err != nil {
			return r, err
		}
		if last := account.DesktopLastKnown(r.p); last != "" && last != acc.AccountUUID {
			msg := fmt.Sprintf("~/.claude.json is signed in to account %s but Claude Desktop last used account %s; pass --to <account>/<org> to choose the target explicitly",
				store.ShortUUID(acc.AccountUUID), store.ShortUUID(last))
			if strict {
				return r, usageError{msg}
			}
			fmt.Fprintln(a.Err, "warning: "+msg)
		}
		r.target = store.ID{Account: acc.AccountUUID, Org: acc.OrgUUID}
		r.targetLabel = fmt.Sprintf("%s · %s (%s, signed in)", acc.Email, acc.OrgName, r.target.Short())
	}
	if o.from != "" {
		r.source, err = store.Resolve(infos, o.from)
	} else {
		r.source, err = a.pickSource(infos, r.target)
	}
	if err != nil {
		return r, err
	}
	if r.source == r.target {
		return r, usagef("source and target are the same store (%s)", r.source.Short())
	}
	return r, nil
}

func (a *App) pickSource(infos []store.Info, target store.ID) (store.ID, error) {
	var cands []store.Info
	for _, in := range infos {
		if in.ID != target && in.Records > 0 {
			cands = append(cands, in)
		}
	}
	switch {
	case len(cands) == 0:
		return store.ID{}, usagef("no other session store with records found on this Mac")
	case len(cands) == 1:
		fmt.Fprintf(a.Err, "Source: %s\n", describe(cands[0]))
		return cands[0].ID, nil
	case !a.IsTTY:
		return store.ID{}, usagef("found %d possible source stores; pass --from (list them with claude-migrate doctor)", len(cands))
	}
	fmt.Fprintln(a.Err, "Session stores on this Mac:")
	for i, c := range cands {
		fmt.Fprintf(a.Err, "  [%d] %s\n", i+1, describe(c))
	}
	fmt.Fprintf(a.Err, "Choose source [1-%d]: ", len(cands))
	line, _ := bufio.NewReader(a.In).ReadString('\n')
	choice := strings.TrimSpace(line)
	n, err := strconv.Atoi(choice)
	if err != nil || n < 1 || n > len(cands) {
		return store.ID{}, usagef("invalid choice %q", choice)
	}
	return cands[n-1].ID, nil
}

func describe(in store.Info) string {
	last := "never"
	if !in.LastActivity.IsZero() {
		last = in.LastActivity.Format("2006-01-02")
	}
	s := fmt.Sprintf("%s  %d records · last activity %s", in.ID.Short(), in.Records, last)
	if in.Err != nil {
		s += " · unreadable: " + in.Err.Error()
	}
	if len(in.SampleTitles) > 0 {
		q := make([]string, len(in.SampleTitles))
		for i, t := range in.SampleTitles {
			q[i] = strconv.Quote(truncate(t, 40))
		}
		s += " · " + strings.Join(q, ", ")
	}
	return s
}

// truncate shortens s to n runes, ending with an ellipsis when cut.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
