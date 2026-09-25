package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The workspace verbs send what the API expects: method, path with its query,
// and body.
func TestWorkspaceRequests(t *testing.T) {
	type got struct {
		method, path string
		body         any
	}
	var last got
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/templates" && r.Method == "GET":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"templates":[
				{"id":"tpl_1","name":"small box","kind":"vm-ubuntu","config":{"vm":{"cpus":2,"memory":"4Gi"}}},
				{"id":"tpl_2","name":"twin","kind":"pod","config":{"pod":{"image":"nginx"}}},
				{"id":"tpl_3","name":"twin","kind":"pod","config":{"pod":{"image":"caddy"}}}]}`))
			return
		case r.URL.Path == "/v1/workspace":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"spec":{"workloads":[
				{"id":"box","type":"vm-ubuntu","vm":{"cpus":2,"credentials":{"username":"me","password":"x"}}},
				{"id":"web","type":"pod","pod":{"image":"nginx","env":[{"name":"A","value":"1"}],"secretEnv":[{"name":"S"}],
				  "imageAuth":{"server":"r"},"source":{"git":{"url":"https://g/x"},"build":"b1"}}},
				{"id":"pool","type":"controller","controller":{"browsers":["a"]}}]}}`))
			return
		case strings.HasSuffix(r.URL.Path, "/rdp-file"):
			last = got{method: r.Method, path: r.URL.RequestURI()}
			w.Header().Set("Content-Type", "application/x-rdp")
			_, _ = w.Write([]byte("full address:s:rdp\r\n"))
			return
		case strings.HasSuffix(r.URL.Path, "/thumbnail"):
			last = got{method: r.Method, path: r.URL.RequestURI()}
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte{0xff, 0xd8, 0xff})
			return
		}
		raw, _ := io.ReadAll(r.Body)
		last = got{method: r.Method, path: r.URL.RequestURI()}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &last.body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	t.Setenv("LIVELLM_API_URL", srv.URL)
	t.Setenv("LIVELLM_API_KEY", "llc_test")
	dir := t.TempDir()
	keys := filepath.Join(dir, "keys.pub")
	_ = os.WriteFile(keys, []byte("# mine\nssh-ed25519 AAAA me@laptop\n\nssh-rsa BBBB ci\n"), 0o600)
	extra := filepath.Join(dir, "extra.json")
	_ = os.WriteFile(extra, []byte(`{"credentials":{"username":"me","password":"p"}}`), 0o600)
	settings := filepath.Join(dir, "settings.json")
	_ = os.WriteFile(settings, []byte(`{"id":"ignored","cpu":"2","memory":"4Gi"}`), 0o600)
	stdout := os.Stdout
	devnull, _ := os.Open(os.DevNull)
	os.Stdout = devnull
	defer func() { os.Stdout = stdout }()

	obj := func(s string) any {
		var v any
		_ = json.Unmarshal([]byte(s), &v)
		return v
	}
	cases := []struct {
		name string
		run  func() error
		want got
	}{
		{"activity", func() error { return cmdActivity(nil) }, got{"GET", "/v1/activity", nil}},
		{"activity filtered", func() error {
			return cmdActivity([]string{"--actor", "platform", "--object", "web", "--limit", "10", "--before", "99"})
		}, got{"GET", "/v1/activity?actor=platform&before=99&limit=10&object=workload%3Aweb", nil}},
		{"monitoring", func() error { return cmdMonitoring(nil) }, got{"GET", "/v1/monitoring", nil}},
		{"one machine's monitor", func() error { return cmdMonitoring([]string{"box", "--range", "24h"}) },
			got{"GET", "/v1/workloads/box/monitor?range=24h", nil}},
		{"api-keys", func() error { return cmdAPIKeys(nil) }, got{"GET", "/v1/keys", nil}},
		{"api-keys create", func() error { return cmdAPIKeys([]string{"create", "ci"}) },
			got{"POST", "/v1/keys", obj(`{"name":"ci"}`)}},
		{"api-keys create with a permission", func() error {
			return cmdAPIKeys([]string{"create", "ci", "--permissions", "billing"})
		}, got{"POST", "/v1/keys", obj(`{"name":"ci","permissions":["billing"]}`)}},
		{"api-keys set", func() error { return cmdAPIKeys([]string{"set", "key_1", "--permissions", "billing"}) },
			got{"PATCH", "/v1/keys/key_1", obj(`{"permissions":["billing"]}`)}},
		{"api-keys set none", func() error { return cmdAPIKeys([]string{"set", "key_1", "--permissions", "none"}) },
			got{"PATCH", "/v1/keys/key_1", obj(`{"permissions":[]}`)}},
		{"api-keys rm", func() error { return cmdAPIKeys([]string{"rm", "key_1", "-y"}) },
			got{"DELETE", "/v1/keys/key_1", nil}},
		{"keys", func() error { return cmdKeys(nil) }, got{"GET", "/v1/ssh-keys", nil}},
		{"keys set from .pub lines", func() error { return cmdKeys([]string{"set", "-f", keys}) },
			got{"PUT", "/v1/ssh-keys", obj(`{"sshKeys":[{"key":"ssh-ed25519 AAAA me@laptop"},{"key":"ssh-rsa BBBB ci"}]}`)}},
		{"plan", func() error { return cmdPlan(nil) }, got{"GET", "/v1/billing", nil}},
		{"plan catalog", func() error { return cmdPlan([]string{"catalog"}) }, got{"GET", "/v1/subscriptions/catalog", nil}},
		{"plan set", func() error { return cmdPlan([]string{"set", "pro"}) },
			got{"PUT", "/v1/subscription", obj(`{"subscription":"pro"}`)}},
		{"plan metered", func() error { return cmdPlan([]string{"metered", "on"}) },
			got{"PUT", "/v1/billing-mode", obj(`{"metered":true}`)}},
		{"reservations", func() error { return cmdReservations(nil) }, got{"GET", "/v1/reservations", nil}},
		{"database", func() error { return cmdDatabase([]string{"db"}) }, got{"GET", "/v1/workloads/db/database", nil}},
		{"install", func() error { return cmdInstall([]string{"win"}) }, got{"GET", "/v1/workloads/win/install-progress", nil}},
		{"agents", func() error { return cmdAgents(nil) }, got{"GET", "/v1/agents", nil}},
		{"agents rm", func() error { return cmdAgents([]string{"rm", "ag_1", "-y"}) }, got{"DELETE", "/v1/agents/ag_1", nil}},
		{"invoices", func() error { return cmdInvoices(nil) }, got{"GET", "/v1/invoices", nil}},
		{"one invoice", func() error { return cmdInvoices([]string{"2026-09"}) }, got{"GET", "/v1/invoices/2026-09", nil}},
		{"backups rm", func() error { return cmdBackups([]string{"rm", "box", "snap-1", "-y"}) },
			got{"DELETE", "/v1/workloads/box/backups/snap-1", nil}},
		{"backups describe", func() error { return cmdBackups([]string{"describe", "box", "snap-1", "before the upgrade"}) },
			got{"PATCH", "/v1/workloads/box/backups/snap-1", obj(`{"description":"before the upgrade"}`)}},
		{"template save from a machine: no login", func() error {
			return cmdTemplate([]string{"save", "small box", "--from", "box", "--description", "for tests"})
		}, got{"POST", "/v1/templates", obj(`{"name":"small box","description":"for tests","kind":"vm-ubuntu","config":{"vm":{"cpus":2}}}`)}},
		{"template save from an app: no env, no pull login, no build", func() error {
			return cmdTemplate([]string{"save", "web", "--from", "web"})
		}, got{"POST", "/v1/templates", obj(`{"name":"web","kind":"pod","config":{"pod":{"image":"nginx","source":{"git":{"url":"https://g/x"}}}}}`)}},
		{"template save from a file", func() error {
			return cmdTemplate([]string{"save", "big browser", "--kind", "browser", "-f", settings})
		}, got{"POST", "/v1/templates", obj(`{"name":"big browser","kind":"browser","config":{"browser":{"cpu":"2","memory":"4Gi"}}}`)}},
		{"template rm by name", func() error { return cmdTemplate([]string{"rm", "small box", "-y"}) },
			got{"DELETE", "/v1/templates/tpl_1", nil}},
		{"create from a template", func() error {
			return cmdCreate([]string{"--template", "small box", "--id", "box2", "-f", extra})
		}, got{"POST", "/v1/workloads/vm-ubuntu", obj(`{"id":"box2","cpus":2,"memory":"4Gi","credentials":{"username":"me","password":"p"}}`)}},
		{"rdp", func() error { return cmdRDP([]string{"win", "--ttl", "8h", "-o", filepath.Join(dir, "win.rdp")}) },
			got{"GET", "/v1/workloads/win/rdp-file?ttl=8h", nil}},
		{"screenshot", func() error {
			return cmdScreenshot([]string{"desks", "--desktop", "2", "--width", "640", "-o", filepath.Join(dir, "d.jpg")})
		}, got{"GET", "/v1/workloads/desks/thumbnail?desktop=2&width=640", nil}},
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
	if b, _ := os.ReadFile(filepath.Join(dir, "win.rdp")); string(b) != "full address:s:rdp\r\n" {
		t.Errorf("the .rdp file was saved as %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "d.jpg")); len(b) != 3 || b[0] != 0xff {
		t.Errorf("the picture was saved as %v", b)
	}

	refused := map[string]func() error{
		"a template named twice":         func() error { return cmdTemplate([]string{"show", "twin"}) },
		"a Browser API as a template":    func() error { return cmdTemplate([]string{"save", "p", "--from", "pool"}) },
		"a template file without a kind": func() error { return cmdTemplate([]string{"save", "x", "-f", settings}) },
		"an rdp ttl that isn't a time":   func() error { return cmdRDP([]string{"win", "--ttl", "tomorrow"}) },
		"api-keys set without a list":    func() error { return cmdAPIKeys([]string{"set", "key_1"}) },
		"plan metered maybe":             func() error { return cmdPlan([]string{"metered", "maybe"}) },
		"create from a template, no id":  func() error { return cmdCreate([]string{"--template", "small box"}) },
	}
	for name, run := range refused {
		last = got{}
		if err := run(); err == nil {
			t.Errorf("%s should be refused", name)
		}
		if last.method != "" && last.method != "GET" {
			t.Errorf("%s sent %s %s before refusing", name, last.method, last.path)
		}
	}
}

// build --wait follows the build it started: an older build's "done" isn't
// taken for it, and a failure is an error.
func TestBuildWait(t *testing.T) {
	var polls int
	fail := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "POST" {
			_, _ = w.Write([]byte(`{"buildId":"new1"}`))
			return
		}
		polls++
		switch {
		case polls == 1:
			_, _ = w.Write([]byte(`{"stage":"live","done":true,"buildId":"old0"}`))
		case polls == 2:
			_, _ = w.Write([]byte(`{"stage":"building","buildId":"new1"}`))
		case fail:
			_, _ = w.Write([]byte(`{"stage":"building","failed":true,"message":"step 3","buildId":"new1","logs":[{"body":"no such file"}]}`))
		default:
			_, _ = w.Write([]byte(`{"stage":"live","done":true,"buildId":"new1","commit":"abc"}`))
		}
	}))
	defer srv.Close()
	t.Setenv("LIVELLM_API_URL", srv.URL)
	t.Setenv("LIVELLM_API_KEY", "llc_test")
	buildPoll = time.Millisecond
	stdout, stderr := os.Stdout, os.Stderr
	devnull, _ := os.Open(os.DevNull)
	os.Stdout, os.Stderr = devnull, devnull
	defer func() { os.Stdout, os.Stderr = stdout, stderr }()

	if err := cmdBuild([]string{"web", "--wait"}); err != nil {
		t.Fatalf("a build that goes live: %v", err)
	}
	if polls != 3 {
		t.Errorf("asked %d times, want 3 (the old build's done is not ours)", polls)
	}
	polls, fail = 0, true
	if err := cmdBuild([]string{"web", "--wait"}); err == nil {
		t.Error("a failed build should be an error")
	}
	polls, fail = 1, false
	if err := cmdBuild([]string{"web", "--wait", "--timeout", "1ns"}); err == nil {
		t.Error("a build still running at the timeout should be an error")
	}
}

