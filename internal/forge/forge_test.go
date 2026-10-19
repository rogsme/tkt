package forge

import (
	"encoding/json"
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
