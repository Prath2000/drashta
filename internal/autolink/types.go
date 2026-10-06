package autolink

import (
	"time"

	"github.com/Prath2000/drashta/internal/probe"
)

// Source is the value of Link.Source. Auto links are always "auto".
const Source = "auto"

// Reasons. The UI shows "auto: <reason>" on the link badge (04 section 3).
const (
	ReasonCommitMessage = "commit message" // message or trailer names the task
	ReasonBranch        = "branch name"    // the commit is on a branch named after the task
	ReasonSessionText   = "session text"   // a user turn mentions the task
	ReasonSessionEdit   = "session edit"   // the session wrote the task file or ran the drashta CLI on it
	ReasonSnapshot      = "snapshot"       // a snapshot of the task names the commit
	ReasonSessionOutput = "session output" // a linked session edited files in this commit
)

// SessionWindow is how long after the last turn of a session a commit still counts as its output.
const SessionWindow = 2 * time.Hour

// Proposal kinds.
const KindCloseTask = "close_task"

// TaskInfo is one task of a project, as the indexer has it.
type TaskInfo struct {
	ID      string // "T-12"
	Title   string
	Status  string   // backlog | todo | in_progress | review | done
	Manual  []string // refs from the task file's links: list
	Unlinks []string // refs from the task file's unlinks: list (auto links the user rejected)
}

// SessionInfo is one Claude session of the project (the indexer builds it from claude.Session).
type SessionInfo struct {
	ID        string
	ProjectID string    // the session's own project; sessions of another project are skipped
	Start     time.Time // first turn
	End       time.Time // last turn
	Title     string
	Edited    []string // project-relative paths the session wrote (claude.Session.Edited)

	// UserTexts are the user's turns (claude.Users). Optional: with none, only the
	// Title is searched for task mentions.
	UserTexts []string
	// Commands are shell commands the session ran. Optional. The claude probe does
	// not export them today, so this rule only fires when the indexer supplies them.
	Commands []string
}

// SnapshotInfo is the part of a snapshot that matters here.
type SnapshotInfo struct {
	TaskID  string
	Version int
	Commit  string // the commit: key of the snapshot, short or full; "" when none
}

// BranchInfo is a branch and the commits that are on it but not on the default branch.
// The indexer gets Commits from `git log <default>..<branch>`.
type BranchInfo struct {
	Name    string
	Default bool     // the default branch never links anything
	Commits []string // commit ids (full or short)
}

// ProjectTasks lists the tasks of ANOTHER project, so `ledger:T-12` can be checked
// and linked (04 section 3, "Cross-project").
type ProjectTasks struct {
	ProjectID string
	Tasks     []TaskInfo
}

// Input is everything Links needs for one project. It is plain data: no file or
// process is touched.
type Input struct {
	ProjectID string
	Tasks     []TaskInfo
	Commits   []probe.Commit // newest 200 is fine; set Files for the session-output rule
	Sessions  []SessionInfo
	Branches  []BranchInfo
	Snapshots []SnapshotInfo
	Others    []ProjectTasks
}

// Link is one derived task link. Store it in task_links with source = Source.
type Link struct {
	ProjectID string `json:"projectId"` // the project that owns the task
	TaskID    string `json:"taskId"`
	Ref       string `json:"ref"` // canonical; written as <From>:commit:<id> when From differs from ProjectID
	Reason    string `json:"reason"`
	Source    string `json:"source"` // always "auto"
	From      string `json:"from"`   // the project whose commit or session this is (Input.ProjectID)
}

// Cross reports whether the link goes from one project to another project's task
// (04 section 3, "Cross-project"). Its Ref starts with From and a colon.
func (l Link) Cross() bool { return l.ProjectID != l.From }

// Proposal asks the user to move a task to done (inbox "proposals", 04 section 6).
type Proposal struct {
	Kind      string `json:"kind"` // KindCloseTask
	ProjectID string `json:"projectId,omitempty"`
	TaskID    string `json:"taskId"`
	TaskRef   string `json:"taskRef"`   // "task:T-12"
	CommitRef string `json:"commitRef"` // "commit:<full id>"
}

// SessionCommit says that a session probably produced a commit.
type SessionCommit struct {
	SessionID string `json:"sessionId"`
	CommitID  string `json:"commitId"`
}
