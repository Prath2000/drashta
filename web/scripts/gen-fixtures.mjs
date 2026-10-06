// Deterministic generator for web/app/mocks/fixtures/db.json.
//
// Everything in the fixture is INVENTED (project names, commit subjects, session titles,
// file contents, paths under /fx/projects). There is no input file and no randomness
// beyond a fixed seed, so running it twice gives byte-identical output.
//
// The shape is what MSW handlers (app/mocks/handlers.ts) serve: the v1 API shapes.
// Three projects have full detail (acme-api, notes-app, widget-kit); the rest are light.
//
// Run: npm run fixtures   (or: node scripts/gen-fixtures.mjs)
import { writeFileSync, mkdirSync } from "node:fs";

const outDir = new URL("../app/mocks/fixtures/", import.meta.url);

// ---------- deterministic helpers ----------
let seed = 20261001;
const rnd = () => {
  seed |= 0;
  seed = (seed + 0x6d2b79f5) | 0;
  let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
  t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
  return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
};
const ri = (a, b) => a + Math.floor(rnd() * (b - a + 1));
const pick = (arr) => arr[Math.floor(rnd() * arr.length)];
const hex = (n) => Array.from({ length: n }, () => "0123456789abcdef"[ri(0, 15)]).join("");
const uuid = () => `${hex(8)}-${hex(4)}-4${hex(3)}-a${hex(3)}-${hex(12)}`;

const NOW = "2026-09-30T21:18:37.000Z";
const NOW_MS = Date.parse(NOW);
const OFF = 330; // minutes: the local day is UTC+05:30
const dayOf = (iso) => new Date(Date.parse(iso) + OFF * 60000).toISOString().slice(0, 10);
const nowDay = dayOf(NOW);
const t0 = Date.parse(nowDay + "T00:00:00Z");
const days = [];
for (let i = 29; i >= 0; i--) days.push(new Date(t0 - i * 86400000).toISOString().slice(0, 10));
const dayIdx = Object.fromEntries(days.map((k, i) => [k, i]));
const agoH = (h) => new Date(NOW_MS - h * 3600000).toISOString();
const iso = (s) => new Date(Date.parse(s)).toISOString();

const ROOT = "/fx/projects";
const FULL = new Set(["acme-api", "notes-app", "widget-kit"]);

// ---------- project list ----------
// [id, group, vcs, branch, dirtyCount, stack, description, remote, hidden, lastChangeHoursAgo]
const P = [
  ["dotfiles", "personal", [], null, 0, ["Shell"], "", null, false, 0],
  ["go-playground", "personal", [], null, 0, ["Go 1.26"], "", null, false, 1100],
  ["chatbot-lab", "personal", [], null, 0, ["Go 1.22", "Swift"], "", null, false, 1900],
  ["recipe-box", "personal", [], null, 0, ["HTML", "C"], "", null, false, 150],
  ["site-v1", "personal", [], null, 0, ["Node"], "", null, false, 1300],
  ["notes-app", "personal", ["fossil"], "trunk", 1, ["Vite", "three.js", "Node"], "A small notes app: a gate into a void of cards that open into pages (ideas, diary, travel).", null, false, 9],
  ["scratchpad", "personal", [], null, 0, ["Python"], "", null, false, 1000],
  ["sampler", "personal", ["git"], "main", 1, ["Go 1.27", "Node"], "", null, false, 76],
  ["meteor", "personal", ["git"], "feat/meteor-shatter", 0, ["Node", "TypeScript", "pnpm", "Docker"], "A flight game down a track in two modes: Race (finite, finish line) and Survival (endless).", "https://github.com/example/meteor.git", false, 5],
  ["acme-api", "personal", ["git", "fossil"], "main", 2, ["Go 1.26", "stdlib", "launchd"], "Local dashboard of active and recent localhost dev servers, with kill and restart.", null, false, 13],
  ["term-demo", "personal", [], null, 0, ["Go 1.26"], "", null, false, 2900],
  ["study-notes", "personal", [], null, 0, [], "", null, false, 5],
  ["venv", "personal", [], null, 0, [], "", null, true, 1200],
  ["form-bot", "personal", [], null, 0, [], "", null, false, 60],
  ["billing-svc", "work", [], null, 0, [], "", null, false, 35],
  ["cad-viewer", "work", [], null, 0, [], "", null, false, 2100],
  ["agent-handbook", "tools", ["git"], "main", 1, ["Python"], "Skills for engineers who get dropped into an unfamiliar codebase and have to deliver.", "https://github.com/example/agent-handbook.git", false, 290],
  ["widget-kit", "tools", ["git"], "feat/editable-checkout", 12, ["Rust", "ratatui", "crossterm"], "Terminal workspace manager for coding agents: keeps terminals alive, detached and restorable.", "https://github.com/example/widget-kit.git", false, 140],
  ["widget-kit-v0.9.0", "tools", ["git"], "editable-checkout-v0.9.0", 10, ["Rust"], "", "https://github.com/example/widget-kit.git", true, 140],
  ["orchestrator", "tools", ["git"], "feat/phases-1-8", 8, ["Go 1.26"], "Hierarchical multi-agent orchestration: one daemon supervises a tree of agents under limits an operator sets.", null, false, 230],
];

