package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Inside access: reach show and set, --reachable-from on create, the Network
// permission on keys, and connect's inside line, against a fake API,
// compared byte for byte with goldenInsideWant in golden_inside_data_test.go
// (go test -run GoldenInside -update writes it again).

const insideWorkspace = `{"name":"ws","spec":{"workloads":[
	{"id":"web","type":"pod","reachableFrom":["edge"],"pod":{"image":"nginx","stack":"shop","hostname":"web",
		"ports":[{"name":"http","port":8080},{"name":"game","port":7777,"udp":true}],"databases":[{"id":"db","env":{"DATABASE_URL":"url"}}]}},
	{"id":"worker","type":"pod","reachableFrom":["edge"],"pod":{"image":"busybox","stack":"shop","dependsOn":["cache"]}},
	{"id":"edge","type":"pod","reachableFrom":[],"pod":{"image":"nginx","ports":[{"name":"http","port":80}],"databases":[{"id":"cache"}]}},
	{"id":"old","type":"pod","pod":{"image":"nginx"}},
	{"id":"db","type":"storage","storage":{"engine":"postgres"}},
	{"id":"cache","type":"storage","storage":{"engine":"redis"}},
	{"id":"lone","type":"storage","storage":{"engine":"postgres"}},
	{"id":"b1","type":"browser","reachableFrom":[]},
	{"id":"pool","type":"controller","reachableFrom":["edge"],"controller":{"autodiscover":false,"browsers":["b1"]}},
	{"id":"every","type":"controller","controller":{"autodiscover":true}},
	{"id":"box","type":"vm-ubuntu","reachableFrom":["*"],"vm":{"ports":[{"name":"web","port":8080},{"name":"pg","port":5432,"tcp":true},{"name":"dns","port":53,"udp":true},{"name":"etcd","port":2380,"internal":true}],"databases":[{"id":"cache"}]}},
	{"id":"win","type":"vm-windows","reachableFrom":["box"],"vm":{}},
	{"id":"desk","type":"desktop","reachableFrom":[],"desktop":{"databases":[{"id":"db"}]}}]}}`

const insideConnect = `{"tool":"api","type":"storage","engine":"postgres",
	"private":{"host":"ws-db-rw","port":5432},
	"inside":{"alsoFrom":[{"id":"web","why":"links it"},{"id":"desk","why":"links it"}],
		"addresses":[{"host":"ws-db-rw","port":5432}]}}`

