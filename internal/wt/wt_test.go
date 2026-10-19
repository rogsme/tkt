package wt

import "testing"

func TestParseListSchemas(t *testing.T) {
	v1 := `[{"branch":"main","path":"/r","kind":"worktree"},{"branch":null,"path":"/d"},{"branch":"ACM-1","path":"/w/ACM-1"}]`
	es, err := parseList([]byte(v1))
	if err != nil || len(es) != 2 || es[1] != (Entry{Branch: "ACM-1", Path: "/w/ACM-1"}) {
		t.Fatalf("schema 1: %+v, %v", es, err)
	}
	v2 := `{"schema":2,"repo":{},"items":[{"branch":"ACM-2","worktree":{"path":"/w/ACM-2","branch_mismatch":true}},{"branch":"x","worktree":null}]}`
	es, err = parseList([]byte(v2))
	if err != nil || len(es) != 1 || !es[0].Mismatch || es[0].Path != "/w/ACM-2" {
		t.Fatalf("schema 2: %+v, %v", es, err)
	}
	if e, ok := es.Find("ACM-2"); !ok || e.Branch != "ACM-2" {
		t.Error("Find")
	}
	if _, err := parseList([]byte(`{"schema":3}`)); err == nil {
		t.Error("schema 3 was accepted")
	}
}
