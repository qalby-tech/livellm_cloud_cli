package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The owner's words for the four refusals, as the API sends them.
const (
	msgSpeedShort   = "Only 6 cores at 5.7 GHz are available. Continue with mixed core speeds?"
	msgSpeedsMixed  = "Mixed core speeds: this machine's cores run at different clock speeds."
	msgSpeedUnknown = "No server this machine may run on has cores at 3 GHz."
	msgServerFull   = "This server is full: it has no room for this machine."
)

func answerStatus(status int, body string) func(http.ResponseWriter, *http.Request, []byte) {
	return func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

// create sends the cores flags flat, beside cpus, by the API's names, each
// in place of the file's value; only a flag given is sent.
func TestCreateMachineCores(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		_ = os.WriteFile(p, []byte(body), 0o600)
		return p
	}
	box := write("box.json", `{"id":"box","cpus":8,"memory":"16Gi"}`)
	dedicated := write("dedicated.json", `{"id":"box","cpus":8,"dedicatedCores":true}`)

	for _, c := range []struct {
		name string
		args []string
		want map[string]any
	}{
		{"dedicated", []string{"vm-ubuntu", "-f", box, "--dedicated-cores"},
			map[string]any{"id": "box", "cpus": float64(8), "memory": "16Gi", "dedicatedCores": true}},
		{"a speed in GHz", []string{"vm-ubuntu", "-f", box, "--dedicated-cores", "--core-speed", "5.7GHz"},
			map[string]any{"id": "box", "cpus": float64(8), "memory": "16Gi", "dedicatedCores": true, "coreSpeed": float64(5700)}},
		{"a speed in GHz with a space", []string{"vm-windows", "-f", box, "--dedicated-cores", "--core-speed", "4.4 GHz"},
			map[string]any{"id": "box", "cpus": float64(8), "memory": "16Gi", "dedicatedCores": true, "coreSpeed": float64(4400)}},
		{"a speed in MHz", []string{"vm-ubuntu-desktop", "-f", box, "--dedicated-cores", "--core-speed", "5700"},
			map[string]any{"id": "box", "cpus": float64(8), "memory": "16Gi", "dedicatedCores": true, "coreSpeed": float64(5700)}},
		{"a speed with MHz", []string{"vm-ubuntu", "-f", box, "--dedicated-cores", "--core-speed", "5700MHz"},
			map[string]any{"id": "box", "cpus": float64(8), "memory": "16Gi", "dedicatedCores": true, "coreSpeed": float64(5700)}},
		{"any one speed", []string{"vm-ubuntu", "-f", box, "--dedicated-cores", "--core-speed", "any"},
			map[string]any{"id": "box", "cpus": float64(8), "memory": "16Gi", "dedicatedCores": true, "coreSpeed": float64(0)}},
		{"mixed speeds", []string{"vm-ubuntu", "-f", box, "--dedicated-cores", "--mixed-speeds"},
			map[string]any{"id": "box", "cpus": float64(8), "memory": "16Gi", "dedicatedCores": true, "mixedSpeeds": true}},
		{"the cores that fit, in place of the file's", []string{"vm-ubuntu", "-f", box, "--dedicated-cores", "--cores", "6"},
			map[string]any{"id": "box", "cpus": float64(6), "memory": "16Gi", "dedicatedCores": true}},
		{"the file is dedicated already", []string{"vm-ubuntu", "-f", dedicated, "--core-speed", "5.7GHz"},
			map[string]any{"id": "box", "cpus": float64(8), "dedicatedCores": true, "coreSpeed": float64(5700)}},
		{"shared, in place of the file's", []string{"vm-ubuntu", "-f", dedicated, "--dedicated-cores=false"},
			map[string]any{"id": "box", "cpus": float64(8), "dedicatedCores": false}},
		{"left out: the file as written", []string{"vm-ubuntu", "-f", box},
			map[string]any{"id": "box", "cpus": float64(8), "memory": "16Gi"}},
	} {
		f := newFakeAPI(t)
		if err := cmdCreate(c.args); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		w := writesOf(f)
		if len(w) != 1 || w[0].method != "POST" || w[0].path != "/v1/workloads/"+c.args[0] {
			t.Fatalf("%s: sent %v", c.name, w)
		}
		if got := bodyOf(t, w[0]); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: sent %v, want %v", c.name, got, c.want)
		}
	}

	for name, c := range map[string]struct {
		args []string
		says string
	}{
		"a speed on shared cores":   {[]string{"vm-ubuntu", "-f", box, "--core-speed", "5.7GHz"}, "--core-speed goes with dedicated cores: add --dedicated-cores"},
		"mixed speeds on shared":    {[]string{"vm-ubuntu", "-f", box, "--mixed-speeds"}, "--mixed-speeds goes with dedicated cores"},
		"a speed, then shared":      {[]string{"vm-ubuntu", "-f", dedicated, "--dedicated-cores=false", "--core-speed", "5700"}, "--core-speed goes with dedicated cores"},
		"a bare GHz":                {[]string{"vm-ubuntu", "-f", box, "--dedicated-cores", "--core-speed", "5.7"}, "--core-speed takes a clock in GHz or MHz"},
		"a word":                    {[]string{"vm-ubuntu", "-f", box, "--dedicated-cores", "--core-speed", "fast"}, "--core-speed takes a clock in GHz or MHz"},
		"no cores":                  {[]string{"vm-ubuntu", "-f", box, "--cores", "0"}, "--cores is a whole number of cores"},
		"on an app":                 {[]string{"pod", "-f", box, "--dedicated-cores"}, "--dedicated-cores goes with a machine"},
		"on a Desktop App":          {[]string{"desktop", "-f", box, "--cores", "2"}, "--cores goes with a machine"},
		"on a browser by id":        {[]string{"browser", "--id", "b", "--mixed-speeds"}, "--mixed-speeds goes with a machine"},
		"on several apps":           {[]string{"apps", "-f", box, "--core-speed", "5700"}, "--core-speed goes with a machine"},
		"on a database, first flag": {[]string{"storage", "-f", box, "--mixed-speeds", "--dedicated-cores"}, "--dedicated-cores goes with a machine"},
		// flag reads `--dedicated-cores false` as true and stops at "false"
		"a yes-or-no after a space": {[]string{"vm-ubuntu", "-f", box, "--dedicated-cores", "false", "--core-speed", "5700"}, `"false" is left over: a yes-or-no flag takes its value after =`},
	} {
		f := newFakeAPI(t)
		err := cmdCreate(c.args)
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %v, want it to say %q", name, err, c.says)
		}
		if w := writesOf(f); len(w) != 0 {
			t.Errorf("%s: sent %v", name, w)
		}
	}
}

