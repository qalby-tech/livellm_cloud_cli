package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Database links: refused before anything is sent where the API would refuse
// them, and what still reaches a database once a link is taken out.

func TestLinkRefusesBeforeSending(t *testing.T) {
	g := newGoldenAPI(t, map[string]string{"GET /v1/workspace": insideWorkspace})
	file := func(body string) string {
		f := filepath.Join(t.TempDir(), "x.json")
		if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return f
	}
	refused := []struct {
		name string
		run  func() error
		says string
	}{
		{"no databases", func() error { return cmdLink([]string{"edge"}) }, "which databases"},
		{"only --remove", func() error { return cmdLink([]string{"edge", "--remove"}) }, "which databases"},
		{"a database linking", func() error { return cmdLink([]string{"db", "cache"}) }, "link it from what uses it"},
		{"a browser linking", func() error { return cmdLink([]string{"b1", "db"}) }, "is a browser"},
		{"a Browser API linking", func() error { return cmdLink([]string{"pool", "db"}) }, "is a Browser API"},
		{"nothing there", func() error { return cmdLink([]string{"nothere", "db"}) }, "nothing called"},
		{"no such database", func() error { return cmdLink([]string{"edge", "nodb"}) }, `no database "nodb"`},
		{"not a database", func() error { return cmdLink([]string{"edge", "web"}) }, "web is not a database"},
		{"named twice", func() error { return cmdLink([]string{"edge", "db", "db"}) }, "named twice"},
		{"a flag it doesn't take", func() error { return cmdLink([]string{"edge", "db", "--env", "X"}) }, "not --env"},
		{"reach on a database", func() error { return cmdReach([]string{"db", "--from", "web"}) },
			"reachableFrom: a database is reached only by what links it: add db to the databases of the app, machine or Desktop App that uses it"},
		{"reach * on a database", func() error { return cmdReach([]string{"cache", "--from", "*"}) }, "add cache to the databases"},
		{"reach --none on a database", func() error { return cmdReach([]string{"db", "--none"}) }, "has no setting"},
		{"create database --reachable-from", func() error {
			return cmdCreate([]string{"storage", "-f", file(`{"id":"db3","engine":"postgres"}`), "--reachable-from", "web"})
		}, "add db3 to the databases"},
		{"create database with a setting in the file", func() error {
			return cmdCreate([]string{"storage", "-f", file(`{"id":"db3","engine":"postgres","reachableFrom":["*"]}`)})
		}, "add db3 to the databases"},
		{"create apps with a database's setting", func() error {
			return cmdCreate([]string{"apps", "-f", file(`{"apps":[{"id":"a","image":"x","databases":[{"id":"d"}]}],"databases":[{"id":"d","engine":"redis","reachableFrom":["a"]}]}`)})
		}, "add d to the databases"},
		{"create pod --database", func() error {
			return cmdCreate([]string{"pod", "-f", file(`{"id":"a","image":"x"}`), "--database", "db"})
		}, "--database goes with create vm"},
		{"create browser --database", func() error {
			return cmdCreate([]string{"browser", "--id", "b9", "--database", "db"})
		}, "--database goes with create vm"},
		{"create apps --database", func() error {
			return cmdCreate([]string{"apps", "-f", file(`[{"id":"a","image":"x"}]`), "--database", "db"})
		}, "--database goes with create vm"},
		{"a machine's link with variables", func() error {
			return cmdCreate([]string{"vm-ubuntu", "-f", file(`{"id":"m","databases":[{"id":"db","env":{"X":"url"}}]}`)})
		}, "databases[0] (db): a machine gets no variables from a link: it only lets it reach the database"},
		{"a Desktop App's link with variables", func() error {
			return cmdCreate([]string{"desktop", "-f", file(`{"id":"d","databases":[{"id":"db","env":{"X":"url"}}]}`)})
		}, "a Desktop App gets no variables"},
		{"a machine linking one twice", func() error {
			return cmdCreate([]string{"vm-ubuntu", "-f", file(`{"id":"m","databases":[{"id":"db"}]}`), "--database", "db"})
		}, "linked twice"},
	}
	for _, c := range refused {
		err := c.run()
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %v (want %q)", c.name, err, c.says)
		}
	}
	if len(g.writes) != 0 {
		t.Errorf("sent %v", g.writes)
	}
}

func TestLinkAtMostEight(t *testing.T) {
	ws := `{"name":"ws","spec":{"workloads":[{"id":"a","type":"pod","pod":{"databases":[` +
		`{"id":"d1"},{"id":"d2"},{"id":"d3"},{"id":"d4"},{"id":"d5"},{"id":"d6"},{"id":"d7"},{"id":"d8"}]}}`
	for i := 1; i <= 9; i++ {
		ws += `,{"id":"d` + string(rune('0'+i)) + `","type":"storage","storage":{"engine":"redis"}}`
	}
	ws += `]}}`
	g := newGoldenAPI(t, map[string]string{"GET /v1/workspace": ws})
	if err := cmdLink([]string{"a", "d9"}); err == nil || !strings.Contains(err.Error(), "at most 8") {
		t.Errorf("a ninth link: %v", err)
	}
	if len(g.writes) != 0 {
		t.Errorf("sent %v", g.writes)
	}
}

// Taking a link out keeps the others as they were (variables and all), and
// says what still reaches the database: a wait, or another service of the
// same Composable App linking it.
func TestLinkRemoveSaysWhatStillReaches(t *testing.T) {
	ws := `{"name":"ws","spec":{"workloads":[
		{"id":"api","type":"pod","pod":{"stack":"s","dependsOn":["cache"],"databases":[{"id":"cache"},{"id":"pg","env":{"DB":"url"}}]}},
		{"id":"api2","type":"pod","pod":{"stack":"s","databases":[{"id":"cache"}]}},
		{"id":"cache","type":"storage","storage":{"engine":"redis"}},
		{"id":"pg","type":"storage","storage":{"engine":"postgres"}}]}}`
	g := newGoldenAPI(t, map[string]string{"GET /v1/workspace": ws})
	out, _, err := captured(t, func() error { return cmdLink([]string{"api", "--remove", "cache"}) })
	if err != nil {
		t.Fatal(err)
	}
	if len(g.writes) != 1 || g.writes[0] != `PATCH /v1/workloads/api {"pod":{"databases":[{"env":{"DB":"url"},"id":"pg"}]}}` {
		t.Errorf("sent %v", g.writes)
	}
	m := printed(t, out)
	note, _ := m["note"].(string)
	if !strings.Contains(note, "api still waits for cache") || !strings.Contains(note, "still reached: api2 of s links cache") {
		t.Errorf("note %q", note)
	}
	if m["app"] != "s" {
		t.Errorf("app %v", m["app"])
	}
	// The last link out: an empty list is sent, not a missing one.
	g.writes = nil
	if _, _, err := captured(t, func() error { return cmdLink([]string{"api2", "cache", "--remove"}) }); err != nil {
		t.Fatal(err)
	}
	if len(g.writes) != 1 || g.writes[0] != `PATCH /v1/workloads/api2 {"pod":{"databases":[]}}` {
		t.Errorf("sent %v", g.writes)
	}
}
