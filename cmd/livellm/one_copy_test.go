package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The API's words when a resource of more than one copy asks for dedicated
// cores (owner, 2026-10-24).
const msgOneCopy = "Dedicated cores work with one copy for now: use 1 copy, or shared cores."

// Dedicated cores work with one copy for now: the API's 422
// dedicated_one_copy is passed on in its words alone (they say what to do),
// with nothing made up to run again, and exits 1 like any value refused. The
// request goes as asked: the CLI judges no copies itself.
func TestDedicatedOneCopyPassedOn(t *testing.T) {
	refused := `{"error":"` + msgOneCopy + `","code":"dedicated_one_copy","workload":"web"}`
	err := coresNext(problemFrom(422, []byte(refused)), "web", func() int { return 1 }, coresAgain{})
	var p *problem
	if !asProblem(err, &p) {
		t.Fatalf("422 dedicated_one_copy: not a problem: %v", err)
	}
	if p.Msg != msgOneCopy || p.Next != "" || exitCode(err) != 1 {
		t.Errorf("422 dedicated_one_copy: %v (next %q, exit %d)", err, p.Next, exitCode(err))
	}

	dir := t.TempDir()
	web := filepath.Join(dir, "web.json")
	_ = os.WriteFile(web, []byte(`{"id":"web","image":"nginx","cpu":"2","replicas":2}`), 0o600)
	f := newFakeAPI(t)
	f.answers["POST /v1/workloads/pod"] = answerStatus(422, refused)
	err = cmdCreate([]string{"pod", "-f", web, "--dedicated-cores"})
	if err == nil || err.Error() != msgOneCopy+" (422)" || exitCode(err) != 1 {
		t.Errorf("create, 2 replicas dedicated: %v (exit %d)", err, exitCode(err))
	}
	if w := writesOf(f); len(w) != 1 || bodyOf(t, w[0])["replicas"] != float64(2) || bodyOf(t, w[0])["dedicatedCores"] != true {
		t.Errorf("create: sent as asked: %v", w)
	}

	f = newFakeAPI(t)
	f.answers["GET /v1/workspace"] = answerJSON(`{"spec":{"workloads":[{"id":"web","type":"pod","pod":{"image":"nginx","cpu":"2","replicas":2}}]}}`)
	f.answers["PATCH /v1/workloads/web"] = answerStatus(422, refused)
	err = cmdSet([]string{"web", "--dedicated-cores"})
	if err == nil || err.Error() != msgOneCopy+" (422)" || exitCode(err) != 1 {
		t.Errorf("set, 2 replicas dedicated: %v (exit %d)", err, exitCode(err))
	}
	if err != nil && strings.Contains(err.Error(), "run it again") {
		t.Errorf("set: a next step made up: %v", err)
	}
}