// sessions per project, spread over the last 45 days (light projects only get a few)
// fixed sessions (tests refer to these): [id, profile, startAgoHours, durMin, title, turns, tools]
const FIXED_SESS = {
  dotfiles: [
    [uuid(), ".claude", 0.08, 5, "tidy the scan label and its states", 6, {}],
    [uuid(), ".claude", 2.5, 20, "add a day readout under the lanes", 9, {}],
  ],
  "notes-app": [["121c3b19-ada3-495e-a518-7fe5acf0a71e", ".claude-work", 7.5, 255, "wire the refresh button to the server", 7, { Read: 3, Bash: 1, Agent: 3, Write: 1, ExitPlanMode: 1 }]],
};
const SESS = { dotfiles: 4, "recipe-box": 6, "notes-app": 16, sampler: 1, meteor: 1, "study-notes": 1, "billing-svc": 30, orchestrator: 1 };
// files touched per day (lane noise) per project: [activeDays, maxFiles]
const FILES = { dotfiles: [8, 20], "recipe-box": [2, 3], "notes-app": [10, 25], sampler: [6, 10], meteor: [20, 120], "acme-api": [4, 6], "billing-svc": [18, 120], "agent-handbook": [6, 20], "widget-kit": [18, 400], "widget-kit-v0.9.0": [12, 400], orchestrator: [6, 25] };

const TITLES = [
  "tidy the scan label and its states",
  "add a day readout under the lanes",
  "wire the refresh button to the server",
  "fix the rail badge count",
  "write tests for the ref parser",
  "sketch the snapshot review flow",
  "split the viewport header into rows",
  "make the search palette keyboard friendly",
];
let titleSeq = 0;
const PROFILES = [".claude", ".claude-a", ".claude-b", ".claude-work", ".claude-extra"];

// ---------- commit subjects ----------
const VERBS = ["Fix", "Add", "Refactor", "Document", "Speed up", "Guard", "Rename", "Split", "Simplify", "Cover"];
const THINGS = [
  "the retry loop", "cache eviction", "config loading", "the status line", "error messages", "overlay focus",
  "the layout pass", "scrollback lookups", "the build script", "test fixtures", "path handling", "the event queue",
  "worktree lookups", "the parser", "keyboard handling", "mouse selection", "the render loop", "shutdown order",
];
const subject = () => `${pick(VERBS)} ${pick(THINGS)}`;
const AUTHORS = ["Ada Example", "Sam Sample", "Kim Placeholder", "Lee Demo"];

// ---------- file contents (invented) ----------
const GO_DISCOVER = `package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Server is a locally listening TCP process.
type Server struct {
	Port    int       \`json:"port"\`
	PID     int       \`json:"pid"\`
	Name    string    \`json:"name"\`
	Command string    \`json:"command"\`
	Cwd     string    \`json:"cwd"\`
	Seen    time.Time \`json:"seen"\`
}

// lsofTimeout bounds one discovery pass.
const lsofTimeout = 3 * time.Second

// devNames are process names that count as dev servers.
var devNames = map[string]bool{
	"node": true, "python": true, "ruby": true, "go": true, "php": true,
	"deno": true, "bun": true, "uvicorn": true, "gunicorn": true,
}

// Discover lists the TCP listeners owned by the current user.
func Discover(ctx context.Context) ([]Server, error) {
	ctx, cancel := context.WithTimeout(ctx, lsofTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "lsof", "-nP", "-iTCP", "-sTCP:LISTEN", "-F", "pcn").Output()
	if err != nil {
		return nil, fmt.Errorf("lsof: %w", err)
	}
	return parseLsof(out)
}

func parseLsof(out []byte) ([]Server, error) {
	var res []Server
	var cur Server
	for _, line := range bytes.Split(out, []byte("\\n")) {
		if len(line) < 2 {
			continue
		}
		val := string(line[1:])
		switch line[0] {
		case 'p':
			pid, err := strconv.Atoi(val)
			if err != nil {
				return nil, errors.New("bad pid " + val)
			}
			cur = Server{PID: pid}
		case 'c':
			cur.Name = val
		case 'n':
			i := strings.LastIndex(val, ":")
			if i < 0 {
				continue
			}
			port, err := strconv.Atoi(val[i+1:])
			if err != nil {
				continue
			}
			s := cur
			s.Port = port
			res = append(res, s)
		}
	}
	return res, nil
}

// IsDev reports whether a server looks like a development server.
func IsDev(s Server) bool {
	return devNames[strings.ToLower(s.Name)]
}
`;
const GO_MAIN = `package main

import (
	"flag"
	"log"
	"net/http"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "listen address")
	flag.Parse()
	srv := newServer()
	log.Printf("acme-api listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, srv.routes()))
}
`;
const files = {
  "acme-api": {
    "main.go": GO_MAIN,
    "discover.go": GO_DISCOVER,
    "server.go": `package main

import "net/http"

type server struct{ mux *http.ServeMux }

func newServer() *server { return &server{mux: http.NewServeMux()} }

func (s *server) routes() http.Handler {
	s.mux.HandleFunc("/api/servers", s.handleServers)
	return s.mux
}

func (s *server) handleServers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte("[]"))
}
`,
    "actions.go": `package main

import "syscall"

// Kill sends SIGTERM to a server's process.
func Kill(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }
`,
    "history.go": `package main

// History keeps the most recent servers seen, newest first.
type History struct{ items []Server }

func (h *History) Add(s Server) { h.items = append([]Server{s}, h.items...) }
`,
    Makefile: `build:
\tgo build -o acme-api .

install:
\tgo build -o /usr/local/bin/acme-api .

test:
\tgo test ./...

.PHONY: build install test
`,
    "go.mod": `module example.test/acme-api

go 1.26
`,
    "in.acme.plist": `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
  <key>Label</key><string>in.acme.api</string>
  <key>ProgramArguments</key><array><string>/usr/local/bin/acme-api</string></array>
  <key>KeepAlive</key><true/>
</dict></plist>
`,
    "web/index.html": `<!doctype html>
<html><head><meta charset="utf-8"><title>acme-api</title></head>
<body><h1>Local servers</h1><ul id="list"></ul><script>fetch("/api/servers").then(r=>r.json())</script></body></html>
`,
    "discover_test.go": `package main

import "testing"

func TestParseLsof(t *testing.T) {
	got, err := parseLsof([]byte("p10\\ncnode\\nn*:3000\\n"))
	if err != nil || len(got) != 1 || got[0].Port != 3000 {
		t.Fatalf("got %v, %v", got, err)
	}
}
`,
  },
  "notes-app": {
    "package.json": `{
  "name": "notes-app",
  "private": true,
  "scripts": { "dev": "vite", "build": "vite build" },
  "dependencies": { "three": "^0.170.0" }
}
`,
    "index.html": `<!doctype html>
<html><head><meta charset="utf-8"><title>notes-app</title></head>
<body><canvas id="stage"></canvas><script type="module" src="/src/main.js"></script></body></html>
`,
    "design/HANDOFF.md": `# Handoff

State of the notes app between sessions.

## Done
- Gate screen and the void of cards.
- Card pages: ideas, diary, travel.

## Next
- Audio for the void.
- Public launch checklist.
`,
    "design/flow-decisions.md": `# Flow decisions

- The gate opens on a click, never on a timer.
- Every card opens into its own page and returns to the void.
- Back always lands on the void, not the gate.
`,
    "plan/roadmap.md": `# Roadmap

1. Pages v3 (review).
2. Gate, void and exit polish.
3. Audio.
4. Ship it.
`,
    "errors_and_learnings.txt": `Learnings

- A canvas needs an explicit size before the first frame.
- Keep the camera rig out of the animation loop setup.
- Measure before you optimise the particle count.
`,
    "src/main.js": `import * as THREE from "three";
import { buildVoid } from "./void/backdrop.js";

const renderer = new THREE.WebGLRenderer({ canvas: document.getElementById("stage"), antialias: true });
const scene = new THREE.Scene();
buildVoid(scene);
renderer.setAnimationLoop(() => renderer.render(scene, new THREE.PerspectiveCamera()));
`,
    "src/style.css": `html, body { margin: 0; height: 100%; background: #05060a; }
#stage { width: 100%; height: 100%; display: block; }
`,
  },
  "widget-kit": {
    "README.md": `# widget-kit

Terminal workspace manager for coding agents. Keeps terminals alive, detached and restorable.

## Install

\`\`\`sh
cargo install --path .
\`\`\`

## Usage

Run \`widget-kit\` in a terminal, then split panes with the leader key.
`,
    "AGENTS.md": `# Agent notes

- Run \`just test\` before committing.
- Keep UI state out of the worker threads.
`,
    "Cargo.toml": `[package]
name = "widget-kit"
version = "0.10.0"
edition = "2021"

[dependencies]
ratatui = "0.29"
crossterm = "0.28"
`,
    "src/main.rs": `mod cli;

fn main() {
    let args = cli::parse();
    println!("widget-kit {:?}", args);
}
`,
    "src/cli.rs": `#[derive(Debug)]
pub struct Args {
    pub detach: bool,
}

pub fn parse() -> Args {
    Args { detach: std::env::args().any(|a| a == "--detach") }
}
`,
    justfile: `test:
    cargo test

fmt:
    cargo fmt --all
`,
    "CONTRIBUTING.md": `# Contributing

Open an issue first for anything larger than a typo.
`,
    "src/app/api/worktrees.rs": `pub fn list() -> Vec<String> {
    Vec::new()
}
`,
    "src/client/shell/worktree_overlays.rs": `pub fn draw_overlay() {
    // draws the checkout overlay
}
`,
  },
};

