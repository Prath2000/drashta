package runx

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRunOutputAndExitCode(t *testing.T) {
	res, err := Run(context.Background(), Options{}, "sh", "-c", "printf hi; exit 3")
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Stdout) != "hi" || res.ExitCode != 3 {
		t.Fatalf("got %q exit=%d", res.Stdout, res.ExitCode)
	}
}

func TestRunNotFound(t *testing.T) {
	_, err := Run(context.Background(), Options{}, "definitely-not-a-binary-xyz")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestRunTimeout(t *testing.T) {
	start := time.Now()
	_, err := Run(context.Background(), Options{Timeout: 150 * time.Millisecond}, "sleep", "5")
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("timeout not enforced: %v", time.Since(start))
	}
}

func TestRunCapsStdout(t *testing.T) {
	res, err := Run(context.Background(), Options{MaxOut: 100}, "sh", "-c", "yes x | head -c 5000")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Stdout) != 100 || !res.Truncated {
		t.Fatalf("len=%d truncated=%v", len(res.Stdout), res.Truncated)
	}
}

func TestRunSetsLocale(t *testing.T) {
	res, err := Run(context.Background(), Options{}, "sh", "-c", "printf %s \"$LC_ALL\"")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(res.Stdout), "C") {
		t.Fatalf("LC_ALL = %q", res.Stdout)
	}
}
