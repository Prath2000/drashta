// Package web holds the built frontend (React Router v7 SPA) for the drashta binary.
//
// Exported API:
//   - Static   embed.FS with every file under build/client (paths start with "build/client/").
//   - Client() the same files as an fs.FS rooted at build/client (index.html at the root).
//   - Built()  true when build/client/index.html exists; false when only the placeholder is there.
//
// How `go build` works before the frontend is built:
// the directive is `//go:embed all:build/client`, and the folder always holds
// the placeholder file build/client/.keep (kept in git, and written again by
// scripts/keep.mjs after every `npm run build`, because the build empties the
// folder). So the pattern always matches at least one file. Built() then
// returns false and the HTTP layer answers "/" with a short "run make web" hint.
//
// index.html carries <meta name="drashta-token" content="__DRASHTA_TOKEN__">.
// The server replaces that tag with the real CSRF token when it serves the page.
//
// The frontend source is in app/ (see docs/architecture.md for the folder map and
// the npm commands).
package web
