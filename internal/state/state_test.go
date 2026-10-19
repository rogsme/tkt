package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRoundTrip(t *testing.T) {
	path := Path(t.TempDir())
	if got := Read(path); len(got) != 0 {
		t.Fatalf("missing file read as %v", got)
	}
	s := State{}
	s.Set("ticket", "ACM-1", "status", "submitted")
	if err := s.Save(path, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "status=submitted\nticket=ACM-1\nupdated=2026-01-02T03:04:05Z\n" {
		t.Errorf("file:\n%s", b)
	}
	if got := Read(path); got["ticket"] != "ACM-1" || got["updated"] == "" {
		t.Errorf("read back %v", got)
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Errorf("left temporary files: %v", entries)
	}
}
