# Security policy

## Reporting a vulnerability

Please report vulnerabilities **privately** through GitHub Security Advisories:
[Report a vulnerability](https://github.com/onlyilkr/claude-migrate/security/advisories/new).
Do not open a public issue or pull request for a security problem.
If that option is not available on the repository, open a regular issue titled "Security contact request"
with no details, and the maintainer will reply with a private channel.

Include the version (`claude-migrate version`), what you did, what happened, and, if you can, a minimal
fake home directory that reproduces it. Redact personal data. You can expect an acknowledgement within a
few days; this is a small volunteer project, so there is no formal SLA.

## Scope

claude-migrate writes and deletes files in a user's Claude Desktop data, so these are in scope:

- Anything that writes, overwrites or deletes a file it should not: outside the target session store,
  over an existing file, or a file `undo` did not create.
- Writing to the wrong account or organization store.
- Bypassing the Desktop-running guard or the SSH refusal so a write happens while Desktop could rewrite
  the same files.
- Path traversal or symlink tricks through account IDs, manifests, `--home` or store names.
- Leaking user data: session titles, paths, account details or tokens written to logs, manifests, output
  or the network. The tool makes no network requests and should never start to.
- Supply-chain issues in the release pipeline (unpinned actions, tampered checksums).

Out of scope: bugs that need an attacker who already controls your user account or home directory, and
changes in Claude Desktop's undocumented formats that make the tool stop with a clear error (that is the
intended fail-closed behavior; file a normal bug report).

## Supported versions

Only the latest release receives fixes.
