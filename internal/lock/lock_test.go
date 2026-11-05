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

func TestHolder(t *testing.T) {
	dir := t.TempDir()
	if h := Holder(dir, "push-0"); h != "" {
		t.Fatalf("free lock: %q", h)
	}
	f, err := Try(dir, "push-0")
	if err != nil {
		t.Fatal(err)
	}
	if err := Mark(f, "acme/app ACM-1"); err != nil {
		t.Fatal(err)
	}
	if h := Holder(dir, "push-0"); h != "acme/app ACM-1" {
		t.Fatalf("held lock: %q", h)
	}
	f.Close()
	// The text stays in the file, but a free lock has no holder.
	if h := Holder(dir, "push-0"); h != "" {
		t.Fatalf("released lock: %q", h)
	}
}
