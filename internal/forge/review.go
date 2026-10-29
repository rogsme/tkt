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
	vs := Verdicts(p)
	if len(vs) == 0 {
		return nil
	}
	v := vs[len(vs)-1]
	v.AtHead = v.Head != "" && strings.HasPrefix(p.HeadRefOid, v.Head)
	return &v
}

// Verdicts returns every review's verdict, oldest first, with the review it
// came from. A review without a verdict is left out.
func Verdicts(p PR) []Verdict {
	var vs []Verdict
	for _, r := range p.Reviews {
		if v, ok := ReviewVerdict(r); ok {
			vs = append(vs, v)
		}
	}
	sort.SliceStable(vs, func(i, j int) bool { return vs[i].SubmittedAt < vs[j].SubmittedAt })
	return vs
}

// ReviewVerdict reads the verdict of one review, from review-pr or from the
// forge's automated reviewer. The head defaults to the commit the review
// was submitted on.
func ReviewVerdict(r Review) (Verdict, bool) {
	oid := ""
	if r.Commit != nil {
		oid = r.Commit.Oid
	}
	v := Verdict{SubmittedAt: r.SubmittedAt}
	switch {
	case contract.AutomatedReviewRE.MatchString(r.Body):
		// The brief above the verdict line can name other heads, so the
		// head comes from that line, the last one, as the summary ends.
		line := verdictLine(r.Body)
		v.Value = contract.AutomatedReviewRE.FindStringSubmatch(line)[1]
		v.Head = firstGroup(contract.AutomatedHeadRE, line, oid)
	case contract.VerdictRE.MatchString(r.Body):
		v.Value = contract.VerdictRE.FindStringSubmatch(r.Body)[1]
		v.Head = firstGroup(contract.ReviewedHeadRE, r.Body, oid)
	default:
		return Verdict{}, false
	}
	return v, true
}

// verdictLine returns the last line of body that holds an "Automated
// review:" verdict.
func verdictLine(body string) string {
	locs := contract.AutomatedReviewRE.FindAllStringIndex(body, -1)
	at := locs[len(locs)-1][0]
	start := strings.LastIndexByte(body[:at], '\n') + 1
	end := len(body)
	if i := strings.IndexByte(body[at:], '\n'); i >= 0 {
		end = at + i
	}
	return body[start:end]
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
	return HumanVerificationAt(p, p.HeadRefOid)
}

