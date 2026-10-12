# Changelog

All notable changes are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and the project aims to follow [Semantic Versioning](https://semver.org/).

## [Unreleased]

Planned as 0.1.0, the first release.

### Added

- Project mascot (`docs/assets/logo.png`) and a 1280×640 social preview image (`docs/assets/social-preview.png`).
- `plan` (default), `apply`, `undo`, `pins`, `doctor` and `version` commands; `--json` on all but `version`.
- Copies Claude Desktop Code session records between account/organization stores, keeping the original
  session ID and file name so Desktop's global pin list still matches. Only `bridgeSessionIds` (emptied) and
  `scheduledTaskId` (removed) change; every other field keeps its original JSON value.
- Skip rules: no `cliSessionId`, deleted in the source, deleted in the target account (`deleted_<id>` marker), already in the target (matched on `cliSessionId` and
  `priorCliSessionIds`), file-name conflict with another session (a symlink or folder named like a record counts), duplicate within the source, and missing
  transcript. `--include-orphans` copies sessions without a transcript anyway.
- `apply` explains an empty run: either the target already has every session, or how many records were
  skipped for other reasons.
- Manual follow-up report: pins, scheduled tasks, sessions in the old account's scratch folders,
  `cleanupPeriodDays`.
- `pins` reports each pinned source session as `pinned in target`, `copied under a different ID; pin it again in Desktop`
  or `not migrated yet`. Which sessions are pinned comes from Desktop's own pin list (read-only), with
  `isStarred` as the fallback when that list cannot be read; `plan` counts pinned sessions that were copied
  earlier under a different ID (`manual.pins_to_redo`).
- `doctor` checks Desktop state, signed-in account, whether Desktop last used the same account, session
  stores and `cleanupPeriodDays`.
- `undo` without a path refuses (exit 2) when a newer run file is unreadable, instead of falling back to an older run;
  pass a manifest path to undo an older run.
- `undo` prints (or emits as JSON) what it already removed when it fails part-way, then reports the error.
- Exit code 2 also for a `~/.claude.json` that cannot be read and for `undo` of a manifest path that does not exist.
- A run file whose header lacks a target (`{}`, `null`) counts as unreadable when `undo` looks for the latest run.
- An unreadable account folder under the sessions root no longer aborts store discovery; `doctor` lists it as
  unreadable and it is never auto-picked as a source.
- Homebrew cask and `darwin/arm64`, `darwin/amd64` release binaries with checksums.
- `scripts/anonymize` to build privacy-safe regression fixtures from a real store.

### Security

- Dry run by default; `apply` is additive and never overwrites or deletes existing files.
- Every file is recorded in a fsynced manifest before it is written; `undo` removes only manifest entries
  inside the target store whose SHA-256 still matches (`--force` for modified files). A file that a newer run
  recorded again (same path, kept ID) is left to that run and reported as `superseded`; unreadable newer runs are listed (`unreadable_newer_runs`).
- `apply` and `undo` refuse while Claude Desktop runs, and over SSH where it cannot be detected. The guard
  for `--home` decides by file identity, so symlinks and alternate spellings of the real home are caught.
- The target account is cross-checked against the account Desktop last used; `apply` stops on a mismatch
  unless `--to` is given.
- Account and organization IDs must be UUIDs before they are used as path components; unfamiliar layouts
  stop the run (exit 4).
- Release workflow pins actions to commit SHAs and uses least-privilege tokens.

[Unreleased]: https://github.com/onlyilkr/claude-migrate/commits/main
