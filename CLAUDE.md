# drashta: guide for contributors and coding agents

drashta is a local command center for `~/projects` (Go backend + embedded React app, `127.0.0.1:4455`). Start with `docs/README.md`, then `docs/architecture.md` (package map) and `docs/development.md` (recipes).

## Rules of this project

- `docs/decisions.md` is binding. If any other doc disagrees, it wins. Then the design spec `docs/superpowers/specs/2026-10-01-drashta-design.md`.
- Do not change the look of the UI without asking the maintainers (ADR 0007).
- Write explanations in plain, simple English with bullets.
- Do not commit, push, install or restart a running service unless asked.
- Project id = folder name (or `<group>.<name>` on a clash). Never put a slash in an id: ids are in URLs.

## Commands (repo root)

```bash
gofmt -l .                                       # must print nothing
go vet ./... && go test -race -count=1 ./...     # about 2 min
cd web && npx tsc --noEmit -p . && npx vitest run && npm run e2e
make logs                                        # tail ~/.drashta/drashta.log and launchd.log
```

- **Do not run `make web` or `npm ci` casually**: `npm ci` deletes `web/node_modules`. Use `npm --prefix web run build` and `make install SKIP_WEB=1`.
- A second instance for checks: `DRASHTA_HOME=$(mktemp -d) ./drashta -port 4467`. Never reuse 4455 and never the real `~/.drashta`.
- Real-data check recipe: `docs/development.md`.

## Where things live

- Go code: `cmd/drashta`, `internal/*`. Every package has `doc.go` with its exported API. Code map: `docs/architecture.md`.
- Web: `web/app/{routes,components,lib,mocks,styles}`. Layout rule: a route or drawer container is a one-cell grid (`dr-mount`) so its child fills it.
- Data: `~/.drashta/` (SQLite index, config, logs). Per-project tracker data: `<project>/.drashta/*.md` (source of truth, git-friendly).
- Spec and ADRs: `docs/spec/`, `docs/superpowers/specs/`, `docs/adr/`. Glossary: `CONTEXT.md`.

## Landmines

- **SQLite is a cache plus a history log.** `daily_activity` and `server_runs` are history (cannot be rebuilt). Never recompute or drop them from caches. `ReplaceCommits` deletes rows missing from the list: pass git and fossil commits of a project in ONE call.
- **Day loops**: never step days with `time.Date(y, m, d+1, 0, 0, ...)` (infinite loop where midnight is skipped, e.g. America/Santiago). Use `store.AddDays` (calendar math on `YYYY-MM-DD` keys) and `store.DayStart` (first instant of a day, safe where midnight is skipped), both in `internal/store/convert.go`, and test with `TZ=America/Santiago`.
- **Anything that runs a command** goes through `internal/runx` (no shell) and `internal/pathguard`. The one allowed `sh -c` is server restart, and it refuses shell characters. The editor command is set only in `config.toml`, never through the API.
- **The Claude parser**: use `ParseFamily` (not `Parse`) for sessions with subagent files. The `.jsonl` format is not a public contract; unknown record types are counted, not fatal.
- **Unit tests cannot see layout.** After any UI change, take a headless screenshot at 1440x900 and 1000x665 and check that each screen fills its mount box. We shipped a collapsed layout once with all tests green.
- **Secrets**: transcript text is redacted (`internal/probe/claude/text.go`) before it reaches the index, FTS or API. Add patterns, never remove. Fixtures must use invented secrets.
- **Test servers**: run `lsof -nP -iTCP:<port> -sTCP:LISTEN` first; it must print nothing, so a throwaway server never lands on a port another app uses.
- **Frame rule** (`actions.ProbePage`): an enforced CSP `frame-ancestors` beats `X-Frame-Options`; `/api/servers/embed` answers only for ports in the live list. Keep both.
- A tool-shell quirk: heredocs with backticks run the backticks. Use the file tool or `<<'EOF'`.
