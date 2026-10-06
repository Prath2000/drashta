// Package resolve turns refs (docs/spec/04-data-model.md section 2.4) into previews, titles and
// native-action targets. It reads the index (internal/store) for observed items and the
// .drashta files (internal/authored) for tracker items, which are the source of truth.
// It also builds the JSON views of tracker items that the HTTP API sends.
//
// Refs: "[<project>:]<type>:<id>[#frag]". The web type "doc" is read as "file". A local ref
// needs a project: the caller passes a default, or the ref is a session or server ref (their
// project comes from the index). Every path goes through pathguard; ".." is refused.
//
// # Exported API
//
// Resolver:
//
//	New(Options{DB, Actions, Home}) *Resolver          DB is required; Actions = editor, terminal, roots
//	(*Resolver).Resolve(ctx, ref) (Resolved, error)     GET /api/resolve reply: ref, type, projectId, title, subtitle,
//	                                                    icon, date, obj, nativeLabel, nativeActions[{action,label,command}],
//	                                                    notes[], tasks[], light (always false)
//	(*Resolver).Title(ctx, ref) (title, subtitle)       for the inbox; the ref itself as title when not found
//	(*Resolver).Summary(ctx, ref, defaultProject) (Summary, error)   {title, subtitle, date} (task links)
//	(*Resolver).Target(ctx, ref) (actions.Target, error) for POST /api/open: project root + RELATIVE path, line,
//	                                                    sha, vcs, session id, profile folder, port, project remote (URL)
//	(*Resolver).Qualify(ctx, ref, defaultProject) (authored.Ref, error)  parse + fill the project
//	(*Resolver).NotesFor(ctx, ref) / TasksFor(ctx, ref)  notes on an item; tasks linking to it (manual + auto,
//	                                                    minus unlinks)
//	(*Resolver).Board(ctx, project) (*Board, error)     one project's tracker (files) + auto links (index)
//	(*Resolver).Project / Projects / Root               project rows and folders from the index
//	(*Resolver).Actions() actions.Config                current config (copy)
//	(*Resolver).SetEditor(argv)                         swap the editor template (settings editorCmd)
//
// Errors: ErrNotFound (404), ErrInvalid (400, bad ref), ErrForbidden (400, path outside the project).
//
// Views (JSON shapes of web/app/lib/types.ts): Task, SnapRef, Feature, Milestone, Snapshot, Note, LogEntry, Link.
//
//	NewBoard(project, root, authored.Tracker, autoLinks) *Board
//	(*Board).Task/Feature/Milestone/Snapshot/Note(id)   find an item
//	(*Board).TaskView/FeatureView/MilestoneView/SnapshotView/NoteView   build the views
//	(*Board).SnapshotsOf(task), Latest(task), Links(task), HasAuto(task, ref), SameLink(a, b), NotesOn(ref), FullRef(s)
//	LogView(task) []LogEntry; RawURL(project, rel) string
//
// Refs: ParseRef(s) (accepts "doc"), SameItem(a, b) (fragments ignored; commit/session ids match by prefix).
//
// Snapshot drop folder (.drashta/snapshots/inbox):
//
//	UnfiledSnapshots(root) []Drop                       images waiting to be filed, newest first ({name, path, size, mtime})
//	CheckDropName(name) error                           one plain image file name (ErrBadDropName, authored.ErrBadExt)
//	DropPath(root, name) (string, error)                absolute path of a drop, through pathguard
//	InboxDir                                            ".drashta/snapshots/inbox"
package resolve
