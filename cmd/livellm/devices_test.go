package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// create takes a machine's devices and time zone: --devices as a list,
// --timezone as timeZone (a browser's stays timezone), each agreeing with
// the file's when it says one.
func TestCreateMachineDevicesAndTimeZone(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		_ = os.WriteFile(p, []byte(body), 0o600)
		return p
	}
	box := write("box.json", `{"id":"box","cpus":4}`)
	gpu := write("gpu.json", `{"id":"box","devices":["rtx5090","usb-1"]}`)
	zoned := write("zoned.json", `{"id":"box","timeZone":"Europe/Moscow"}`)
	web := write("web.json", `{"id":"web","image":"nginx"}`)

	for _, c := range []struct {
		name string
		args []string
		want map[string]any
	}{
		{"devices", []string{"vm-windows", "-f", box, "--devices", "rtx5090, usb-1"},
			map[string]any{"id": "box", "cpus": float64(4), "devices": []any{"rtx5090", "usb-1"}}},
		{"the file's devices again, in another order", []string{"vm-ubuntu", "-f", gpu, "--devices", "usb-1,rtx5090"},
			map[string]any{"id": "box", "devices": []any{"usb-1", "rtx5090"}}},
		{"the file's devices alone", []string{"vm-ubuntu", "-f", gpu},
			map[string]any{"id": "box", "devices": []any{"rtx5090", "usb-1"}}},
		{"a time zone", []string{"vm-ubuntu-desktop", "-f", box, "--timezone", "Europe/Moscow"},
			map[string]any{"id": "box", "cpus": float64(4), "timeZone": "Europe/Moscow"}},
		{"the file's time zone again", []string{"vm-ubuntu", "-f", zoned, "--timezone", "Europe/Moscow"},
			map[string]any{"id": "box", "timeZone": "Europe/Moscow"}},
		{"devices, a time zone and dedicated cores", []string{"vm-windows", "-f", box, "--devices", "rtx5090", "--timezone", "Asia/Almaty", "--dedicated-cores"},
			map[string]any{"id": "box", "cpus": float64(4), "devices": []any{"rtx5090"}, "timeZone": "Asia/Almaty", "dedicatedCores": true}},
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

	// A browser's time zone keeps its own name.
	f := newFakeAPI(t)
	if err := cmdCreate([]string{"browser", "--id", "shop", "--timezone", "Europe/Moscow"}); err != nil {
		t.Fatal(err)
	}
	if w := writesOf(f); len(w) != 1 || !reflect.DeepEqual(bodyOf(t, w[0]), map[string]any{"id": "shop", "timezone": "Europe/Moscow"}) {
		t.Errorf("browser: sent %v", w)
	}

	for name, c := range map[string]struct {
		args []string
		says string
	}{
		"devices the file disagrees with": {[]string{"vm-ubuntu", "-f", gpu, "--devices", "rtx5090"}, "gpu.json says devices rtx5090, usb-1 and --devices says devices rtx5090: change one of them"},
		"no devices beside the file's":    {[]string{"vm-ubuntu", "-f", gpu, "--devices", ""}, "gpu.json says devices rtx5090, usb-1 and --devices says no devices: change one of them"},
		"a device twice":                  {[]string{"vm-ubuntu", "-f", box, "--devices", "rtx5090,rtx5090"}, "--devices names rtx5090 twice"},
		"devices on an app":               {[]string{"pod", "-f", web, "--devices", "rtx5090"}, "--devices goes with a machine"},
		"devices on a browser":            {[]string{"browser", "--id", "b", "--devices", "rtx5090"}, "--devices goes with a machine"},
		"a time zone the file disagrees":  {[]string{"vm-ubuntu", "-f", zoned, "--timezone", "UTC"}, "zoned.json says the time zone Europe/Moscow and --timezone says UTC: change one of them"},
		"a time zone on an app":           {[]string{"pod", "-f", web, "--timezone", "UTC"}, "--timezone goes with create of a browser or a machine"},
		"a locale on a machine":           {[]string{"vm-ubuntu", "-f", box, "--locale", "ru-RU"}, "go with create browser"},
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

// set takes a machine's devices in its vm block (” takes them all off) and
// says they change at its next restart; a time zone is refused, with where
// it is changed instead.
func TestSetMachineDevices(t *testing.T) {
	dir := t.TempDir()
	mem := filepath.Join(dir, "mem.json")
	_ = os.WriteFile(mem, []byte(`{"vm":{"memory":"16Gi"}}`), 0o600)
	gpu := filepath.Join(dir, "gpu.json")
	_ = os.WriteFile(gpu, []byte(`{"vm":{"devices":["rtx5090"]}}`), 0o600)
	zone := filepath.Join(dir, "zone.json")
	_ = os.WriteFile(zone, []byte(`{"vm":{"timeZone":"UTC","memory":"8Gi"}}`), 0o600)
	empty := filepath.Join(dir, "empty.json")
	_ = os.WriteFile(empty, []byte(`{}`), 0o600)
	const ws = `{"spec":{"workloads":[
		{"id":"box","type":"vm-windows","vm":{"cpus":4,"devices":["usb-1"]}},
		{"id":"web","type":"pod","pod":{"image":"nginx"}},
		{"id":"shop","type":"browser","browser":{}}]}}`

	for _, c := range []struct {
		name, note string
		args       []string
		want       map[string]any
	}{
		{"devices", "the devices change at box's next restart (livellm restart box)", []string{"box", "--devices", "rtx5090"},
			map[string]any{"vm": map[string]any{"devices": []any{"rtx5090"}}}},
		{"all taken off", "the devices change at box's next restart", []string{"box", "--devices", ""},
			map[string]any{"vm": map[string]any{"devices": []any{}}}},
		{"beside the file's", "the devices change at", []string{"box", "-f", mem, "--devices", "rtx5090,usb-1"},
			map[string]any{"vm": map[string]any{"memory": "16Gi", "devices": []any{"rtx5090", "usb-1"}}}},
		{"the file's again", "the devices change at", []string{"box", "-f", gpu, "--devices", "rtx5090"},
			map[string]any{"vm": map[string]any{"devices": []any{"rtx5090"}}}},
		{"with the cores", "the cores and the devices change at box's next restart (livellm restart box)", []string{"box", "--devices", "rtx5090", "--dedicated-cores"},
			map[string]any{"vm": map[string]any{"devices": []any{"rtx5090"}, "dedicatedCores": true}}},
		{"an empty file beside them", "the devices change at", []string{"box", "-f", empty, "--devices", "rtx5090"},
			map[string]any{"vm": map[string]any{"devices": []any{"rtx5090"}}}},
	} {
		f := newFakeAPI(t)
		f.answers["GET /v1/workspace"] = answerJSON(ws)
		_, stderr, err := captured(t, func() error { return cmdSet(c.args) })
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		w := writesOf(f)
		if len(w) != 1 || w[0].method != "PATCH" || w[0].path != "/v1/workloads/box" {
			t.Fatalf("%s: sent %v", c.name, w)
		}
		if got := bodyOf(t, w[0]); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: sent %v, want %v", c.name, got, c.want)
		}
		if !strings.Contains(stderr, c.note) {
			t.Errorf("%s: stderr %q, want %q", c.name, stderr, c.note)
		}
	}

	for name, c := range map[string]struct {
		args []string
		says string
	}{
		"devices on an app":               {[]string{"web", "--devices", "rtx5090"}, "--devices goes with a machine"},
		"devices the file disagrees with": {[]string{"box", "-f", gpu, "--devices", "usb-1"}, "gpu.json says devices rtx5090 and --devices says devices usb-1: change one of them"},
		"a machine's time zone":           {[]string{"box", "--timezone", "UTC"}, "a machine's time zone is set when it is made (livellm create vm-windows --timezone ZONE); change it on the machine itself"},
		"a browser's time zone":           {[]string{"shop", "--timezone", "UTC"}, "a browser's time zone: livellm browser locale shop --timezone ZONE"},
		"an app's time zone":              {[]string{"web", "--timezone", "UTC"}, "--timezone goes with create of a machine or a browser"},
		"a time zone in the file":         {[]string{"box", "-f", zone}, `zone.json: a machine's time zone is set when it is made, and a set doesn't change it: leave "timeZone" out`},
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

// livellm devices prints the API's list; an API without one is named, and
// an empty list says so on stderr.
func TestDevicesCommand(t *testing.T) {
	const list = `{"devices":[{"id":"rtx5090","label":"NVIDIA RTX 5090","kind":"gpu","total":1,"free":0,"here":["box"]}]}`
	f := newFakeAPI(t)
	f.answers["GET /v1/machine-devices"] = answerJSON(list)
	stdout, stderr, err := captured(t, func() error { return cmdDevices(nil) })
	if err != nil {
		t.Fatal(err)
	}
	var got, want map[string]any
	_ = json.Unmarshal([]byte(stdout), &got)
	_ = json.Unmarshal([]byte(list), &want)
	if !reflect.DeepEqual(got, want) || stderr != "" {
		t.Errorf("printed %s / %q", stdout, stderr)
	}

	f = newFakeAPI(t)
	f.answers["GET /v1/machine-devices"] = answerJSON(`{"devices":[]}`)
	_, stderr, err = captured(t, func() error { return cmdDevices(nil) })
	if err != nil || !strings.Contains(stderr, "no devices are offered to this workspace's machines") {
		t.Errorf("empty: %v / %q", err, stderr)
	}

	f = newFakeAPI(t)
	f.answers["GET /v1/machine-devices"] = answerStatus(404, `{"error":"not found"}`)
	if err := cmdDevices(nil); err == nil || !strings.Contains(err.Error(), "this LiveLLM gives machines no devices yet") {
		t.Errorf("404: %v", err)
	}
}

// connect lists where a machine's port groups are reached: each port at its
// number plus the group's offset, on the host of its SSH address.
func TestConnectMachinePortGroups(t *testing.T) {
	f := newFakeAPI(t)
	f.answers["POST /v1/workloads/box/connect"] = answerJSON(`{"type":"vm-windows","tool":"ssh"}`)
	f.answers["GET /v1/status"] = answerJSON(`{"workloads":[{"id":"box","ssh":"203.0.113.10:30022"}]}`)
	f.answers["GET /v1/workspace"] = answerJSON(`{"spec":{"workloads":[{"id":"box","type":"vm-windows","vm":{"portGroups":[
		{"name":"moonlight","offset":-10000,"ports":[{"port":47984,"tcp":true},{"port":47998,"udp":true},{"port":48010,"tcp":true,"udp":true}]},
		{"name":"ftp","mirrored":true,"count":2,"offset":0,"allowCIDRs":["198.51.100.0/24"],"ports":[{"port":40000,"tcp":true},{"port":40001,"tcp":true}]},
		{"name":"new","mirrored":true,"count":4}]}}]}}`)
	stdout, _, err := captured(t, func() error { return cmdConnect([]string{"box"}) })
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		PortGroups []map[string]any `json:"portGroups"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("%v: %s", err, stdout)
	}
	want := []map[string]any{
		{"name": "moonlight", "offset": float64(-10000), "ports": []any{
			map[string]any{"port": float64(47984), "published": float64(37984), "address": "203.0.113.10:37984", "tcp": true, "udp": false},
			map[string]any{"port": float64(47998), "published": float64(37998), "address": "203.0.113.10:37998", "tcp": false, "udp": true},
			map[string]any{"port": float64(48010), "published": float64(38010), "address": "203.0.113.10:38010", "tcp": true, "udp": true},
		}},
		{"name": "ftp", "mirrored": true, "offset": float64(0), "allowCIDRs": []any{"198.51.100.0/24"}, "ports": []any{
			map[string]any{"port": float64(40000), "published": float64(40000), "address": "203.0.113.10:40000", "tcp": true, "udp": false},
			map[string]any{"port": float64(40001), "published": float64(40001), "address": "203.0.113.10:40001", "tcp": true, "udp": false},
		}},
		{"name": "new", "mirrored": true, "offset": float64(0), "ports": []any{}},
	}
	if !reflect.DeepEqual(out.PortGroups, want) {
		t.Errorf("portGroups %v, want %v", out.PortGroups, want)
	}

	// No SSH address yet: the published numbers alone. No groups: none.
	if g := groupAddresses(map[string]any{"portGroups": []any{map[string]any{"name": "g", "offset": float64(5), "ports": []any{map[string]any{"port": float64(10)}}}}}, ""); len(g) != 1 ||
		!reflect.DeepEqual(g[0]["ports"], []map[string]any{{"port": 10, "published": 15, "tcp": true, "udp": false}}) {
		t.Errorf("no address: %v", g)
	}
	if g := groupAddresses(map[string]any{}, "203.0.113.10:30022"); g != nil {
		t.Errorf("no groups: %v", g)
	}
}

// The device refusals keep the API's words and say what to do next.
func TestMachineNextDevices(t *testing.T) {
	for _, c := range []struct {
		status    int
		body, has string
	}{
		{403, `{"error":"NVIDIA RTX 5090 isn't offered to this workspace."}`, "no permission changes that: tell the user"},
		{409, `{"error":"NVIDIA RTX 5090 is in use by another machine (1 in the cluster)."}`, "livellm devices shows how many are free"},
		{409, `{"error":"NVIDIA RTX 5090: no node of this cluster has one."}`, "livellm devices shows how many are free"},
	} {
		err := machineNext(problemFrom(c.status, []byte(c.body)))
		var msg map[string]string
		_ = json.Unmarshal([]byte(c.body), &msg)
		if !strings.Contains(err.Error(), msg["error"]) || !strings.Contains(err.Error(), c.has) {
			t.Errorf("%d %s: %v", c.status, c.body, err)
		}
	}
	// Anything else is left as it was, and so is a next step the API sent.
	for _, c := range []struct {
		status int
		body   string
	}{
		{409, `{"error":"box: no free place on this cluster for group moonlight's 7 ports"}`},
		{403, `{"error":"isn't offered to this workspace","next":"ask the owner"}`},
	} {
		p := problemFrom(c.status, []byte(c.body))
		next := p.Next
		if machineNext(p); p.Next != next {
			t.Errorf("%s: next %q", c.body, p.Next)
		}
	}
}

// browser-api create takes --dedicated-cores, and refuses a machine's cores
// flags before anything is sent.
func TestBrowserAPICreateDedicatedCores(t *testing.T) {
	f := newFakeAPI(t)
	if err := browserAPICreate([]string{"pool", "--browsers", "b1,b2", "--dedicated-cores"}); err != nil {
		t.Fatal(err)
	}
	w := writesOf(f)
	if len(w) != 1 || w[0].path != "/v1/workloads/controller" || bodyOf(t, w[0])["dedicatedCores"] != true {
		t.Fatalf("sent %v", w)
	}
	f = newFakeAPI(t)
	if err := browserAPICreate([]string{"pool", "--browsers", "b1"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := bodyOf(t, writesOf(f)[0])["dedicatedCores"]; ok {
		t.Errorf("dedicatedCores sent without the flag")
	}
	for args, says := range map[string]string{
		"--cores 2":               "--cores goes with a machine",
		"--dedicated-cores false": `"false" is left over`,
	} {
		f := newFakeAPI(t)
		err := browserAPICreate(append([]string{"pool", "--browsers", "b1"}, strings.Fields(args)...))
		if err == nil || !strings.Contains(err.Error(), says) {
			t.Errorf("%s: %v", args, err)
		}
		if w := writesOf(f); len(w) != 0 {
			t.Errorf("%s: sent %v", args, w)
		}
	}
}