// ---------- tree builders ----------
const parentOf = (p) => (p.includes("/") ? p.slice(0, p.lastIndexOf("/")) : "");
const entry = (path, o = {}) => ({
  path,
  parent: parentOf(path),
  dir: !!o.dir,
  size: o.dir ? null : (o.size ?? 1000),
  mtime: o.dir ? null : iso(o.mtime ?? agoH(ri(10, 400))),
  hasChildren: !!o.hasChildren,
  collapsed: !!o.collapsed,
});
// spec: "d" open dir with children listed, "c" collapsed dir, otherwise a file with a size
function buildTree(spec, fileMap) {
  const out = [];
  for (const [path, kind, size] of spec) {
    if (kind === "d") out.push(entry(path, { dir: true, hasChildren: true }));
    else if (kind === "c") out.push(entry(path, { dir: true, collapsed: true }));
    else out.push(entry(path, { size: fileMap[path] ? fileMap[path].length : (size ?? ri(200, 9000)) }));
  }
  return out;
}
const TREE = {
  "acme-api": [
    [".git", "c"], [".gitignore", "f", 8], ["Makefile", "f"], ["actions.go", "f"], ["discover.go", "f"], ["discover_test.go", "f"],
    ["go.mod", "f"], ["history.go", "f"], ["history_test.go", "f", 900], ["in.acme.plist", "f"], ["main.go", "f"],
    ["server.go", "f"], ["server_test.go", "f", 1400], ["acme-api", "f", 6200000], ["web", "d"], ["web/index.html", "f"],
  ],
  "notes-app": [
    [".claude", "c"], [".fossil-settings", "c"], [".gitignore", "f", 253], ["dist", "c"], ["errors_and_learnings.txt", "f"],
    ["gate.png", "f", 18109], ["void.png", "f", 1351497], ["page_ideas.png", "f", 1497386], ["page_diary.png", "f", 1624037],
    ["page_travel.png", "f", 1646892], ["exit.png", "f", 37827], ["design", "d"], ["design/HANDOFF.md", "f"],
    ["design/flow-decisions.md", "f"], ["design/params.2026-09-30.json", "f", 1173], ["design/prototype", "c"],
    ["design/screenshots", "c"], ["design/tools", "c"], ["index.html", "f"], ["node_modules", "c"], ["package-lock.json", "f", 31888],
    ["package.json", "f"], ["plan", "d"], ["plan/backup.main.js", "f", 2860], ["plan/roadmap.md", "f", 3900], ["public", "d"],
    ["public/theme.mp3", "f", 9611851], ["public/favicon.svg", "f", 9523], ["public/icons.svg", "f", 5031], ["public/hero.jpg", "f", 2664616],
    ["src", "d"], ["src/assets", "c"], ["src/pages", "d"], ["src/pages/pages.js", "f", 3842], ["src/counter.js", "f", 247], ["src/main.js", "f"],
    ["src/style.css", "f"], ["src/void", "d"], ["src/void/audio.js", "f", 667], ["src/void/backdrop.js", "f", 2070],
    ["src/void/dive.js", "f", 2429], ["src/void/picking.js", "f", 1836], ["src/void/cards.js", "f", 7265], ["yarn.lock", "f", 7929],
  ],
  "widget-kit": null, // built below
};
{
  const rs = ["app", "client", "server", "ui", "term", "config", "util"];
  const spec = [
    [".agents", "d"], [".agents/notes.md", "f"], [".cargo", "d"], [".cargo/config.toml", "f"], [".git", "c"], [".gitattributes", "f"],
    [".githooks", "d"], [".githooks/pre-commit", "f"], [".github", "d"], [".github/workflows", "d"], [".github/workflows/ci.yml", "f"],
    [".gitignore", "f"], ["AGENTS.md", "f"], ["CHANGELOG.md", "f"], ["CONTRIBUTING.md", "f"], ["Cargo.lock", "f"], ["Cargo.toml", "f"],
    ["LICENSE", "f"], ["README.md", "f"], ["assets", "c"], ["build.rs", "f"], ["clippy.toml", "f"], ["docs", "d"], ["flake.nix", "f"],
    ["justfile", "f"], ["packaging", "d"], ["packaging/brew.rb", "f"], ["rust-toolchain.toml", "f"], ["scripts", "d"],
    ["scripts/release.sh", "f"], ["src", "d"], ["src/main.rs", "f"], ["src/cli.rs", "f"], ["target", "c"], ["tests", "d"], ["vendor", "c"],
  ];
  for (let i = 1; i <= 16; i++) spec.push(["docs/" + (i <= 4 ? "guide-" + i + ".md" : "page-" + i + ".mdx"), "f"]);
  for (const m of rs) {
    spec.push(["src/" + m, "d"]);
    for (let i = 0; i < 12; i++) spec.push(["src/" + m + "/" + pick(["mod", "state", "events", "render", "input", "tests", "layout", "focus"]) + i + ".rs", "f"]);
  }
  spec.push(["src/app/api", "d"], ["src/app/api/worktrees.rs", "f"], ["src/client/shell", "d"], ["src/client/shell/worktree_overlays.rs", "f"]);
  for (let i = 0; i < 40; i++) spec.push(["tests/case_" + i + ".rs", "f"]);
  TREE["widget-kit"] = spec;
}

