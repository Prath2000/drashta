// Package tree is the file tree probe. It lists the files of one project and
// reads single files live for the viewport.
//
// Rules (03-architecture.md sections 3 and 8):
//   - Folders on the skip list are shown as Collapsed entries and never opened.
//     Default list: node_modules .git dist build target venv .venv __pycache__
//     .next .cache .DS_Store .fslckout. Names from config.skip are added.
//     An extra entry with a "/" (for example "docs/generated") matches that
//     relative path; one without matches a folder name at any depth.
//   - Symlinks are never followed in a walk.
//   - Every read goes through pathguard.Resolve. A path or link that leaves the
//     project root is an error (errors.Is(err, pathguard.ErrOutside)).
//   - Secret files (.env*, *.pem, *.key, id_*, *.p12) are listed, but their body
//     is never read (ReadFile gives Redacted:true) and never served (RawPath
//     gives ErrSecret).
//
// Exported API:
//
//	type Entry struct                        one file or folder
//	    Path string                          relative to the root, "/" separators
//	    Dir bool; Size int64; MTime time.Time
//	    Kind string                          files: "code" | "doc" | "image" | "other"; folders: ""
//	    Collapsed bool                       shown but not walked (skip list, or a link to a folder)
//	    HasChildren bool                     set by ListDir only
//
//	type Result struct                       what Walk returns
//	    Entries []Entry                      never nil; parents come before their children
//	    Truncated bool                       the 50,000 entry cap or the depth cap (12) cut the walk
//	    Denied []string                      folders that could not be read (relative paths)
//
//	func Walk(ctx context.Context, root string, extraSkip []string) (Result, error)
//	                                         whole project listing; error if root is missing or ctx ends
//	func ListDir(root, dir string, depth int, extraSkip ...string) ([]Entry, error)
//	                                         lazy tree: children of dir down to depth levels
//	                                         (folders first, then files, A-Z); guarded; ErrNotDir for a file
//
//	type FileText struct                     live read of one file
//	    Path, Size, MTime, Kind, IsDoc, Lines, From, To, Text,
//	    Truncated, Binary, Redacted
//	func ReadFile(root, rel string, from, to int) (FileText, error)
//	                                         1-based line window; default first 2000 lines; at most 1 MB
//	                                         read; NUL in the first 8 KB means Binary (no text);
//	                                         secret files give Redacted (no text, no error)
//	func RawPath(root, rel string) (abs string, mime string, err error)
//	                                         guarded path and a safe content type for the raw endpoint
//
//	func Classify(name string) string        Kind from a file name
//	func IsSecret(name string) bool          true for .env*, *.pem, *.key, id_*, *.p12 and the wider
//	                                         list in read.go (.npmrc, .netrc, .git/config, *.tfvars, ...; any case)
//	func DefaultSkipNames() []string         copy of the built-in skip list
//
//	const KindCode, KindDoc, KindImage, KindOther
//	var ErrIsDir, ErrNotRegular, ErrSecret, ErrNotDir
//
// Notes for the HTTP layer:
//   - FileText.Lines counts lines in the part that was read. For a file over
//     1 MB that is only the first 1 MB, and Truncated is true.
//   - FileText.Truncated is true when more text exists after To, or the 1 MB
//     cap cut the file. It is not set because of lines before From.
//   - RawPath returns text/plain for html, xml and js on purpose. Still send
//     "X-Content-Type-Options: nosniff" and "Content-Security-Policy: sandbox"
//     with raw bytes, because svg keeps image/svg+xml.
//   - The caps are package variables (maxEntries, maxDepth, maxFileBytes) so
//     tests can make them small.
package tree
