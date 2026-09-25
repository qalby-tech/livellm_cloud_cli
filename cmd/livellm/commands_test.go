package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
)

// The screen and command verbs send what the API expects: the method, the
// path and the body, flags included.
func TestScreenAndCommandRequests(t *testing.T) {
	type got struct {
		method, path string
		body         map[string]any
	}
	var last got
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		last = got{method: r.Method, path: r.URL.Path}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &last.body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	t.Setenv("LIVELLM_API_URL", srv.URL)
	t.Setenv("LIVELLM_API_KEY", "llc_test")
	stdout := os.Stdout
	devnull, _ := os.Open(os.DevNull)
	os.Stdout = devnull
	defer func() { os.Stdout = stdout }()

	cases := []struct {
		name string
		run  func() error
		want got
	}{
		{"exec", func() error {
			return cmdExec([]string{"box", "uname -a", "--session", "s1", "--timeout", "120", "--desktop", "0"})
		}, got{"POST", "/v1/workloads/box/exec", map[string]any{
			"command": "uname -a", "session": "s1", "timeout": float64(120), "desktop": float64(0)}}},
		{"exec defaults", func() error { return cmdExec([]string{"box", "ls"}) },
			got{"POST", "/v1/workloads/box/exec", map[string]any{"command": "ls", "timeout": float64(60)}}},
		{"share view", func() error { return cmdShare([]string{"box"}) },
			got{"POST", "/v1/workloads/box/shares", map[string]any{"mode": "view"}}},
		{"share control", func() error {
			return cmdShare([]string{"desks", "--control", "--for", "7d", "--desktop", "2"})
		}, got{"POST", "/v1/workloads/desks/shares", map[string]any{
			"mode": "control", "for": "7d", "desktop": float64(2)}}},
		{"shares", func() error { return cmdShares([]string{"box"}) },
			got{"GET", "/v1/workloads/box/shares", nil}},
		{"unshare", func() error { return cmdUnshare([]string{"box", "sh1"}) },
			got{"DELETE", "/v1/workloads/box/shares/sh1", nil}},
		{"release", func() error { return cmdRelease([]string{"box"}) },
			got{"DELETE", "/v1/workloads/box/reservation", nil}},
		{"connect a desktop", func() error {
			return cmdConnect([]string{"desks", "--tool", "computer", "--desktop", "1", "--screen-width", "1024", "--format", "jpeg"})
		}, got{"POST", "/v1/workloads/desks/connect", map[string]any{
			"tool": "computer", "desktop": float64(1),
			"screen": map[string]any{"width": float64(1024), "format": "jpeg"}}}},
	}
	for _, c := range cases {
		last = got{}
		if err := c.run(); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if last.method != c.want.method || last.path != c.want.path || !reflect.DeepEqual(last.body, c.want.body) {
			t.Errorf("%s: sent %s %s %v, want %s %s %v", c.name,
				last.method, last.path, last.body, c.want.method, c.want.path, c.want.body)
		}
	}
	if err := cmdExec([]string{"box"}); err == nil {
		t.Error("exec without a command should be refused")
	}
}

// stop and start patch "stopped" alone, so a change made meanwhile is kept;
// they read the resource only to refuse what can't stop and to skip a no-op.
func TestStopAndStart(t *testing.T) {
	workloads := []map[string]any{
		{"id": "web", "type": "pod", "pod": map[string]any{"image": "nginx"}},
		{"id": "box", "type": "vm-ubuntu", "stopped": true, "vm": map[string]any{"cpus": float64(2)}},
		{"id": "db", "type": "storage", "storage": map[string]any{"engine": "postgres"}},
		{"id": "chrome", "type": "browser", "browser": map[string]any{}},
	}
	type write struct {
		method, path string
		body         map[string]any
	}
	var writes []write
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/workspace":
			_ = json.NewEncoder(w).Encode(map[string]any{"name": "ws", "spec": map[string]any{"workloads": workloads}})
		case r.Method == "GET":
			w.WriteHeader(404)
		default:
			var b map[string]any
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &b)
			writes = append(writes, write{r.Method, r.URL.Path, b})
			_, _ = w.Write([]byte(`{"status":"accepted"}`))
		}
	}))
	defer srv.Close()
	t.Setenv("LIVELLM_API_URL", srv.URL)
	t.Setenv("LIVELLM_API_KEY", "llc_test")
	quiet(t)

	if err := cmdStop([]string{"web"}); err != nil {
		t.Fatal(err)
	}
	want := []write{{"PATCH", "/v1/workloads/web", map[string]any{"stopped": true}}}
	if !reflect.DeepEqual(writes, want) {
		t.Fatalf("stop sent %v, want %v", writes, want)
	}
	if err := cmdStart([]string{"box"}); err != nil {
		t.Fatal(err)
	}
	want = append(want, write{"PATCH", "/v1/workloads/box", map[string]any{"stopped": false}})
	if !reflect.DeepEqual(writes, want) {
		t.Errorf("start sent %v", writes[1:])
	}

	// already in that state: nothing is written
	if err := cmdStop([]string{"box"}); err != nil {
		t.Fatal(err)
	}
	if err := cmdStop([]string{"nope"}); err == nil {
		t.Error("stopping something that isn't there should be refused")
	}
	if err := cmdStart(nil); err == nil {
		t.Error("start without an id should be refused")
	}
	// a database or a browser can't be stopped: refused, nothing written
	for _, id := range []string{"db", "chrome"} {
		if err := cmdStop([]string{id}); err == nil {
			t.Errorf("stopping %s should be refused", id)
		}
		if err := cmdStart([]string{id}); err == nil {
			t.Errorf("starting %s should be refused", id)
		}
	}
	if len(writes) != 2 {
		t.Errorf("no-op and refused stops wrote %v", writes[2:])
	}
}