const mdTxt = /\.(md|mdx|txt)$/i;

// ---------- build projects ----------
const taskNum = (n) => "T-" + n;
const projects = [];
const lanes = [];
const detail = {};
const events = [];
const allServers = [];

// fixed commits for the acme-api (tests and the e2e smoke refer to these ids)
const ACME_COMMITS = [
  ["93e82aa", "Run as launchd agent: make service / unservice, disable VCS stamping", 31, 2, ["Makefile", "in.acme.plist"], 14],
  ["bdbd091", "Fix review findings: stable merge, cache eviction, scan errors", 208, 30, ["discover.go", "history.go", "server.go", "actions.go", "main.go", "history_test.go", "server_test.go", "Makefile"], 15],
  ["5ebdfc5", "Add a history of recent servers", 120, 4, ["history.go", "history_test.go", "main.go"], 18],
  ["7074f80", "Dashboard with live previews", 340, 12, ["web/index.html", "server.go", "main.go"], 20],
  ["bdcb99c", "Guard API against CSRF and DNS rebinding", 190, 29, ["actions.go", "main.go", "server.go", "server_test.go"], 22],
  ["566c4ae", "Parse lsof output into servers", 150, 0, ["discover.go", "discover_test.go"], 30],
  ["f38f2f9", "Detect dev processes by name", 60, 5, ["discover.go"], 31],
  ["2fb63fe", "Kill and restart actions", 88, 3, ["actions.go", "server.go"], 36],
  ["eb36543", "Add Makefile and go.mod", 22, 0, ["Makefile", "go.mod"], 40],
  ["edfffbe957", "Initial import", 40, 0, ["main.go"], 44, "fossil"],
  ["1d1e9adf21", "Commit the fossil side", 5, 1, ["go.mod"], 45, "fossil"],
];
const NOTES_COMMITS = [
  ["b7dc920bef", "Cards v3: ideas, diary and travel pages", 410, 120, ["src/pages/pages.js", "src/style.css"], 20],
  ["a1223b19ca", "Void backdrop: dim the stars", 38, 12, ["src/void/backdrop.js"], 60],
  ["33f0aa0176", "Cards: add a hover glow", 52, 9, ["src/main.js"], 120],
  ["7e12ab4c0d", "Add the handoff notes", 90, 0, ["design/HANDOFF.md"], 200],
  ["0c6d5be8f3", "Initial scaffold", 120, 0, ["package.json", "index.html"], 330],
];

// a line of code-looking text in the language of the file, so the diff view gets syntax colours
function ctx(path, tag = "ctx") {
  if (path.endsWith(".go")) return `func ${tag}Handler() error { return nil }`;
  if (path.endsWith(".rs")) return `fn ${tag}_handler() -> bool { true }`;
  return `${tag} line`;
}

