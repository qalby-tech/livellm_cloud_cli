package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// create takes a machine's private address (--private-address, sent as
// privateAddress) or none (--no-private-network, sent as privateNetwork
// false), each agreeing with the file's; neither sends nothing, so the API
// gives one wherever it offers the private network.
func TestCreateMachinePrivateAddress(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		_ = os.WriteFile(p, []byte(body), 0o600)
		return p
	}
	box := write("box.json", `{"id":"box"}`)
	fixed := write("fixed.json", `{"id":"box","privateAddress":"172.16.0.12"}`)
	off := write("off.json", `{"id":"box","privateNetwork":false}`)
	on := write("on.json", `{"id":"box","privateNetwork":true}`)
	web := write("web.json", `{"id":"web","image":"nginx"}`)

	for _, c := range []struct {
		name string
		args []string
		want map[string]any
	}{
		{"neither", []string{"vm-ubuntu", "-f", box}, map[string]any{"id": "box"}},
		{"an address", []string{"vm-ubuntu", "-f", box, "--private-address", "172.16.0.12"},
			map[string]any{"id": "box", "privateAddress": "172.16.0.12"}},
		{"the file's address again", []string{"vm-windows", "-f", fixed, "--private-address", " 172.16.0.12 "},
			map[string]any{"id": "box", "privateAddress": "172.16.0.12"}},
		{"the file's address alone", []string{"vm-ubuntu", "-f", fixed},
			map[string]any{"id": "box", "privateAddress": "172.16.0.12"}},
		{"none", []string{"vm-ubuntu-desktop", "-f", box, "--no-private-network"},
			map[string]any{"id": "box", "privateNetwork": false}},
		{"none, as the file says", []string{"vm-ubuntu", "-f", off, "--no-private-network"},
			map[string]any{"id": "box", "privateNetwork": false}},
		{"none=false asks nothing", []string{"vm-ubuntu", "-f", box, "--no-private-network=false"},
			map[string]any{"id": "box"}},
		{"beside the time zone and cores", []string{"vm-ubuntu", "-f", box, "--private-address", "172.16.0.20", "--timezone", "UTC", "--dedicated-cores"},
			map[string]any{"id": "box", "privateAddress": "172.16.0.20", "timeZone": "UTC", "dedicatedCores": true}},
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
		"both":                       {[]string{"vm-ubuntu", "-f", box, "--private-address", "172.16.0.12", "--no-private-network"}, "--private-address and --no-private-network don't go together"},
		"an empty address":           {[]string{"vm-ubuntu", "-f", box, "--private-address", ""}, "--private-address takes one address, e.g. 172.16.0.12 (leave it out for the next free one)"},
		"another address than file":  {[]string{"vm-ubuntu", "-f", fixed, "--private-address", "172.16.0.13"}, "fixed.json says the private address 172.16.0.12 and --private-address says 172.16.0.13: change one of them"},
		"an address, the file none":  {[]string{"vm-ubuntu", "-f", off, "--private-address", "172.16.0.12"}, `off.json says "privateNetwork": false and --private-address says 172.16.0.12: change one of them`},
		"none, the file an address":  {[]string{"vm-ubuntu", "-f", fixed, "--no-private-network"}, "fixed.json says the private address 172.16.0.12 and --no-private-network says none: change one of them"},
		"none, the file one":         {[]string{"vm-ubuntu", "-f", on, "--no-private-network"}, `on.json says "privateNetwork": true and --no-private-network says none: change one of them`},
		"a value after a space":      {[]string{"vm-ubuntu", "-f", box, "--no-private-network", "false"}, `"false" is left over: a yes-or-no flag takes its value after =, e.g. --no-private-network=false`},
		"an address on an app":       {[]string{"pod", "-f", web, "--private-address", "172.16.0.12"}, "--private-address and --no-private-network go with a machine"},
		"none on a database":         {[]string{"storage", "-f", web, "--no-private-network"}, "--private-address and --no-private-network go with a machine"},
		"an address on a browser id": {[]string{"browser", "--id", "b", "--private-address", "172.16.0.12"}, "--private-address and --no-private-network go with a machine"},
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

// set refuses both flags before anything is sent: a machine's private
// address is chosen when it is made.
func TestSetRefusesPrivateAddress(t *testing.T) {
	const ws = `{"spec":{"workloads":[
		{"id":"box","type":"vm-ubuntu","vm":{"privateNetwork":true,"privateAddress":"172.16.0.12"}},
		{"id":"web","type":"pod","pod":{"image":"nginx"}}]}}`
	for _, c := range []struct {
		args []string
		says string
	}{
		{[]string{"box", "--private-address", "172.16.0.13"}, "a machine's private address is chosen when it is made (livellm create vm-ubuntu -f FILE --private-address 172.16.0.12, or --no-private-network for none) and fixed afterwards"},
		{[]string{"box", "--no-private-network"}, "a machine's private address is chosen when it is made"},
		{[]string{"web", "--private-address", "172.16.0.13"}, "--private-address and --no-private-network go with a machine: vm-ubuntu, vm-ubuntu-desktop or vm-windows, when it is made"},
	} {
		f := newFakeAPI(t)
		f.answers["GET /v1/workspace"] = answerJSON(ws)
		err := cmdSet(c.args)
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%v: %v, want it to say %q", c.args, err, c.says)
		}
		if w := writesOf(f); len(w) != 0 {
			t.Errorf("%v: sent %v", c.args, w)
		}
	}
}

