package claude

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// secretRE is the SECRET regex from the frozen prototype's session parser, widened
// after the security review (I-2). Every match becomes "[redacted]".
// Go's regexp (RE2) runs in linear time, so no input can make it slow.
// Token prefixes start at a word boundary (\b) so words like "task-list-…"
// are not cut in the middle.
var secretRE = regexp.MustCompile(
	`\b(?:sk-[A-Za-z0-9_-]{10,}|[sr]k_(?:live|test)_[A-Za-z0-9]{10,}|ghp_[A-Za-z0-9]{20,}|AKIA[0-9A-Z]{16}` +
		`|gh[ousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|xox[abeprs]-[A-Za-z0-9-]{10,}|AIza[0-9A-Za-z_-]{30,}` +
		`|glpat-[A-Za-z0-9_-]{20,}|npm_[A-Za-z0-9]{36,}` +
		// Google OAuth access token, Stripe webhook secret, Slack app-level token,
		// DigitalOcean token, Twilio API key SID, SendGrid key
		`|ya29\.[A-Za-z0-9_-]{20,}|whsec_[A-Za-z0-9]{16,}|xapp-[A-Za-z0-9-]{10,}|dop_v1_[A-Za-z0-9]{20,}` +
		`|SK[0-9a-fA-F]{32}|SG\.[A-Za-z0-9_-]{16,}\.[A-Za-z0-9_-]{16,}` +
		`|eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,})` +
		`|hooks\.slack\.com/services/[A-Za-z0-9/_-]+` +
		// a whole private key block; only the header line when the END line is missing
		`|-----BEGIN [A-Z ]*PRIVATE KEY-----(?:[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----)?` +
		// "Authorization: Bearer x", "Authorization": "Basic x"
		`|(?i:authorization["']?\s*[:=]\s*["']?(?:bearer\s+|basic\s+|token\s+)?[^\s"']+)` +
		// a bare "Bearer <token>" of 20 or more characters
		`|(?i:\bbearer\s+[A-Za-z0-9._~+/=-]{20,})` +
		// key = value, key: value and JSON "key": "value" (the quote between the
		// key and the colon is allowed). The key may carry a prefix and a suffix
		// joined with _ or - (AWS_SECRET_ACCESS_KEY, client_secret, api-token).
		`|(?i:[A-Za-z0-9_-]*(?:api[_-]?key|apikey|passw(?:or)?d|secret|token|access[_-]?key|private[_-]?key)` +
		`(?:[_-][A-Za-z0-9_-]*)?["']?\s*[:=]\s*["']?[^\s"',}]+["']?)`)

// credRE finds a password inside a URL (scheme://user:pass@host) and the
// user:pass argument of curl -u / --user. Only the password part is replaced.
var credRE = regexp.MustCompile(
	`(\b[A-Za-z][A-Za-z0-9+.-]*://[^/\s:@]+:)[^@\s/]+(@)` +
		`|((?:^|\s)(?:-u|--user)(?:\s+|=)["']?[^\s:"']+:)[^\s"']+`)

// secretHints are lower-case pieces that every match of secretRE or credRE
// contains. Go's regexp scans plain text at only a few MB/s, so Redact first
// checks for these with fast substring search and skips the regexps when none
// is present (the common case).
var secretHints = []string{
	"sk-", "k_live_", "k_test_", "ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_", "akia",
	"xox", "aiza", "glpat-", "npm_", "eyj", "hooks.slack.com", "-----begin", "authorization", "bearer",
	"apik", "api_k", "api-k", "passw", "secret", "token", "access_k", "access-k", "accessk",
	"private_k", "private-k", "privatek", "://", "-u",
	"ya29.", "whsec_", "xapp-", "dop_v1_",
}

func mayHoldSecret(s string) bool {
	// Twilio (SK + hex) and SendGrid (SG.) are case sensitive and "sk" is far too
	// common in prose to be a lower-case hint, so they get their own exact check.
	if strings.Contains(s, "SK") || strings.Contains(s, "SG.") {
		return true
	}
	lower := strings.ToLower(s)
	for _, h := range secretHints {
		if strings.Contains(lower, h) {
			return true
		}
	}
	return false
}

// Redact replaces every secret-looking piece of text with "[redacted]".
func Redact(s string) string {
	if !mayHoldSecret(s) {
		return s
	}
	s = secretRE.ReplaceAllLiteralString(s, "[redacted]")
	if strings.Contains(s, "://") || strings.Contains(s, "-u") {
		s = credRE.ReplaceAllString(s, "${1}${3}[redacted]${2}")
	}
	return s
}