func TestGoldenInsideAccess(t *testing.T) {
	file := func(name, body string) string {
		f := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return f
	}
	cases := []struct {
		name string
		run  func() error
	}{
		{"reach-show-app", func() error { return cmdReach([]string{"web"}) }},
		{"reach-show-database-linked", func() error { return cmdReach([]string{"db"}) }},
		{"reach-show-absent", func() error { return cmdReach([]string{"old"}) }},
		{"reach-show-database-redis", func() error { return cmdReach([]string{"cache"}) }},
		{"reach-show-database-unlinked", func() error { return cmdReach([]string{"lone"}) }},
		{"reach-show-browser-driven", func() error { return cmdReach([]string{"b1"}) }},
		{"reach-show-closed", func() error { return cmdReach([]string{"edge"}) }},
		{"reach-show-machine", func() error { return cmdReach([]string{"box"}) }},
		{"reach-show-windows", func() error { return cmdReach([]string{"win"}) }},
		{"reach-show-browser-api", func() error { return cmdReach([]string{"pool"}) }},
		{"reach-show-desktop", func() error { return cmdReach([]string{"desk"}) }},
		{"reach-set-from", func() error { return cmdReach([]string{"edge", "--from", "web,box"}) }},
		{"reach-set-star", func() error { return cmdReach([]string{"edge", "--from", "*"}) }},
		{"link-app", func() error { return cmdLink([]string{"edge", "db"}) }},
		{"link-app-stack", func() error { return cmdLink([]string{"worker", "cache,lone"}) }},
		{"link-machine", func() error { return cmdLink([]string{"win", "cache", "db"}) }},
		{"link-desktop", func() error { return cmdLink([]string{"desk", "cache"}) }},
		{"link-already", func() error { return cmdLink([]string{"box", "cache"}) }},
		{"link-some-already", func() error { return cmdLink([]string{"box", "cache", "lone"}) }},
		{"link-remove-with-variables", func() error { return cmdLink([]string{"web", "db", "--remove"}) }},
		{"link-remove-machine", func() error { return cmdLink([]string{"box", "--remove", "cache"}) }},
		{"link-remove-not-linked", func() error { return cmdLink([]string{"edge", "--remove", "db"}) }},
		{"create-vm-database", func() error {
			return cmdCreate([]string{"vm-ubuntu", "-f", file("v.json", `{"id":"m2","databases":[{"id":"lone"}]}`), "--database", "cache", "--database", "db"})
		}},
		{"create-desktop-database", func() error {
			return cmdCreate([]string{"desktop", "-f", file("d.json", `{"id":"d2"}`), "--database", "db"})
		}},
		{"reach-set-none", func() error { return cmdReach([]string{"web", "--none"}) }},
		{"create-browser-reachable-from", func() error {
			return cmdCreate([]string{"browser", "--id", "shop", "--reachable-from", "pool"})
		}},
		{"create-file-reachable-from", func() error {
			return cmdCreate([]string{"pod", "-f", file("p.json", `{"id":"api","image":"nginx"}`), "--reachable-from", "*"})
		}},
		{"create-file-reachable-from-none", func() error {
			return cmdCreate([]string{"pod", "-f", file("p.json", `{"id":"api2","image":"nginx"}`), "--reachable-from", ""})
		}},
		{"create-database-reachable-from-none", func() error {
			return cmdCreate([]string{"storage", "-f", file("s.json", `{"id":"db2","engine":"redis","reachableFrom":[]}`), "--reachable-from", ""})
		}},
		{"create-apps-reachable-from", func() error {
			return cmdCreate([]string{"apps", "-f", file("a.json",
				`{"apps":[{"id":"front","image":"nginx","databases":[{"id":"pg"}]},{"id":"back","image":"busybox"}],"databases":[{"id":"pg","engine":"postgres"}]}`),
				"--reachable-from", "edge"})
		}},
		{"create-apps-join-same-reachable-from", func() error {
			return cmdCreate([]string{"apps", "-f", file("a.json", `[{"id":"cron","image":"busybox"}]`), "--join", "shop", "--reachable-from", "edge"})
		}},
		{"create-apps-join-no-reachable-from", func() error {
			return cmdCreate([]string{"apps", "-f", file("a.json", `[{"id":"cron","image":"busybox"}]`), "--join", "web"})
		}},
		{"create-apps-no-reachable-from", func() error {
			return cmdCreate([]string{"apps", "-f", file("a.json", `[{"id":"front","image":"nginx"}]`)})
		}},
		{"browser-api-create-reachable-from", func() error {
			return cmdBrowserAPI([]string{"create", "pool2", "--browsers", "b1", "--reachable-from", "edge,box"})
		}},
		{"api-keys-set-network", func() error {
			return cmdAPIKeys([]string{"set", "k1", "--permissions", "billing,network"})
		}},
		{"api-keys-set-legacy-names", func() error {
			return cmdAPIKeys([]string{"set", "k1", "--permissions", "network,proxies,profiles"})
		}},
		{"api-keys-create-network", func() error {
			return cmdAPIKeys([]string{"create", "ci", "--permissions", "network"})
		}},
		{"connect-inside", func() error { return cmdConnect([]string{"db"}) }},
	}
	written := map[string]string{}
	for _, c := range cases {
		g := newGoldenAPI(t, map[string]string{
			"GET /v1/workspace":             insideWorkspace,
			"POST /v1/workloads/db/connect": insideConnect,
		})
		out, errOut, err := captured(t, c.run)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		got := "== stdout\n" + out + "== stderr\n" + errOut + "== writes\n" + strings.Join(g.writes, "\n") + "\n"
		if *updateGolden {
			written[c.name] = got
			continue
		}
		want, ok := goldenInsideWant[c.name]
		if !ok {
			t.Fatalf("%s: no golden (go test -run GoldenInside -update writes it)", c.name)
		}
		if got != want {
			t.Errorf("%s changed:\n--- got\n%s--- want\n%s", c.name, got, want)
		}
	}
	if *updateGolden {
		writeGoldenData(t, written, "golden_inside_data_test.go", "goldenInsideWant", "GoldenInside")
	}
}

