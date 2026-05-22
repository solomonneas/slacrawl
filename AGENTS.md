# AGENTS.md

## Purpose

`slacrawl` is a local-first Slack archive CLI. Preserve the read-only archive
model: inspect local caches, exports, databases, conversations, and snapshots
without mutating Slack workspace state.

Reusable archive mechanics belong in `crawlkit`. Keep Slack-specific parsing,
metadata, auth discovery, and CLI behavior in this repository.

## Development Rules

- Do not write to live Slack app data or real user archive stores.
- Use temp directories and temp SQLite databases in tests.
- Do not print tokens, cookies, channel contents, DM text, emails, or decrypted
  key material from diagnostics.
- Keep CLI output explicit about partial coverage, missing caches, and
  unavailable local state.
- Read `SPEC.md` before changing behavior or CLI surfaces.

## Validation

Run before handoff:

```bash
GOWORK=off go mod tidy
git diff --exit-code -- go.mod go.sum
GOWORK=off go vet ./...
GOWORK=off go test -count=1 ./...
```