// injOpenRE finds the opening tag of text that Claude Code injects into a user
// message. sessions3.py matches the whole <tag>...</tag> with a back reference;
// RE2 has no back references, so stripInjected finds the closing tag by hand.
var injOpenRE = regexp.MustCompile(`<(system-reminder|command-[\w-]+|local-command-[\w-]+|ide_\w+|user-prompt-submit-hook|` +
	`task-notification|bash-input|bash-stdout|bash-stderr|environment_details|context)\b[^>]*>`)

var cmdArgsRE = regexp.MustCompile(`(?s)<command-args>(.*?)</command-args>`)

var skipPrefixes = []string{"Caveat:", "[Request interrupted", "This session is being continued", "[Image",
	"Base directory for this skill", "API Error", "Unknown skill", "Warmup"}

// stripInjected removes every <tag>...</tag> block from injOpenRE, replacing it with a space.
func stripInjected(s string) string {
	if !strings.Contains(s, "<") {
		return s
	}
	var b strings.Builder
	i := 0
	for i < len(s) {
		loc := injOpenRE.FindStringSubmatchIndex(s[i:])
		if loc == nil {
			break
		}
		name := s[i+loc[2] : i+loc[3]]
		closeTag := "</" + name + ">"
		end := strings.Index(s[i+loc[1]:], closeTag)
		if end < 0 { // no closing tag: keep the text, look further on
			b.WriteString(s[i : i+loc[1]])
			i += loc[1]
			continue
		}
		b.WriteString(s[i : i+loc[0]])
		b.WriteByte(' ')
		i += loc[1] + end + len(closeTag)
	}
	b.WriteString(s[i:])
	return b.String()
}

// realText returns the user's own words, or "" for injected or system text
// (port of real_text in sessions3.py).
func realText(raw string) string {
	if raw == "" {
		return ""
	}
	t := strings.TrimSpace(stripInjected(raw))
	if t == "" {
		if m := cmdArgsRE.FindStringSubmatch(raw); m != nil {
			t = strings.TrimSpace(m[1])
		}
	}
	if t == "" || t[0] == '<' {
		return ""
	}
	for _, p := range skipPrefixes {
		if strings.HasPrefix(t, p) {
			return ""
		}
	}
	return t
}

// clean redacts, shortens home to "~/", collapses white space and keeps at
// most n runes, adding "…" when it cut (port of clean in sessions3.py).
func clean(t string, n int, home string) string {
	t = Redact(t)
	if home != "" {
		t = strings.ReplaceAll(t, home+"/", "~/")
	}
	t = strings.Join(strings.Fields(t), " ")
	return cut(t, n)
}

// cut keeps at most n runes of t and adds "…" when it cut something.
func cut(t string, n int) string {
	if len(t) <= n || utf8.RuneCountInString(t) <= n {
		return t
	}
	i, k := 0, 0
	for i < len(t) && k < n {
		_, sz := utf8.DecodeRuneInString(t[i:])
		i += sz
		k++
	}
	return t[:i] + "…"
}

// cutBytes keeps at most n bytes of t (on a rune boundary) and adds "…" when it cut.
func cutBytes(t string, n int) string {
	if len(t) <= n {
		return t
	}
	i := n
	for i > 0 && !utf8.RuneStart(t[i]) {
		i--
	}
	return t[:i] + "…"
}

// Encode turns an absolute path into the folder name Claude Code uses under
// <profile>/projects. Claude Code replaces every character that is not a
// letter or digit with "-"; this covers "/", "_" and "." from the docs.
// The encoding is lossy: never decode it.
func Encode(path string) string {
	b := []byte(path)
	for i, c := range b {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			b[i] = '-'
		}
	}
	return string(b)
}

// pathRE finds absolute (or "~/") paths in free text and shell commands.
var pathRE = regexp.MustCompile("(?:^|[\\s\"'=(:,`])(~?/[^\\s\"'`<>|;&(){}\\[\\],*?]+)")

// absPaths calls fn for each absolute path found in text. "~/" is expanded with home.
func absPaths(text, home string, fn func(string)) {
	if !strings.Contains(text, "/") {
		return
	}
	for _, m := range pathRE.FindAllStringSubmatchIndex(text, -1) {
		p := text[m[2]:m[3]]
		if strings.HasPrefix(p, "//") {
			continue // a URL tail like "//host/x"
		}
		if strings.HasPrefix(p, "~/") {
			if home == "" {
				continue
			}
			p = home + p[1:]
		}
		fn(p)
	}
}
