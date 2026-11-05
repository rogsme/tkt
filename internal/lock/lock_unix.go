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

// Mark writes what holds a lock into its file, such as a ticket's id, for
// Holder to read.
func Mark(f *os.File, text string) error {
	if err := f.Truncate(0); err != nil {
		return err
	}
	_, err := f.WriteAt([]byte(text), 0)
	return err
}

// Holder reads what Mark wrote into the named lock's file while another
// process holds it, or "" when the lock is free or the holder wrote nothing.
// It takes the lock for a moment to tell, so a Try elsewhere at the same
// time may see it held.
func Holder(common, name string) string {
	f, err := Try(common, name)
	if err == nil {
		f.Close()
		return ""
	}
	if !errors.Is(err, ErrHeld) {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(common, "tkt", "locks", name+".lock"))
	if err != nil {
		return ""
	}
	return string(b)
}
