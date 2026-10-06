// Package drawing reads DWG drawings through the external program chitralipi.
//
// chitralipi (GPL-3) is only ever run as a subprocess through internal/runx (argv, no shell),
// never linked. Its path is config.Chitralipi, set only in config.toml.
//
// Exported API:
//
//	var ErrUnavailable                                   chitralipi is not installed (or not executable)
//	var ErrBadDrawing                                    chitralipi ran but could not read the file
//	const MaxEntitiesBytes = 128 << 20                   stdout cap of `entities` (a bigger drawing is ErrBadDrawing)
//	const Timeout = 60 * time.Second                     one chitralipi run
//	func New(bin, cacheDir string) *Reader               bin is config.Chitralipi; cacheDir is <data dir>/cache/dwg
//	func (r *Reader) Layers(ctx, abs) ([]byte, error)    stdout of `chitralipi layers <abs> --as json` (cached)
//	func (r *Reader) Entities(ctx, abs) (string, error)  path of a cached JSONL file of the model-space entities
//	Reader.Run                                           the runner; default runx.Run, tests replace it
//
// Rules:
//   - Entities runs `chitralipi entities <abs> --space model --as jsonl`. Model space only: all
//     spaces of a large drawing is 44 MB of JSONL, the model space alone about 3 MB.
//   - The cache file is <cacheDir>/<sha256(abs|size|mtime-ns|binSize|binMtime-ns)>.jsonl for the
//     entities and the same key with .layers.json for the layers, written through a temp file and
//     a rename (file 0600, folder 0700). A changed drawing or an upgraded chitralipi gives a new
//     key. A 0-byte cache file counts as missing. At most 200 files (both kinds together) stay;
//     the oldest by mtime go first, and so do .tmp-* files older than 1 hour (crashed writes).
//   - Concurrent calls for one key and kind run chitralipi once; the others wait and read the
//     cache. Layers and entities of one drawing may run at the same time.
//   - A run does not end when its caller leaves (the context is detached with
//     context.WithoutCancel); it ends after Timeout and fills the cache for the next caller. A
//     caller that is already gone before the run starts does not start one.
//   - A missing program (or runx.ErrNotFound) is ErrUnavailable and the message names the path.
//     A non-zero exit, a timeout or a capped stdout is ErrBadDrawing; for an exit code the message
//     holds the first stderr line (200 characters at most). A cancelled caller gets ctx.Err().
//
// httpapi.ProjectDrawings is the small interface the HTTP routes use; *Reader implements it.
package drawing