// HumanVerificationAt returns the procedure in the newest review that says
// READY_FOR_HUMAN about head, a commit the pull request had, or nil.
func HumanVerificationAt(p PR, at string) *Procedure {
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
		if head == "" || !strings.HasPrefix(at, head) {
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

// BodyVerification reads the Human verification section that work-ticket
// writes into the pull request body. It stands in for review-pr's procedure
// when the review bot cleared the head and no review-pr review exists.
func BodyVerification(p PR) *Procedure {
	section, steps, ok := ParseProcedure(p.Body)
	if !ok {
		return nil
	}
	return &Procedure{Head: p.HeadRefOid, Section: section, Steps: steps}
}

var stepRE = regexp.MustCompile(`^( {0,3})(\d+)[.)][ \t]+(.*)$`)

// ParseProcedure reads the Human verification section of a review body: the
// text up to the next level-two heading, and its numbered steps. A step's
// indented or wrapped lines belong to it, and so does a numbered list
// indented to its text, as Markdown nests one; a blank line followed by
// text that is neither indented nor numbered, such as a cleanup paragraph,
// ends it.
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
	// col is where the last step's text starts; a numbered line indented
	// that far is a list inside it.
	col := 0
	for _, l := range lines {
		if m := stepRE.FindStringSubmatchIndex(l); m != nil && (len(steps) == 0 || m[3]-m[2] < col) {
			n, _ := strconv.Atoi(l[m[4]:m[5]])
			steps = append(steps, Step{N: n, Text: strings.TrimSpace(l[m[6]:m[7]])})
			col, blank = m[6], false
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

// Verification is a verify-pr report: the head it ran the human verification
// steps on, the outcome, and each step's result.
type Verification struct {
	Value string `json:"verification"`
	Head  string `json:"head"`
	// AtHead says the report is about the pull request's current head, and
	// Carried that it is about an older one that only clean merges of the
	// base separate from it, which the caller checks with git. The report
	// holds for the head in either case.
	AtHead      bool         `json:"at_head"`
	Carried     bool         `json:"carried"`
	SubmittedAt string       `json:"submitted_at"`
	Steps       []StepResult `json:"steps"`
}

// Holds reports whether the report counts for the pull request's head.
func (v *Verification) Holds() bool { return v != nil && (v.AtHead || v.Carried) }

// StepResult is one step of a verify-pr report: PASSED, FAILED or NOT RUN,
// with what verify-pr saw.
type StepResult struct {
	N      int    `json:"n"`
	Result string `json:"result"`
	Text   string `json:"text"`
}

// LatestVerification returns the newest verify-pr report on a pull request,
// or nil.
func LatestVerification(p PR) *Verification {
	var best *Verification
	for _, r := range p.Reviews {
		v, ok := ReviewVerification(r)
		if !ok || best != nil && best.SubmittedAt > v.SubmittedAt {
			continue
		}
		best = &v
	}
	if best != nil {
		best.AtHead = best.Head != "" && strings.HasPrefix(p.HeadRefOid, best.Head)
	}
	return best
}

// ReviewVerification reads a verify-pr report from a review. The head
// defaults to the commit the review was submitted on.
func ReviewVerification(r Review) (Verification, bool) {
	m := contract.VerificationRE.FindStringSubmatch(r.Body)
	if m == nil {
		return Verification{}, false
	}
	oid := ""
	if r.Commit != nil {
		oid = r.Commit.Oid
	}
	v := Verification{Value: m[1], Head: firstGroup(contract.VerifiedHeadRE, r.Body, oid), SubmittedAt: r.SubmittedAt, Steps: []StepResult{}}
	for _, s := range contract.VerifiedStepRE.FindAllStringSubmatch(r.Body, -1) {
		n, _ := strconv.Atoi(s[1])
		v.Steps = append(v.Steps, StepResult{N: n, Result: s[2], Text: strings.TrimSpace(s[3])})
	}
	return v, true
}

// Acceptance is a review the user accepted as it is: the head it names,
// the reason, and the comment that says so.
type Acceptance struct {
	Head      string `json:"head"`
	Reason    string `json:"reason"`
	URL       string `json:"url"`
	CreatedAt string `json:"created_at"`
	// AtHead and Carried say what they say of a Verification: the
	// acceptance is about the head, or about an older one that only clean
	// merges of the base separate from it, which the caller checks.
	AtHead  bool `json:"at_head"`
	Carried bool `json:"carried"`
}

// Holds reports whether the acceptance counts for the pull request's head.
func (a *Acceptance) Holds() bool { return a != nil && (a.AtHead || a.Carried) }

// LatestAcceptance returns the newest acceptance on a pull request, or nil.
// A comment without a reason accepts nothing, as the skills read it.
func LatestAcceptance(p PR) *Acceptance {
	var best *Acceptance
	for _, c := range p.Comments {
		if !contract.AcceptedHeadingRE.MatchString(c.Body) {
			continue
		}
		head, reason := firstGroup(contract.AcceptedHeadRE, c.Body, ""), firstGroup(contract.AcceptedReasonRE, c.Body, "")
		if head == "" || reason == "" || best != nil && best.CreatedAt > c.CreatedAt {
			continue
		}
		best = &Acceptance{Head: head, Reason: reason, URL: c.URL, CreatedAt: c.CreatedAt}
	}
	if best != nil {
		best.AtHead = strings.HasPrefix(p.HeadRefOid, best.Head)
	}
	return best
}

// The lines of tkt verified's comment, which tkt writes and reads back.
// The skills read past it: it holds no status or verdict line.
const (
	ConfirmedHeading  = "Human verification passed"
	ConfirmedEvidence = "confirmed from verify-pr's evidence"
)

var (
	confirmedHeadingRE = regexp.MustCompile(`(?m)^` + ConfirmedHeading + `\b`)
	confirmedHeadRE    = regexp.MustCompile(`passed on head ([0-9a-f]{7,40})`)
)

// Confirmation is tkt verified's comment: every human verification step
// passed on Head, and Evidence says you confirmed them from verify-pr's
// evidence.
type Confirmation struct {
	Head      string `json:"head"`
	Evidence  bool   `json:"evidence"`
	URL       string `json:"url"`
	CreatedAt string `json:"created_at"`
	// AtHead and Carried say what they say of a Verification.
	AtHead  bool `json:"at_head"`
	Carried bool `json:"carried"`
}

// Holds reports whether the confirmation counts for the pull request's
// head.
func (c *Confirmation) Holds() bool { return c != nil && (c.AtHead || c.Carried) }

// LatestConfirmation returns the newest tkt verified comment on a pull
// request, or nil.
func LatestConfirmation(p PR) *Confirmation {
	var best *Confirmation
	for _, c := range p.Comments {
		if !confirmedHeadingRE.MatchString(c.Body) {
			continue
		}
		head := firstGroup(confirmedHeadRE, c.Body, "")
		if head == "" || best != nil && best.CreatedAt > c.CreatedAt {
			continue
		}
		best = &Confirmation{Head: head, Evidence: strings.Contains(c.Body, ConfirmedEvidence), URL: c.URL, CreatedAt: c.CreatedAt}
	}
	if best != nil {
		best.AtHead = strings.HasPrefix(p.HeadRefOid, best.Head)
	}
	return best
}
