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
	msgSpeedsMixed  = "No server has 20 free cores of one speed. Continue with cores of mixed speeds?"
	msgMixedCard    = "Mixed core speeds"
	msgSpeedUnknown = "No server this machine may run on has cores at 3 GHz."
	msgServerFull   = "This server is full: it has no room for this machine."
	msgPairs        = "Dedicated cores come in pairs on these servers: use 8 or 10."
	modelI9         = "Intel Core i9-14900K"
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
	sized := write("sized.json", `{"id":"box","memory":"16Gi"}`)
	one := write("one.json", `{"id":"box","cpus":1}`)
	huge := write("huge.json", `{"id":"box","cpus":1e20}`)
	worded := write("worded.json", `{"id":"box","cpus":"8"}`)

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
		// --cores agrees with the file's cpus, or the file says none.
		{"the file's cores again", []string{"vm-ubuntu", "-f", box, "--dedicated-cores", "--cores", "8"},
			map[string]any{"id": "box", "cpus": float64(8), "memory": "16Gi", "dedicatedCores": true}},
		{"cores the file leaves out", []string{"vm-ubuntu", "-f", sized, "--dedicated-cores", "--cores", "6"},
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
		"a speed on shared cores": {[]string{"vm-ubuntu", "-f", box, "--core-speed", "5.7GHz"}, "--core-speed goes with dedicated cores: add --dedicated-cores"},
		"mixed speeds on shared":  {[]string{"vm-ubuntu", "-f", box, "--mixed-speeds"}, "--mixed-speeds goes with dedicated cores"},
		"a speed, then shared":    {[]string{"vm-ubuntu", "-f", dedicated, "--dedicated-cores=false", "--core-speed", "5700"}, "--core-speed goes with dedicated cores"},
		"a bare GHz":              {[]string{"vm-ubuntu", "-f", box, "--dedicated-cores", "--core-speed", "5.7"}, "--core-speed takes a clock in GHz or MHz"},
		"a word":                  {[]string{"vm-ubuntu", "-f", box, "--dedicated-cores", "--core-speed", "fast"}, "--core-speed takes a clock in GHz or MHz"},
		"no cores":                {[]string{"vm-ubuntu", "-f", box, "--cores", "0"}, "--cores is a whole number of cores"},
		// Two counts: which one is meant is the user's to say (owner,
		// 2026-10-22).
		"cores the file disagrees with": {[]string{"vm-ubuntu", "-f", box, "--dedicated-cores", "--cores", "6"}, "box.json says 8 cores and --cores says 6: change one of them"},
		"one core in the file":          {[]string{"vm-ubuntu", "-f", one, "--cores", "2"}, "one.json says 1 core and --cores says 2: change one of them"},
		"cores in words in the file":    {[]string{"vm-ubuntu", "-f", worded, "--cores", "8"}, `worded.json says "cpus": "8" and --cores says 8: change one of them`},
		"more cores than any count":     {[]string{"vm-ubuntu", "-f", huge, "--cores", "2"}, `huge.json says "cpus": 100000000000000000000 and --cores says 2: change one of them`},
		"on an app":                     {[]string{"pod", "-f", box, "--cores", "2"}, "--cores goes with a machine"},
		"on a Desktop App":              {[]string{"desktop", "-f", box, "--cores", "2"}, "--cores goes with a machine"},
		"on a browser by id":            {[]string{"browser", "--id", "b", "--mixed-speeds"}, "--mixed-speeds goes with a machine"},
		"on several apps":               {[]string{"apps", "-f", box, "--core-speed", "5700"}, "--core-speed goes with one resource"},
		"dedicated on several apps":     {[]string{"apps", "-f", box, "--dedicated-cores"}, `set "dedicatedCores": true on each service`},
		"on a database, not dedicated":  {[]string{"storage", "-f", box, "--mixed-speeds", "--dedicated-cores"}, "--mixed-speeds goes with a machine"},
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
	memory := filepath.Join(dir, "memory.json")
	_ = os.WriteFile(memory, []byte(`{"vm":{"memory":"16Gi"},"reachableFrom":["web"]}`), 0o600)
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
		{"the cores that fit, beside the file's", []string{"box", "-f", memory, "--cores", "6", "--dedicated-cores"},
			map[string]any{"vm": map[string]any{"cpus": float64(6), "memory": "16Gi", "dedicatedCores": true}, "reachableFrom": []any{"web"}}},
		{"the file's cores again", []string{"box", "-f", more, "--cores", "8", "--dedicated-cores"},
			map[string]any{"vm": map[string]any{"cpus": float64(8), "memory": "16Gi", "dedicatedCores": true}, "reachableFrom": []any{"web"}}},
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
		"cores the file disagrees with":    {[]string{"box", "-f", more, "--cores", "6", "--dedicated-cores"}, "more.json says 8 cores and --cores says 6: change one of them"},
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
		// Too few whole cores free to dedicate: the count that fits, or
		// shared cores; never mixed speeds, never 0.
		{409, `{"error":"Only 4 dedicated cores are available.","code":"cores_short","workload":"box","cores":6,"fit":4}`,
			"Only 4 dedicated cores", `use 4 cores dedicated: set "cpus": 4 (a machine) or "cpu": "4" (any other kind) and run it again; or shared cores: run it again with --dedicated-cores=false`, false},
		{409, `{"error":"No dedicated cores are available.","code":"cores_short","workload":"box","cores":2,"fit":0}`,
			"No dedicated cores", "shared cores: run it again with --dedicated-cores=false", true},
		{409, `{"error":"Only 4 dedicated cores are available.","code":"cores_short","workload":"other","cores":6,"fit":4}`, "Only 4 dedicated cores", "", true},
		// Dedicated cores in pairs is the API's to judge: its words as it
		// says them, nothing to run again made up here.
		{422, `{"error":"` + msgPairs + `","code":"cores_pairs","workload":"box","cores":9,"choices":[8,10]}`, msgPairs, "", true},
		{422, `{"error":"` + msgPairs + `"}`, msgPairs, "", true},
	}
	for _, c := range cases {
		err := coresNext(problemFrom(c.status, []byte(c.body)), "box", func() int { return 1 }, coresAgain{})
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
		status    int
		body      string
		floor     int
		wantNext  string
		wantNoUse bool
		asked     bool
	}{
		{409, one, 2, "continue with mixed speeds (4 cores): run it again with --mixed-speeds", true, true},
		{409, `{"error":"` + msgSpeedsMixed + `","code":"core_speeds_mixed","workload":"box","cores":4,"fit":1}`, 2,
			"continue with mixed speeds (4 cores): run it again with --mixed-speeds", true, true},
		{409, one, 1, "use 1 core: run it again with --cores 1", false, true},
		{409, `{"error":"Only 2 cores at 5.7 GHz are available. Continue with mixed core speeds?","code":"core_speed_short","workload":"box","cores":4,"fit":2,"speedMHz":5700}`, 2,
			"use 2 cores: run it again with --cores 2", false, true},
		{409, `{"error":"` + msgSpeedsMixed + `","code":"core_speeds_mixed","workload":"box","cores":4,"fit":0}`, 2,
			"continue with mixed speeds (4 cores)", true, false},
		// A speed no server has is a 422, as the API sends it.
		{422, `{"error":"` + msgSpeedUnknown + `","code":"core_speed_unknown","workload":"box","cores":4,"fit":0,"speedMHz":3000}`, 2,
			"livellm cores lists the speeds", true, false},
	} {
		asked := false
		err := coresNext(problemFrom(c.status, []byte(c.body)), "box", func() int { asked = true; return c.floor }, coresAgain{})
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
		if exitCode(err) != map[int]int{422: 1, 409: 4}[c.status] {
			t.Errorf("floor %d, %s: exit %d", c.floor, c.body, exitCode(err))
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

	// A create from a file: the cores that fit are said in the file, with
	// --cores beside them when the command gave it (owner, 2026-10-22).
	f := newFakeAPI(t)
	f.answers["POST /v1/workloads/vm-ubuntu"] = answerStatus(409, short)
	err := cmdCreate([]string{"vm-ubuntu", "-f", box, "--dedicated-cores", "--core-speed", "5.7GHz"})
	if err == nil || !strings.Contains(err.Error(), msgSpeedShort+` (409) — use 6 cores: set "cpus": 6 in `+box+
		` and run it again; or continue with mixed speeds (8 cores): run it again with --mixed-speeds`) {
		t.Errorf("create: %v", err)
	}
	sized := filepath.Join(dir, "sized.json")
	_ = os.WriteFile(sized, []byte(`{"id":"box"}`), 0o600)
	for _, file := range []string{box, sized} {
		f = newFakeAPI(t)
		f.answers["POST /v1/workloads/vm-ubuntu"] = answerStatus(409, short)
		err = cmdCreate([]string{"vm-ubuntu", "-f", file, "--dedicated-cores", "--cores", "8"})
		if err == nil || !strings.Contains(err.Error(), `use 6 cores: set "cpus": 6 in `+file+" and run it again with --cores 6; or") {
			t.Errorf("create %s --cores 8: %v", file, err)
		}
	}

	// Too few whole cores free to dedicate: a machine's cpus, an app's cpu
	// (flat in a create's file), or shared cores.
	coresShort := `{"error":"Only 4 dedicated cores are available.","code":"cores_short","workload":"box","cores":8,"fit":4}`
	f = newFakeAPI(t)
	f.answers["POST /v1/workloads/vm-ubuntu"] = answerStatus(409, coresShort)
	err = cmdCreate([]string{"vm-ubuntu", "-f", box, "--dedicated-cores"})
	if err == nil || !strings.Contains(err.Error(), `Only 4 dedicated cores are available. (409) — use 4 cores dedicated: set "cpus": 4 in `+box+
		` and run it again; or shared cores: run it again with --dedicated-cores=false`) || exitCode(err) != 4 {
		t.Errorf("create, cores_short: %v", err)
	}
	app := filepath.Join(dir, "app.json")
	_ = os.WriteFile(app, []byte(`{"id":"box","image":"nginx","cpu":"8"}`), 0o600)
	f = newFakeAPI(t)
	f.answers["POST /v1/workloads/pod"] = answerStatus(409, coresShort)
	err = cmdCreate([]string{"pod", "-f", app, "--dedicated-cores"})
	if err == nil || !strings.Contains(err.Error(), `use 4 cores dedicated: set "cpu": "4" in `+app+` and run it again; or shared cores`) {
		t.Errorf("create app, cores_short: %v", err)
	}
	f = newFakeAPI(t)
	f.answers["GET /v1/workspace"] = answerJSON(`{"spec":{"workloads":[{"id":"box","type":"vm-ubuntu","vm":{"cpus":8}}]}}`)
	f.answers["PATCH /v1/workloads/box"] = answerStatus(409, coresShort)
	err = cmdSet([]string{"box", "--dedicated-cores"})
	if err == nil || !strings.Contains(err.Error(), "use 4 cores dedicated: run it again with --cores 4; or shared cores: run it again with --dedicated-cores=false") {
		t.Errorf("set, cores_short: %v", err)
	}

	// The API trims the id it makes and names the machine so: still this one.
	spaced := filepath.Join(dir, "spaced.json")
	_ = os.WriteFile(spaced, []byte(`{"id":" box ","cpus":8}`), 0o600)
	f = newFakeAPI(t)
	f.answers["POST /v1/workloads/vm-ubuntu"] = answerStatus(409, short)
	err = cmdCreate([]string{"vm-ubuntu", "-f", spaced, "--dedicated-cores", "--core-speed", "5.7GHz"})
	if err == nil || !strings.Contains(err.Error(), `use 6 cores: set "cpus": 6 in `+spaced+" and run it again") {
		t.Errorf("create, id with spaces: %v", err)
	}

	f = newFakeAPI(t)
	f.answers["GET /v1/workspace"] = answerJSON(`{"spec":{"workloads":[{"id":"box","type":"vm-ubuntu","vm":{"cpus":20}}]}}`)
	f.answers["PATCH /v1/workloads/box"] = answerStatus(409, `{"error":"`+msgSpeedsMixed+`","code":"core_speeds_mixed","workload":"box","cores":20,"fit":16}`)
	err = cmdSet([]string{"box", "--dedicated-cores"})
	if err == nil || !strings.Contains(err.Error(), msgSpeedsMixed+" (409) — use 16 cores: run it again with --cores 16; or continue with mixed speeds (20 cores): run it again with --mixed-speeds") {
		t.Errorf("set: %v", err)
	}

	// A set whose file says the cores: they are changed there; a file that
	// says none takes --cores.
	counted := filepath.Join(dir, "counted.json")
	_ = os.WriteFile(counted, []byte(`{"vm":{"cpus":20,"dedicatedCores":true}}`), 0o600)
	uncounted := filepath.Join(dir, "uncounted.json")
	_ = os.WriteFile(uncounted, []byte(`{"vm":{"dedicatedCores":true}}`), 0o600)
	for _, c := range []struct {
		args []string
		next string
	}{
		{[]string{"box", "-f", counted}, `use 16 cores: set "cpus": 16 in ` + counted + `'s "vm" block and run it again; or continue with mixed speeds (20 cores)`},
		{[]string{"box", "-f", counted, "--cores", "20"}, `use 16 cores: set "cpus": 16 in ` + counted + `'s "vm" block and run it again with --cores 16; or`},
		{[]string{"box", "-f", uncounted}, "use 16 cores: run it again with --cores 16; or continue with mixed speeds (20 cores)"},
	} {
		f = newFakeAPI(t)
		f.answers["GET /v1/workspace"] = answerJSON(`{"spec":{"workloads":[{"id":"box","type":"vm-ubuntu","vm":{"cpus":20}}]}}`)
		f.answers["PATCH /v1/workloads/box"] = answerStatus(409, `{"error":"`+msgSpeedsMixed+`","code":"core_speeds_mixed","workload":"box","cores":20,"fit":16}`)
		err = cmdSet(c.args)
		if err == nil || !strings.Contains(err.Error(), c.next) {
			t.Errorf("set %v: %v, want %q", c.args, err, c.next)
		}
	}

	// Dedicated cores in pairs: the API's words as it says them, on a create
	// and a set, with nothing to run again made up here.
	pairs := answerStatus(422, `{"error":"`+msgPairs+`","code":"cores_pairs","workload":"box","cores":9,"choices":[8,10]}`)
	nine := filepath.Join(dir, "nine.json")
	_ = os.WriteFile(nine, []byte(`{"id":"box","cpus":9}`), 0o600)
	f = newFakeAPI(t)
	f.answers["POST /v1/workloads/vm-ubuntu"] = pairs
	err = cmdCreate([]string{"vm-ubuntu", "-f", nine, "--dedicated-cores"})
	if err == nil || err.Error() != msgPairs+" (422)" || exitCode(err) != 1 {
		t.Errorf("create, pairs: %v", err)
	}
	f = newFakeAPI(t)
	f.answers["GET /v1/workspace"] = answerJSON(`{"spec":{"workloads":[{"id":"box","type":"vm-ubuntu","vm":{"cpus":8,"dedicatedCores":true}}]}}`)
	f.answers["PATCH /v1/workloads/box"] = pairs
	err = cmdSet([]string{"box", "--cores", "9"})
	if err == nil || err.Error() != msgPairs+" (422)" {
		t.Errorf("set, pairs: %v", err)
	}
	if w := writesOf(f); len(w) != 1 || bodyOf(t, w[0])["vm"].(map[string]any)["cpus"] != float64(9) {
		t.Errorf("set, pairs: the count is sent as asked: %v", w)
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
	mixed := "continue with mixed speeds (4 cores): run it again with --mixed-speeds"

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
		file := write("box.json", c.body)
		offer := `use 1 core: set "cpus": 1 in ` + file + " and run it again"
		err := cmdCreate([]string{c.kind, "-f", file, "--dedicated-cores", "--core-speed", "5.7GHz"})
		if err == nil || !strings.Contains(err.Error(), "Only 1 core at 5.7 GHz") || !strings.Contains(err.Error(), mixed) ||
			strings.Contains(err.Error(), offer) != c.offered || strings.Contains(err.Error(), "use ") != c.offered || exitCode(err) != 4 {
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
	f.answers["GET /v1/machine-cores"] = answerJSON(`{"showSpeed":true,"speeds":[{"speedMHz":5700,"fit":16,"model":"` + modelI9 +
		`"},{"speedMHz":4400,"fit":16,"model":"` + modelI9 + `"}],"dedicatedFit":16}`)
	out, stderr, err := captured(t, func() error { return cmdCores(nil) })
	if err != nil {
		t.Fatal(err)
	}
	got := printed(t, out)
	if got["showSpeed"] != true || len(got["speeds"].([]any)) != 2 || got["dedicatedFit"] != float64(16) || stderr != "" {
		t.Errorf("%v %q", got, stderr)
	}
	// Each speed's CPU model as the API names it.
	if s := got["speeds"].([]any)[0].(map[string]any); s["model"] != modelI9 {
		t.Errorf("speed: %v", s)
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

// ls shows a running resource's cores as the status says: the CPU model of
// its server, shared or dedicated, and for dedicated cores their clock, or
// mixed core speeds with the words for it; a shared one the model alone (no
// clock); nothing where none is said.
func TestListShowsCores(t *testing.T) {
	f := newFakeAPI(t)
	f.answers["GET /v1/workspace"] = answerJSON(`{"spec":{"workloads":[
		{"id":"fast","type":"vm-ubuntu","vm":{"cpus":8,"dedicatedCores":true}},
		{"id":"desk","type":"vm-ubuntu-desktop","vm":{"cpus":4,"dedicatedCores":true,"mixedSpeeds":true}},
		{"id":"box","type":"vm-ubuntu","vm":{"cpus":2}},
		{"id":"api","type":"pod","pod":{"image":"nginx"}},
		{"id":"web","type":"pod","pod":{"image":"nginx"}}]}}`)
	f.answers["GET /v1/status"] = answerJSON(`{"workloads":[
		{"id":"fast","phase":"Running","ready":true,"cores":{"speedMHz":5700,"model":"` + modelI9 + `"}},
		{"id":"desk","phase":"Running","ready":true,"cores":{"mixed":true,"message":"` + msgMixedCard + `"}},
		{"id":"box","phase":"Running","ready":true,"cores":{"model":"` + modelI9 + `"}},
		{"id":"api","phase":"Running","ready":true,"cores":{"model":"` + modelI9 + `"}},
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
		"fast": map[string]any{"speedMHz": float64(5700), "model": modelI9},
		"desk": map[string]any{"mixed": true, "message": msgMixedCard},
		"box":  map[string]any{"model": modelI9},
		"api":  map[string]any{"model": modelI9},
		"web":  nil,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("cores: %v, want %v", got, want)
	}
}

// --dedicated-cores on an app, a Desktop App, a database, a browser or a
// Browser API goes into what is sent, in place of the file's; an object
// storage has none.
func TestDedicatedCoresBeyondMachines(t *testing.T) {
	dir := t.TempDir()
	web := filepath.Join(dir, "web.json")
	_ = os.WriteFile(web, []byte(`{"id":"web","image":"nginx","cpu":"1"}`), 0o600)
	files := filepath.Join(dir, "files.json")
	_ = os.WriteFile(files, []byte(`{"id":"files","engine":"s3"}`), 0o600)
	for _, c := range []struct {
		name string
		args []string
		want map[string]any
	}{
		{"an app", []string{"pod", "-f", web, "--dedicated-cores"}, map[string]any{"id": "web", "image": "nginx", "cpu": "1", "dedicatedCores": true}},
		{"an app, shared", []string{"pod", "-f", web, "--dedicated-cores=false"}, map[string]any{"id": "web", "image": "nginx", "cpu": "1", "dedicatedCores": false}},
		{"a Browser API", []string{"controller", "-f", web, "--dedicated-cores"}, map[string]any{"id": "web", "image": "nginx", "cpu": "1", "dedicatedCores": true}},
		{"a browser by id", []string{"browser", "--id", "b", "--dedicated-cores"}, map[string]any{"id": "b", "dedicatedCores": true}},
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
	f := newFakeAPI(t)
	if err := cmdCreate([]string{"storage", "-f", files, "--dedicated-cores"}); err == nil || !strings.Contains(err.Error(), "an object storage has no dedicated cores") {
		t.Errorf("object storage: %v", err)
	}
	if w := writesOf(f); len(w) != 0 {
		t.Errorf("object storage: sent %v", w)
	}

	const ws = `{"spec":{"workloads":[
		{"id":"web","type":"pod","pod":{"image":"nginx","cpu":"1"}},
		{"id":"pg","type":"storage","storage":{"engine":"postgres","dedicatedCores":true}},
		{"id":"files","type":"storage","storage":{"engine":"s3"}},
		{"id":"desk","type":"desktop","desktop":{"cpu":"2"}}]}}`
	mem := filepath.Join(dir, "mem.json")
	_ = os.WriteFile(mem, []byte(`{"pod":{"memory":"1Gi"}}`), 0o600)
	for _, c := range []struct {
		name string
		args []string
		want map[string]any
	}{
		{"an app, no file", []string{"web", "--dedicated-cores"}, map[string]any{"pod": map[string]any{"dedicatedCores": true}}},
		{"an app, beside the file's", []string{"web", "-f", mem, "--dedicated-cores"}, map[string]any{"pod": map[string]any{"memory": "1Gi", "dedicatedCores": true}}},
		{"a database shared again", []string{"pg", "--dedicated-cores=false"}, map[string]any{"storage": map[string]any{"dedicatedCores": false}}},
		{"a Desktop App", []string{"desk", "--dedicated-cores"}, map[string]any{"desktop": map[string]any{"dedicatedCores": true}}},
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
		if strings.Contains(stderr, "next restart") {
			t.Errorf("%s: a restart note for a pod: %q", c.name, stderr)
		}
	}
	for name, c := range map[string]struct {
		args []string
		says string
	}{
		"an object storage": {[]string{"files", "--dedicated-cores"}, "an object storage has no dedicated cores"},
		"a speed on an app": {[]string{"web", "--dedicated-cores", "--core-speed", "5700"}, "--core-speed goes with a machine"},
	} {
		f := newFakeAPI(t)
		f.answers["GET /v1/workspace"] = answerJSON(ws)
		if err := cmdSet(c.args); err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %v, want it to say %q", name, err, c.says)
		}
		if w := writesOf(f); len(w) != 0 {
			t.Errorf("%s: sent %v", name, w)
		}
	}
}
