package tree_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Prath2000/drashta/internal/probe/docs"
	"github.com/Prath2000/drashta/internal/probe/tree"
)

// Secret files never become documents, so their text never reaches the index.
func TestNewSecretFilesNeverDocs(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"secrets.md":                 "# secret notes\ntoken=FAKE\n",
		"config/secrets.txt":         "FAKE",
		"README.md":                  "# hello\n",
		"gcp/service-account-x.json": "{}",
		"infra/prod.tfvars":          "x = 1",
	}
	for rel, body := range files {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	res, err := tree.Walk(t.Context(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := docs.Extract(res.Entries, root)
	if len(got) != 1 || got[0].Path != "README.md" {
		t.Fatalf("docs = %+v, want only README.md", got)
	}
}
