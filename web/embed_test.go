package web

import (
	"io/fs"
	"testing"
)

func TestStaticHasPlaceholderOrBuild(t *testing.T) {
	if _, err := fs.Stat(Static, "build/client/.keep"); err != nil {
		if !Built() {
			t.Fatalf("neither .keep nor index.html embedded: %v", err)
		}
	}
	if _, err := fs.Stat(Client(), "."); err != nil {
		t.Fatalf("Client root: %v", err)
	}
}
