package tree

import (
	"errors"
	"testing"
)

// newSecretNames are the files added after the security review (M-6).
var newSecretNames = []string{
	".npmrc", ".netrc", "_netrc", ".pgpass", ".git-credentials", ".dev.vars",
	"certs/site.pfx", "android/release.jks", "android/app.keystore", "putty/me.ppk",
	"credentials", "gcp/credentials.json", "infra/prod.tfvars", "infra/terraform.tfstate",
	"infra/terraform.tfstate.backup", "config/secrets.yml", "secrets.json",
	"gcp/service-account-prod.json", "service-account.json", ".aws/credentials",
	".docker/config.json", ".git/config", "sub/.GIT/CONFIG",
}

func TestIsSecretWiderList(t *testing.T) {
	for _, n := range newSecretNames {
		if !IsSecret(n) {
			t.Errorf("IsSecret(%q) = false", n)
		}
	}
	if !IsSecret("/Users/fake/projects/alpha/.git/config") {
		t.Error("absolute .git/config must be secret")
	}
	for _, n := range []string{"config", "src/config.go", "docs/credentials-howto.md", "app/config.json",
		"README.md", "service.json", "secretary.txt", ".gitignore", "git/config", "keystore.md", "main.tf"} {
		if IsSecret(n) {
			t.Errorf("IsSecret(%q) = true, want false", n)
		}
	}
}

// The new secret files are redacted on read, refused raw and still listed in the tree.
func TestNewSecretFilesRedacted(t *testing.T) {
	root := t.TempDir()
	for _, n := range newSecretNames {
		mk(t, root, n, "token=FAKEfake0000\n")
	}
	for _, n := range newSecretNames {
		ft, err := ReadFile(root, n, 0, 0)
		if err != nil {
			t.Errorf("%s: %v", n, err)
			continue
		}
		if !ft.Redacted || ft.Text != "" {
			t.Errorf("%s not redacted: %+v", n, ft)
		}
		if _, _, err := RawPath(root, n); !errors.Is(err, ErrSecret) {
			t.Errorf("%s raw: want ErrSecret, got %v", n, err)
		}
	}
	res, err := Walk(t.Context(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{".npmrc", "infra/prod.tfvars", ".aws/credentials", "gcp/service-account-prod.json"} {
		if _, ok := find(res.Entries, n); !ok {
			t.Errorf("%s must still be listed in the tree", n)
		}
	}
}