// set puts the cores flags in the machine's own block, beside what the file
// holds there and in place of its values, with no file at all too.
func TestSetMachineCores(t *testing.T) {
	dir := t.TempDir()
	more := filepath.Join(dir, "more.json")
	_ = os.WriteFile(more, []byte(`{"vm":{"cpus":8,"memory":"16Gi"},"reachableFrom":["web"]}`), 0o600)
	const ws = `{"spec":{"workloads":[
		{"id":"box","type":"vm-ubuntu","vm":{"cpus":4}},
		{"id":"fast","type":"vm-windows","vm":{"cpus":8,"dedicatedCores":true,"coreSpeed":5700}},
		{"id":"floats","type":"vm-ubuntu","vm":{"cpus":20,"dedicatedCores":true,"mixedSpeeds":true}},
		{"id":"web","type":"pod","pod":{"image":"nginx"}}]}}`

	for _, c := range []struct {
		name string
		args []string
		want map[string]any
	}{
		{"dedicated, no file", []string{"box", "--dedicated-cores"},
			map[string]any{"vm": map[string]any{"dedicatedCores": true}}},
		{"the cores that fit, beside the file's", []string{"box", "-f", more, "--cores", "6", "--dedicated-cores"},
			map[string]any{"vm": map[string]any{"cpus": float64(6), "memory": "16Gi", "dedicatedCores": true}, "reachableFrom": []any{"web"}}},
		{"another speed on a dedicated machine", []string{"fast", "--core-speed", "4.4GHz"},
			map[string]any{"vm": map[string]any{"coreSpeed": float64(4400)}}},
		{"mixed speeds on a dedicated machine", []string{"fast", "--mixed-speeds"},
			map[string]any{"vm": map[string]any{"mixedSpeeds": true}}},
		{"any one speed again", []string{"fast", "--core-speed", "any", "--mixed-speeds=false"},
			map[string]any{"vm": map[string]any{"coreSpeed": float64(0), "mixedSpeeds": false}}},
		{"shared again", []string{"fast", "--dedicated-cores=false"},
			map[string]any{"vm": map[string]any{"dedicatedCores": false}}},
		// A new speed on a machine kept on mixed speeds gives them up, as the
		// console's does; with --mixed-speeds, or more cores alone, they stay.
		{"a speed gives up mixed speeds", []string{"floats", "--core-speed", "4.4GHz"},
			map[string]any{"vm": map[string]any{"coreSpeed": float64(4400), "mixedSpeeds": false}}},
		{"any one speed gives them up too", []string{"floats", "--core-speed", "any"},
			map[string]any{"vm": map[string]any{"coreSpeed": float64(0), "mixedSpeeds": false}}},
		{"a speed, mixed speeds kept", []string{"floats", "--core-speed", "4.4GHz", "--mixed-speeds"},
			map[string]any{"vm": map[string]any{"coreSpeed": float64(4400), "mixedSpeeds": true}}},
		{"more cores keep mixed speeds", []string{"floats", "--cores", "24"},
			map[string]any{"vm": map[string]any{"cpus": float64(24)}}},
		{"cores alone on a shared machine", []string{"box", "--cores", "2"},
			map[string]any{"vm": map[string]any{"cpus": float64(2)}}},
		{"a file alone: as written", []string{"box", "-f", more},
			map[string]any{"vm": map[string]any{"cpus": float64(8), "memory": "16Gi"}, "reachableFrom": []any{"web"}}},
	} {
		f := newFakeAPI(t)
		f.answers["GET /v1/workspace"] = answerJSON(ws)
		_, stderr, err := captured(t, func() error { return cmdSet(c.args) })
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		w := writesOf(f)
		if len(w) != 1 || w[0].method != "PATCH" || w[0].path != "/v1/workloads/"+c.args[0] {
			t.Fatalf("%s: sent %v", c.name, w)
		}
		if got := bodyOf(t, w[0]); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: sent %v, want %v", c.name, got, c.want)
		}
		// Every cores flag takes effect at the next restart, --cores alone
		// too; a file alone says nothing.
		cores := false
		for _, a := range c.args {
			cores = cores || strings.HasPrefix(a, "--")
		}
		if note := strings.Contains(stderr, "next restart (livellm restart "+c.args[0]+")"); note != cores {
			t.Errorf("%s: stderr %q", c.name, stderr)
		}
	}

	for name, c := range map[string]struct {
		args []string
		says string
	}{
		"mixed speeds on a shared machine": {[]string{"box", "--mixed-speeds"}, "--mixed-speeds goes with dedicated cores: add --dedicated-cores"},
		"a speed on a shared machine":      {[]string{"box", "--core-speed", "5.7GHz"}, "--core-speed goes with dedicated cores"},
		"on an app":                        {[]string{"web", "--cores", "2"}, "--cores goes with a machine"},
		"nothing":                          {[]string{"box"}, "pass the changes with -f"},
		"a yes-or-no after a space":        {[]string{"fast", "--dedicated-cores", "false", "--cores", "2"}, `"false" is left over`},
		"no such machine":                  {[]string{"gone", "--dedicated-cores"}, `there is nothing called "gone" here`},
	} {
		f := newFakeAPI(t)
		f.answers["GET /v1/workspace"] = answerJSON(ws)
		err := cmdSet(c.args)
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %v, want it to say %q", name, err, c.says)
		}
		if w := writesOf(f); len(w) != 0 {
			t.Errorf("%s: sent %v", name, w)
		}
	}
}

