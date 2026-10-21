package state

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
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

func TestUpdateKeepsKeysOthersWrote(t *testing.T) {
	path := Path(t.TempDir())
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if _, err := Update(path, now, "stage", "start", "auto", "plan"); err != nil {
		t.Fatal(err)
	}
	// tkt run read the state before tkt auto cleared its mark, then sets
	// only its own keys.
	if _, err := Update(path, now, "auto", ""); err != nil {
		t.Fatal(err)
	}
	got, err := Update(path, now, "stage", "address")
	if err != nil {
		t.Fatal(err)
	}
	if got["stage"] != "address" || got["auto"] != "" {
		t.Errorf("update returned %v", got)
	}
	if back := Read(path); back["stage"] != "address" || back["auto"] != "" || back["updated"] == "" {
		t.Errorf("file holds %v", back)
	}
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			if _, err := Update(path, now, fmt.Sprintf("k%d", i), "v"); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	back := Read(path)
	for i := range 20 {
		if back[fmt.Sprintf("k%d", i)] != "v" {
			t.Errorf("k%d lost in concurrent updates: %v", i, back)
		}
	}
}
