// Package statuspage reads the status pages of the services tkt's agents
// depend on, such as GitHub's or a model provider's, as Atlassian Statuspage
// serves them at /api/v2/summary.json; most services' pages do.
package statuspage

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Summary is what a page says about its service: the overall status, each
// component's, and the incidents still open.
type Summary struct {
	Page struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"page"`
	Status struct {
		// Indicator is none, minor, major, critical or maintenance.
		Indicator   string `json:"indicator"`
		Description string `json:"description"`
	} `json:"status"`
	Components []Component `json:"components"`
	Incidents  []struct {
		Name string `json:"name"`
	} `json:"incidents"`
}

// Component is a part of the service. Status is operational,
// degraded_performance, partial_outage, major_outage or under_maintenance.
type Component struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// Client reads status pages.
type Client struct {
	HTTP *http.Client
}

// New returns a client that gives a page ten seconds. It also reads file://
// URLs, which the tests use instead of a server.
func New() Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.RegisterProtocol("file", http.NewFileTransport(http.Dir("/")))
	return Client{HTTP: &http.Client{Transport: t, Timeout: 10 * time.Second}}
}

// Read fetches a page's summary.
func (c Client) Read(ctx context.Context, page string) (Summary, error) {
	url := strings.TrimRight(page, "/") + "/api/v2/summary.json"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Summary{}, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Summary{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Summary{}, fmt.Errorf("%s answered %s", url, resp.Status)
	}
	var s Summary
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&s); err != nil {
		return Summary{}, fmt.Errorf("%s is not a status page summary: %v", url, err)
	}
	if s.Status.Indicator == "" {
		return Summary{}, fmt.Errorf("%s has no status indicator; tkt reads Statuspage's /api/v2/summary.json", url)
	}
	return s, nil
}

// Problem says what is wrong with the service, or "" while it works. With
// components, only theirs counts, such as "Actions partial outage"; without,
// the page's own status, with the first incident it names. Missing lists
// the components the page does not have, which count for nothing.
func (s Summary) Problem(components []string) (problem string, missing []string) {
	if len(components) == 0 {
		if s.Status.Indicator == "none" {
			return "", nil
		}
		p := s.Status.Description
		if len(s.Incidents) > 0 && s.Incidents[0].Name != "" {
			p += ": " + s.Incidents[0].Name
		}
		return p, nil
	}
	var parts []string
	for _, want := range components {
		found := false
		for _, c := range s.Components {
			if !strings.EqualFold(c.Name, want) {
				continue
			}
			found = true
			if c.Status != "operational" {
				parts = append(parts, c.Name+" "+strings.ReplaceAll(c.Status, "_", " "))
			}
			break
		}
		if !found {
			missing = append(missing, want)
		}
	}
	return strings.Join(parts, ", "), missing
}