// The four refusals keep the API's (the owner's) words; the two 409s about
// speeds say what to run again: the cores that fit, or --mixed-speeds.
func TestCoresRefusalsSayWhatNext(t *testing.T) {
	cases := []struct {
		status    int
		body      string
		wantMsg   string
		wantNext  string
		wantNoUse bool
	}{
		{409, `{"error":"` + msgSpeedShort + `","code":"core_speed_short","workload":"box","cores":8,"fit":6,"speedMHz":5700}`,
			msgSpeedShort, "use 6 cores: run it again with --cores 6; or continue with mixed speeds (8 cores): run it again with --mixed-speeds", false},
		{409, `{"error":"` + msgSpeedsMixed + `","code":"core_speeds_mixed","workload":"box","cores":20,"fit":16}`,
			msgSpeedsMixed, "use 16 cores: run it again with --cores 16; or continue with mixed speeds (20 cores): run it again with --mixed-speeds", false},
		// Nothing of that speed is free: only the count at mixed speeds.
		{409, `{"error":"Only 0 cores at 5.7 GHz are available. Continue with mixed core speeds?","code":"core_speed_short","workload":"box","cores":8,"fit":0,"speedMHz":5700}`,
			"Only 0 cores", "continue with mixed speeds (8 cores): run it again with --mixed-speeds", true},
		// One core: one core, as the console says it.
		{409, `{"error":"Only 1 core at 5.7 GHz is available. Continue with mixed core speeds?","code":"core_speed_short","workload":"box","cores":4,"fit":1,"speedMHz":5700}`,
			"Only 1 core", "use 1 core: run it again with --cores 1; or continue with mixed speeds (4 cores): run it again with --mixed-speeds", false},
		{409, `{"error":"` + msgSpeedsMixed + `","code":"core_speeds_mixed","workload":"box","cores":1,"fit":0}`,
			msgSpeedsMixed, "continue with mixed speeds (1 core): run it again with --mixed-speeds", true},
		// The same in the API's words for none free, and with fit left out:
		// never "use 0 cores".
		{409, `{"error":"No cores at 5.7 GHz are available. Continue with mixed core speeds?","code":"core_speed_short","workload":"box","cores":8,"fit":0,"speedMHz":5700}`,
			"No cores at 5.7 GHz", "continue with mixed speeds (8 cores): run it again with --mixed-speeds", true},
		{409, `{"error":"` + msgSpeedsMixed + `","code":"core_speeds_mixed","workload":"box","cores":20}`,
			msgSpeedsMixed, "continue with mixed speeds (20 cores): run it again with --mixed-speeds", true},
		{422, `{"error":"` + msgSpeedUnknown + `","code":"core_speed_unknown","workload":"box","cores":8,"fit":0,"speedMHz":3000}`,
			msgSpeedUnknown, "livellm cores lists the speeds", true},
		{409, `{"error":"` + msgServerFull + `","code":"server_full","workload":"box"}`, msgServerFull, "", true},
		{409, `{"error":"No server has room for this machine.","code":"server_full","workload":"box"}`, "No server has room for this machine.", "", true},
		// A refusal that names another machine: its words alone, for each
		// code (a run again would change this one).
		{409, `{"error":"` + msgSpeedShort + `","code":"core_speed_short","workload":"other","cores":8,"fit":6,"speedMHz":5700}`, msgSpeedShort, "", true},
		{409, `{"error":"` + msgSpeedsMixed + `","code":"core_speeds_mixed","workload":"other","cores":20,"fit":16}`, msgSpeedsMixed, "", true},
		{422, `{"error":"` + msgSpeedUnknown + `","code":"core_speed_unknown","workload":"other","cores":8,"fit":0,"speedMHz":3000}`, msgSpeedUnknown, "", true},
		// No machine named: what to run again, as before.
		{409, `{"error":"` + msgSpeedsMixed + `","code":"core_speeds_mixed","cores":20,"fit":16}`, msgSpeedsMixed, "use 16 cores: run it again with --cores 16", false},
		// The API named the next step: it stays.
		{409, `{"error":"` + msgSpeedsMixed + `","code":"core_speeds_mixed","cores":20,"fit":16,"next":"theirs"}`, msgSpeedsMixed, "theirs", true},
		// Not a cores refusal: untouched.
		{409, `{"error":"workload box already exists"}`, "already exists", "", true},
	}
	for _, c := range cases {
		err := coresNext(problemFrom(c.status, []byte(c.body)), "box", func() int { return 1 })
		var p *problem
		if !asProblem(err, &p) {
			t.Fatalf("%s: not a problem: %v", c.body, err)
		}
		if !strings.Contains(p.Msg, c.wantMsg) {
			t.Errorf("%s: message %q", c.body, p.Msg)
		}
		if c.wantNext == "" && p.Next != "" || !strings.Contains(p.Next, c.wantNext) {
			t.Errorf("%s: next %q, want %q", c.body, p.Next, c.wantNext)
		}
		if c.wantNoUse && (strings.Contains(p.Next, "use ") || strings.Contains(p.Next, "--cores 0")) {
			t.Errorf("%s: offers cores that don't fit: %q", c.body, p.Next)
		}
		if exitCode(err) != map[int]int{422: 1, 409: 4}[c.status] {
			t.Errorf("%s: exit %d", c.body, exitCode(err))
		}
	}

	// Another refusal's field of the same name never costs it its words.
	p := problemFrom(402, []byte(`{"error":"the plan is full","code":"payg_off","cores":{"used":2}}`))
	if p.Msg != "the plan is full" || p.Cores != nil {
		t.Errorf("%+v", p)
	}

	// Never fewer cores than the machine runs on: 1 that fits is not offered
	// to a machine that needs 2, 2 are; the floor is asked only when cores
	// fit.
	one := `{"error":"Only 1 core at 5.7 GHz is available. Continue with mixed core speeds?","code":"core_speed_short","workload":"box","cores":4,"fit":1,"speedMHz":5700}`
	for _, c := range []struct {
		body      string
		floor     int
		wantNext  string
		wantNoUse bool
		asked     bool
	}{
		{one, 2, "continue with mixed speeds (4 cores): run it again with --mixed-speeds", true, true},
		{`{"error":"` + msgSpeedsMixed + `","code":"core_speeds_mixed","workload":"box","cores":4,"fit":1}`, 2,
			"continue with mixed speeds (4 cores): run it again with --mixed-speeds", true, true},
		{one, 1, "use 1 core: run it again with --cores 1", false, true},
		{`{"error":"Only 2 cores at 5.7 GHz are available. Continue with mixed core speeds?","code":"core_speed_short","workload":"box","cores":4,"fit":2,"speedMHz":5700}`, 2,
			"use 2 cores: run it again with --cores 2", false, true},
		{`{"error":"` + msgSpeedsMixed + `","code":"core_speeds_mixed","workload":"box","cores":4,"fit":0}`, 2,
			"continue with mixed speeds (4 cores)", true, false},
		{`{"error":"` + msgSpeedUnknown + `","code":"core_speed_unknown","workload":"box","cores":4,"fit":0,"speedMHz":3000}`, 2,
			"livellm cores lists the speeds", true, false},
	} {
		asked := false
		err := coresNext(problemFrom(409, []byte(c.body)), "box", func() int { asked = true; return c.floor })
		var p *problem
		if !asProblem(err, &p) || !strings.Contains(p.Next, c.wantNext) {
			t.Errorf("floor %d, %s: next %v, want %q", c.floor, c.body, err, c.wantNext)
			continue
		}
		if c.wantNoUse && (strings.Contains(p.Next, "use ") || strings.Contains(p.Next, "--cores ")) {
			t.Errorf("floor %d, %s: offers fewer cores than the machine runs on: %q", c.floor, c.body, p.Next)
		}
		if asked != c.asked {
			t.Errorf("floor %d, %s: floor asked %v", c.floor, c.body, asked)
		}
	}

	for _, c := range []struct {
		kind  string
		vm    map[string]any
		floor int
	}{
		{"vm-ubuntu", nil, 1}, {"vm-ubuntu", map[string]any{"cpus": float64(1)}, 1},
		{"vm-ubuntu-desktop", nil, 2}, {"vm-windows", nil, 2},
		{"vm-windows", map[string]any{"windowsEdition": "desktop"}, 2},
		{"vm-windows", map[string]any{"windowsEdition": "server"}, 1},
		{"", nil, 2},
	} {
		if got := coreFloor(c.kind, c.vm); got != c.floor {
			t.Errorf("coreFloor(%q, %v) = %d, want %d", c.kind, c.vm, got, c.floor)
		}
	}
}

