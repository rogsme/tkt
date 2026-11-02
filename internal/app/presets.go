package app

import (
	"context"
	"io"

	"gitlab.com/rogs/tkt/internal/config"
	"gitlab.com/rogs/tkt/internal/output"
)

// PresetsOpts are tkt presets's arguments.
type PresetsOpts struct{}

// PresetsResult lists the presets --as takes in this repository, in order:
// the first is the default. Suggest is the preset tkt suggest runs on
// without --as, empty for the default one.
type PresetsResult struct {
	Presets []config.Preset `json:"presets"`
	Suggest string          `json:"suggest"`
}

func (r *PresetsResult) Text(w io.Writer) {
	var rows [][]string
	for i, p := range r.Presets {
		name := p.Name
		if i == 0 {
			name += " (default)"
		}
		rows = append(rows, []string{name, p.Harness, or(p.Model, "default model")})
	}
	output.Table(w, []string{"PRESET", "HARNESS", "MODEL"}, rows)
}

// Presets lists the presets of the repository tkt runs in, with its own
// files' first.
func (a *App) Presets(ctx context.Context, _ PresetsOpts) (*PresetsResult, error) {
	c, err := a.begin(ctx, false, false)
	if err != nil {
		return nil, err
	}
	defer c.release()
	return &PresetsResult{Presets: c.Cfg.Presets, Suggest: c.Cfg.Suggest.As}, nil
}
