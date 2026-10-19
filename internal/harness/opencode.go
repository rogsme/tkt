package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"gitlab.com/rogs/tkt/internal/apperr"
	"gitlab.com/rogs/tkt/internal/runner"
)

// OpenCode talks to the local OpenCode service through `opencode api`.
type OpenCode struct {
	R runner.Runner
}

// Model is an OpenCode model choice.
type Model struct {
	ProviderID string `json:"providerID"`
	ID         string `json:"id"`
	Variant    string `json:"variant,omitempty"`
}

// OpenCodeModel parses provider/model#variant. An empty spec is the default.
func OpenCodeModel(spec string) (*Model, error) {
	if spec == "" {
		return nil, nil
	}
	provider, rest, ok := strings.Cut(spec, "/")
	if !ok || provider == "" || rest == "" {
		return nil, apperr.New(apperr.ConfigInvalid,
			"OpenCode model '%s' must look like provider/model or provider/model#variant", spec)
	}
	id, variant, _ := strings.Cut(rest, "#")
	return &Model{ProviderID: provider, ID: id, Variant: variant}, nil
}

func (o OpenCode) api(ctx context.Context, timeout time.Duration, args ...string) (runner.Result, bool) {
	r := o.R.Run(ctx, runner.Cmd{Argv: append([]string{"opencode", "api"}, args...), Timeout: timeout})
	return r, r.OK()
}

// call runs an API request, starting the service once when it fails.
func (o OpenCode) call(ctx context.Context, args ...string) ([]byte, error) {
	r, ok := o.api(ctx, 60*time.Second, args...)
	if !ok {
		o.R.Run(ctx, runner.Cmd{Argv: []string{"opencode", "service", "start"}, Timeout: 30 * time.Second})
		r, ok = o.api(ctx, 60*time.Second, args...)
	}
	if !ok {
		return nil, apperr.New(apperr.OpenCodeFailed, "opencode api %s %s failed: %s", args[0], args[1], runner.Head(strings.TrimSpace(r.ErrText()), 200))
	}
	return r.Stdout, nil
}

// SessionBody is the request that creates a session on the build agent.
func SessionBody(path, title string, m *Model) string {
	body := struct {
		Title    string `json:"title"`
		Agent    string `json:"agent"`
		Location struct {
			Directory string `json:"directory"`
		} `json:"location"`
		Model *Model `json:"model,omitempty"`
	}{Title: title, Agent: "build", Model: m}
	body.Location.Directory = path
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.Encode(body)
	return strings.TrimSpace(b.String())
}

// CreateSessionArgv is the request CreateSession sends.
func CreateSessionArgv(body string) []string {
	return []string{"opencode", "api", "POST", "/api/session", "-d", body}
}

// CreateSession creates a session on the build agent with a preset's model
// and returns its id; `opencode -s <id> <dir>` then opens it.
func (o OpenCode) CreateSession(ctx context.Context, body string) (string, error) {
	out, err := o.call(ctx, CreateSessionArgv(body)[2:]...)
	if err != nil {
		return "", err
	}
	var v struct {
		ID   string `json:"id"`
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	json.Unmarshal(out, &v)
	sid := v.Data.ID
	if sid == "" {
		sid = v.ID
	}
	if !strings.HasPrefix(sid, "ses") {
		return "", apperr.New(apperr.OpenCodeFailed, "opencode did not return a session id: %s", runner.Head(string(out), 200))
	}
	return sid, nil
}

// SessionActive reports whether OpenCode is still working on a session.
func (o OpenCode) SessionActive(ctx context.Context, sid string) bool {
	r, ok := o.api(ctx, 30*time.Second, "GET", "/api/session/active")
	if !ok {
		return false
	}
	var v struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if json.Unmarshal(r.Stdout, &v) != nil {
		return false
	}
	_, active := v.Data[sid]
	return active
}

// Newest returns the newest top-level session started in path, skipping
// plan-review sessions.
func (o OpenCode) Newest(ctx context.Context, path string) (Session, bool) {
	if _, err := exec.LookPath("opencode"); err != nil {
		return Session{}, false
	}
	r, ok := o.api(ctx, 60*time.Second, "GET", "/api/session?directory="+uriEscape(path)+"&order=desc&limit=20")
	if !ok {
		return Session{}, false
	}
	var v struct {
		Data []struct {
			ID       string  `json:"id"`
			Agent    string  `json:"agent"`
			ParentID *string `json:"parentID"`
			Location struct {
				Directory string `json:"directory"`
			} `json:"location"`
			Time struct {
				Updated int64 `json:"updated"`
			} `json:"time"`
		} `json:"data"`
	}
	if json.Unmarshal(r.Stdout, &v) != nil {
		return Session{}, false
	}
	for _, s := range v.Data {
		if s.ParentID == nil && s.Agent != "plan-review" && s.Location.Directory == path {
			return Session{ID: s.ID, UpdatedMs: s.Time.Updated}, true
		}
	}
	return Session{}, false
}

// UserTurns counts the prompts OpenCode recorded for a session.
func (o OpenCode) UserTurns(ctx context.Context, sid string) (int, bool) {
	r, ok := o.api(ctx, 30*time.Second, "GET", "/api/session/"+sid+"/message?type=user&limit=200")
	if !ok {
		return 0, false
	}
	var v struct {
		Data []struct {
			Type string `json:"type"`
		} `json:"data"`
	}
	if json.Unmarshal(r.Stdout, &v) != nil {
		return 0, false
	}
	n := 0
	for _, m := range v.Data {
		if m.Type == "user" {
			n++
		}
	}
	return n, true
}

// uriEscape escapes everything but RFC 3986 unreserved characters, like jq's
// @uri.
func uriEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.IndexByte("-_.~", c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
