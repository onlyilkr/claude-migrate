# Contributing

Thanks for helping. This tool writes into a user's Claude Desktop data, so the bar is: small changes,
tests first, nothing that can overwrite or delete a file the tool did not create.

## Setup

Go 1.24 or newer (see `go.mod`); macOS is the only supported platform. The module has no dependencies.

```sh
git clone https://github.com/onlyilkr/claude-migrate
cd claude-migrate
make check
```

`make check` runs the race-enabled tests, `go vet`, a gofmt check and staticcheck (fetched with
`go run`, nothing to install). CI runs the same on macOS.

staticcheck runs on Go 1.26; `make lint` selects that toolchain automatically (Go downloads it on first use).

| Target | Does |
|---|---|
| `make test` | `go test -race ./...` |
| `make vet` | `go vet ./...` |
| `make fmt-check` | fails if `gofmt -l .` prints anything |
| `make lint` | staticcheck |
| `make check` | all of the above |
| `make build` | `bin/claude-migrate` |
| `make snapshot` | local GoReleaser build, publishes nothing |

## Tests first

Behavior changes and bug fixes start with a failing test: write it, run it, and confirm it fails for the
reason you expect, then write the smallest change that makes it pass. Table-driven tests for `plan`;
`testutil.NewHome` gives you a fake `$HOME` for anything that touches disk. Never run tests or the CLI
against your real Desktop data; everything goes through a temporary home (`--home` exists for this).

Conventional commit subjects (`feat(plan): ...`, `fix(guard): ...`, `docs: ...`), one logical change each.

## Fixtures and personal data

Fixtures must never contain personal data: names, emails, real account/org/session UUIDs, real session
titles, project paths, URLs. Build them from a real store only with the anonymizer:

```sh
go run ./scripts/anonymize -src <store dir> -out <dir>
```

Then search the output for your name, email, organization, account IDs and home paths, and
confirm nothing matches. Use placeholders like `dev@example.com` and `1a2b3c4d/5e6f7a8b` everywhere else,
including issues and pull requests.

## Docs

User-visible behavior is documented in `README.md` and `CHANGELOG.md` (under Unreleased). Keep the usage
text in `cmd/claude-migrate/app.go` and the README command table in sync.

## Pull requests

Fill in the template. Keep the change additive by default; anything that adds a new way to write or delete
files needs a stated reason and tests for the failure paths. Report security problems privately, see
[SECURITY.md](SECURITY.md).
