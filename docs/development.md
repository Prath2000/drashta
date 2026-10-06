# Development

All commands run from the repository root.

## What you need

| Tool | Used for |
|---|---|
| Go (see `go.mod`) | backend |
| Xcode command line tools | FSEvents needs **cgo** (ADR 0002) |
| Node + npm | web app (React Router 7, Vite) |
| git, gh, lsof (fossil optional) | probes. A missing tool greys that part out; nothing else stops. |
| Chromium for Playwright | the end-to-end smoke test and screenshots |
| `chitralipi` (optional) | DWG preview. Without it a `.dwg` shows "Drawing preview is not available". Path in `config.toml` key `chitralipi`. |

drashta targets macOS (FSEvents, `open`, `lsof`, launchd).

## First steps

```bash
git clone <this repo> drashta && cd drashta
npm --prefix web ci                  # install web dependencies (once)
npm --prefix web run build           # builds web/build/client, which the Go binary embeds
go build ./... && go test -race -count=1 ./...
```

- `go build` works on a bare clone because `web/build/client/.keep` is tracked (it satisfies `go:embed`), but the binary serves **no UI** until the web build exists.
- `make build` does both steps, but it runs `npm ci`, which deletes `web/node_modules`. Do not use it while something else uses that folder.
- Go tests need `git` (`fossil` tests are skipped if it is missing). No test touches your real `~/projects`, `~/.claude*` or `~/.drashta`.

## Run

- Dev with the real backend: `make dev` (Go with `-dev` on 4455, Vite on 5173; Vite proxies `/api`, SSE included).
- Dev without a backend: `cd web && npm run dev:mock` (MSW answers `/api` from `app/mocks/fixtures/db.json`).
- Production-style: `npm --prefix web run build`, then `make install SKIP_WEB=1`. `make service` installs the launchd agent (`in.drashta`, restarts it); `make unservice` removes it; `make logs` tails the logs in `~/.drashta/`.
- Health: `curl -s localhost:4455/healthz` returns `ok`.
- CLI (no server needed, run inside a project folder): `drashta task new "Title" --feature F-1`, `drashta task list`, `drashta note add ...`, `drashta snap add --task T-1 --image shot.png`.
- Data lives in `~/.drashta/` (SQLite cache and history, config, logs). Set `DRASHTA_HOME` to use another folder. Tracker data lives in `<project>/.drashta/*.md` and is the source of truth.

## Test

```bash
gofmt -l .                                    # must print nothing
go vet ./... && go test -race -count=1 ./...  # about 2 minutes
cd web && npx tsc --noEmit -p . && npx vitest run && npm run e2e
```

## Verify on real data

After any change to a probe, the indexer or the store, run a **second** instance with a throwaway data folder. It reads your real `~/projects` and `~/.claude*` but writes only to the temp folder. Check the port is free first: `lsof -nP -iTCP:4467 -sTCP:LISTEN` must print nothing.

```bash
go build -buildvcs=false -o /tmp/drashta-check ./cmd/drashta
H=$(mktemp -d); DRASHTA_HOME=$H /tmp/drashta-check -port 4467 &
sleep 25
curl -s "localhost:4467/api/home?range=30" | python3 -c "import sys,json;d=json.load(sys.stdin);print(len(d['lanes']),'lanes')"
# compare a busy repo with git: the app's 30-day commit count must equal
cd ~/projects/<group>/<repo> && git log --since="<first day of the range>" --oneline | wc -l
kill %1; rm -r "$H" /tmp/drashta-check
```

Never reuse 4455 (the installed service) and never point a test at the real `~/.drashta`.

## Look at the UI

Unit tests cannot see layout. After any UI change, take a headless screenshot (Playwright) at 1440x900 and 1000x665 and check that each screen's root fills its mount box (`getBoundingClientRect`).

## Remove or reset

| Goal | Command |
|---|---|
| Stop the service | `launchctl bootout gui/$(id -u)/in.drashta` |
| Remove the service | `make unservice` |
| Rebuild the cache only | stop the service, then `drashta -reindex` (keeps history; refuses to run while the service runs) |
| Forget all data | `rm -r ~/.drashta` (you lose history that cannot be rebuilt: file-change counts, server runs) |
