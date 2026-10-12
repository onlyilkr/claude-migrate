package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/onlyilkr/claude-migrate/internal/account"
	"github.com/onlyilkr/claude-migrate/internal/manifest"
	"github.com/onlyilkr/claude-migrate/internal/migrate"
	"github.com/onlyilkr/claude-migrate/internal/paths"
	"github.com/onlyilkr/claude-migrate/internal/plan"
	"github.com/onlyilkr/claude-migrate/internal/report"
	"github.com/onlyilkr/claude-migrate/internal/store"
)

func (a *App) load(r resolved, includeOrphans bool) (plan.Plan, *store.Store, error) {
	src, err := store.Load(r.p, r.source)
	if err != nil {
		return plan.Plan{}, nil, err
	}
	tgt, err := store.Load(r.p, r.target)
	if err != nil {
		return plan.Plan{}, nil, err
	}
	tx, err := store.TranscriptIndex(r.p)
	if err != nil {
		return plan.Plan{}, nil, err
	}
	in := plan.Input{
		Source: src, Target: tgt, Transcripts: tx,
		ScratchRoot: r.p.ScratchRoot(), CleanupPeriodSet: account.CleanupPeriodSet(r.p), IncludeOrphans: includeOrphans,
	}
	if ids, ok := account.DesktopPinnedIDs(r.p); ok {
		in.Pinned = ids // Desktop's real pin list; otherwise isStarred stands in
	}
	pl := plan.Build(in)
	return pl, tgt, nil
}

// cmdPlan runs plan, or apply when apply is true.
func (a *App) cmdPlan(args []string, apply bool) error {
	o, rest, err := a.parse("plan", args, flagStores|flagJSON|flagOrphans)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usagef("unexpected argument %q", rest[0])
	}
	if apply && o.from == "" {
		return usagef("apply needs --from <account>/<org> (run claude-migrate plan to see the exact command)")
	}
	if apply && o.home == "" {
		if err := a.ensureDesktopClosed(o, ""); err != nil {
			return err
		}
	}
	r, err := a.resolve(o, apply)
	if err != nil {
		return err
	}
	pl, _, err := a.load(r, o.includeOrphans)
	if err != nil {
		return err
	}
	if apply && o.home != "" {
		if err := a.ensureDesktopClosed(o, pl.TargetDir); err != nil {
			return err
		}
	}
	if o.json {
		if !apply {
			return report.WriteJSON(a.Out, report.ToJSON(pl))
		}
		res, err := a.apply(r.p, pl)
		werr := report.WriteJSON(a.Out, report.ApplyJSON{Plan: report.ToJSON(pl), Written: res.written, Manifest: res.manifest})
		if err == nil {
			err = werr
		}
		return err
	}
	fmt.Fprintf(a.Out, "%s\nTarget: %s\n\n", disclaimer, r.targetLabel)
	report.Text(a.Out, pl)
	if !apply {
		// --to is repeated only when the user passed it: without it, apply keeps
		// its account cross-check against the account Desktop last used.
		suggestion := "claude-migrate apply --from " + pl.Source.Short()
		if o.to != "" {
			suggestion += " --to " + pl.Target.Short()
		}
		if o.includeOrphans {
			suggestion += " --include-orphans"
		}
		fmt.Fprintf(a.Out, "\nDry run: nothing was written.\nTo apply: %s\n", suggestion)
		return nil
	}
	res, err := a.apply(r.p, pl)
	if err != nil {
		return err
	}
	if res.written == 0 {
		if n := pl.SkippedOther(); n > 0 {
			fmt.Fprintf(a.Out, "\nNothing to copy: %d records were skipped; see the counts above.\n", n)
		} else {
			fmt.Fprintln(a.Out, "\nNothing to copy: the target already has every session.")
		}
		return nil
	}
	fmt.Fprintf(a.Out, "\nCopied %d sessions.\nManifest: %s\nUndo with: claude-migrate undo '%s'\nOpen Claude Desktop; the sessions appear in the target account's sidebar.\n",
		res.written, res.manifest, res.manifest)
	return nil
}

type applyResult struct {
	written  int
	manifest string
}

