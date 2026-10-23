// Package output writes what tkt tells people: progress lines on stderr,
// tables, and the JSON envelope on stdout.
package output

import (
	"fmt"
	"io"
	"sync"
)

// UI writes progress lines to stderr. Warnings are also kept, so JSON output
// can carry them.
type UI struct {
	Err io.Writer

	mu       sync.Mutex
	warnings []string
}

// Info prints "tkt: <message>".
func (u *UI) Info(format string, args ...any) {
	u.line("tkt: "+format, args...)
}

// Warn prints "tkt: warning: <message>" and keeps the message.
func (u *UI) Warn(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	u.mu.Lock()
	u.warnings = append(u.warnings, msg)
	u.mu.Unlock()
	u.line("tkt: warning: %s", msg)
}

// Raw prints a line as it is.
func (u *UI) Raw(format string, args ...any) {
	u.line(format, args...)
}

// ForgetWarnings drops the warnings kept so far, for a command that runs for
// days, such as tkt watch, whose result carries only its last pass.
func (u *UI) ForgetWarnings() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.warnings = nil
}

// Warnings returns the warnings printed so far.
func (u *UI) Warnings() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string{}, u.warnings...)
}

func (u *UI) line(format string, args ...any) {
	u.mu.Lock()
	defer u.mu.Unlock()
	fmt.Fprintf(u.Err, format+"\n", args...)
}
