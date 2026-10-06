// See doc.go for the package overview and the exported API.

package web

import (
	"embed"
	"io/fs"
)

//go:embed all:build/client
var Static embed.FS

// Client returns the built files rooted at build/client.
func Client() fs.FS {
	sub, err := fs.Sub(Static, "build/client")
	if err != nil {
		// fs.Sub only fails on an invalid path name; "build/client" is valid.
		panic(err)
	}
	return sub
}

// Built reports whether the frontend was built (index.html is present).
func Built() bool {
	_, err := fs.Stat(Static, "build/client/index.html")
	return err == nil
}
