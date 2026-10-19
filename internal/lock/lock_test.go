//go:build unix

package lock

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestTryAndWait(t *testing.T) {
	dir := t.TempDir()
	f, err := Try(dir, "ACM-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Try(dir, "ACM-1"); !errors.Is(err, ErrHeld) {
		t.Fatalf("second Try: %v", err)
	}
	if _, err := Wait(context.Background(), dir, "ACM-1", 300*time.Millisecond); !errors.Is(err, ErrHeld) {
		t.Fatalf("Wait on a held lock: %v", err)
	}
	f.Close()
	g, err := Wait(context.Background(), dir, "ACM-1", time.Second)
	if err != nil {
		t.Fatalf("Wait after release: %v", err)
	}
	g.Close()
}
