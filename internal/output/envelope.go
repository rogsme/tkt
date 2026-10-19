package output

import (
	"encoding/json"
	"io"
)

// Schema is the envelope's version. It changes only when a field changes
// meaning or goes away; new fields may appear at any time.
const Schema = 1

// Envelope is the one JSON document tkt prints on stdout in JSON mode.
type Envelope struct {
	Schema   int      `json:"schema"`
	OK       bool     `json:"ok"`
	Command  string   `json:"command"`
	Result   any      `json:"result,omitempty"`
	Error    *ErrBody `json:"error,omitempty"`
	Warnings []string `json:"warnings"`
}

// ErrBody describes a failure.
type ErrBody struct {
	Code    string         `json:"code"`
	Exit    int            `json:"exit"`
	Message string         `json:"message"`
	Hint    string         `json:"hint"`
	Details map[string]any `json:"details"`
}

// Emit writes the envelope, indented when a person reads it.
func Emit(w io.Writer, e Envelope, indent bool) error {
	if e.Warnings == nil {
		e.Warnings = []string{}
	}
	if e.Error != nil && e.Error.Details == nil {
		e.Error.Details = map[string]any{}
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if indent {
		enc.SetIndent("", "  ")
	}
	return enc.Encode(e)
}