// A create or a set refused for its cores prints the owner's words and what
// to run again.
func TestCreateAndSetRefusedForCores(t *testing.T) {
	dir := t.TempDir()
	box := filepath.Join(dir, "box.json")
	_ = os.WriteFile(box, []byte(`{"id":"box","cpus":8}`), 0o600)
	short := `{"error":"` + msgSpeedShort + `","code":"core_speed_short","workload":"box","cores":8,"fit":6,"speedMHz":5700}`

	f := newFakeAPI(t)
	f.answers["POST /v1/workloads/vm-ubuntu"] = answerStatus(409, short)
	err := cmdCreate([]string{"vm-ubuntu", "-f", box, "--dedicated-cores", "--core-speed", "5.7GHz"})
	if err == nil || !strings.Contains(err.Error(), msgSpeedShort+" (409) — use 6 cores: run it again with --cores 6") {
		t.Errorf("create: %v", err)
	}

	// The API trims the id it makes and names the machine so: still this one.
	spaced := filepath.Join(dir, "spaced.json")
	_ = os.WriteFile(spaced, []byte(`{"id":" box ","cpus":8}`), 0o600)
	f = newFakeAPI(t)
	f.answers["POST /v1/workloads/vm-ubuntu"] = answerStatus(409, short)
	err = cmdCreate([]string{"vm-ubuntu", "-f", spaced, "--dedicated-cores", "--core-speed", "5.7GHz"})
	if err == nil || !strings.Contains(err.Error(), "use 6 cores: run it again with --cores 6") {
		t.Errorf("create, id with spaces: %v", err)
	}

	f = newFakeAPI(t)
	f.answers["GET /v1/workspace"] = answerJSON(`{"spec":{"workloads":[{"id":"box","type":"vm-ubuntu","vm":{"cpus":20}}]}}`)
	f.answers["PATCH /v1/workloads/box"] = answerStatus(409, `{"error":"`+msgSpeedsMixed+`","code":"core_speeds_mixed","workload":"box","cores":20,"fit":16}`)
	err = cmdSet([]string{"box", "--dedicated-cores"})
	if err == nil || !strings.Contains(err.Error(), msgSpeedsMixed+" (409) — use 16 cores: run it again with --cores 16; or continue with mixed speeds (20 cores): run it again with --mixed-speeds") {
		t.Errorf("set: %v", err)
	}

	// A set of box refused for another machine's cores offers nothing to run
	// again: --cores or --mixed-speeds would change box.
	f = newFakeAPI(t)
	f.answers["GET /v1/workspace"] = answerJSON(`{"spec":{"workloads":[{"id":"box","type":"vm-ubuntu","vm":{"cpus":20}}]}}`)
	f.answers["PATCH /v1/workloads/box"] = answerStatus(409, `{"error":"`+msgSpeedsMixed+`","code":"core_speeds_mixed","workload":"other","cores":20,"fit":16}`)
	err = cmdSet([]string{"box", "--dedicated-cores"})
	if err == nil || !strings.Contains(err.Error(), msgSpeedsMixed) || strings.Contains(err.Error(), "run it again") {
		t.Errorf("set, another machine: %v", err)
	}
}

