# Contributing

Thanks for helping. This is a small project, so keep changes focused.

## Setup

- Install Go (version in `go.mod`), Node.js, and `lsof`.
- Clone, then build as described in `README.md` ("Quick start").
- Use a throwaway data folder for tests and manual runs: `DRASHTA_HOME=$(mktemp -d) ./drashta -port 4467`. Never point a test at your real `~/.drashta`, and check the port is free first (`lsof -nP -iTCP:4467 -sTCP:LISTEN` prints nothing).

## Checks before a pull request

```bash
gofmt -l .                                   # must print nothing
go vet ./... && go test -race -count=1 ./...
cd web && npx tsc --noEmit -p . && npx vitest run && npm run e2e
```

- `npm run e2e` needs Playwright browsers and is optional in CI.
- Do not run `npm ci` out of habit: it deletes `web/node_modules`.
- If you change dates or day loops, also run the tests with `TZ=America/Santiago` (midnight is skipped there). Use `store.AddDays` and `store.DayStart` in `internal/store/convert.go`, never `time.Date(y, m, d+1, ...)`.

## Rules

- `docs/decisions.md` is binding. If another doc disagrees, the decisions doc wins. To change a decision, open an issue first.
- Add tests with every change. Unit tests cannot see layout, so for any UI change also take headless screenshots at 1440x900 and 1000x665.
- Do not change the look (layout, colors, spacing, copy style) without discussing it in an issue first.
- Anything that runs a command goes through `internal/runx` and `internal/pathguard`. No new shell calls. The editor command is set only in `config.toml`, never through the API.
- Redaction is add-only: add patterns in `internal/probe/claude/text.go`, never remove one.
- Test fixtures must use invented secrets and invented project data. Never commit real transcripts, tokens, paths or scan data.
- SQLite is a cache plus a history log. Never recompute or drop `daily_activity` or `server_runs` from caches.
- Project ids come from the folder name and never contain a slash.
- Use `ParseFamily` (not `Parse`) for Claude sessions that have subagent files.

## Pull requests

- Keep one topic per pull request, and explain the why in plain English.
- Fill in the pull request template.
- Security problems: do not use issues or pull requests. See `SECURITY.md`.
