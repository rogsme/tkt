//go:build unix

// Package lock keeps two tkt commands from working on the same ticket, or
// creating worktrees, at the same time. Locks live in the repository's
// common git directory and are released when the process exits.
package lock

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// ErrHeld means another process holds the lock.
var ErrHeld = errors.New("lock held")

func open(common, name string) (*os.File, error) {
	dir := filepath.Join(common, "tkt", "locks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(filepath.Join(dir, name+".lock"), os.O_CREATE|os.O_RDWR, 0o644)
}

// Try takes the named lock or returns ErrHeld at once. Keep the file until
// the lock should end; closing it releases the lock.
func Try(common, name string) (*os.File, error) {
	f, err := open(common, name)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrHeld
		}
		return nil, err
	}
	return f, nil
}

// Wait takes the named lock, polling until timeout.
func Wait(ctx context.Context, common, name string, timeout time.Duration) (*os.File, error) {
	deadline := time.Now().Add(timeout)
	for {
		f, err := Try(common, name)
		if !errors.Is(err, ErrHeld) || time.Now().After(deadline) {
			return f, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}
