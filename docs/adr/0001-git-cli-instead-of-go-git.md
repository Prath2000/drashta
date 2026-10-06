# ADR 0001: Use the git CLI, not go-git

- Status: Proposed. It becomes Accepted when the user approves the spec. (It comes from `docs/spec/03-architecture.md` section 3. It is not in the decisions log.)
- Date: 2026-10-01

## Context

- drashta reads commits, status, branch, remote and diffs for about 20 projects.
- Many repos use worktrees (Claude Code works in `.claude/worktrees/`), packed refs, sparse checkout, LFS pointers and `.gitattributes` diff drivers.
- go-git lags on several of these. It also computes diffs in Go, which is slow on big repos. It adds a large dependency tree to the binary.
- fossil and gh are CLIs anyway. One way to call tools is simpler than two.

## Decision

- Shell out to `git` through `runx.Run`. No shell, 10 s timeout, stdout capped at 8 MB, `LC_ALL=C`.
- Every call is `git -C <path> --no-optional-locks ...`, so drashta never takes the index lock.
- Use fixed `--format` strings with `-z`, `%x1f` and `%x1e` to parse output.
- The probe runs read commands only. It never commits, checks out or pushes.
- A sha is checked against `^[0-9a-f]{7,40}$` before any command runs. A patch is capped at 400 KB.

## Consequences

- Good: the same behaviour as the user's own git, for every repo feature they use.
- Good: `log --numstat` and `show` run at C speed. The binary stays small.
- Good: git, fossil and gh share one calling pattern and one test style.
- Cost: one process spawn per call, about 5 ms. This is fine, because git runs only when something changed.
- Cost: `git` must be on PATH. The launchd plist sets PATH. A missing git gives an `unavailable` result, not a crash.
- Cost: we parse text output. Fixture repos built in tests pin the format.