// wait asks until the resource is ready, and says so when it failed or
// never came up.
func TestWait(t *testing.T) {
	phase := "Pending"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		ready := phase == "Running"
		_, _ = w.Write([]byte(`{"workloads":[{"id":"db","phase":"` + phase + `","ready":` + map[bool]string{true: "true", false: "false"}[ready] + `,"message":"m"}]}`))
		if phase == "Pending" {
			phase = "Running"
		}
	}))
	defer srv.Close()
	t.Setenv("LIVELLM_API_URL", srv.URL)
	t.Setenv("LIVELLM_API_KEY", "llc_test")
	waitPoll = time.Millisecond
	stdout, stderr := os.Stdout, os.Stderr
	devnull, _ := os.Open(os.DevNull)
	os.Stdout, os.Stderr = devnull, devnull
	defer func() { os.Stdout, os.Stderr = stdout, stderr }()
	if err := cmdWait([]string{"db"}); err != nil {
		t.Fatalf("ready on the second look: %v", err)
	}
	phase = "Failed"
	if err := cmdWait([]string{"db"}); err == nil {
		t.Error("a failed resource should be an error")
	}
	phase = "Starting"
	if err := cmdWait([]string{"db", "--timeout", "1ns"}); err == nil {
		t.Error("not ready in time should be an error")
	}
}