// A cores refusal never offers fewer cores than the machine runs on: a
// desktop machine and Windows 11 need 2, a server and Windows Server 1. A
// create knows the kind; a set reads it (once) when cores fit.
func TestCoresRefusalsKeepTheMachinesFloor(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		_ = os.WriteFile(p, []byte(body), 0o600)
		return p
	}
	one := `{"error":"Only 1 core at 5.7 GHz is available. Continue with mixed core speeds?","code":"core_speed_short","workload":"box","cores":4,"fit":1,"speedMHz":5700}`
	offer, mixed := "use 1 core: run it again with --cores 1", "continue with mixed speeds (4 cores): run it again with --mixed-speeds"

	for _, c := range []struct {
		kind, body string
		offered    bool
	}{
		{"vm-ubuntu", `{"id":"box","cpus":4}`, true},
		{"vm-ubuntu-desktop", `{"id":"box","cpus":4}`, false},
		{"vm-windows", `{"id":"box","cpus":4}`, false},
		{"vm-windows", `{"id":"box","cpus":4,"windowsEdition":"desktop"}`, false},
		{"vm-windows", `{"id":"box","cpus":4,"windowsEdition":"server"}`, true},
	} {
		f := newFakeAPI(t)
		f.answers["POST /v1/workloads/"+c.kind] = answerStatus(409, one)
		err := cmdCreate([]string{c.kind, "-f", write("box.json", c.body), "--dedicated-cores", "--core-speed", "5.7GHz"})
		if err == nil || !strings.Contains(err.Error(), "Only 1 core at 5.7 GHz") || !strings.Contains(err.Error(), mixed) ||
			strings.Contains(err.Error(), offer) == !c.offered || exitCode(err) != 4 {
			t.Errorf("create %s %s: %v", c.kind, c.body, err)
		}
	}

	ws := `{"spec":{"workloads":[{"id":"box","type":"vm-ubuntu","vm":{"cpus":4}},` +
		`{"id":"desk","type":"vm-ubuntu-desktop","vm":{"cpus":4}},` +
		`{"id":"win","type":"vm-windows","vm":{"cpus":4,"windowsEdition":"desktop"}},` +
		`{"id":"winsrv","type":"vm-windows","vm":{"cpus":4,"windowsEdition":"server"}}]}}`
	file := write("set.json", `{"vm":{"dedicatedCores":true}}`)
	reads := func(f *fakeAPI) (n int) {
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, s := range f.seen {
			if s.method == "GET" && s.path == "/v1/workspace" {
				n++
			}
		}
		return n
	}
	for _, c := range []struct {
		name      string
		args      []string
		workspace func(http.ResponseWriter, *http.Request, []byte)
		fit       int
		offered   bool
		reads     int
	}{
		// A file alone: the kind is read after the refusal.
		{"a file, a server", []string{"box", "-f", file}, answerJSON(ws), 1, true, 1},
		{"a file, a desktop machine", []string{"desk", "-f", file}, answerJSON(ws), 1, false, 1},
		{"a file, Windows 11", []string{"win", "-f", file}, answerJSON(ws), 1, false, 1},
		{"a file, Windows Server", []string{"winsrv", "-f", file}, answerJSON(ws), 1, true, 1},
		// Unread: the most any machine needs, and the refusal as it came.
		{"a file, the machine unread", []string{"box", "-f", file}, answerStatus(500, `{"error":"down"}`), 1, false, 1},
		// None fit: nothing to read.
		{"a file, none fit", []string{"desk", "-f", file}, answerJSON(ws), 0, false, 0},
		// The flags read the machine before the set: not again.
		{"flags, a desktop machine", []string{"desk", "--dedicated-cores"}, answerJSON(ws), 1, false, 1},
		{"flags, a server", []string{"box", "--dedicated-cores"}, answerJSON(ws), 1, true, 1},
	} {
		id := c.args[0]
		words := fmt.Sprintf("Only %d cores at 5.7 GHz are available. Continue with mixed core speeds?", c.fit)
		if c.fit == 1 {
			words = "Only 1 core at 5.7 GHz is available. Continue with mixed core speeds?"
		}
		f := newFakeAPI(t)
		f.answers["GET /v1/workspace"] = c.workspace
		f.answers["PATCH /v1/workloads/"+id] = answerStatus(409, fmt.Sprintf(
			`{"error":%q,"code":"core_speed_short","workload":%q,"cores":4,"fit":%d,"speedMHz":5700}`, words, id, c.fit))
		err := cmdSet(c.args)
		if err == nil || !strings.Contains(err.Error(), words) || !strings.Contains(err.Error(), mixed) ||
			strings.Contains(err.Error(), "--cores ") != c.offered || exitCode(err) != 4 {
			t.Errorf("set %s: %v", c.name, err)
		}
		if n := reads(f); n != c.reads {
			t.Errorf("set %s: read the workspace %d times, want %d", c.name, n, c.reads)
		}
	}
}

