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
				{"id":"tpl_3","name":"twin","kind":"pod","config":{"pod":{"image":"caddy"}}},
				{"id":"tpl_4","name":"shop","kind":"stack","config":{"stack":{"name":"shop","services":[
				  {"name":"web","id":"shop-web","pod":{"image":"shop","secretEnv":[{"name":"API_KEY","required":true}]}},
				  {"name":"worker","id":"shop-worker","pod":{"image":"shop","secretEnv":[{"name":"API_KEY","required":true}],
				    "imageAuth":{"registry":"r","username":"u","required":true}}}],
				  "databases":[{"name":"db","id":"shop-db","storage":{"engine":"postgres"}}]}}},
				{"id":"tpl_5","name":"solo","kind":"stack","config":{"stack":{"name":"solo","services":[
				  {"name":"solo","id":"solo","pod":{"image":"x","imageAuth":{"registry":"r","username":"u","required":true}}}]}}},
				{"id":"tpl_6","name":"api","kind":"pod","config":{"pod":{"image":"api","secretEnv":[{"name":"S","required":true}]}}}]}`))
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
	stack := filepath.Join(dir, "stack.json")
	_ = os.WriteFile(stack, []byte(`{"apps":[{"id":"shop-web","image":"shop","databases":[{"id":"shop-db","env":{"DATABASE_URL":"url"}}]}],
		"databases":[{"id":"shop-db","engine":"postgres"}]}`), 0o600)
	cache := filepath.Join(dir, "cache.json")
	_ = os.WriteFile(cache, []byte(`[{"id":"nextcloud-cache","hostname":"cache","image":"redis:7"}]`), 0o600)
	cacheJoin := filepath.Join(dir, "cache-join.json")
	_ = os.WriteFile(cacheJoin, []byte(`{"apps":[{"id":"nextcloud-cache","image":"redis:7"}],"join":"nextcloud"}`), 0o600)
	stackSecrets := filepath.Join(dir, "stack-secrets.json")
	_ = os.WriteFile(stackSecrets, []byte(`{"services":{"worker":{"imagePassword":"pw"}}}`), 0o600)
	withEnv := filepath.Join(dir, "with-env.json")
	_ = os.WriteFile(withEnv, []byte(`{"secretEnv":{"S":"v"},"env":[{"name":"A","value":"1"}]}`), 0o600)
	t.Setenv("SHOP_API_KEY", "from-env")
	t.Setenv("EMPTY_VAR", "")
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
		{"one machine's monitor, its id after the flag", func() error { return cmdMonitoring([]string{"--range", "6h", "box"}) },
			got{"GET", "/v1/workloads/box/monitor?range=6h", nil}},
		{"a Browser API as a template: LiveLLM reads it", func() error { return cmdTemplate([]string{"save", "p", "--from", "pool"}) },
			got{"POST", "/v1/templates", obj(`{"name":"p","from":"pool"}`)}},
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
		{"template save from a machine: LiveLLM reads it, without the login", func() error {
			return cmdTemplate([]string{"save", "small box", "--from", "box", "--description", "for tests"})
		}, got{"POST", "/v1/templates", obj(`{"name":"small box","description":"for tests","from":"box"}`)}},
		{"template save from an app of a Composable App: LiveLLM saves the whole app", func() error {
			return cmdTemplate([]string{"save", "shop", "--from", "shop-web"})
		}, got{"POST", "/v1/templates", obj(`{"name":"shop","from":"shop-web"}`)}},
		{"template save of a Composable App from a file", func() error {
			return cmdTemplate([]string{"save", "shop", "--kind", "stack", "-f", settings})
		}, got{"POST", "/v1/templates", obj(`{"name":"shop","kind":"stack","config":{"stack":{"cpu":"2","memory":"4Gi"}}}`)}},
		{"template save from a file", func() error {
			return cmdTemplate([]string{"save", "big browser", "--kind", "browser", "-f", settings})
		}, got{"POST", "/v1/templates", obj(`{"name":"big browser","kind":"browser","config":{"browser":{"cpu":"2","memory":"4Gi"}}}`)}},
		{"template rm by name", func() error { return cmdTemplate([]string{"rm", "small box", "-y"}) },
			got{"DELETE", "/v1/templates/tpl_1", nil}},
		{"create from a template: the new id and the login", func() error {
			return cmdCreate([]string{"--template", "small box", "--id", "box2", "-f", extra})
		}, got{"POST", "/v1/templates/tpl_1/create", obj(`{"id":"box2","credentials":{"username":"me","password":"p"}}`)}},
		{"create from a template: a login from --secret", func() error {
			return cmdCreate([]string{"--template", "tpl_1", "--id", "box3", "--secret", "credentials.username=me", "--secret", "credentials.password=a=b"})
		}, got{"POST", "/v1/templates/tpl_1/create", obj(`{"id":"box3","credentials":{"username":"me","password":"a=b"}}`)}},
		{"create from an app's template: a bare name is a secret env value", func() error {
			return cmdCreate([]string{"--template", "api", "--id", "api2", "--secret", "S=v", "--secret", "imagePassword=x",
				"--secret", "portPasswords.http.alice=pw", "--secret", "gitToken=t"})
		}, got{"POST", "/v1/templates/tpl_6/create", obj(`{"id":"api2","secretEnv":{"S":"v"},"imagePassword":"x","portPasswords":{"http":{"alice":"pw"}},"gitToken":"t"}`)}},
		{"create a Composable App from its template: a secret goes to every service that has it", func() error {
			return cmdCreate([]string{"--template", "shop", "--id", "shop2", "--secret", "API_KEY=k", "-f", stackSecrets})
		}, got{"POST", "/v1/templates/tpl_4/create", obj(`{"name":"shop2","services":{
			"web":{"secretEnv":{"API_KEY":"k"}},"worker":{"secretEnv":{"API_KEY":"k"},"imagePassword":"pw"}}}`)}},
		{"create a Composable App from its template: one service's secret, from a variable", func() error {
			return cmdCreate([]string{"--template", "shop", "--name", "shop3", "--secret", "services.web.secretEnv.API_KEY=w",
				"--secret-env", "services.worker.secretEnv.API_KEY=SHOP_API_KEY", "--secret", "services.worker.imagePassword=pw"})
		}, got{"POST", "/v1/templates/tpl_4/create", obj(`{"name":"shop3","services":{
			"web":{"secretEnv":{"API_KEY":"w"}},"worker":{"secretEnv":{"API_KEY":"from-env"},"imagePassword":"pw"}}}`)}},
		{"create an app of one service from its template: a secret goes to that service", func() error {
			return cmdCreate([]string{"--template", "solo", "--id", "solo2", "--secret", "imagePassword=pw"})
		}, got{"POST", "/v1/templates/tpl_5/create", obj(`{"name":"solo2","services":{"solo":{"imagePassword":"pw"}}}`)}},
		{"create apps with their databases, linked", func() error { return cmdCreate([]string{"apps", "-f", stack}) },
			got{"POST", "/v1/workloads", obj(`{"apps":[{"id":"shop-web","image":"shop","databases":[{"id":"shop-db","env":{"DATABASE_URL":"url"}}]}],
				"databases":[{"id":"shop-db","engine":"postgres"}]}`)}},
		{"create apps onto an existing app", func() error { return cmdCreate([]string{"apps", "-f", cache, "--join", "nextcloud"}) },
			got{"POST", "/v1/workloads", obj(`{"apps":[{"id":"nextcloud-cache","hostname":"cache","image":"redis:7"}],"join":"nextcloud"}`)}},
		{"create apps onto an existing app, named in the file", func() error { return cmdCreate([]string{"apps", "-f", cacheJoin}) },
			got{"POST", "/v1/workloads", obj(`{"apps":[{"id":"nextcloud-cache","image":"redis:7"}],"join":"nextcloud"}`)}},
		{"rm", func() error { return cmdRemove([]string{"web", "-y"}) }, got{"DELETE", "/v1/workloads/web", nil}},
		{"rm an app with its databases", func() error { return cmdRemove([]string{"web", "--with-databases", "-y"}) },
			got{"DELETE", "/v1/workloads/web?withDatabases=true", nil}},
		{"rm with force", func() error { return cmdRemove([]string{"web", "-y", "--force", "--with-databases"}) },
			got{"DELETE", "/v1/workloads/web?force=true&withDatabases=true", nil}},
		{"rdp", func() error { return cmdRDP([]string{"win", "--ttl", "8h", "-o", filepath.Join(dir, "win.rdp")}) },
			got{"GET", "/v1/workloads/win/rdp-file?ttl=8h", nil}},
		{"screenshot", func() error {
			return cmdScreenshot([]string{"desk", "--width", "640", "-o", filepath.Join(dir, "d.jpg")})
		}, got{"GET", "/v1/workloads/desk/thumbnail?width=640", nil}},
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
		"a template file without a kind": func() error { return cmdTemplate([]string{"save", "x", "-f", settings}) },
		"an rdp ttl that isn't a time":   func() error { return cmdRDP([]string{"win", "--ttl", "tomorrow"}) },
		"api-keys set without a list":    func() error { return cmdAPIKeys([]string{"set", "key_1"}) },
		"plan metered maybe":             func() error { return cmdPlan([]string{"metered", "maybe"}) },
		"create from a template, no id":  func() error { return cmdCreate([]string{"--template", "small box"}) },
		"create from a template with settings in -f": func() error {
			return cmdCreate([]string{"--template", "api", "--id", "x", "-f", withEnv})
		},
		"a Composable App's secret no service has": func() error {
			return cmdCreate([]string{"--template", "shop", "--id", "x", "--secret", "NOPE=1"})
		},
		"a Composable App's image password, no service named": func() error {
			return cmdCreate([]string{"--template", "shop", "--id", "x", "--secret", "imagePassword=1"})
		},
		"a service's secret on an app's template": func() error {
			return cmdCreate([]string{"--template", "api", "--id", "x", "--secret", "services.web.secretEnv.S=1"})
		},
		"a secret from an empty variable": func() error {
			return cmdCreate([]string{"--template", "api", "--id", "x", "--secret-env", "S=EMPTY_VAR"})
		},
		"a secret without a value": func() error {
			return cmdCreate([]string{"--template", "api", "--id", "x", "--secret", "S"})
		},
		"a whole block as one secret": func() error {
			return cmdCreate([]string{"--template", "api", "--id", "x", "--secret", "credentials=1"})
		},
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
	phase, stopped := "Pending", false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/workspace" {
			_, _ = w.Write([]byte(`{"spec":{"workloads":[{"id":"db","type":"storage","stopped":` + map[bool]string{true: "true", false: "false"}[stopped] + `}]}}`))
			return
		}
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
	// Stopped and meant to be: said at once, not after the timeout.
	phase, stopped = "Stopped", true
	if err := cmdWait([]string{"db", "--timeout", "1h"}); err == nil || !strings.Contains(err.Error(), "is stopped") {
		t.Errorf("a stopped resource: %v, want \"db is stopped\"", err)
	}
	// Just started (reads Stopped for a moment): it waits.
	phase, stopped = "Stopped", false
	if err := cmdWait([]string{"db", "--timeout", "1ns"}); err == nil || strings.Contains(err.Error(), "is stopped") {
		t.Errorf("a resource just started: %v, want the timeout", err)
	}
}

// A create from a template refused for the secrets it still needs says which
// flags give them; one that works prints what was made, databases too.
func TestTemplateCreateAnswers(t *testing.T) {
	missing := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/templates":
			_, _ = w.Write([]byte(`{"templates":[{"id":"tpl_4","name":"shop","kind":"stack","config":{"stack":{"name":"shop","services":[{"name":"web","id":"shop-web","pod":{}}]}}}]}`))
		case missing:
			w.WriteHeader(422)
			_, _ = w.Write([]byte(`{"error":"the template needs these secrets to create from it: services.web.secretEnv.API_KEY, secretEnv.S","missing":["services.web.secretEnv.API_KEY","secretEnv.S"]}`))
		default:
			w.WriteHeader(202)
			_, _ = w.Write([]byte(`{"tenant":"acme","created":["shop2-web"],"databases":["shop2-db"],"status":"updated"}`))
		}
	}))
	defer srv.Close()
	t.Setenv("LIVELLM_API_URL", srv.URL)
	t.Setenv("LIVELLM_API_KEY", "llc_test")
	stdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = stdout }()

	err := cmdCreate([]string{"--template", "shop", "--id", "shop2"})
	var p *problem
	if !asProblem(err, &p) || p.Status != 422 {
		t.Fatalf("want the 422, got %v", err)
	}
	if want := "add --secret services.web.secretEnv.API_KEY=… --secret S=…"; p.Next != want {
		t.Errorf("next: %q, want %q", p.Next, want)
	}
	missing = false
	if err := cmdCreate([]string{"--template", "shop", "--id", "shop2"}); err != nil {
		t.Fatal(err)
	}
	w.Close()
	raw, _ := io.ReadAll(r)
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("printed %q: %v", raw, err)
	}
	want := map[string]any{"template": "shop", "type": "stack", "created": []any{"shop2-web"}, "databases": []any{"shop2-db"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("printed %v, want %v", got, want)
	}
}

// ls shows an app's database links as its settings hold them, and the apps a
// database serves.
func TestListShowsDatabaseLinks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/workspace" {
			_, _ = w.Write([]byte(`{"spec":{"workloads":[{"id":"web","type":"pod","pod":{"databases":[{"id":"db","env":{"DATABASE_URL":"url"}}]}},{"id":"db","type":"storage"}]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"workloads":[{"id":"web","phase":"Running","databases":["db"]},{"id":"db","phase":"Running","usedBy":["web"]}]}`))
	}))
	defer srv.Close()
	t.Setenv("LIVELLM_API_URL", srv.URL)
	t.Setenv("LIVELLM_API_KEY", "llc_test")
	all, err := resources()
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]resource{}
	for _, r := range all {
		by[r.ID] = r
	}
	if want := []map[string]any{{"id": "db", "env": map[string]any{"DATABASE_URL": "url"}}}; !reflect.DeepEqual(by["web"].Databases, want) {
		t.Errorf("web's links: %v, want %v", by["web"].Databases, want)
	}
	if !reflect.DeepEqual(by["db"].UsedBy, []string{"web"}) || by["web"].UsedBy != nil || by["db"].Databases != nil {
		t.Errorf("usedBy: web %v, db %v", by["web"].UsedBy, by["db"].UsedBy)
	}
}

// A delete whose answer is lost on the way (LiveLLM took longer than the
// connection lasted) is looked at again: gone is deleted, and the databases
// made with the app say which went.
func TestRemoveWhenTheAnswerIsLost(t *testing.T) {
	deleted := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			deleted = true
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close() // no answer at all
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if !deleted {
			_, _ = w.Write([]byte(`{"spec":{"workloads":[{"id":"web","type":"pod"},
				{"id":"db","type":"storage","storage":{"createdWith":["web"]}},
				{"id":"cache","type":"storage","storage":{"createdWith":["web"]}},
				{"id":"other","type":"storage","storage":{}}]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"spec":{"workloads":[{"id":"cache","type":"storage","storage":{"createdWith":["web"]}},{"id":"other","type":"storage"}]}}`))
	}))
	defer srv.Close()
	t.Setenv("LIVELLM_API_URL", srv.URL)
	t.Setenv("LIVELLM_API_KEY", "llc_test")
	stdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = stdout }()
	if err := cmdRemove([]string{"web", "--with-databases", "-y"}); err != nil {
		t.Fatalf("the workspace shows it gone: %v", err)
	}
	w.Close()
	raw, _ := io.ReadAll(r)
	var got map[string]any
	_ = json.Unmarshal(raw, &got)
	want := map[string]any{"deleted": []any{"db"}, "kept": []any{"cache"}}
	if got["deleted"] != "web" || !reflect.DeepEqual(got["databases"], want) {
		t.Errorf("printed %s", raw)
	}
	// Still there after a lost answer: the error stands.
	deleted = false
	os.Stdout, _ = os.Open(os.DevNull)
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"spec":{"workloads":[{"id":"web","type":"pod"}]}}`))
	})
	if err := cmdRemove([]string{"web", "-y"}); err == nil {
		t.Error("a delete with no answer and the app still there should be an error")
	}
}