// The API's refusals of a private address are passed on in its words, with
// what to run next where there is something; a 409 exits 4, a 422 1.
func TestPrivateAddressRefusalsSayWhatNext(t *testing.T) {
	box := filepath.Join(t.TempDir(), "box.json")
	_ = os.WriteFile(box, []byte(`{"id":"box"}`), 0o600)
	for _, c := range []struct {
		status     int
		body, next string
		exit       int
	}{
		{422, `{"error":"workload box: this workspace has no private network","code":"private_network_not_offered"}`,
			`leave --private-address (and the file's "privateAddress" and "privateNetwork": true) out: the machine is made without one`, 1},
		{422, `{"error":"workload box: privateAddress \"172.16.0\" is not one IPv4 address","code":"private_address_invalid"}`, "", 1},
		{422, `{"error":"the private address 10.0.0.12 is outside this workspace's range 172.16.0.0/16","code":"private_address_outside"}`,
			"leave --private-address out for the next free one", 1},
		{422, `{"error":"the private address 172.16.0.2 is reserved in 172.16.0.0/16: the first ten addresses and the last are the network's; machines take 172.16.0.10 to 172.16.255.254","code":"private_address_reserved"}`,
			"leave --private-address out for the next free one", 1},
		{409, `{"error":"workload box: the private address 172.16.0.12 is taken by web","code":"private_address_taken"}`,
			"pick another (livellm ls shows the machines' private addresses), or leave --private-address out for the next free one", 4},
		{409, `{"error":"workload box: the private address 172.16.0.12 is being freed (a deleted machine's); try again in a minute","code":"private_address_releasing"}`, "", 4},
		{422, `{"error":"workload box: a machine's private address is fixed when it is made","code":"private_address_fixed"}`,
			`leave "privateAddress" and "privateNetwork" out of the file`, 1},
		{409, `{"error":"workload box: no private address is left in this workspace's range 172.16.0.0/16","code":"private_range_full"}`,
			"tell the user, or make the machine with --no-private-network", 4},
	} {
		f := newFakeAPI(t)
		f.answers["POST /v1/workloads/vm-ubuntu"] = answerStatus(c.status, c.body)
		err := privateNext(cmdCreate([]string{"vm-ubuntu", "-f", box, "--private-address", "172.16.0.12"}))
		var p *problem
		if !asProblem(err, &p) {
			t.Fatalf("%s: %v", c.body, err)
		}
		if p.Next != c.next {
			t.Errorf("%s: next %q, want %q", p.Code, p.Next, c.next)
		}
		if !strings.HasPrefix(err.Error(), p.Msg) || p.Msg == "" {
			t.Errorf("%s: %q isn't the API's words", p.Code, err)
		}
		if got := exitCode(err); got != c.exit {
			t.Errorf("%s: exits %d, want %d", p.Code, got, c.exit)
		}
	}
	// A next step the API names stays its own.
	p := &problem{Status: 409, Msg: "taken", Code: "private_address_taken", Next: "the API's"}
	if privateNext(p); p.Next != "the API's" {
		t.Errorf("the API's next step was replaced: %q", p.Next)
	}
}