func (a *App) apply(p paths.Paths, pl plan.Plan) (applyResult, error) {
	if len(pl.Copy) == 0 {
		return applyResult{}, nil
	}
	w, err := manifest.Create(p.RunsDir(), manifest.Header{
		ToolVersion: version, Source: pl.Source.String(), Target: pl.Target.String(),
		TargetDir: pl.TargetDir, StartedAt: a.now(),
	})
	if err != nil {
		return applyResult{}, err
	}
	n, err := migrate.Apply(pl, w)
	res := applyResult{written: n, manifest: w.Path}
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return res, fmt.Errorf("%w (wrote %d of %d; undo with: claude-migrate undo '%s')", err, n, len(pl.Copy), w.Path)
	}
	return res, nil
}

// undoIncomplete explains why files were kept; --force only helps for modified ones.
func undoIncomplete(res manifest.UndoResult) error {
	var why []string
	if n := len(res.Modified); n > 0 {
		why = append(why, fmt.Sprintf("%d changed since migration (--force removes them)", n))
	}
	if n := len(res.Rejected); n > 0 {
		why = append(why, fmt.Sprintf("%d outside the target store (never removed)", n))
	}
	return fmt.Errorf("undo incomplete: kept %s", strings.Join(why, ", "))
}

func (a *App) cmdUndo(args []string) error {
	o, rest, err := a.parse("undo", args, flagForce|flagJSON)
	if err != nil {
		return err
	}
	if len(rest) > 1 {
		return usagef("undo takes at most one manifest path")
	}
	if o.home == "" {
		if err := a.ensureDesktopClosed(o, ""); err != nil {
			return err
		}
	}
	var path string
	var skipped []string
	if len(rest) == 1 {
		path = rest[0]
		if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
			return usagef("no such manifest: %s", path)
		}
	} else {
		runsDir := a.pathsFor(o).RunsDir()
		path, skipped, err = manifest.Latest(runsDir)
		switch {
		case errors.Is(err, manifest.ErrNoRuns) && len(skipped) > 0:
			return usagef("no readable migration runs (%d unreadable in %s)", len(skipped), runsDir)
		case err != nil:
			return err
		case len(skipped) > 0:
			// A newer run that cannot be read may be the one meant: do not fall back to an older one.
			return usagef("the newest run(s) are unreadable: %s; pass a manifest path to undo an older run", strings.Join(skipped, ", "))
		}
	}
	targetDir, err := confineUndo(a.pathsFor(o), path)
	if err != nil {
		return err
	}
	if o.home != "" {
		if err := a.ensureDesktopClosed(o, targetDir); err != nil {
			return err
		}
	}
	// Undo returns what it did even when it fails part-way; print that first so
	// the user can see which files are already gone.
	res, uerr := manifest.Undo(path, o.force)
	if o.json {
		if err := report.WriteJSON(a.Out, report.ToUndoJSON(path, res)); err != nil && uerr == nil {
			uerr = err
		}
	} else {
		fmt.Fprintf(a.Out, "Undo %s\n  removed %d · already gone %d · modified since migration %d · refused %d · kept for a newer run %d · temp files cleaned %d\n",
			path, len(res.Removed), len(res.Missing), len(res.Modified), len(res.Rejected), len(res.Superseded), len(res.TempRemoved))
		for _, f := range res.UnreadableNewer {
			fmt.Fprintf(a.Out, "  ! newer run unreadable, its files were not protected: %s\n", f)
		}
		for _, f := range res.Modified {
			fmt.Fprintf(a.Out, "  ! kept (changed since migration): %s\n", f)
		}
		for _, f := range res.Rejected {
			fmt.Fprintf(a.Out, "  ! refused (not a session record in the target store): %s\n", f)
		}
		for _, f := range res.Superseded {
			fmt.Fprintf(a.Out, "  ! kept (recorded again by a newer run): %s\n", f)
		}
	}
	if uerr != nil {
		return uerr
	}
	if !res.Completed {
		return undoIncomplete(res)
	}
	return nil
}

// confineUndo refuses a manifest whose target is not a real <acct>/<org> store
// of these paths, so a hand-edited target_dir cannot point undo at other files.
// It returns the confined target directory.
func confineUndo(p paths.Paths, path string) (string, error) {
	h, _, err := manifest.Read(path)
	if err != nil {
		return "", err
	}
	acct, org, ok := strings.Cut(h.Target, "/")
	if !ok || !paths.IsUUID(acct) || !paths.IsUUID(org) {
		return "", usagef("%s: target %q is not <account-uuid>/<org-uuid>; refusing to undo", path, h.Target)
	}
	want := p.StoreDir(acct, org)
	if filepath.Clean(h.TargetDir) != want && !sameFile(h.TargetDir, want) {
		return "", usagef("%s: target_dir %q is not the session store %s; refusing to undo", path, h.TargetDir, want)
	}
	return filepath.Clean(h.TargetDir), nil
}

