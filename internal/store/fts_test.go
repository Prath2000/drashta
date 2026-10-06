package store

import (
	"strings"
	"testing"
	"time"

	"github.com/Prath2000/drashta/internal/probe"
)

func TestSearchFindsCommitByMidWordFragment(t *testing.T) {
	db := openTest(t)
	seedProject(t, db, "alphalpha")
	commits := []probe.Commit{
		{ID: "bdcb99c0001", VCS: "git", Author: "Asha", Time: at("2026-09-30", 10, 0), Msg: "Fix the discoverer for IPv6 sockets", Plus: 4, Minus: 1,
			Files: []probe.CommitFile{{Path: "discover.go", Plus: 4, Minus: 1}}},
		{ID: "11aa22bb003", VCS: "git", Author: "Asha", Time: at("2026-09-29", 9, 0), Msg: "Add history merge", Body: "Merge runs by key."},
	}
	if err := db.ReplaceCommits(bg(), "alphalpha", commits); err != nil {
		t.Fatal(err)
	}

	for _, q := range []string{"scoverer", "SCOVERER", "ipv6", "merge runs", "bdcb99c", "0001"} {
		hits, err := db.Search(bg(), q, "", "", 10)
		if err != nil {
			t.Fatalf("Search(%q): %v", q, err)
		}
		if len(hits) == 0 {
			t.Errorf("Search(%q) found nothing", q)
			continue
		}
		if hits[0].Kind != "commit" || !strings.HasPrefix(hits[0].Ref, "commit:") || hits[0].ProjectID != "alphalpha" {
			t.Errorf("Search(%q) first hit = %+v", q, hits[0])
		}
	}

	hits, _ := db.Search(bg(), "scoverer", "", "", 10)
	if len(hits) != 1 || hits[0].Ref != "commit:bdcb99c0001" || hits[0].Title != "Fix the discoverer for IPv6 sockets" {
		t.Fatalf("hits = %+v", hits)
	}
	if !hits[0].TS.Equal(at("2026-09-30", 10, 0)) {
		t.Errorf("hit time = %v", hits[0].TS)
	}
	if hits[0].Snippet == "" {
		t.Error("snippet should not be empty")
	}
}

func TestSearchFilters(t *testing.T) {
	db := openTest(t)
	ctx := bg()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(db.FTSReplace(ctx, "file", "file:src/router.go", "alpha", "src/router.go", "", time.Unix(10, 0)))
	must(db.FTSReplace(ctx, "file", "file:src/router.go", "beta", "src/router.go", "", time.Unix(20, 0)))
	must(db.FTSReplace(ctx, "commit", "commit:abc1234", "alpha", "Rewrite the router", "", time.Unix(30, 0)))

	all, _ := db.Search(ctx, "router", "", "", 10)
	if len(all) != 3 {
		t.Fatalf("all = %d", len(all))
	}
	onlyA, _ := db.Search(ctx, "router", "alpha", "", 10)
	if len(onlyA) != 2 {
		t.Fatalf("project filter = %d", len(onlyA))
	}
	onlyFiles, _ := db.Search(ctx, "router", "", "file", 10)
	if len(onlyFiles) != 2 {
		t.Fatalf("kind filter = %d", len(onlyFiles))
	}
	one, _ := db.Search(ctx, "router", "alpha", "commit", 10)
	if len(one) != 1 || one[0].Ref != "commit:abc1234" {
		t.Fatalf("both filters = %+v", one)
	}
	limited, _ := db.Search(ctx, "router", "", "", 1)
	if len(limited) != 1 {
		t.Fatalf("limit = %d", len(limited))
	}
}

func TestSearchRanksTitleAboveBodyThenNewest(t *testing.T) {
	db := openTest(t)
	ctx := bg()
	_ = db.FTSReplace(ctx, "doc", "file:a.md", "p", "notes.md", "we talk about the parser a little", time.Unix(100, 0))
	_ = db.FTSReplace(ctx, "doc", "file:b.md", "p", "parser design", "short", time.Unix(50, 0))
	hits, err := db.Search(ctx, "parser", "", "", 10)
	if err != nil || len(hits) != 2 {
		t.Fatalf("hits = %+v, %v", hits, err)
	}
	if hits[0].Ref != "file:b.md" {
		t.Errorf("a title match should rank first: %+v", hits)
	}

	// Equal text: the newer row comes first.
	_ = db.FTSReplace(ctx, "note", "note:N-1", "p", "same words here", "", time.Unix(10, 0))
	_ = db.FTSReplace(ctx, "note", "note:N-2", "p", "same words here", "", time.Unix(99, 0))
	hits, _ = db.Search(ctx, "same words here", "", "note", 10)
	if len(hits) != 2 || hits[0].Ref != "note:N-2" {
		t.Errorf("newest should win a tie: %+v", hits)
	}
}