// A port password's username may hold dots: --secret keeps it whole, as the
// refusal's missing path names it.
func TestSecretPathKeepsAUsernameWhole(t *testing.T) {
	pod := &template{Name: "blog", Kind: "pod"}
	stack := &template{Name: "shop", Kind: "stack", Config: map[string]any{"stack": map[string]any{
		"services": []any{map[string]any{"name": "web", "pod": map[string]any{}}}}}}
	for _, c := range []struct {
		t    *template
		path string
		want string
	}{
		{pod, "portPasswords.http.alice.smith", `{"portPasswords":{"http":{"alice.smith":"pw"}}}`},
		{pod, "secretEnv.API_KEY", `{"secretEnv":{"API_KEY":"pw"}}`},
		{stack, "services.web.portPasswords.http.a@b.com", `{"services":{"web":{"portPasswords":{"http":{"a@b.com":"pw"}}}}}`},
		{stack, "portPasswords.http.alice.smith", `{"services":{"web":{"portPasswords":{"http":{"alice.smith":"pw"}}}}}`},
	} {
		body := map[string]any{}
		if err := putSecret(body, c.t, c.path, "pw"); err != nil {
			t.Fatalf("%s: %v", c.path, err)
		}
		if b, _ := json.Marshal(body); string(b) != c.want {
			t.Errorf("%s: %s, want %s", c.path, b, c.want)
		}
	}
}