// sameFile reports whether both paths exist and name the same directory entry.
func sameFile(x, y string) bool {
	fx, errx := os.Stat(x)
	fy, erry := os.Stat(y)
	return errx == nil && erry == nil && os.SameFile(fx, fy)
}

func (a *App) cmdPins(args []string) error {
	o, rest, err := a.parse("pins", args, flagStores|flagJSON)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usagef("unexpected argument %q", rest[0])
	}
	r, err := a.resolve(o, false)
	if err != nil {
		return err
	}
	pl, tgt, err := a.load(r, false)
	if err != nil {
		return err
	}
	rows := report.PinRows(pl, tgt)
	if o.json {
		return report.WriteJSON(a.Out, rows)
	}
	report.Pins(a.Out, rows)
	return nil
}

func (a *App) cmdDoctor(args []string) error {
	o, rest, err := a.parse("doctor", args, flagJSON)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usagef("unexpected argument %q", rest[0])
	}
	p := a.pathsFor(o)
	d := report.DoctorJSON{Stores: []report.DoctorStore{}, CleanupPeriodSet: account.CleanupPeriodSet(p)}
	var gerr error
	if d.DesktopRunning, gerr = a.Guard.DesktopRunning(); gerr != nil {
		d.DesktopRunning, d.DesktopError = false, gerr.Error()
	}
	acc, aerr := account.Current(p)
	if aerr != nil {
		d.AccountError = aerr.Error()
	} else {
		d.Account = &report.DoctorAccount{AccountUUID: acc.AccountUUID, OrgUUID: acc.OrgUUID, Email: acc.Email, OrgName: acc.OrgName}
	}
	d.DesktopLastAccount = account.DesktopLastKnown(p)
	d.AccountsAgree = aerr != nil || d.DesktopLastAccount == "" || d.DesktopLastAccount == acc.AccountUUID
	infos, derr := store.Discover(p)
	if derr != nil {
		d.StoresError = derr.Error()
	}
	for _, in := range infos {
		ds := report.DoctorStore{ID: in.ID.String(), Records: in.Records, SampleTitles: report.Strs(in.SampleTitles)}
		if !in.LastActivity.IsZero() {
			ds.LastActivity = in.LastActivity.Format(time.RFC3339)
		}
		if in.Err != nil {
			ds.Error = in.Err.Error()
		}
		d.Stores = append(d.Stores, ds)
	}
	if o.json {
		if err := report.WriteJSON(a.Out, d); err != nil {
			return err
		}
		return derr
	}
	check := func(ok bool, msg string) {
		mark := "✓"
		if !ok {
			mark = "✗"
		}
		fmt.Fprintf(a.Out, "%s %s\n", mark, msg)
	}
	fmt.Fprintln(a.Out, disclaimer)
	switch {
	case gerr != nil:
		check(false, "could not tell whether Claude Desktop is running: "+gerr.Error())
	case d.DesktopRunning:
		check(false, "Claude Desktop is running (quit it before apply or undo)")
	default:
		check(true, "Claude Desktop is not running")
	}
	if aerr != nil {
		check(false, "signed-in account: "+aerr.Error())
	} else {
		check(true, fmt.Sprintf("signed-in account: %s · %s (%s/%s)", acc.Email, acc.OrgName,
			store.ShortUUID(acc.AccountUUID), store.ShortUUID(acc.OrgUUID)))
	}
	switch {
	case aerr != nil || d.DesktopLastAccount == "":
	case d.AccountsAgree:
		check(true, "Claude Desktop last used the same account")
	default:
		check(false, fmt.Sprintf("Claude Desktop last used account %s, but ~/.claude.json is signed in to %s (apply will refuse unless you pass --to)",
			store.ShortUUID(d.DesktopLastAccount), store.ShortUUID(acc.AccountUUID)))
	}
	check(d.CleanupPeriodSet, "cleanupPeriodDays set in ~/.claude/settings.json")
	if derr != nil {
		check(false, "session stores: "+derr.Error())
		return derr
	}
	check(true, fmt.Sprintf("%d session stores:", len(infos)))
	for _, in := range infos {
		mark := " "
		if in.Err != nil {
			mark = "✗"
		}
		fmt.Fprintf(a.Out, "  %s %s\n", mark, describe(in))
	}
	return nil
}
