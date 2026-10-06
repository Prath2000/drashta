// Package claude reads Claude Code session transcripts (.jsonl) for the index
// and for the live transcript view (03-architecture.md section 3, "claude sessions").
//
// Files live at <profile>/projects/<encoded-cwd>/<session-id>.jsonl, and
// subagent files at <encoded-cwd>/<session-id>/subagents/*.jsonl. A subagent
// file is counted into its parent session. The folder name is never decoded:
// the project comes from the "cwd" field (longest project path that is a
// prefix of cwd). Only when no record has a cwd, each project path is encoded
// and matched against the folder name (longest wins; a tie is unassigned).
//
// Only the title and counts leave this package for the index. Every text that
// leaves it (title, transcript, user texts) is redacted with the SECRET regex
// ported from the frozen prototype's session parser.
//
// # Exported API
//
// Discovery and incremental state:
//   - File: one .jsonl on disk {Path, Profile, Size, MTime, Inode, SessionID, IsSubagent, ParentID}.
//   - File.Owner() string: the session the file counts into.
//   - File.ParentPath() string: the session's main .jsonl path.
//   - Discover(profiles []string) ([]File, error): all transcript and subagent files, newest first.
//   - Stat(path, profile string) (File, error): one File, for a file-system event.
//   - Family: one session's files {ID, Profile, ParentPath, Files (main first), MTime}.
//   - Group(files []File) []Family: group Discover's output into sessions, newest first.
//   - State: what the store keeps per file (claude_file table) {Size, MTime, Offset, Inode}.
//   - Mode: Skip | Append | Full.
//   - NeedsParse(prev *State, cur File) Mode: same size+mtime skip; grown append; shrunk or new inode full.
//   - CheckPath(profiles []string, path string) (string, error): path must be a .jsonl inside <profile>/projects (pathguard).
//   - ErrNotTranscript: returned by CheckPath.
//
// Parsing:
//   - NewScanner(projects []probe.Project) *Scanner: resolver + parser. Field Home (for "~/") can be set.
//   - Scanner.Parse(ctx, f File, prev *Session, st *State) (Result, error): parse one file (Skip, Append from
//     st.Offset merged into prev, or Full). Only complete lines are consumed.
//   - Scanner.ParseFamily(ctx, fam Family, prev *Session, states map[string]State) (FamilyResult, error):
//     the indexer's main call. Parses a session and its subagents into one row; rebuilds from 0 when any
//     known file needs Full, so nothing is counted twice.
//   - Scanner.ProjectFor(abs string) string: project ID by longest path prefix, or "".
//   - Scanner.ProjectForFolder(folder string) string: fallback by encoded folder name, or "".
//   - Session: {ID, Profile, JSONLPath, CWD, ProjectID, Start, End, Title, Turns, KB, Tools, Edited, Size,
//     MTime, Mentions, Subagents, LastRole}. ProjectID "" = unassigned. Title "" = no real user text yet.
//     LastRole carries the run state between an Append pass and the next (keep the Session in memory).
//   - Result: {Session, State, Mentions (projectID -> count, other projects), Skipped, Unknown, Mode}.
//   - FamilyResult: {Session, States (per path), Mentions, Skipped, Unknown, Changed, Rebuilt}.
//   - Unknown counts only record types outside knownTypes (record.go). Bookkeeping types of real
//     transcripts (attachment, system, mode, atis-latch, frame-link, ai-title, ...) are known and ignored.
//   - Encode(path string) string: the lossy folder-name encoding (non letters/digits -> "-").
//   - Redact(s string) string: the SECRET regex, matches become "[redacted]". It covers API keys and
//     tokens (sk-, sk_live_, ghp_, glpat-, npm_, xox?-, AIza, JWTs, AKIA), key=value and JSON "key": "v"
//     pairs (password, secret, token, api_key, private_key, AWS_SECRET_ACCESS_KEY), Authorization and
//     Bearer headers, PEM private key blocks, Slack webhooks, and the password in scheme://user:pass@host
//     and curl -u user:pass.
//   - MaxLine, TitleMax, EditedMax: limits (16 MB line, 89 runes + "…", 200 edited paths).
//
// Live transcript:
//   - Transcript(ctx, path string, offset int64, limit int, roles []string) (Page, error): one page of turns
//     from a byte offset (0 or Page.Next). roles: "user", "claude", "tool"; empty = all.
//   - Page: {Turns, Next, Done}. Turn: {I, Role, TS, Text, Tool}. I is unique and increasing (line offset +
//     block index). Tool output is cut to 2 KB; user/claude text to 16 KB.
//   - ErrBadOffset: the offset is not at a line start.
//   - Users(ctx, path string) []string: user texts for the search index (redacted, <= 500 runes, max 50).
//   - ToolTextMax, TurnTextMax, PageDefault, PageMax, UserTextMax, UserTextsMax: limits.
package claude