function diffFor(paths, nlines = 12) {
  return paths
    .map(
      (p) =>
        `diff --git a/${p} b/${p}\nindex ${hex(7)}..${hex(7)} 100644\n--- a/${p}\n+++ b/${p}\n@@ -1,4 +1,${nlines} @@\n ${ctx(p)}\n-${ctx(p, "old")}\n+${ctx(p, "new")}\n+${ctx(p, "extra")}\n ${ctx(p)}\n`,
    )
    .join("");
}

function makeCommits(id, group) {
  const list = [];
  if (id === "acme-api" || id === "notes-app") {
    for (const [cid, msg, plus, minus, fl, hAgo, vcs] of id === "acme-api" ? ACME_COMMITS : NOTES_COMMITS)
      list.push({ id: cid, vcs: vcs || (id === "notes-app" ? "fossil" : "git"), author: id === "acme-api" ? "Claude Code" : "dev", ts: agoH(hAgo), msg, plus, minus, fl });
    return list;
  }
  const counts = { "widget-kit": 245, "widget-kit-v0.9.0": 49, meteor: 60, sampler: 14, orchestrator: 13 };
  const n = counts[id] ?? 0;
  const span = id === "widget-kit" ? 24 * 28 : 24 * 20;
  for (let i = 0; i < n; i++)
    list.push({
      id: hex(8),
      vcs: "git",
      author: pick(AUTHORS),
      ts: agoH(ri(3, span)),
      msg: subject() + (id.startsWith("widget") ? ` (#${4000 + ri(1, 700)})` : ""),
      plus: ri(2, 250),
      minus: ri(0, 60),
      fl: Array.from({ length: ri(1, 5) }, () => "src/" + pick(["app", "client", "ui"]) + "/" + pick(["mod", "state", "events"]) + ri(0, 9) + ".rs"),
    });
  if (id === "sampler") list.push({ id: "04069bd", vcs: "git", author: "dev", ts: agoH(70), msg: "Tweak the sampler defaults", plus: 12, minus: 3, fl: ["main.go"] });
  list.sort((a, b) => (a.ts < b.ts ? 1 : -1));
  return list;
}

