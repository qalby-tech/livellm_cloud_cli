package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writesOf are the requests other than GET the fake API saw.
func writesOf(f *fakeAPI) []seenReq {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []seenReq
	for _, s := range f.seen {
		if s.method != "GET" {
			out = append(out, s)
		}
	}
	return out
}

func bodyOf(t *testing.T, s seenReq) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(s.body, &m); err != nil {
		t.Fatalf("%s %s sent %q", s.method, s.path, s.body)
	}
	return m
}

// --engine camoufox is the only way an engine is sent; chrome (or none)
// sends the body as before.
func TestCreateEngineBodies(t *testing.T) {
	dir := t.TempDir()
	chromeFile := filepath.Join(dir, "chrome.json")
	_ = os.WriteFile(chromeFile, []byte(`{"id":"shop","engine":"chrome"}`), 0o600)
	plainFile := filepath.Join(dir, "plain.json")
	_ = os.WriteFile(plainFile, []byte(`{"id":"shop"}`), 0o600)
	apiFile := filepath.Join(dir, "api.json")
	_ = os.WriteFile(apiFile, []byte(`{"id":"pool","autodiscover":true}`), 0o600)

	cases := []struct {
		name string
		args []string
		path string
		want map[string]any
	}{
		{"camoufox browser", []string{"browser", "--id", "fox", "--engine", "camoufox"},
			"/v1/workloads/browser", map[string]any{"id": "fox", "engine": "camoufox"}},
		{"camoufox browser, any case, with a locale", []string{"browser", "--id", "fox", "--engine", "Camoufox", "--locale", "ru-RU"},
			"/v1/workloads/browser", map[string]any{"id": "fox", "engine": "camoufox", "locale": "ru-RU"}},
		{"chrome sends no engine", []string{"browser", "--id", "shop", "--engine", "chrome"},
			"/v1/workloads/browser", map[string]any{"id": "shop"}},
		{"camoufox browser from a file", []string{"browser", "-f", plainFile, "--engine", "camoufox"},
			"/v1/workloads/browser", map[string]any{"id": "shop", "engine": "camoufox"}},
		{"a file's own chrome is sent as written", []string{"browser", "-f", chromeFile, "--engine", "chrome"},
			"/v1/workloads/browser", map[string]any{"id": "shop", "engine": "chrome"}},
		{"camoufox Browser API from a file", []string{"browser-api", "-f", apiFile, "--engine", "camoufox"},
			"/v1/workloads/controller", map[string]any{"id": "pool", "autodiscover": true, "engine": "camoufox"}},
	}
	for _, c := range cases {
		f := newFakeAPI(t)
		if err := cmdCreate(c.args); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		w := writesOf(f)
		if len(w) != 1 || w[0].method != "POST" || w[0].path != c.path {
			t.Fatalf("%s: sent %v", c.name, w)
		}
		if got := bodyOf(t, w[0]); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: sent %v, want %v", c.name, got, c.want)
		}
	}

	refused := map[string][]string{
		"unknown engine":         {"browser", "--id", "x", "--engine", "firefox"},
		"engine on an app":       {"pod", "-f", plainFile, "--engine", "camoufox"},
		"file says another one":  {"browser", "-f", chromeFile, "--engine", "camoufox"},
		"engine on a machine":    {"vm-ubuntu", "-f", plainFile, "--engine", "chrome"},
		"browser-api bad engine": {"browser-api", "-f", apiFile, "--engine", "safari"},
	}
	for name, args := range refused {
		f := newFakeAPI(t)
		if err := cmdCreate(args); err == nil {
			t.Errorf("%s: should be refused", name)
		}
		if w := writesOf(f); len(w) != 0 {
			t.Errorf("%s: sent %v", name, w)
		}
	}
}

func TestBrowserAPICreateEngine(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want map[string]any
	}{
		{"every Camoufox browser", []string{"create", "foxes", "--all", "--engine", "camoufox"},
			map[string]any{"id": "foxes", "autodiscover": true, "engine": "camoufox"}},
		{"named Camoufox browsers", []string{"create", "foxes", "--engine", "camoufox", "--browsers", "f1,f2"},
			map[string]any{"id": "foxes", "autodiscover": false, "browsers": []any{"f1", "f2"}, "engine": "camoufox"}},
		{"chrome sends no engine", []string{"create", "pool", "--all", "--engine", "chrome"},
			map[string]any{"id": "pool", "autodiscover": true}},
	}
	for _, c := range cases {
		f := newFakeAPI(t)
		if err := cmdBrowserAPI(c.args); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		w := writesOf(f)
		if len(w) != 1 || w[0].path != "/v1/workloads/controller" {
			t.Fatalf("%s: sent %v", c.name, w)
		}
		if got := bodyOf(t, w[0]); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: sent %v, want %v", c.name, got, c.want)
		}
	}
	for name, args := range map[string][]string{
		"remote in a Camoufox one": {"create", "foxes", "--all", "--engine", "camoufox", "--remote", "office=wss://o"},
		"unknown engine":           {"create", "foxes", "--all", "--engine", "webkit"},
	} {
		f := newFakeAPI(t)
		if err := cmdBrowserAPI(args); err == nil {
			t.Errorf("%s: should be refused", name)
		}
		if w := writesOf(f); len(w) != 0 {
			t.Errorf("%s: sent %v", name, w)
		}
	}
}