const privateWorkspace = `{"name":"ws","plan":"pro","privateNetwork":{"range":"172.16.0.0/16","gateway":"172.16.0.1"},"spec":{"workloads":[
	{"id":"box","type":"vm-ubuntu","vm":{"privateNetwork":true,"privateAddress":"172.16.0.12","privateMac":"02:4c:4c:3a:91:0c"}},
	{"id":"solo","type":"vm-ubuntu","vm":{"privateNetwork":false}},
	{"id":"web","type":"pod","pod":{"image":"nginx"}}]}}`

// ls and connect print a machine's private address (connect its private
// card's MAC too), absent on one without; whoami prints the workspace's
// private network, absent where the cluster offers none.
func TestPrivateAddressShown(t *testing.T) {
	f := newFakeAPI(t)
	f.answers["GET /v1/workspace"] = answerJSON(privateWorkspace)
	f.answers["GET /v1/status"] = answerJSON(`{"workloads":[
		{"id":"box","phase":"Running","ready":true,"ssh":"1.2.3.4:30022"},
		{"id":"solo","phase":"Running","ready":true},
		{"id":"web","phase":"Running","ready":true}]}`)
	f.answers["POST /v1/workloads/box/connect"] = answerJSON(`{"tool":"terminal","type":"vm-ubuntu"}`)
	f.answers["POST /v1/workloads/solo/connect"] = answerJSON(`{"tool":"terminal","type":"vm-ubuntu"}`)

	out, _, err := captured(t, func() error { return cmdList(nil) })
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]any{}
	for _, raw := range printed(t, out)["resources"].([]any) {
		m := raw.(map[string]any)
		got[m["id"].(string)] = m["privateAddress"]
	}
	if want := map[string]any{"box": "172.16.0.12", "solo": nil, "web": nil}; !reflect.DeepEqual(got, want) {
		t.Errorf("ls: %v, want %v", got, want)
	}

	for id, want := range map[string][2]any{
		"box":  {"172.16.0.12", "02:4c:4c:3a:91:0c"},
		"solo": {nil, nil},
	} {
		out, _, err := captured(t, func() error { return cmdConnect([]string{id}) })
		if err != nil {
			t.Fatal(err)
		}
		m := printed(t, out)
		if m["privateAddress"] != want[0] || m["privateMac"] != want[1] {
			t.Errorf("connect %s: %v %v, want %v", id, m["privateAddress"], m["privateMac"], want)
		}
	}

	out, _, err = captured(t, cmdWhoamiNoArgs)
	if err != nil {
		t.Fatal(err)
	}
	if got := printed(t, out)["privateNetwork"]; !reflect.DeepEqual(got, map[string]any{"range": "172.16.0.0/16", "gateway": "172.16.0.1"}) {
		t.Errorf("whoami: %v", got)
	}
	f.answers["GET /v1/workspace"] = answerJSON(`{"name":"ws","plan":"pro","privateNetwork":null}`)
	out, _, err = captured(t, cmdWhoamiNoArgs)
	if err != nil {
		t.Fatal(err)
	}
	if m := printed(t, out); m["privateNetwork"] != nil || m["workspace"] != "ws" {
		t.Errorf("whoami without a private network: %v", m)
	}
}

func cmdWhoamiNoArgs() error { return cmdWhoami(nil) }

// reach lists a machine's private address among its inside addresses, last,
// as the API lists it: on every port, marked private.
func TestReachListsPrivateAddress(t *testing.T) {
	ws := map[string]any{"id": "box", "type": "vm-ubuntu", "vm": map[string]any{"privateAddress": "172.16.0.12"}}
	got := insideAddresses("ws-box", ws)
	if last := got[len(got)-1]; !reflect.DeepEqual(last, map[string]any{"host": "172.16.0.12", "private": true}) {
		t.Errorf("last inside address %v", last)
	}
	without := insideAddresses("ws-solo", map[string]any{"id": "solo", "type": "vm-ubuntu", "vm": map[string]any{}})
	for _, a := range without {
		if a["private"] != nil {
			t.Errorf("a machine without one lists %v", a)
		}
	}
}
