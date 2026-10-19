// Package apperr defines the errors tkt reports to people and programs: each
// carries a stable code, and the code alone decides the exit status.
package apperr

import (
	"errors"
	"fmt"
)

// Error is a failure tkt reports. Msg is the human message; Hint, when set,
// names what to do next, often a tkt command.
type Error struct {
	Code    Code
	Msg     string
	Hint    string
	Details map[string]any
	Err     error
}

func (e *Error) Error() string { return e.Msg }

func (e *Error) Unwrap() error { return e.Err }

// New returns an error with the given code and a formatted message.
func New(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// WithHint sets the hint and returns the error.
func (e *Error) WithHint(format string, args ...any) *Error {
	e.Hint = fmt.Sprintf(format, args...)
	return e
}

// With adds a detail for machine-readable output and returns the error.
func (e *Error) With(key string, value any) *Error {
	if e.Details == nil {
		e.Details = map[string]any{}
	}
	e.Details[key] = value
	return e
}

// Wrap keeps err as the cause and returns the error.
func (e *Error) Wrap(err error) *Error {
	e.Err = err
	return e
}

// As returns err as an *Error. An error tkt did not classify becomes an
// internal error that keeps its message.
func As(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return &Error{Code: Internal, Msg: err.Error(), Err: err}
}

// Exit returns the exit status for err: 0 for nil, otherwise the status
// registered for its code.
func Exit(err error) int {
	if err == nil {
		return 0
	}
	return Lookup(As(err).Code).Exit
}
