package release

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A download may take longer than the stall limit as long as bytes keep
// arriving; one that stops for the limit fails.
func TestStall(t *testing.T) {
	done := make(chan struct{})
	defer close(done)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for i := range 6 {
			if strings.HasSuffix(r.URL.Path, "/stalls") && i == 2 {
				select {
				case <-done:
				case <-r.Context().Done():
				}
				return
			}
			w.Write([]byte("x"))
			w.(http.Flusher).Flush()
			time.Sleep(40 * time.Millisecond)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, srv.URL)
	c.Stall = 150 * time.Millisecond

	var b bytes.Buffer
	if err := c.Fetch(context.Background(), "v1.0.0", "slow", &b); err != nil || b.String() != "xxxxxx" {
		t.Errorf("a slow download: %q, %v", b.String(), err)
	}
	b.Reset()
	start := time.Now()
	err := c.Fetch(context.Background(), "v1.0.0", "stalls", &b)
	if err == nil || !strings.Contains(err.Error(), "no data for 150ms") {
		t.Errorf("a stalled download: %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("the stalled download took %s", d)
	}
}