for (const [id, group, vcs, branch, dirtyCount, stack, description, remote, hidden, lastH] of P) {
  const full = FULL.has(id);
  const path = `${ROOT}/${group}/${id}`;
  const lastChangeAt = agoH(lastH);

  // servers (invented)
  const servers = [];
  const addSrv = (port, name, cmd, kind, title) =>
    servers.push({
      key: "server-key:" + id + "-" + port,
      port,
      pid: port === 8765 ? 43661 : ri(20000, 70000),
      name,
      cmd,
      cwd: path,
      projectId: id,
      kind,
      title,
      firstSeen: NOW,
      running: true,
    });
  if (id === "recipe-box") addSrv(55829, "agent-browser", "/fx/bin/agent-browser", "helper", "browser automation helper");
  if (id === "notes-app") {
    addSrv(8765, "node", "node design/tools/serve.mjs", "dev", "notes-app: handoff preview");
    for (const p of [51738, 53429, 54878]) addSrv(p, "agent-browser", "agent-browser", "helper", "browser automation helper");
  }
  if (id === "meteor") addSrv(8787, "proxy", "/fx/bin/proxy --port 8787", "helper", "local API proxy");
  if (id === "acme-api") addSrv(8090, "static-server", "static-server --port 8090", "dev", "acme-api preview");
  if (id === "billing-svc") {
    addSrv(3001, "server", "node server.js", "dev", "billing-svc dev server");
    addSrv(9333, "server", "node debug.js", "helper", "debug helper");
    for (const p of [53607, 53629, 54189]) addSrv(p, "agent-browser", "agent-browser", "helper", "browser automation helper");
  }
  allServers.push(...servers);

  // commits
  const commitsAll = makeCommits(id, group);
  const commits = commitsAll.map((c) => ({
    id: c.id, vcs: c.vcs, author: c.author, ts: iso(c.ts), msg: c.msg, plus: c.plus, minus: c.minus, nfiles: c.fl.length, tasks: [],
  }));

  // lane cells
  const cells = days.map(() => ({ files: 0, commits: 0, linesPlus: 0, linesMinus: 0, sessions: 0, snapshots: 0, notes: 0, serverMinutes: 0 }));
  const [activeDays, maxFiles] = FILES[id] ?? [0, 0];
  for (let k = 0; k < activeDays; k++) cells[ri(0, 29)].files += ri(1, maxFiles);
  if (activeDays) cells[29].files += ri(1, Math.max(2, maxFiles >> 2));

  const commitDetail = {};
  commitsAll.forEach((c, i) => {
    const k = dayIdx[dayOf(c.ts)];
    if (k != null) {
      cells[k].commits += 1;
      cells[k].linesPlus += c.plus || 0;
      cells[k].linesMinus += c.minus || 0;
    }
    events.push({ ts: iso(c.ts), kind: "commit", ref: id + ":commit:" + c.id, label: c.msg, projectId: id, context: "+" + c.plus + " −" + c.minus });
    if (full && i < (id === "widget-kit" ? 25 : 99)) {
      commitDetail[c.id] = {
        body: i % 2 ? "" : "Co-Authored-By: Example Assistant <noreply@example.test>",
        files: c.fl.map((p) => ({ path: p, plus: null, minus: null })),
        patch: diffFor(c.fl.slice(0, 3)),
      };
    }
  });
  // acme-api keeps commit details for the first eight only (the fossil ones have no patch)
  if (id === "acme-api") for (const k of ["eb36543", "edfffbe957", "1d1e9adf21"]) delete commitDetail[k];
  if (id === "notes-app") delete commitDetail["0c6d5be8f3"];

  // sessions
  const nSess = SESS[id] ?? 0;
  const sessions = [];
  const fixed = FIXED_SESS[id] ?? [];
  for (let i = 0; i < nSess; i++) {
    const fx = fixed[i];
    const start = new Date(NOW_MS - (fx ? fx[2] : ri(10, 45 * 24)) * 3600000);
    const end = new Date(start.getTime() + (fx ? fx[3] : ri(5, 240)) * 60000);
    const k = dayIdx[dayOf(start.toISOString())];
    if (k != null) cells[k].sessions += 1;
    const title = fx ? fx[4] : group === "work" ? "assistant session" : TITLES[titleSeq++ % TITLES.length];
    events.push({ ts: start.toISOString(), kind: "session", ref: id + ":session:" + "S" + i, label: title, projectId: id, context: fx ? fx[1] : PROFILES[i % PROFILES.length] });
    sessions.push({
      id: fx ? fx[0] : uuid(),
      profile: fx ? fx[1] : PROFILES[i % PROFILES.length],
      start: start.toISOString(),
      end: end.toISOString(),
      title,
      turns: fx ? fx[5] : ri(3, 40),
      kb: ri(20, 900),
      tools: fx ? fx[6] : full ? { Read: ri(1, 9), Bash: ri(1, 5), Edit: ri(0, 6) } : {},
      edited: fx ? [] : full ? [`${path}/src/` + pick(["main.js", "style.css"])] : [],
      tasks: [],
      reason: "cwd",
    });
  }
  // events carry the real session ids
  sessions.forEach((s, i) => {
    const ev = events.find((e) => e.projectId === id && e.kind === "session" && e.ref === id + ":session:S" + i);
    if (ev) ev.ref = id + ":session:" + s.id;
  });
  sessions.sort((a, b) => (a.start < b.start ? 1 : -1));
  const sessionTurns = {};
  if (full)
    sessions.slice(0, 6).forEach((s) => {
      const out = [];
      for (let i = 0; i < Math.min(s.turns, 12); i++) {
        const role = i % 3 === 0 ? "user" : i % 3 === 1 ? "claude" : "tool";
        out.push({
          i,
          role,
          ts: new Date(Date.parse(s.start) + i * 45000).toISOString(),
          text: role === "user" ? `sample request ${i + 1}: please adjust the layout.` : role === "claude" ? `sample answer ${i + 1}: I changed the layout and ran the tests.` : `sample tool output ${i + 1}`,
          ...(role === "tool" ? { tool: i % 2 ? "Edit" : "Bash" } : {}),
        });
      }
      sessionTurns[s.id] = out;
    });

  // files and trees
  const fileMap = files[id] ?? {};
  const tree = TREE[id] ? buildTree(TREE[id], fileMap) : [];
  const fileContents = {};
  for (const [p, text] of Object.entries(fileMap)) fileContents[p] = { lines: text.split("\n").length - 1, text };
  const docs = tree
    .filter((n) => !n.dir && mdTxt.test(n.path))
    .map((n) => ({ path: n.path, title: n.path.split("/").pop(), mtime: n.mtime ?? iso(NOW), lines: fileContents[n.path]?.lines ?? ri(5, 200) }));

  // dirty working copy
  let dirty = [];
  let diff = "";
  if (id === "acme-api") dirty = [{ path: ".DS_Store", state: "??" }, { path: ".fslckout", state: "??" }];
  if (id === "notes-app") dirty = [{ path: "src/.DS_Store", state: "EDITED" }];
  if (id === "widget-kit") {
    const dp = ["docs/page-5.mdx", "src/app/api/worktrees.rs", "src/client/shell/worktree_overlays.rs"];
    for (const m of ["composition", "mouse", "overlays", "state", "worktrees", "text_editing", "graphics", "popup_focus", "agents"]) dp.push("src/client/shell/" + m + ".rs");
    dirty = dp.map((p) => ({ path: p, state: "M" }));
    diff = diffFor(dp.slice(0, 3)).slice(0, 3000);
  }

  // day files
  const dayFiles = {};
  if (full) {
    for (const dk of days) dayFiles[dk] = [];
    const cand = tree.filter((n) => !n.dir).map((n) => n.path);
    for (let k = 0; k < Math.min(cand.length, 14); k++) dayFiles[days[ri(20, 29)]].push(cand[k]);
  }

  detail[id] = {
    commits,
    commitDetail,
    tree,
    files: fileContents,
    dirty,
    diff,
    sessions,
    sessionTurns,
    docs,
    dayFiles,
    readme: fileContents["README.md"] ? { path: "README.md", text: fileContents["README.md"].text } : null,
  };

  projects.push({
    id, name: id, group, path, vcs, branch, lastChangeAt, hidden, surveyed: true, dirtyCount, description, stack, remote, detail: full ? "full" : "light",
  });
  lanes.push({ projectId: id, cells, liveServers: servers.filter((s) => s.running).map((s) => s.port), lastChangeAt });
}

