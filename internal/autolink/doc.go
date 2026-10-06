// Package autolink derives the "auto" links between tasks, commits and sessions
// (04-data-model.md section 3). Everything here is a pure function: no file, no
// process, no database, no clock. The indexer builds an Input from the store,
// calls Links, and writes the result to task_links with source = "auto".
//
// Exported API
//
//	const Source = "auto"                    the value of Link.Source
//	const ReasonCommitMessage, ReasonBranch, ReasonSessionText, ReasonSessionEdit,
//	      ReasonSnapshot, ReasonSessionOutput  the Link.Reason texts ("commit message", ...)
//	const SessionWindow = 2 * time.Hour      commit window after a session's last turn
//	const KindCloseTask = "close_task"
//
//	type TaskInfo struct     {ID, Title, Status string; Manual, Unlinks []string}
//	type SessionInfo struct  {ID, ProjectID string; Start, End time.Time; Title string;
//	                          Edited, UserTexts, Commands []string}
//	type SnapshotInfo struct {TaskID string; Version int; Commit string}
//	type BranchInfo struct   {Name string; Default bool; Commits []string}
//	type ProjectTasks struct {ProjectID string; Tasks []TaskInfo}   other projects, for ledger:T-12
//	type Input struct        {ProjectID; Tasks; Commits []probe.Commit; Sessions; Branches; Snapshots; Others}
//	type Link struct         {ProjectID, TaskID, Ref, Reason, Source, From string}
//	    func (Link) Cross() bool                       the task belongs to another project than From
//	type Proposal struct     {Kind, ProjectID, TaskID, TaskRef, CommitRef string}
//	type SessionCommit struct{SessionID, CommitID string}
//
//	func Links(in Input) []Link
//	    All auto links of one project, sorted (project, task number, commit before session, ref).
//	func ProposeClose(task TaskInfo, commits []probe.Commit) *Proposal
//	    "Fixes T-12" / "Closes T-12" -> move the task to done? nil when none. (inbox proposals)
//	func Proposals(in Input) []Proposal
//	    ProposeClose for every task of the project, with ProjectID filled, sorted by task number.
//	func SessionCommits(sessions []SessionInfo, commits []probe.Commit) []SessionCommit
//	    Which commit a session probably produced (the "session output" rule), on its own.
//	func BranchTaskIDs(name string) []string
//	    Task ids named in a branch name. The indexer runs `git log <default>..<branch>`
//	    only for branches where this is not empty.
//
// Rules (what Links does)
//
//   - commit message: `\b[Tt]-\d+\b` in the subject or body of a commit of Input.Commits,
//     for tasks that exist. Trailers (`Task: T-12`, `Refs: T-12, T-14`) are found by the
//     same scan. T-123 is not T-12; ST-12 and T-12abc are not mentions.
//     Reason "commit message". Ref commit:<full id>.
//   - branch name: for every Input.Branches entry that is not Default and whose name
//     holds a task id (t-12-x, T-12/x, feat/T-12-x, fix_t-12), each id in its Commits links.
//     Reason "branch name".
//   - snapshot: SnapshotInfo.Commit links to the snapshot's task. Reason "snapshot".
//   - session text: an upper case `T-12`, or `task:T-12` in any case, in the session title or
//     in one of UserTexts. Reason "session text". Ref session:<id>.
//   - session edit: the session's Edited list holds `.drashta/tasks/T-12.md` or a file under
//     `.drashta/snapshots/T-12/`, or one of its Commands runs `drashta` with T-12 in it.
//     Reason "session edit".
//   - session output: a commit that is inside [session start, session end + 2 h] and has a
//     file the session edited links to every task the session is linked to (by the rules
//     above, or by hand). Reason "session output". Needs Commit.Files.
//   - cross-project: `ledger:T-12` (or `ledger:task:T-12`) in a commit message or a session
//     text links ledger's task T-12 when ledger is listed in Others. The link has
//     ProjectID "ledger", From = this project, and Ref "<this project>:commit:<id>".
//   - When several rules give the same (task, ref), one link is kept. The reason that wins:
//     commit message and session edit, then snapshot and session text, then branch name,
//     then session output.
//   - A link is dropped when the task already has the ref in Manual (a short commit or
//     session id matches the full one) or in Unlinks. Output never repeats a manual link.
//
// Notes for the indexer (writing the result)
//
//   - Fill Input from the store: Tasks from the authored task files (Manual = links:,
//     Unlinks = unlinks:), Commits with Files (commit_files), Sessions from the sessions
//     table (set UserTexts with claude.Users(ctx, jsonl_path) when you have it; without it
//     only the title is searched), Branches from git, Snapshots from the snapshots table.
//     Pass only sessions whose own project is this project.
//   - Own links: for each link with ProjectID == Input.ProjectID, group by TaskID and call
//     store.UpsertAuthoredTaskLinks(ctx, project, taskID, "auto", ...) with ref and reason.
//     That call replaces all auto rows of the task, so also pass along the auto rows whose
//     ref starts with another project's name and a colon (those come from the other
//     project's own run), or write them with a query that deletes only unqualified refs.
//   - Cross links (Link.Cross()): they belong to the other project's task. When project A
//     is refreshed, delete the auto rows with ref LIKE 'A:%' in other projects' tasks and
//     insert A's cross links. Then a refresh of A never loses links written by B.
//   - Tasks that stop existing, or links the user rejected, vanish by themselves: Links is
//     recomputed from scratch each time.
//   - Run it after commits, sessions or .drashta/ files of the project changed. It takes
//     about 0.4 s for 3000 sessions with 50 turns each; for a normal project it is a few ms.
package autolink
