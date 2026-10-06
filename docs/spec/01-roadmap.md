# drashta: end-to-end roadmap

This plan runs from the early prototype to drashta running as the opening screen of a Mac.
The rule from [00-decisions.md](../decisions.md) applies: **nothing moves to the next phase without the user's green light.**

| Phase | What | Output | Gate |
|---|---|---|---|
| 0 | Planning docs | this `docs/` folder | user reviews these docs |
| 1 | Prototype v3 | the full app click-through in an artifact | **frozen 2026-10-01** (user green light) |
| 2 | Spec and plan | `docs/superpowers/specs/…-drashta-design.md` and an implementation plan | **docs written 2026-10-01, awaiting user review**; the user approves the spec |
| 3 | Build: foundation | Go server, probes, SQLite, API, launchd | **done 2026-10-01**: tests pass, `/healthz` ok, runs on real data |
| 4 | Build: screens | React UI for Home, Project, Tracker, Inbox and the viewport | **done 2026-10-01**: matches the frozen prototype (screenshots compared) |
| 5 | Desktop | Plash-aware home and setup | one week of daily use |
| 6 | Harden and later | fixes from real use; the chart map as a folder browser | as needed |

---

## Phase 0: planning docs (done, awaiting review)

- [00-decisions.md](../decisions.md): every confirmed decision.
- [02-ui-spec.md](02-ui-spec.md): screens, behaviour and the visual system.
- [03-architecture.md](03-architecture.md): Go server, probes, indexer, storage, security and operations.
- [04-data-model.md](04-data-model.md): entities, `.drashta/` files, SQLite schema and HTTP API.
- [06-open-questions.md](06-open-questions.md): the questions that still need an answer.

## Phase 1: prototype v3 (artifact)

An artifact cannot open real apps; "Open ↗" shows the command it would run. It can show the whole app, with real scanned data, for a limited set of projects.

1. Start from the design prototype (the Design canvas: Main shell with Project, Tracker, Inbox and the viewports).
2. **Shell changes:**
   - the top-left "drashta" name is the home link;
   - remove the Map rail button and the wallpaper button;
   - the rail is Project · Tracker · Inbox.
3. **Home = seismograph**, rebuilt from the design prototype:
   - JetBrains Mono 400, no italics;
   - lanes, filters, order, range and the day readout;
   - every day-readout item previews in the viewport.
4. **Readout vs browsing mode toggle** (a prototype-only switch that stands in for Plash's class), so the user can judge both.
5. **Data:**
   - re-scan with the design prototype and `sessions.py`;
   - all 17 folders get lanes (a light summary is enough for the home screen);
   - 2–3 projects get full detail (acme, acme, plus one git-heavy project such as widgets).
6. Publish to the prototype artifact, then iterate until the user gives the **green light**, which freezes it.

## Phase 2: spec and plan (docs written, awaiting user review)

Written on 2026-10-01: the spec (`docs/superpowers/specs/2026-10-01-drashta-design.md`), `CONTEXT.md`, and ADRs 0001 to 0007 in `docs/adr/`. The spec adopts the proposed defaults for Q7 to Q14 and lists them (section 4) so you can overrule them. It also lists the conflicts it resolved between these docs (section 5).

1. Merge `02`, `03` and `04` plus the answers to `06` into one spec, `docs/superpowers/specs/2026-10-01-drashta-design.md` (brainstorming skill, architectural path). Add `CONTEXT.md` (the domain glossary) and ADRs for the big choices: git CLI, FSEvents/cgo, SQLite as cache, `.drashta/` format.
2. User approves the spec.
3. Write the implementation plan (writing-plans skill): task-sized steps, TDD, one worktree.
4. `git init` the repo at this point, not before.

## Phase 3: build the foundation (Go)

Order matters: each step works end to end before the next one starts.

1. Module skeleton, config (`~/.drashta/config.toml`), `/healthz`, security guard (copied from acme), launchd plist on **4455**.
2. **Projects probe:** folder discovery and the hide list.
3. **SQLite:** migrations, cache and history tables, and the rebuild path.
4. **git and fossil probes** give commits and daily activity. This is the first milestone: the home API returns real lanes.
5. **Servers probe** (copied from acme): lsof, history, kill/restart with confirmation.
6. **Claude sessions probe:** 5 profiles, `cwd` roll-up, incremental parsing, cross-project mentions.
7. File watching (FSEvents), the hourly sweep, per-day file-change counts.
8. Docs probe, then the `gh` probe (kept minimal).
9. `.drashta/` authored data: read, safe write, and the `drashta task|note|snap` CLI.
10. Search (FTS5), SSE events, native "open" actions.

## Phase 4: build the screens (React Router)

1. Shell: header with the name link, search, refresh, counters, rail, viewport, toasts.
2. **Home seismograph** on the live API, with server push and resync on visibility.
3. Project page and its tabs.
4. Tracker: board, milestones, task create/edit, snapshots, awaiting review, verdicts, pins.
5. Inbox.
6. Viewport bodies: code/doc, diff, image/snapshot, session transcript, server, task.
7. Compare side by side with the frozen prototype. The prototype wins unless the user says otherwise.

## Phase 5: desktop

1. Plash-aware modes (readout vs browsing), `?desktop=1` marker, no in-Plash navigation, no idle animation.
3. One week of daily use. Check the risks: does Plash report "hidden" (R2), does SSE survive sleep (R3), do desktop icons conflict.
4. Decide: keep Plash, switch to the full-screen Safari web app, or (last resort) build a native helper.

## Phase 6: later

- The chart map as a folder browser inside a project (the design prototype).
- Everything listed under "Out of scope for v1" in `02-ui-spec.md` §9.