func TestReachList(t *testing.T) {
	// "none" is a name like any other (a resource may be called that).
	ok := map[string]string{"a,b": "a,b", " a , b ": "a,b", "*": "*", "none": "none", "a,none": "a,none", "": "", ",": ""}
	for in, want := range ok {
		got, err := reachList(in)
		if err != nil || strings.Join(got, ",") != want || got == nil {
			t.Errorf("reachList(%q) = %v, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"*,a", "a,*", "a,a", "*,*"} {
		if _, err := reachList(in); err == nil {
			t.Errorf("reachList(%q) passed", in)
		}
	}
}

func TestReachRefusesBeforeSending(t *testing.T) {
	g := newGoldenAPI(t, map[string]string{"GET /v1/workspace": insideWorkspace})
	for _, args := range [][]string{
		{"edge", "--from", "web", "--none"},
		{"edge", "--from", ""},
		{"edge", "--from", "*,web"},
		{"nothere", "--none"},
		{"edge", "--from", "web,nothere"},
		{"edge", "--from", "none"},
		// "--from web, box": box is left after the flags, and web alone
		// would be sent.
		{"edge", "--from", "web,", "box"},
		{"edge", "box"},
	} {
		if err := cmdReach(args); err == nil {
			t.Errorf("reach %v passed", args)
		}
	}
	if err := cmdReach([]string{"edge", "--from", "none"}); err == nil || !strings.Contains(err.Error(), "--none lets nothing in") {
		t.Errorf("--from none: %v", err)
	}
	f := filepath.Join(t.TempDir(), "a.json")
	if err := os.WriteFile(f, []byte(`[{"id":"cron","image":"busybox"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		// shop is reachable from edge: joining it with another value would
		// change the whole app (here: close it).
		{"apps", "-f", f, "--join", "shop", "--reachable-from", ""},
		{"apps", "-f", f, "--join", "web", "--reachable-from", "*"},
		{"pod", "-f", f, "--reachable-from", "web,", "box"},
	} {
		if err := cmdCreate(args); err == nil {
			t.Errorf("create %v passed", args)
		}
	}
	if err := cmdBrowserAPI([]string{"create", "p3", "--browsers", "b1", "--reachable-from", "edge,", "box"}); err == nil {
		t.Error("browser-api create with a word left over passed")
	}
	if len(g.writes) != 0 {
		t.Errorf("sent %v", g.writes)
	}
}

func TestWithReachKeepsTheFileHonest(t *testing.T) {
	r := &reachFlag{}
	_ = r.Set("a,b")
	same := map[string]any{"reachableFrom": []any{"b", "a"}}
	if err := withReach(same, r); err != nil {
		t.Errorf("the same names in another order: %v", err)
	}
	other := map[string]any{"reachableFrom": []any{"c"}}
	if err := withReach(other, r); err == nil {
		t.Error("a file saying otherwise was overridden")
	}
	left := map[string]any{"id": "x"}
	if err := withReach(left, &reachFlag{}); err != nil || left["reachableFrom"] != nil {
		t.Errorf("an unset flag wrote %v (%v)", left, err)
	}
}

// A refusal for want of the Network permission says what to do next: ask
// the user, and where a person turns it on, for a key or for an agent.
func TestNetworkPermissionNext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			_, _ = w.Write([]byte(insideWorkspace))
			return
		}
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"error":"This API key can't let web reach db inside the workspace. A person can turn on Network for it on the Keys page.","code":"network_permission"}`))
	}))
	defer srv.Close()
	t.Setenv("LIVELLM_API_URL", srv.URL)
	t.Setenv("LIVELLM_API_KEY", "llc_test")
	err := networkNext(cmdReach([]string{"edge", "--from", "web"}))
	if err == nil || !strings.Contains(err.Error(), "ask them first") || !strings.Contains(err.Error(), "Keys page") ||
		!strings.Contains(err.Error(), "can't let web reach db") || !strings.Contains(err.Error(), "a service added to a Composable App") {
		t.Fatalf("key: %v", err)
	}
	if err := networkNext(cmdLink([]string{"edge", "db"})); err == nil || !strings.Contains(err.Error(), "ask them first") {
		t.Fatalf("link: %v", err)
	}
	if exitCode(err) != 2 {
		t.Errorf("exit %d", exitCode(err))
	}
	t.Setenv("LIVELLM_API_KEY", "")
	p := &problem{Status: 403, Msg: "This agent can't …", Code: "network_permission"}
	if e := networkNext(p); !strings.Contains(e.Error(), "Agents page") {
		t.Errorf("agent: %v", e)
	}
	other := &problem{Status: 403, Msg: "no", Code: "other"}
	if networkNext(other); other.Next != "" {
		t.Errorf("another refusal got %q", other.Next)
	}
	named := &problem{Status: 403, Msg: "no", Code: "network_permission", Next: "the API's own"}
	if networkNext(named); named.Next != "the API's own" {
		t.Errorf("the API's next step was replaced: %q", named.Next)
	}
}

// A Composable App's service with no hostname is found by its stack mates at
// its id, on every port.
func TestInsideAddressesStackDefaultHostname(t *testing.T) {
	w := map[string]any{"id": "api", "type": "pod", "pod": map[string]any{"stack": "shop",
		"ports": []any{map[string]any{"name": "http", "port": float64(80)}, map[string]any{"name": "raw", "port": float64(9000), "tcp": true}}}}
	got := insideAddresses("ws-api", w)
	if len(got) != 2 || got[0]["inStack"] != "api:80" || got[1]["inStack"] != "api:9000" || got[1]["host"] != "ws-api-raw" {
		t.Errorf("%v", got)
	}
	lone := map[string]any{"id": "api", "type": "pod", "pod": map[string]any{"hostname": "x",
		"ports": []any{map[string]any{"name": "http", "port": float64(80)}}}}
	if got := insideAddresses("ws-api", lone); got[0]["inStack"] != nil {
		t.Errorf("an app on its own has no stack mates: %v", got)
	}
}