// quiet sends stdout to /dev/null for the rest of the test.
func quiet(t *testing.T) {
	t.Helper()
	stdout := os.Stdout
	devnull, _ := os.Open(os.DevNull)
	os.Stdout = devnull
	t.Cleanup(func() { os.Stdout = stdout; devnull.Close() })
}

// recorder answers every call with {} and keeps the last one.
type recorded struct {
	method, path string
	body         map[string]any
}

func recorder(t *testing.T, last *recorded) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		*last = recorded{method: r.Method, path: r.URL.Path}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &last.body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("LIVELLM_API_URL", srv.URL)
	t.Setenv("LIVELLM_API_KEY", "llc_test")
	quiet(t)
}

// The Browser API verbs and set send what the API expects.
func TestBrowserAPIAndSetRequests(t *testing.T) {
	var last recorded
	recorder(t, &last)
	dir := t.TempDir()
	patch := dir + "/p.json"
	_ = os.WriteFile(patch, []byte(`{"pod":{"cpu":"1"},"stopped":null}`), 0o600)
	api := dir + "/api.json"
	_ = os.WriteFile(api, []byte(`{"id":"scrapers","browsers":["a"]}`), 0o600)

	cases := []struct {
		name string
		run  func() error
		want recorded
	}{
		{"create named", func() error {
			return cmdBrowserAPI([]string{"create", "scrapers", "--browsers", "agent-1, agent-2"})
		}, recorded{"POST", "/v1/workloads/controller", map[string]any{
			"id": "scrapers", "autodiscover": false, "browsers": []any{"agent-1", "agent-2"}}}},
		{"create every browser", func() error {
			return cmdBrowserAPI([]string{"create", "all", "--all"})
		}, recorded{"POST", "/v1/workloads/controller", map[string]any{"id": "all", "autodiscover": true}}},
		{"create with a remote", func() error {
			return cmdBrowserAPI([]string{"create", "mix", "--browsers", "a", "--remote", "office=wss://o.example.com/devtools/browser/x"})
		}, recorded{"POST", "/v1/workloads/controller", map[string]any{
			"id": "mix", "autodiscover": false, "browsers": []any{"a"},
			"externalBrowsers": []any{map[string]any{"id": "office", "wsUrl": "wss://o.example.com/devtools/browser/x"}}}}},
		{"create with a remote's login from the environment", func() error {
			t.Setenv("OFFICE_AUTH", " X-Token: s3cret ")
			return cmdBrowserAPI([]string{"create", "mix", "--all", "--remote", "office=wss://o", "--remote-auth", "office=OFFICE_AUTH"})
		}, recorded{"POST", "/v1/workloads/controller", map[string]any{
			"id": "mix", "autodiscover": true,
			"externalBrowsers": []any{map[string]any{"id": "office", "wsUrl": "wss://o", "authHeader": "X-Token: s3cret"}}}}},
		{"create from a file", func() error { return cmdCreate([]string{"browser-api", "-f", api}) },
			recorded{"POST", "/v1/workloads/controller", map[string]any{"id": "scrapers", "browsers": []any{"a"}}}},
		{"add", func() error { return cmdBrowserAPI([]string{"add", "scrapers", "agent-3"}) },
			recorded{"PUT", "/v1/workloads/scrapers/browsers/agent-3", map[string]any{}}},
		{"remove", func() error { return cmdBrowserAPI([]string{"remove", "scrapers", "agent-3"}) },
			recorded{"DELETE", "/v1/workloads/scrapers/browsers/agent-3", nil}},
		{"set", func() error { return cmdSet([]string{"web", "-f", patch}) },
			recorded{"PATCH", "/v1/workloads/web", map[string]any{"pod": map[string]any{"cpu": "1"}, "stopped": nil}}},
	}
	for _, c := range cases {
		last = recorded{}
		if err := c.run(); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !reflect.DeepEqual(last, c.want) {
			t.Errorf("%s: sent %v, want %v", c.name, last, c.want)
		}
	}

	refused := map[string][]string{
		"no browsers":          {"create", "empty"},
		"all and names":        {"create", "x", "--all", "--browsers", "a"},
		"remote not ws":        {"create", "x", "--remote", "office=https://o"},
		"remote without id":    {"create", "x", "--remote", "wss://o"},
		"add without a name":   {"add", "scrapers"},
		"login without remote": {"create", "x", "--all", "--remote-auth", "office=OFFICE_AUTH"},
		"login var unset":      {"create", "x", "--remote", "office=wss://o", "--remote-auth", "office=LIVELLM_TEST_UNSET_VAR"},
		"login not name=var":   {"create", "x", "--remote", "office=wss://o", "--remote-auth", "office"},
		"unknown verb":         {"grow", "scrapers"},
	}
	for name, args := range refused {
		last = recorded{}
		if err := cmdBrowserAPI(args); err == nil {
			t.Errorf("%s: should be refused", name)
		}
		if last.method != "" {
			t.Errorf("%s: sent %v", name, last)
		}
	}
}

