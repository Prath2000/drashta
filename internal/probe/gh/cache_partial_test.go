package gh

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Prath2000/drashta/internal/probe"
)

// TestCachePartialKeepsGoodParts (review I8): when one gh call fails, the
// previous data for that part stays and Partial names the failed part.
func TestCachePartialKeepsGoodParts(t *testing.T) {
	clock := newClock()
	partial := false
	c := NewCache(nil)
	c.Now = clock.Now
	c.Fetch = func(ctx context.Context, p probe.Project, remote, branch string) (Result, error) {
		r := emptyResult(clock.Now())
		r.Repo, r.Branch = "acme/widgets", branch
		r.Issues = IssueSummary{OpenCount: 3, Latest: []Issue{{Number: 9, Title: "new issue"}}}
		if partial {
			r.Partial = []string{"prs"} // the PR call failed: PRs is empty
			return r, nil
		}
		r.PRs = []PR{{Number: 7, Title: "good pr", State: "open"}}
		r.Runs = []Run{{Status: "completed"}}
		return r, nil
	}
	first := get(t, c, false)
	if len(first.PRs) != 1 {
		t.Fatalf("first = %+v", first)
	}
	partial = true
	clock.Advance(DefaultTTL + time.Second)
	second := get(t, c, false)
	if len(second.PRs) != 1 || second.PRs[0].Number != 7 {
		t.Errorf("the failed part must keep the old PRs, got %+v", second.PRs)
	}
	if second.Issues.OpenCount != 3 || len(second.Partial) != 1 || second.Partial[0] != "prs" {
		t.Errorf("issues/partial = %+v %v", second.Issues, second.Partial)
	}
	// The cached copy keeps the old PRs too.
	if r, _ := c.Peek(testProject.ID); len(r.PRs) != 1 {
		t.Errorf("peek PRs = %+v", r.PRs)
	}
}

// TestCacheInFlightKeyedByBranch (review M9): a Get for branch B while A's
// fetch runs must not be answered with A's data.
func TestCacheInFlightKeyedByBranch(t *testing.T) {
	clock := newClock()
	release := make(chan struct{})
	c := NewCache(nil)
	c.Now = clock.Now
	c.Fetch = func(ctx context.Context, p probe.Project, remote, branch string) (Result, error) {
		if branch == "slow" {
			<-release
		}
		r := emptyResult(clock.Now())
		r.Repo, r.Branch = "acme/widgets", branch
		return r, nil
	}
	ctx := context.Background()
	var wg sync.WaitGroup
	wg.Add(1)
	var slow Result
	go func() {
		defer wg.Done()
		slow, _ = c.Get(ctx, testProject, goodRemote, "slow", false)
	}()
	// Wait until the slow fetch is in flight.
	for i := 0; ; i++ {
		c.mu.Lock()
		e := c.entries[testProject.ID]
		busy := e != nil && e.call != nil
		c.mu.Unlock()
		if busy {
			break
		}
		if i > 2000 {
			t.Fatal("slow fetch never started")
		}
		time.Sleep(time.Millisecond)
	}
	got := make(chan Result, 1)
	go func() {
		r, _ := c.Get(ctx, testProject, goodRemote, "feature", false)
		got <- r
	}()
	select {
	case other := <-got:
		if other.Branch != "feature" {
			t.Errorf("feature Get answered with branch %q", other.Branch)
		}
	case <-time.After(2 * time.Second):
		t.Error("feature Get waited for the slow fetch of another branch")
	}
	close(release)
	wg.Wait()
	if slow.Branch != "slow" {
		t.Errorf("slow waiter got %q", slow.Branch)
	}
	// The late answer for "slow" must not replace the newer "feature" data.
	if r, _ := c.Peek(testProject.ID); r.Branch != "feature" {
		t.Errorf("cached branch = %q, want feature", r.Branch)
	}
}