// ---------- tracker ----------
const M = (id, projectId, title, due, featureIds) => ({ id, projectId, title, due, featureIds, rev: "r1" });
const milestones = [
  M("M-1", "notes-app", "Pages v3", "2026-10-05", ["F-1", "F-2"]),
  M("M-2", "notes-app", "Public launch", "2026-10-20", ["F-3", "F-4"]),
  M("M-3", "acme-api", "Always-on service", "2026-10-02", ["F-5", "F-6", "F-7"]),
  M("M-1", "widget-kit", "Editable worktrees", "2026-10-10", ["F-1"]),
];
const FE = (id, projectId, title, summary, milestoneId) => ({ id, projectId, title, summary, milestoneId, rev: "r1" });
const features = [
  FE("F-1", "notes-app", "Card pages", "Each card opens into its own page: ideas, diary, travel.", "M-1"),
  FE("F-2", "notes-app", "Gate, void and exit", "Arrival gate, the void of cards, and the exit screen.", "M-1"),
  FE("F-3", "notes-app", "Audio", "Ambient audio for the void.", "M-2"),
  FE("F-4", "notes-app", "Ship it", "Launch checklist and deploy.", "M-2"),
  FE("F-5", "acme-api", "Discovery", "Find listening dev servers.", "M-3"),
  FE("F-6", "acme-api", "Dashboard", "Live previews, kill and restart.", "M-3"),
  FE("F-7", "acme-api", "Service", "Run as an always-on agent.", "M-3"),
  FE("F-1", "widget-kit", "Editable worktree checkout", "Check out a worktree from inside the client.", "M-1"),
];
const mOf = (pid, fid) => (features.find((f) => f.projectId === pid && f.id === fid) || {}).milestoneId ?? null;
const TK = (n, projectId, featureId, title, status, assignee, created, links) => ({
  id: taskNum(n), projectId, featureId, milestoneId: mOf(projectId, featureId), title, status, assignee, created: iso(created), rev: "r1", links,
});
const tasksRaw = [
  TK(1, "notes-app", "F-1", "Ideas page: constellation over a compass rose", "review", "dev + Claude", "2026-09-29T07:40:00Z", ["notes-app:commit:b7dc920bef", "notes-app:snapshot:T-1/v3", "notes-app:file:design/HANDOFF.md", "@SESSION"]),
  TK(2, "notes-app", "F-1", "Diary page: a closed book on a lectern", "review", "dev + Claude", "2026-09-29T07:42:00Z", ["notes-app:commit:b7dc920bef", "notes-app:snapshot:T-2/v3", "@SESSION"]),
  TK(3, "notes-app", "F-1", "Travel page: map with route lines", "in_progress", "dev + Claude", "2026-09-29T07:44:00Z", ["notes-app:snapshot:T-3/v4"]),
  TK(4, "notes-app", "F-1", "Cards: add a hover glow", "done", "dev", "2026-09-28T09:00:00Z", ["notes-app:commit:33f0aa0176"]),
  TK(5, "notes-app", "F-2", "Gate screen", "done", "dev + Claude", "2026-09-27T09:00:00Z", ["notes-app:snapshot:T-5/v1"]),
  TK(6, "notes-app", "F-2", "Void screen", "done", "dev + Claude", "2026-09-27T09:10:00Z", ["notes-app:snapshot:T-6/v1"]),
  TK(7, "notes-app", "F-2", "Exit screen", "in_progress", "dev + Claude", "2026-09-27T09:20:00Z", ["notes-app:snapshot:T-7/v1"]),
  TK(8, "notes-app", "F-3", "Ambient loop for the void", "backlog", "dev", "2026-09-29T10:00:00Z", []),
  TK(9, "notes-app", "F-3", "Mute control", "backlog", "dev", "2026-09-29T10:05:00Z", []),
  TK(10, "notes-app", "F-4", "Draft the launch checklist", "done", "dev", "2026-09-29T10:10:00Z", []),
  TK(11, "acme-api", "F-5", "lsof parsing and dev-process detection", "done", "dev + Claude", "2026-09-29T05:00:00Z", ["acme-api:commit:566c4ae", "acme-api:commit:f38f2f9"]),
  TK(12, "acme-api", "F-5", "Persistent history of recent servers", "done", "dev + Claude", "2026-09-29T05:10:00Z", ["acme-api:commit:5ebdfc5", "acme-api:file:history.go"]),
  TK(13, "acme-api", "F-6", "Dashboard with live previews, kill and restart", "done", "dev + Claude", "2026-09-29T05:20:00Z", ["acme-api:commit:7074f80", "acme-api:commit:2fb63fe", "acme-api:file:web/index.html"]),
  TK(14, "acme-api", "F-6", "Guard API against CSRF and DNS rebinding", "done", "dev + Claude", "2026-09-29T05:30:00Z", ["acme-api:commit:bdcb99c", "acme-api:file:server.go"]),
  TK(15, "acme-api", "F-7", "Run as a launchd agent", "done", "dev + Claude", "2026-09-29T05:40:00Z", ["acme-api:commit:93e82aa", "acme-api:file:Makefile"]),
  TK(16, "acme-api", "F-7", "Commit the fossil side (.fslckout untracked in git)", "todo", "dev", "2026-09-30T05:00:00Z", ["acme-api:commit:1d1e9adf21"]),
  TK(17, "acme-api", "F-7", "Copy discovery into the next project", "in_progress", "dev + Claude", "2026-09-30T05:10:00Z", ["acme-api:file:discover.go", "acme-api:commit:bdbd091"]),
  TK(1, "widget-kit", "F-1", "Move worktree lookups off the UI thread", "done", "dev", "2026-09-24T05:00:00Z", ["widget-kit:file:src/app/api/worktrees.rs", "widget-kit:commit:@C0", "widget-kit:commit:@C1"]),
  TK(2, "widget-kit", "F-1", "Worktree checkout overlay in the client shell", "in_progress", "dev", "2026-09-24T05:10:00Z", ["widget-kit:file:src/client/shell/worktree_overlays.rs", "widget-kit:commit:@C2"]),
  TK(3, "widget-kit", "F-1", "Text editing inside worktree overlays", "review", "dev", "2026-09-24T05:20:00Z", ["widget-kit:commit:@C3", "widget-kit:commit:@C4"]),
  TK(4, "widget-kit", "F-1", "Document the worktree checkout options", "todo", "dev", "2026-09-24T05:30:00Z", ["widget-kit:file:README.md"]),
];
// resolve placeholders
const nSessionRef = detail["notes-app"].sessions[0].id;
const wkCommits = detail["widget-kit"].commits;
for (const t of tasksRaw)
  t.links = t.links.map((l) => (l === "@SESSION" ? "notes-app:session:" + nSessionRef : l.replace(/@C(\d)/, (_, i) => wkCommits[+i].id)));

