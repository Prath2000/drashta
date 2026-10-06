package claude

import (
	"strings"
	"testing"
)

// TestRedactFormats has one invented example per secret format from the
// security review (I-2). Each value must not survive Redact.
func TestRedactFormats(t *testing.T) {
	cases := []struct{ name, in, secret string }{
		{"stripe live", "key sk_live_FAKEfake0000FAKE1111 end", "sk_live_FAKEfake0000FAKE1111"},
		{"stripe test", "STRIPE=sk_test_FAKEfake0000FAKE2222", "sk_test_FAKEfake0000FAKE2222"},
		{"stripe restricted", "use rk_live_FAKEfake0000FAKE3333 now", "rk_live_FAKEfake0000FAKE3333"},
		{"stripe env", "STRIPE_SECRET_KEY=sk_live_FAKEfake0000FAKE4444", "FAKEfake0000FAKE4444"},
		{"aws env", "export AWS_SECRET_ACCESS_KEY=FakeFakeFake/FakeFakeFake+Fake0000", "FakeFakeFake/FakeFakeFake+Fake0000"},
		{"aws lower", "aws_secret_access_key = FakeAwsKey0000FakeAwsKey", "FakeAwsKey0000FakeAwsKey"},
		{"json password", `{"user": "bob", "password": "hunter2xyz"}`, "hunter2xyz"},
		{"json secret", `{"secret":"fakesecretvalue1"}`, "fakesecretvalue1"},
		{"json token", `{"token": "faketokenvalue12"}`, "faketokenvalue12"},
		{"json api_key", `{"api_key": "fakeapikeyvalue1"}`, "fakeapikeyvalue1"},
		{"json apikey", `{"apikey": "fakeapikeyvalue2"}`, "fakeapikeyvalue2"},
		{"json passwd", `{"passwd": "fakepasswdvalue"}`, "fakepasswdvalue"},
		{"json private_key", `{"private_key": "fakeprivatekeyvalue"}`, "fakeprivatekeyvalue"},
		{"auth header", "Authorization: Bearer abc.def", "abc.def"},
		{"auth header json", `"Authorization": "Basic ZmFrZTpmYWtl"`, "ZmFrZTpmYWtl"},
		{"bare bearer", "send bearer FAKEtoken0000FAKEtoken0000 now", "FAKEtoken0000FAKEtoken0000"},
		{"url creds", "postgres://admin:S3cretFakePass@db.example:5432/app", "S3cretFakePass"},
		{"https creds", "git clone https://bob:ghfaketoken99@example.com/x.git", "ghfaketoken99"},
		{"gitlab", "glpat-FAKEfakeFAKEfake0000", "glpat-FAKEfakeFAKEfake0000"},
		{"npm", "npm_FAKEfakeFAKEfakeFAKEfakeFAKEfake0000", "npm_FAKEfakeFAKEfakeFAKEfakeFAKEfake0000"},
		{"slack bot", "xoxb-0000000000-FAKEfakeFAKE", "xoxb-0000000000-FAKEfakeFAKE"},
		{"slack user", "xoxp-1111111111-FAKEfakeFAKE", "xoxp-1111111111-FAKEfakeFAKE"},
		{"slack webhook", "POST https://hooks.slack.com/services/T000FAKE/B000FAKE/FAKEfakeFAKEfake", "T000FAKE/B000FAKE/FAKEfakeFAKEfake"},
		{"curl -u", "curl -u admin:S3cretFakePass https://example.com", "S3cretFakePass"},
		{"curl --user", "curl --user admin:S3cretFakePass2 https://example.com", "S3cretFakePass2"},
		{"pem block", "-----BEGIN OPENSSH PRIVATE KEY-----\nFAKEbase64FAKEbase64\nMOREfakeBASE64==\n-----END OPENSSH PRIVATE KEY-----\nafter", "FAKEbase64FAKEbase64"},
		{"pem header", "-----BEGIN RSA PRIVATE KEY-----", "BEGIN RSA PRIVATE KEY"},
		{"google", "AIzaFAKEfakeFAKEfakeFAKEfakeFAKEfake123", "AIzaFAKEfakeFAKEfakeFAKEfakeFAKEfake123"},
		// Values are invented and assembled from pieces so no scanner sees a whole token in the source.
		{"google oauth", "token " + "ya29" + ".FAKEfakeFAKEfakeFAKEfake0000", "ya29" + ".FAKEfakeFAKEfakeFAKEfake0000"},
		{"stripe webhook", "whs" + "ec_FAKEfakeFAKEfake0000", "whs" + "ec_FAKEfakeFAKEfake0000"},
		{"slack app", "xa" + "pp-0-FAKEfakeFAKEfake", "xa" + "pp-0-FAKEfakeFAKEfake"},
		{"digitalocean", "d" + "op_v1_" + "FAKEfakeFAKEfake00000000", "d" + "op_v1_" + "FAKEfakeFAKEfake00000000"},
		{"twilio sid", "sid S" + "K" + strings.Repeat("0123abcd", 4) + " end", "S" + "K" + strings.Repeat("0123abcd", 4)},
		{"sendgrid", "S" + "G." + "FAKEfakeFAKEfake00" + "." + "FAKEfakeFAKEfake11", "S" + "G." + "FAKEfakeFAKEfake00" + "." + "FAKEfakeFAKEfake11"},
		{"jwt", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJmYWtlIn0.FAKEsignatureFAKE", "eyJhbGciOiJIUzI1NiJ9"},
		{"github", "GITHUB_TOKEN=ghp_FAKEFAKEFAKEFAKEFAKE1234", "ghp_FAKEFAKEFAKEFAKEFAKE1234"},
	}
	for _, c := range cases {
		got := Redact(c.in)
		if strings.Contains(got, c.secret) || !strings.Contains(got, "[redacted]") {
			t.Errorf("%s: Redact(%q) = %q, secret %q survived", c.name, c.in, got, c.secret)
		}
	}
}

// TestRedactKeepsContext checks that only the password part of a URL or a
// curl -u argument is replaced, and the text after a PEM block stays.
func TestRedactKeepsContext(t *testing.T) {
	cases := map[string]string{
		"postgres://admin:S3cretFakePass@db.example:5432/app":                       "postgres://admin:[redacted]@db.example:5432/app",
		"curl -u admin:S3cretFakePass https://example.com":                          "curl -u admin:[redacted] https://example.com",
		"-----BEGIN EC PRIVATE KEY-----\nFAKE\n-----END EC PRIVATE KEY-----\nafter": "[redacted]\nafter",
	}
	for in, want := range cases {
		if got := Redact(in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestRedactNegatives proves ordinary text, paths, git shas, UUIDs and URLs
// without credentials are left alone.
func TestRedactNegatives(t *testing.T) {
	keep := []string{
		"please fix the task-management-system page",
		"the risk-assessment-checklist is done",
		"/Users/fake/projects/secret-garden/notes.md",
		"open ~/projects/alpha/src/password-reset.tsx",
		"commit 3f9a2c1d4e5b6a7980c1d2e3f4a5b6c7d8e9f0a1",
		"session 4b1d2c3e-5f60-4a7b-8c9d-0e1f2a3b4c5d",
		"see https://github.com/fake/repo/pull/12 and http://localhost:8080/x",
		"max_tokens 100 and the tokens count",
		"I forgot my password again",
		"run npm install then npm_config is set",
		"bearer of bad news",
		"user@example.com wrote it",
		"ssh-keygen -t ed25519 -u fixes nothing",
	}
	for _, s := range keep {
		if got := Redact(s); got != s {
			t.Errorf("Redact(%q) = %q, want it unchanged", s, got)
		}
	}
}

// BenchmarkRedact runs Redact over 1 MB of ordinary transcript text.
func BenchmarkRedact(b *testing.B) {
	text := strings.Repeat("please change the parser in /fx/projects/alpha/src/x.go so it handles more cases; commit 3f9a2c1d ok\n", 10000)
	b.SetBytes(int64(len(text)))
	for i := 0; i < b.N; i++ {
		Redact(text)
	}
}