// show reads the Browser API's browsers from the workspace and how they are
// doing from the status.
func TestBrowserAPIShow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/workspace":
			_, _ = w.Write([]byte(`{"spec":{"workloads":[
				{"id":"scrapers","type":"controller","controller":{"browsers":["agent-2"],"externalBrowsers":[{"id":"office","wsUrl":"wss://o"}]}},
				{"id":"agent-2","type":"browser"}]}}`))
		case "/v1/status":
			_, _ = w.Write([]byte(`{"workloads":[{"id":"scrapers","phase":"Running","ready":true,"browsers":[{"id":"agent-2","tabs":3}]}]}`))
		}
	}))
	defer srv.Close()
	t.Setenv("LIVELLM_API_URL", srv.URL)
	t.Setenv("LIVELLM_API_KEY", "llc_test")
	r, w, _ := os.Pipe()
	stdout := os.Stdout
	os.Stdout = w
	err := browserAPIShow([]string{"scrapers"})
	w.Close()
	os.Stdout = stdout
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.NewDecoder(r).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["drives"] != "only these" || !reflect.DeepEqual(got["browsers"], []any{"agent-2"}) ||
		!reflect.DeepEqual(got["remoteBrowsers"], []any{"office"}) || got["state"] != "running" || got["answering"] == nil {
		t.Errorf("show: %v", got)
	}
	quiet(t)
	if err := browserAPIShow([]string{"agent-2"}); err == nil {
		t.Error("show on a browser should say it isn't a Browser API")
	}
}

// connect gives an app's raw ports the host:port the status reports.
func TestConnectRawAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/status" {
			_, _ = w.Write([]byte(`{"workloads":[{"id":"mc","endpoints":[{"name":"game","tcp":true,"addr":"h:31000"},{"name":"voice","udp":true,"addr":"h:31001"}]}]}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	t.Setenv("LIVELLM_API_URL", srv.URL)
	t.Setenv("LIVELLM_API_KEY", "llc_test")
	out := map[string]any{"urls": []any{
		map[string]any{"port": "game", "raw": true},
		map[string]any{"port": "voice", "raw": true},
		map[string]any{"port": "http", "url": "https://x"},
	}}
	fillRawAddresses("mc", out)
	u := out["urls"].([]any)
	if g := u[0].(map[string]any); g["address"] != "h:31000" || g["protocol"] != "tcp" {
		t.Errorf("tcp port: %v", g)
	}
	if v := u[1].(map[string]any); v["address"] != "h:31001" || v["protocol"] != "udp" {
		t.Errorf("udp port: %v", v)
	}
	if h := u[2].(map[string]any); h["address"] != nil {
		t.Errorf("an HTTP port got an address: %v", h)
	}
}