func TestSearchCannotBeBrokenByUserInput(t *testing.T) {
	db := openTest(t)
	_ = db.FTSReplace(bg(), "commit", "commit:1", "p", `He said "OR" and NEAR( things * ok`, "text with AND NOT col:x -y ^z", time.Unix(1, 0))

	inputs := []string{
		`"`, `""`, `"""`, `*`, `**`, `NEAR(`, `NEAR(a b)`, `OR`, `AND`, `NOT`, `a OR b`, `foo OR`, `OR foo`,
		`title:foo`, `body : x`, `-foo`, `^foo`, `foo*`, `(`, `)`, `()`, `((("`, `{`, `}`, `:`, `'`, `\`,
		`"unterminated`, `x" OR "y`, `NEAR/2`, `foo NEAR bar`, `+`, `~`, `;`, `--`, `/*`, `%`, `_`,
		"nul\x00byte", "tab\tsep", "new\nline", "", "   ", "é", "日本語のテスト", strings.Repeat("a", 5000),
		strings.Repeat("word ", 500), `"; DROP TABLE search; --`, `NEAR(`, `OR OR OR`, `* * *`,
	}
	for _, in := range inputs {
		hits, err := db.Search(bg(), in, "", "", 10)
		if err != nil {
			t.Errorf("Search(%q) returned an error: %v", in, err)
		}
		if hits == nil {
			t.Errorf("Search(%q) returned nil, want an empty list", in)
		}
	}

	// Syntax-looking words are plain text: they can be found.
	for _, in := range []string{"NEAR(", "col:x", "AND NOT"} {
		hits, err := db.Search(bg(), in, "", "", 10)
		if err != nil || len(hits) == 0 {
			t.Errorf("Search(%q) = %v, %v; want the row", in, hits, err)
		}
	}
	// The table is still there.
	if n := count(t, db, `SELECT COUNT(*) FROM search`); n != 1 {
		t.Fatalf("search rows = %d", n)
	}
}

func TestSearchShortQueriesGiveNothing(t *testing.T) {
	db := openTest(t)
	_ = db.FTSReplace(bg(), "file", "file:go.mod", "p", "go.mod", "", time.Unix(1, 0))
	for _, q := range []string{"", "g", "go", "go ", " m ", `"a"`, "a b"} {
		hits, err := db.Search(bg(), q, "", "", 10)
		if err != nil || len(hits) != 0 {
			t.Errorf("Search(%q) = %v, %v; want empty", q, hits, err)
		}
	}
	if hits, _ := db.Search(bg(), ".mod", "", "", 10); len(hits) != 1 {
		t.Errorf("a 4 character query should match")
	}
}

func TestMatchExpr(t *testing.T) {
	cases := map[string]string{
		"":              "",
		"ab":            "",
		"abc":           `"abc"`,
		`  Foo   bar  `: `"Foo" "bar"`,
		`"foo bar"`:     `"foo" "bar"`,
		`foo OR`:        `"foo"`,
		"foo foo FOO":   `"foo"`,
		"NEAR(":         `"NEAR("`,
		"a\x00bc":       "",
		"abc\x00def":    `"abc" "def"`,
		"one two three four five six seven eight nine ten": `"one" "two" "three" "four" "five" "six" "seven" "eight"`,
	}
	for in, want := range cases {
		if got := MatchExpr(in); got != want {
			t.Errorf("MatchExpr(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFTSReplaceAndDelete(t *testing.T) {
	db := openTest(t)
	ctx := bg()
	_ = db.FTSReplace(ctx, "task", "task:T-1", "p", "first title", "body one", time.Unix(1, 0))
	_ = db.FTSReplace(ctx, "task", "task:T-1", "p", "second title", "body two", time.Unix(2, 0))
	if n := count(t, db, `SELECT COUNT(*) FROM search`); n != 1 {
		t.Fatalf("replace must not duplicate: %d rows", n)
	}
	if hits, _ := db.Search(ctx, "first title", "", "", 5); len(hits) != 0 {
		t.Fatal("old text must be gone")
	}
	if hits, _ := db.Search(ctx, "second title", "", "", 5); len(hits) != 1 {
		t.Fatal("new text must be found")
	}
	if err := db.FTSDelete(ctx, "task", "task:T-1", "p"); err != nil {
		t.Fatal(err)
	}
	if err := db.FTSDelete(ctx, "task", "task:T-1", "p"); err != nil {
		t.Fatalf("deleting a missing row is not an error: %v", err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM search`) + count(t, db, `SELECT COUNT(*) FROM search_ref`); n != 0 {
		t.Fatalf("rows left: %d", n)
	}
}

func TestFTSBodyIsCut(t *testing.T) {
	db := openTest(t)
	big := strings.Repeat("é", MaxBody) // 2 bytes each, far over the limit
	if err := db.FTSReplace(bg(), "doc", "file:big.md", "p", "big", big, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.Read().QueryRow(`SELECT length(CAST(body AS BLOB)) FROM search`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n > MaxBody {
		t.Fatalf("stored body = %d bytes, limit %d", n, MaxBody)
	}
	if got := clip("aé", 2); got != "a" {
		t.Fatalf("clip must not split a character: %q", got)
	}
}
