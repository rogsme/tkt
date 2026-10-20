package forge

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gitlab.com/rogs/tkt/internal/contract"
)

// Verdict is the latest review verdict on a pull request.
type Verdict struct {
	Value string `json:"verdict"`
	// Head is the commit the review names; AtHead says whether it is the
	// pull request's current head. A verdict on an older head does not count.
	Head        string `json:"head"`
	AtHead      bool   `json:"at_head"`
	SubmittedAt string `json:"submitted_at"`
}

// LatestVerdict returns the newest review that carries a verdict, from
// review-pr or from the forge's automated reviewer, or nil.
func LatestVerdict(p PR) *Verdict {
	var vs []Verdict
	for _, r := range p.Reviews {
		oid := ""
		if r.Commit != nil {
			oid = r.Commit.Oid
		}
		var v Verdict
		switch {
		case contract.AutomatedReviewRE.MatchString(r.Body):
			v.Value = contract.AutomatedReviewRE.FindStringSubmatch(r.Body)[1]
			v.Head = firstGroup(contract.AutomatedHeadRE, r.Body, oid)
		case contract.VerdictRE.MatchString(r.Body):
			v.Value = contract.VerdictRE.FindStringSubmatch(r.Body)[1]
			v.Head = firstGroup(contract.ReviewedHeadRE, r.Body, oid)
		default:
			continue
		}
		v.SubmittedAt = r.SubmittedAt
		vs = append(vs, v)
	}
	if len(vs) == 0 {
		return nil
	}
	sort.SliceStable(vs, func(i, j int) bool { return vs[i].SubmittedAt < vs[j].SubmittedAt })
	v := vs[len(vs)-1]
	v.AtHead = v.Head != "" && strings.HasPrefix(p.HeadRefOid, v.Head)
	return &v
}

func firstGroup(re interface{ FindStringSubmatch(string) []string }, s, def string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return def
}

// Checks summarizes the checks on a pull request's head.
type Checks struct {
	State   string   `json:"state"` // ok, fail, pending or none
	Total   int      `json:"total"`
	Failing []string `json:"failing"`
	Pending int      `json:"pending"`
}

// Text renders the summary for the CHECKS column.
func (c Checks) Text() string {
	switch c.State {
	case "fail":
		return "fail: " + strings.Join(c.Failing, ",")
	case "pending":
		return "pending " + strconv.Itoa(c.Pending)
	case "ok":
		return "ok " + strconv.Itoa(c.Total)
	}
	return "-"
}

// Summarize reads a pull request's check rollup.
func Summarize(p PR) Checks {
	c := Checks{State: "none", Total: len(p.StatusCheckRollup), Failing: []string{}}
	failing := map[string]bool{}
	for _, ch := range p.StatusCheckRollup {
		name, s := ch.Name, "ok"
		if ch.Context != nil {
			name = *ch.Context
			switch ch.State {
			case "PENDING", "EXPECTED":
				s = "pending"
			case "FAILURE", "ERROR":
				s = "fail"
			}
		} else if ch.Status != "COMPLETED" {
			s = "pending"
		} else {
			switch ch.Conclusion {
			case "FAILURE", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "STARTUP_FAILURE":
				s = "fail"
			}
		}
		switch s {
		case "fail":
			failing[name] = true
		case "pending":
			c.Pending++
		}
	}
	for n := range failing {
		c.Failing = append(c.Failing, n)
	}
	sort.Strings(c.Failing)
	switch {
	case c.Total == 0:
	case len(c.Failing) > 0:
		c.State = "fail"
	case c.Pending > 0:
		c.State = "pending"
	default:
		c.State = "ok"
	}
	return c
}

// Step is one numbered step of a human verification procedure.
type Step struct {
	N    int    `json:"n"`
	Text string `json:"text"`
}

// Procedure is the Human verification section of review-pr's ready summary:
// the whole section as written, and its numbered steps.
type Procedure struct {
	Head        string `json:"head"`
	SubmittedAt string `json:"submitted_at"`
	Section     string `json:"section"`
	Steps       []Step `json:"steps"`
}

// HumanVerification returns the procedure in the newest review that says
// READY_FOR_HUMAN about the pull request's current head, or nil.
func HumanVerification(p PR) *Procedure {
	var best *Procedure
	for _, r := range p.Reviews {
		m := contract.VerdictRE.FindStringSubmatch(r.Body)
		if m == nil || m[1] != contract.VerdictReadyForHuman {
			continue
		}
		oid := ""
		if r.Commit != nil {
			oid = r.Commit.Oid
		}
		head := firstGroup(contract.ReviewedHeadRE, r.Body, oid)
		if head == "" || !strings.HasPrefix(p.HeadRefOid, head) {
			continue
		}
		section, steps, ok := ParseProcedure(r.Body)
		if !ok || best != nil && best.SubmittedAt > r.SubmittedAt {
			continue
		}
		best = &Procedure{Head: head, SubmittedAt: r.SubmittedAt, Section: section, Steps: steps}
	}
	return best
}

var stepRE = regexp.MustCompile(`^ {0,3}(\d+)[.)][ \t]+(.*)$`)

// ParseProcedure reads the Human verification section of a review body: the
// text up to the next level-two heading, and its numbered steps. A step's
// indented or wrapped lines belong to it; a blank line followed by text that
// is neither indented nor numbered, such as a cleanup paragraph, ends it.
func ParseProcedure(body string) (section string, steps []Step, ok bool) {
	loc := contract.HumanVerificationRE.FindStringIndex(body)
	if loc == nil {
		return "", nil, false
	}
	rest := body[loc[1]:]
	var lines []string
	for _, l := range strings.Split(rest, "\n") {
		if strings.HasPrefix(l, "## ") {
			break
		}
		lines = append(lines, strings.TrimRight(l, " \t\r"))
	}
	section = strings.TrimSpace(strings.Join(lines, "\n"))
	blank := false
	for _, l := range lines {
		if m := stepRE.FindStringSubmatch(l); m != nil {
			n, _ := strconv.Atoi(m[1])
			steps = append(steps, Step{N: n, Text: strings.TrimSpace(m[2])})
			blank = false
			continue
		}
		if strings.TrimSpace(l) == "" {
			blank = true
			continue
		}
		indented := strings.HasPrefix(l, "  ") || strings.HasPrefix(l, "\t")
		if len(steps) > 0 && (indented || !blank) && !strings.HasPrefix(l, "#") {
			s := &steps[len(steps)-1]
			s.Text += "\n" + strings.TrimSpace(l)
			blank = false
			continue
		}
		if len(steps) > 0 {
			break
		}
	}
	return section, steps, len(steps) > 0
}