// cores prints the API's answer; servers of one speed get a line saying
// there is none to choose.
func TestCores(t *testing.T) {
	f := newFakeAPI(t)
	f.answers["GET /v1/machine-cores"] = answerJSON(`{"showSpeed":true,"speeds":[{"speedMHz":5700,"fit":16},{"speedMHz":4400,"fit":16}],"dedicatedFit":16}`)
	out, stderr, err := captured(t, func() error { return cmdCores(nil) })
	if err != nil {
		t.Fatal(err)
	}
	got := printed(t, out)
	if got["showSpeed"] != true || len(got["speeds"].([]any)) != 2 || got["dedicatedFit"] != float64(16) || stderr != "" {
		t.Errorf("%v %q", got, stderr)
	}

	f.answers["GET /v1/machine-cores"] = answerJSON(`{"showSpeed":false,"speeds":[],"dedicatedFit":8}`)
	_, stderr, err = captured(t, func() error { return cmdCores(nil) })
	if err != nil || !strings.Contains(stderr, "one speed: leave --core-speed out") {
		t.Errorf("%v %q", err, stderr)
	}

	f.answers["GET /v1/machine-cores"] = answerStatus(404, `404 page not found`)
	if err := cmdCores(nil); err == nil || !strings.Contains(err.Error(), "no dedicated cores yet") {
		t.Errorf("%v", err)
	}
	if s := f.last("GET", "/v1/machine-cores"); s == nil {
		t.Error("never asked")
	}
}