// browser engines needs no sign-in, and passes the answer through.
func TestBrowserEngines(t *testing.T) {
	f := newFakeAPI(t)
	t.Setenv("LIVELLM_API_KEY", "")
	t.Setenv("LIVELLM_CREDENTIALS", filepath.Join(t.TempDir(), "none.json"))
	answer := `{"engines":[{"id":"chrome","name":"Chrome","protocol":"cdp","default":true},{"id":"camoufox","name":"Camoufox","protocol":"playwright","playwright":"1.62","preview":false}]}`
	f.answers["GET /v1/browsers/engines"] = answerJSON(answer)
	out, _, err := captured(t, func() error { return cmdBrowser([]string{"engines"}) })
	if err != nil {
		t.Fatal(err)
	}
	var got, want map[string]any
	_ = json.Unmarshal([]byte(answer), &want)
	if err := json.Unmarshal([]byte(out), &got); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("printed %q", out)
	}
	if r := f.last("GET", "/v1/browsers/engines"); r == nil || r.header.Get("x-api-key") != "" || r.header.Get("Authorization") != "" {
		t.Errorf("sent %v", r)
	}
	// A platform from before engines answers 404: Chrome only.
	f.answers["GET /v1/browsers/engines"] = func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		w.WriteHeader(404)
	}
	if err := cmdBrowser([]string{"engines"}); err == nil || !strings.Contains(err.Error(), "Chrome browsers only") {
		t.Errorf("404: %v", err)
	}
}

// A Camoufox browser's connect answer is printed as it came, and one line on
// stderr says how to drive it.
func TestConnectCamoufox(t *testing.T) {
	f := newFakeAPI(t)
	answer := `{"tool":"cdp","engine":"camoufox","token":"llt_x","expiresAt":"2026-10-05T12:00:00Z",
		"playwright":{"url":"wss://fox-cdp.example/playwright/default","headers":{"Authorization":"Bearer llt_x"},"browser":"firefox","version":"1.62"},
		"api":{"url":"https://fox-cdp.example","headers":{"Authorization":"Bearer llt_x"}},"how":"…"}`
	f.answers["POST /v1/workloads/fox/connect"] = answerJSON(answer)
	out, errOut, err := captured(t, func() error { return cmdConnect([]string{"fox"}) })
	if err != nil {
		t.Fatal(err)
	}
	var got, want map[string]any
	_ = json.Unmarshal([]byte(answer), &want)
	if err := json.Unmarshal([]byte(out), &got); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("printed %q", out)
	}
	if errOut != "Playwright 1.62: firefox.connect(playwright.url, headers=playwright.headers)\n" {
		t.Errorf("stderr %q", errOut)
	}
}

const camoufoxWorkspace = `{"name":"ws","spec":{"workloads":[
	{"id":"fox","type":"browser","browser":{"engine":"camoufox","locale":"ru-RU"}},
	{"id":"shop","type":"browser","browser":{}},
	{"id":"foxes","type":"controller","controller":{"engine":"camoufox","autodiscover":true}},
	{"id":"pool","type":"controller","controller":{"autodiscover":true}}]}}`

const camoufoxStatus = `{"workloads":[
	{"id":"fox","type":"browser","phase":"Running","ready":true},
	{"id":"shop","type":"browser","phase":"Running","ready":true},
	{"id":"foxes","type":"controller","phase":"Running","ready":true},
	{"id":"pool","type":"controller","phase":"Running","ready":true}]}`

func engineFake(t *testing.T) *fakeAPI {
	f := newFakeAPI(t)
	f.answers["GET /v1/workspace"] = answerJSON(camoufoxWorkspace)
	f.answers["GET /v1/status"] = answerJSON(camoufoxStatus)
	return f
}

