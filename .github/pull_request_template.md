## What and why

<!-- One or two sentences. Link the issue if there is one. -->

## Checklist

- [ ] Tests were written first and I saw them fail for the right reason
- [ ] `make check` passes (race tests, vet, gofmt, staticcheck)
- [ ] No personal data in fixtures, tests or docs (names, emails, real UUIDs, real session titles); privacy grep run if a fixture changed
- [ ] Docs updated (README, CHANGELOG) if behavior changed
- [ ] The change stays additive: no new way to overwrite or delete files the tool did not create