// ls shows a running machine's cores as the status says: their clock, or
// mixed core speeds with the words for it; nothing where none is said.
func TestListShowsCores(t *testing.T) {
	f := newFakeAPI(t)
	f.answers["GET /v1/workspace"] = answerJSON(`{"spec":{"workloads":[
		{"id":"fast","type":"vm-ubuntu","vm":{"cpus":8,"dedicatedCores":true}},
		{"id":"desk","type":"vm-ubuntu-desktop","vm":{"cpus":4}},
		{"id":"web","type":"pod","pod":{"image":"nginx"}}]}}`)
	f.answers["GET /v1/status"] = answerJSON(`{"workloads":[
		{"id":"fast","phase":"Running","ready":true,"cores":{"speedMHz":5700}},
		{"id":"desk","phase":"Running","ready":true,"cores":{"mixed":true,"message":"` + msgSpeedsMixed + `"}},
		{"id":"web","phase":"Running","ready":true}]}`)
	out, _, err := captured(t, func() error { return cmdList(nil) })
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]any{}
	for _, raw := range printed(t, out)["resources"].([]any) {
		m := raw.(map[string]any)
		got[m["id"].(string)] = m["cores"]
	}
	want := map[string]any{
		"fast": map[string]any{"speedMHz": float64(5700)},
		"desk": map[string]any{"mixed": true, "message": msgSpeedsMixed},
		"web":  nil,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("cores: %v, want %v", got, want)
	}
}
