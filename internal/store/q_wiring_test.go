package store

import (
	"testing"
	"time"
)

func TestWiringGHCacheRoundTrip(t *testing.T) {
	db := openTest(t)
	ctx := bg()
	if _, _, ok, err := db.WiringGHGet(ctx, "alpha", "result"); err != nil || ok {
		t.Fatalf("empty table: ok=%v err=%v", ok, err)
	}
	at1 := time.Unix(1_790_000_000, 0)
	if err := db.WiringGHPut(ctx, "alpha", "result", []byte(`{"prs":[]}`), at1); err != nil {
		t.Fatal(err)
	}
	at2 := at1.Add(time.Minute)
	if err := db.WiringGHPut(ctx, "alpha", "result", []byte(`{"prs":[1]}`), at2); err != nil {
		t.Fatal(err)
	}
	data, at, ok, err := db.WiringGHGet(ctx, "alpha", "result")
	if err != nil || !ok || string(data) != `{"prs":[1]}` || !at.Equal(at2) {
		t.Fatalf("got %q %v %v %v", data, at, ok, err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM gh_cache`); n != 1 {
		t.Errorf("rows = %d, want 1 (replace, not append)", n)
	}
	if _, _, ok, _ := db.WiringGHGet(ctx, "beta", "result"); ok {
		t.Error("other project must not match")
	}
}