// ls and status say engine camoufox for Camoufox browsers and Browser APIs,
// and nothing for Chrome ones.
func TestListAndStatusShowCamoufox(t *testing.T) {
	engineFake(t)
	engines := func(list []any) map[string]any {
		got := map[string]any{}
		for _, raw := range list {
			m := raw.(map[string]any)
			got[m["id"].(string)] = m["engine"]
		}
		return got
	}
	want := map[string]any{"fox": "camoufox", "shop": nil, "foxes": "camoufox", "pool": nil}

	out, _, err := captured(t, func() error { return cmdList(nil) })
	if err != nil {
		t.Fatal(err)
	}
	if got := engines(printed(t, out)["resources"].([]any)); !reflect.DeepEqual(got, want) {
		t.Errorf("ls: %v", got)
	}
	out, _, err = captured(t, func() error { return cmdStatus(nil) })
	if err != nil {
		t.Fatal(err)
	}
	if got := engines(printed(t, out)["workloads"].([]any)); !reflect.DeepEqual(got, want) {
		t.Errorf("status: %v", got)
	}
	for id, e := range want {
		out, _, err = captured(t, func() error { return cmdStatus([]string{id}) })
		if err != nil {
			t.Fatal(err)
		}
		if got := printed(t, out)["engine"]; got != e {
			t.Errorf("status %s: engine %v", id, got)
		}
	}
}

// A status of things that are neither browsers nor Browser APIs reads no
// settings.
func TestStatusReadsSettingsOnlyForBrowsers(t *testing.T) {
	f := newFakeAPI(t)
	f.answers["GET /v1/status"] = answerJSON(`{"workloads":[{"id":"web","type":"pod","phase":"Running"}]}`)
	if err := cmdStatus(nil); err != nil {
		t.Fatal(err)
	}
	if f.last("GET", "/v1/workspace") != nil {
		t.Error("status read the workspace for an app")
	}
}

func TestBrowserAPIShowCamoufox(t *testing.T) {
	engineFake(t)
	out, _, err := captured(t, func() error { return browserAPIShow([]string{"foxes"}) })
	if err != nil {
		t.Fatal(err)
	}
	got := printed(t, out)
	if got["engine"] != "camoufox" || got["drives"] != "every Camoufox browser in the workspace" {
		t.Errorf("show foxes: %v", got)
	}
	out, _, err = captured(t, func() error { return browserAPIShow([]string{"pool"}) })
	if err != nil {
		t.Fatal(err)
	}
	got = printed(t, out)
	if _, has := got["engine"]; has || got["drives"] != "every browser in the workspace" {
		t.Errorf("show pool: %v", got)
	}
}

// The engine refusals keep the API's words and gain the next step; any other
// refusal is left as it came.
func TestEngineRefusalsSayWhatNext(t *testing.T) {
	cases := []struct {
		status    int
		body      string
		wantNext  string
		wantInMsg string
	}{
		{422, `{"error":"A browser's engine can't change after creation — make a new browser (its cookies can be imported into it).","code":"engine_fixed"}`,
			"livellm browser cookies import NEW", "engine can't change"},
		{422, `{"error":"This platform doesn't offer Camoufox browsers.","code":"engine_unavailable"}`,
			"livellm browser engines", "doesn't offer Camoufox"},
		{422, `{"error":"Camoufox browsers take no extensions yet.","code":"extensions_unsupported"}`,
			"\"extensions\"", "take no extensions"},
		{422, `{"error":"Browser API foxes drives Camoufox browsers; shop runs Chrome","code":"engine_mismatch"}`,
			"its own engine only", "drives Camoufox"},
		{422, `{"error":"A profile copies only between browsers of one engine: shop runs Chrome, fox runs Camoufox.","code":"engine_mismatch"}`,
			"import its cookies instead", "profile copies"},
		{422, `{"error":"This profile is from a Chrome browser; this browser runs Camoufox. Profiles move only between browsers of one engine — import its cookies instead.","code":"profile_engine"}`,
			"livellm browser cookies import ID", "from a Chrome browser"},
		// No code: the wording tells.
		{422, `{"error":"This platform doesn't offer Camoufox browsers."}`, "livellm browser engines", "doesn't offer"},
		// The API named the next step: it stays.
		{422, `{"error":"Camoufox browsers take no extensions yet.","code":"extensions_unsupported","next":"theirs"}`, "theirs", "extensions"},
		// Not an engine refusal: untouched.
		{422, `{"error":"unknown locale xx-QQ","code":"bad_locale"}`, "", "unknown locale"},
		{409, `{"error":"workload shop already exists"}`, "", "already exists"},
	}
	for _, c := range cases {
		err := engineNext(problemFrom(c.status, []byte(c.body)))
		var p *problem
		if !asProblem(err, &p) {
			t.Fatalf("%s: not a problem: %v", c.body, err)
		}
		if c.wantNext == "" && p.Next != "" || !strings.Contains(p.Next, c.wantNext) {
			t.Errorf("%s: next %q, want %q", c.body, p.Next, c.wantNext)
		}
		if !strings.Contains(p.Msg, c.wantInMsg) {
			t.Errorf("%s: message %q", c.body, p.Msg)
		}
		if exitCode(err) != map[int]int{422: 1, 409: 4}[c.status] {
			t.Errorf("%s: exit %d", c.body, exitCode(err))
		}
	}
}
