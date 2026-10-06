# drashta

A local command center for the projects in your `~/projects` folder.

- One Go program with a React app embedded in it.
- It listens on `127.0.0.1:4455` (loopback only) and opens in your browser.
- It shows your projects, git and fossil activity, Claude Code sessions, running dev servers, and a small per-project task and note tracker.
- Everything stays on your machine.

## Screenshots

Screenshots live in `docs/screenshots/`. They have not been captured yet. Planned shots (1440x900):

- `docs/screenshots/overview.png`: the main projects view.
- `docs/screenshots/project.png`: one project open (activity, tasks, notes).
- `docs/screenshots/sessions.png`: the Claude Code sessions view.
- `docs/screenshots/servers.png`: the running dev servers view.
- `docs/screenshots/viewer.png`: a file viewer (PDF, drawing or 3D).

Take them from a throwaway instance with invented data, not your real projects (see "Development").

## Requirements

- macOS is the main platform. Linux may build and run, but it is not tested.
- Go: the version in `go.mod` (currently 1.26).
- Node.js: a current LTS or newer, only needed to build the web app (CI uses the latest 22.x; the author develops on 26).
- `lsof`: used to find running dev servers. It ships with macOS.
- Optional:
  - `git`: without it, git history and activity are missing.
  - `gh` (GitHub CLI): adds GitHub data. Set `gh_enabled = false` to turn it off. Without `gh`, GitHub panels stay empty.
  - `chitralipi`: a DWG reader. Without it, a DWG file shows "Open in default app" instead of a preview.
  - An editor command (default `code -g {path}:{line}`): used by "open in editor".

## Quick start

```bash
git clone https://github.com/Prath2000/drashta.git
cd drashta
npm --prefix web ci
npm --prefix web run build
go build -o drashta ./cmd/drashta
./drashta
```

Then open <http://127.0.0.1:4455>.

- Use `./drashta -port 4467` to pick another port.
- `./drashta -version` prints the version. `./drashta -reindex` rebuilds the cache and exits.
- `make install` copies the binary to `/opt/homebrew/bin`, and `make service` installs a macOS launchd agent. Both change your machine, so read the `Makefile` first.

## Configuration

On first start drashta writes `~/.drashta/config.toml`. Changes are picked up when drashta restarts. Keys:

- `projects_dir`: the folder that holds your projects (default `~/projects`).
- `roots`: group folders inside `projects_dir`; each folder inside a group is a project.
- `hidden`: project folders to hide from the UI (a folder name, `group/folder`, or a project id). They are still found.
- `skip`: extra folder names the file-tree walk will not enter.
- `editor`: the command that opens a file. `{path}` and `{line}` are filled in. Only `config.toml` can set it, never the API.
- `terminal`: the terminal app used to open a project in a terminal (default `Terminal`).
- `claude_profiles`: Claude Code profile folders that hold `projects/<folder>/*.jsonl` sessions.
- `gh_enabled`: `true` or `false`. Allow calls to the `gh` CLI.
- `desktop_script`: optional script behind the desktop mode buttons. Empty means off. It must be inside `projects_dir`.
- `chitralipi`: path of the DWG reader program.

Set `DRASHTA_HOME` to use another data folder instead of `~/.drashta` (useful for tests).

## Where data lives

- `~/.drashta/`: the SQLite index (`index.db`), `config.toml`, and logs.
- `<project>/.drashta/*.md`: per-project tasks and notes. These files are the source of truth and are friendly to git.
- The SQLite index is a cache plus a history log. Most of it can be rebuilt with `-reindex`, but the activity and server-run history tables cannot, so do not delete them if you want that history.

## Privacy

- drashta reads Claude Code transcripts: `~/.claude*/projects/*/*.jsonl`.
- Transcript text is redacted (known secret patterns) before it reaches the index, full-text search or the API.
- The server binds to loopback only.
- Nothing is sent anywhere. The one exception is the optional `gh` CLI, which talks to GitHub the way it normally does. Set `gh_enabled = false` to avoid that.
- Redaction is pattern based. It is a safety net, not a guarantee. Treat `~/.drashta/index.db` as sensitive.

## Threat model

- Any process running under your user account is trusted. It can already read your files, so drashta does not try to defend against it.
- The goal is to stop web pages in your browser from talking to the local server. drashta does this with:
  - `Host`, `Origin` and `Sec-Fetch-Site` checks on requests;
  - a token sent in the `X-Drashta-Token` header on every mutating call. The token is embedded in `index.html` when the page is served.
- Anything that runs a command goes through a no-shell runner and path checks. The editor command is set only in `config.toml`.
- Do not expose the port to a network (no reverse proxy, no port forwarding).

See `SECURITY.md` for how to report a problem.

## Limitations

- Built and used mainly on macOS.
- It is a single-user, single-machine tool. There is no login and no remote access.
- The Claude Code `.jsonl` format is not a public contract. Unknown record types are counted and skipped, but a format change can hide some data.
- DWG preview needs the optional `chitralipi` program.
- The default config was written for one person's folder layout; check `config.toml` after first start.

## Development

Run from the repository root:

```bash
gofmt -l .                                   # must print nothing
go vet ./... && go test -race -count=1 ./... # takes a couple of minutes
cd web && npx tsc --noEmit -p . && npx vitest run && npm run e2e
```

- Warning: `npm ci` deletes `web/node_modules` and reinstalls it. Run it on purpose, not by habit. `make web` runs it.
- For a second instance, use a throwaway data folder and a free port: `DRASHTA_HOME=$(mktemp -d) ./drashta -port 4467`. Check the port is free first with `lsof -nP -iTCP:4467 -sTCP:LISTEN`.
- After any UI change, check the layout in a headless browser at 1440x900 and 1000x665. Unit tests cannot see layout.
- `npm --prefix web run dev` starts the Vite dev server (run drashta with `-dev` beside it); `npm --prefix web run dev:mock` runs it on mock data.

## Project layout

- `cmd/drashta`: the program entry point and command line.
- `internal/`: Go packages (`httpapi`, `indexer`, `store`, `probe`, `config`, `runx`, `pathguard`, and more). Each has a `doc.go` that lists its API.
- `web/`: the React app (`web/app/{routes,components,lib,mocks,styles}`), built into `web/build/client` and embedded in the binary.
- `docs/`: specs, decisions and architecture notes. Start with `docs/README.md`; the binding decisions are in `docs/decisions.md`.
- `docs/adr/`: architecture decision records.
- `contrib/desktop/`: optional desktop mode scripts for macOS.
- `CONTEXT.md`: glossary.

## Contributing

See `CONTRIBUTING.md`.

## License

MIT. See `LICENSE`.
