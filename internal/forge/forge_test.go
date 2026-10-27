package forge

import (
	"encoding/json"
	"strings"
	"testing"
)

func pr(t *testing.T, js string) PR {
	t.Helper()
	var p PR
	if err := json.Unmarshal([]byte(js), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLatestVerdict(t *testing.T) {
	head := "6f3f46aa71062cb73ab94d0f91993229aab65833"
	p := pr(t, `{"headRefOid":"`+head+`","reviews":[
	  {"body":"**Automated review: FINDINGS** · head `+"`97c7506`"+` · reviewed against ACM-1","submittedAt":"2026-01-01T10:00:00Z","commit":{"oid":"97c7506"}},
	  {"body":"**Automated review: CLEAR** · head `+"`6f3f46a`"+` · reviewed against ACM-1","submittedAt":"2026-01-02T10:00:00Z","commit":{"oid":"`+head+`"}},
	  {"body":"looks fine","submittedAt":"2026-01-03T10:00:00Z","commit":{"oid":"`+head+`"}}]}`)
	v := LatestVerdict(p)
	if v == nil || v.Value != "CLEAR" || !v.AtHead {
		t.Errorf("automated: %+v", v)
	}
	p = pr(t, `{"headRefOid":"`+head+`","reviews":[{"body":"Summary\n\nReviewed head: 1111111111111111111111111111111111111111\nReviewed base: 2222222\nVerdict: CHANGES_REQUESTED\n","submittedAt":"2026-01-02T10:00:00Z","commit":{"oid":"1111111"}}]}`)
	v = LatestVerdict(p)
	if v == nil || v.Value != "CHANGES_REQUESTED" || v.AtHead {
		t.Errorf("review-pr on an old head: %+v", v)
	}
	if v := LatestVerdict(pr(t, `{"reviews":[]}`)); v != nil {
		t.Errorf("no reviews: %+v", v)
	}
}

func TestSummarize(t *testing.T) {
	p := pr(t, `{"statusCheckRollup":[
	  {"name":"tests","status":"COMPLETED","conclusion":"SUCCESS"},
	  {"name":"lint","status":"COMPLETED","conclusion":"FAILURE"},
	  {"context":"deploy","state":"PENDING"}]}`)
	if got := Summarize(p).Text(); got != "fail: lint" {
		t.Errorf("fail: %s", got)
	}
	p = pr(t, `{"statusCheckRollup":[{"name":"tests","status":"IN_PROGRESS","conclusion":null},{"name":"x","status":"COMPLETED","conclusion":"SKIPPED"}]}`)
	if c := Summarize(p); c.State != "pending" || c.Text() != "pending 1" {
		t.Errorf("pending: %+v", c)
	}
	if c := Summarize(pr(t, `{"statusCheckRollup":[]}`)); c.State != "none" || c.Text() != "-" {
		t.Errorf("none: %+v", c)
	}
	if c := Summarize(pr(t, `{"statusCheckRollup":[{"context":"ci/a","state":"SUCCESS"}]}`)); c.Text() != "ok 1" {
		t.Errorf("ok: %+v", c)
	}
}

func TestMine(t *testing.T) {
	g := GH{Name: "acme/app"}
	prs := []PR{
		pr(t, `{"number":1,"headRefName":"ACM-1","headRepository":{"nameWithOwner":"Acme/App"}}`),
		pr(t, `{"number":2,"headRefName":"ACM-1","headRepository":{"nameWithOwner":"fork/app"}}`),
		pr(t, `{"number":3,"headRefName":"ACM-1"}`),
		pr(t, `{"number":4,"headRefName":"ACM-10"}`),
	}
	got := g.Mine(prs, "ACM-1")
	if len(got) != 2 || got[0].Number != 1 || got[1].Number != 3 {
		t.Errorf("got %+v", got)
	}
}

// A numbered list indented to a step's text is that step's, as Markdown
// nests it, so a procedure's steps never repeat.
func TestParseProcedureNestsSubSteps(t *testing.T) {
	body := "## Human verification\n\n1. Open the login page.\n2. Sign in:\n   1. Type the name.\n   2. Press Enter.\n3. Check the banner.\n  4. Two spaces in is still a step.\n"
	_, steps, ok := ParseProcedure(body)
	if !ok || len(steps) != 4 {
		t.Fatalf("steps: %+v", steps)
	}
	for i, n := range []int{1, 2, 3, 4} {
		if steps[i].N != n {
			t.Errorf("step %d is numbered %d", i, steps[i].N)
		}
	}
	if steps[1].Text != "Sign in:\n1. Type the name.\n2. Press Enter." {
		t.Errorf("step 2: %q", steps[1].Text)
	}
}

func TestLatestAcceptance(t *testing.T) {
	head := strings.Repeat("b2", 20)
	accept := func(at, h, reason string) Comment {
		body := "## Review accepted\n\nAccepted head: `" + h + "`\n"
		if reason != "" {
			body += "Reason: " + reason + "\n"
		}
		return Comment{Body: body, CreatedAt: at, URL: "https://github.com/acme/app/pull/7#issuecomment-" + at}
	}
	p := PR{HeadRefOid: head, Comments: []Comment{
		{Body: "Looks fine to me.", CreatedAt: "2026-01-04"},
		accept("2026-01-02", "a1a1a1a", "the wording"),
		accept("2026-01-03", head[:7], "the description"),
		// A comment without a reason accepts nothing.
		accept("2026-01-05", head, ""),
	}}
	a := LatestAcceptance(p)
	if a == nil || a.Head != head[:7] || a.Reason != "the description" || !a.AtHead || !a.Holds() {
		t.Fatalf("LatestAcceptance = %+v", a)
	}
	p.HeadRefOid = strings.Repeat("c3", 20)
	if a := LatestAcceptance(p); a == nil || a.AtHead || a.Holds() {
		t.Errorf("an acceptance of an older head holds: %+v", a)
	}
	if LatestAcceptance(PR{HeadRefOid: head}) != nil {
		t.Errorf("a pull request without comments has an acceptance")
	}
}