const SN = (pid, tn, v, title, image, captured, verdict, reviewNote = "") => ({
  id: taskNum(tn) + "/v" + v,
  projectId: pid,
  taskId: taskNum(tn),
  version: v,
  title,
  path: image,
  url: "/api/projects/" + pid + "/raw?path=" + encodeURIComponent(image),
  captured: iso(captured),
  author: "Claude",
  commit: null,
  verdict,
  reviewNote,
});
const snapshots = [
  SN("notes-app", 1, 2, "Ideas page", "design/screenshots/ideas2.png", "2026-09-29T15:40:00Z", "changes", "Too busy. Want it to feel calmer."),
  SN("notes-app", 1, 3, "Ideas page", "design/screenshots/ideas3.png", "2026-09-30T06:32:00Z", "pending"),
  SN("notes-app", 2, 2, "Diary page", "design/screenshots/diary2.png", "2026-09-29T16:10:00Z", "changes", "Book is too small."),
  SN("notes-app", 2, 3, "Diary page", "design/screenshots/diary3.png", "2026-09-30T06:50:00Z", "pending"),
  SN("notes-app", 3, 2, "Travel page", "design/screenshots/travel2.png", "2026-09-29T17:00:00Z", "changes", "Drop the frame. The map should take the whole screen."),
  SN("notes-app", 3, 3, "Travel page", "design/screenshots/travel3.png", "2026-09-30T05:10:00Z", "approved"),
  SN("notes-app", 3, 4, "Travel page", "design/screenshots/travel4.png", "2026-09-30T08:00:00Z", "pending"),
  SN("notes-app", 5, 1, "Gate", "gate.png", "2026-09-28T10:00:00Z", "approved"),
  SN("notes-app", 6, 1, "Void", "void.png", "2026-09-28T10:30:00Z", "approved"),
  SN("notes-app", 7, 1, "Exit", "exit.png", "2026-09-29T11:00:00Z", "pending"),
];
const notes = [];
const NT = (n, ref, text, date, resolved, extra = {}) => {
  notes.push({ id: "N-" + n, ref, projectId: ref.split(":")[0], text, author: "dev", date: iso(date), resolved, ...extra });
};
NT(1, "notes-app:snapshot:T-1/v3", "Constellation lines slightly too bright against the dome.", "2026-09-30T07:30:00Z", false, { x: 0.46, y: 0.2 });
NT(2, "notes-app:snapshot:T-1/v3", '"turn the needle" hint needs more contrast.', "2026-09-30T07:31:00Z", false, { x: 0.5, y: 0.9 });
NT(3, "acme-api:commit:bdcb99c", "Carry this Host/Origin guard into the next project as-is.", "2026-09-30T14:25:00Z", false);
NT(4, "acme-api:file:discover.go", "devNames list: add 'vite' variants and 'python3.x' here.", "2026-09-30T14:30:00Z", false, { line: 30 });
NT(5, "notes-app:file:design/HANDOFF.md", "Keep this file as the single handoff between sessions.", "2026-09-30T06:40:00Z", true, { line: 3 });
NT(6, "notes-app:session:" + nSessionRef, "This session is where the dashboard idea started. Link sessions across projects.", "2026-09-30T14:35:00Z", false);

const snapKey = (t) => t.id;
const tasks = tasksRaw.map((t) => {
  const snaps = snapshots.filter((s) => s.taskId === t.id && s.projectId === t.projectId).sort((a, b) => a.version - b.version);
  const latest = snaps[snaps.length - 1];
  const self = t.projectId + ":task:" + t.id;
  return {
    ...t,
    latestSnapshot: latest ? { version: latest.version, verdict: latest.verdict } : null,
    linkCount: t.links.length,
    openNotes: notes.filter((n) => !n.resolved && (n.ref === self || t.links.includes(n.ref))).length,
  };
});
void snapKey;

// tracker items on the lanes and in the event list
const laneOf = (pid) => lanes.find((l) => l.projectId === pid);
for (const s of snapshots) {
  const k = dayIdx[dayOf(s.captured)];
  if (k != null) laneOf(s.projectId).cells[k].snapshots += 1;
  events.push({ ts: s.captured, kind: "snapshot", ref: s.projectId + ":snapshot:" + s.id, label: s.title + " v" + s.version, projectId: s.projectId, context: s.verdict });
}
for (const n of notes) {
  const k = dayIdx[dayOf(n.date)];
  if (k != null) laneOf(n.projectId).cells[k].notes += 1;
  events.push({ ts: n.date, kind: "note", ref: n.ref, label: n.text, projectId: n.projectId, context: "on " + n.ref });
}
for (const t of tasks) events.push({ ts: t.created, kind: "task", ref: t.projectId + ":task:" + t.id, label: "Task created: " + t.title, projectId: t.projectId, context: t.status });
events.sort((a, b) => (a.ts < b.ts ? -1 : a.ts > b.ts ? 1 : 0));

// links from commits to tasks (the fixture keeps `tasks` empty on commits, like the converter did)

const db = {
  source: "synthetic: generated by web/scripts/gen-fixtures.mjs",
  now: NOW,
  nowDay,
  dayOffsetMinutes: OFF,
  statuses: ["backlog", "todo", "in_progress", "review", "done"],
  profiles: PROFILES,
  author: "dev",
  hiddenFolders: ["venv", "widget-kit-v0.9.0"],
  days,
  projects,
  lanes,
  detail,
  servers: allServers,
  events,
  milestones,
  features,
  tasks,
  snapshots,
  notes,
};

mkdirSync(outDir, { recursive: true });
const text = JSON.stringify(db);
writeFileSync(new URL("db.json", outDir), text);
console.log("wrote app/mocks/fixtures/db.json", Math.round(text.length / 1024) + " KB", projects.length, "projects", events.length, "events");
