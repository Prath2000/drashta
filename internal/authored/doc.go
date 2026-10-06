// Package authored reads and writes the per-project tracker data in <project>/.drashta/.
//
// Files are the source of truth (decision S9). Each item is one markdown file with YAML front
// matter. The layout, ids, front matter and safe-write rules are in docs/spec/04-data-model.md
// section 2. Everywhere below, root means the PROJECT folder (the one that contains
// .drashta), not the .drashta folder itself.
//
// # Safe writes
//
// Save patches only the keys the type knows. Unknown keys keep their order and comments,
// and unchanged body or log text stays byte for byte. The write goes through a temp file
// in the same folder, fsync and rename. Save compares the sha256 of the file on disk with
// ifRev first, and returns ErrConflict when they differ. Save, Update*, Delete and
// AddSnapshotImage hold an flock on .drashta/.lock, so the CLI, the server and goroutines
// do not mix. Every path goes through internal/pathguard.
//
// # Types
//
//	Milestone, Feature, Task, Snapshot, Note   one struct per item, with Body and Rev
//	LogEntry                                   one line of a task's "## Log" section
//	Tracker                                    everything LoadProject found
//	FileError                                  a problem (or warning) about one file
//	Ref                                        a parsed pointer such as ledger:commit:bdcb99c#L3
//
// # Reading
//
//	LoadProject(root) (Tracker, []error)       read all items; a bad file never stops the others.
//	                                           Each item has Rev = sha256 hex of its file bytes.
//	LoadItem(root, kind, id) (any, error)      one item as a pointer (*Task, ...)
//	LoadMilestone / LoadFeature / LoadTask / LoadSnapshot / LoadNote (root, id)
//	Classify(root, path) (kind, id, ok)        which item a changed path belongs to (text check only)
//
// # Writing
//
//	Save(root, item, ifRev) (newRev, error)    ifRev=="" creates (ErrExists if the file is there);
//	                                           otherwise ifRev must match the file (ErrConflict).
//	UpdateMilestone / UpdateFeature / UpdateTask / UpdateSnapshot / UpdateNote
//	                                           (root, id, fn) load + change + save under the lock
//	Validate(item) error                       check an item as Save would, without writing; call BEFORE NextID
//	NextID(root, kind) (id, error)             reserve T-n / F-n / M-n / N-n with O_EXCL; never reused
//	Release(root, kind, id) error              give back a reserved id whose file is still empty (Save failed)
//	Delete(root, kind, id) error               move to .drashta/.trash/
//	DeleteMoved(root, kind, id) ([]string, error)  like Delete, also moves snapshots/T-n and returns what moved
//	AddSnapshotImage(root, taskID, r, ext) (version, relPath, error)
//	                                           store snapshots/<task>/v<N>.<ext>; png jpg jpeg webp gif; 20 MB
//	EnsureLayout(root) / EnsureGitignore(root) make the folders, README.md, .lock and .gitignore
//	                                           (existing files are never changed)
//	(*Task).AppendLog(logTime, by, text)       add a log line
//	(*Snapshot).ImageRel() string              image path relative to the project root
//
// # Refs
//
//	ParseRef(s) (Ref, error), Ref.String(), Ref.Canonical(), Ref.Lines(), Ref.Point(), Ref.Local(),
//	Ref.IsAuthored(). Ref also reads and writes JSON in both forms (string or object).
//
// # Helpers
//
//	FormatTime(t), FormatLogTime(t), ParseTime(s), SnapshotID(task, version)
//	Statuses, Status*, Verdict*, State*, Kind*, Ref* constants; MaxImageBytes; ImageExts; ReadmeText
//	Errors: ErrConflict ErrExists ErrNotFound ErrInvalid ErrTooLarge ErrBadExt ErrLockTimeout
//
// Do not call another authored function that takes the lock from inside an Update* callback.
package authored
