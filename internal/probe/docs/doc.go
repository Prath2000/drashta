// Package docs is the documents probe. It picks the readable documents out of
// a tree listing and reads a title and a text head for the search index
// (03-architecture.md section 3, docs). It reuses the tree result, so it does
// not walk the disk itself.
//
// A document is a file named *.md, *.markdown or *.txt at any depth, or README*
// (any extension), or CLAUDE.md, or AGENTS.md. Files inside skip folders are
// not documents. Secret files (.env*, *.pem, *.key, id_*, *.p12) are never
// documents and are never read.
//
// Exported API:
//
//	type Doc struct
//	    Path  string      relative to the project root, "/" separators
//	    Title string      first "# " heading (code blocks ignored), else the file name
//	    MTime time.Time   copied from the tree entry
//	    Lines int         line count; 0 when the file was not read
//	    Head  string      first 4 KB of text for FTS; "" when the file was not read
//
//	func Extract(entries []tree.Entry, root string) []Doc
//	    entries is tree.Walk's Entries, root is the project folder. Keeps the
//	    order of entries. Never returns nil. Reads go through tree.ReadFile, so
//	    links that leave the project are dropped. A file over 1 MB, or a binary
//	    one, is listed with Title = file name, Lines 0 and an empty Head.
//
//	func IsDocPath(rel string) bool
//	    The name rule alone. Use it to decide whether a changed file should
//	    trigger a docs update.
//
// Cost: Extract reads every document (up to 1 MB each) on each call. The
// indexer should call it only when a doc file changed, and may skip files whose
// path and mtime are unchanged since the last run.
package docs
