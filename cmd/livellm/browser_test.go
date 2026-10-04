package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeAPI answers by method and path, and keeps every request it saw.
type fakeAPI struct {
	mu      sync.Mutex
	seen    []seenReq
	answers map[string]func(w http.ResponseWriter, r *http.Request, body []byte)
}

type seenReq struct {
	method, path, query string
	header              http.Header
	body                []byte
}

func (f *fakeAPI) last(method, path string) *seenReq {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.seen) - 1; i >= 0; i-- {
		if f.seen[i].method == method && f.seen[i].path == path {
			return &f.seen[i]
		}
	}
	return nil
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	f := &fakeAPI{answers: map[string]func(http.ResponseWriter, *http.Request, []byte){}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		h := f.answers[key]
		var body []byte
		if h == nil || !strings.HasSuffix(r.URL.Path, "/import") {
			body, _ = io.ReadAll(r.Body)
		}
		f.mu.Lock()
		f.seen = append(f.seen, seenReq{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Clone(), body})
		f.mu.Unlock()
		if h != nil {
			h(w, r, body)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("LIVELLM_API_URL", srv.URL)
	t.Setenv("LIVELLM_API_KEY", "llc_test")
	quiet(t)
	return f
}

func answerJSON(v string) func(http.ResponseWriter, *http.Request, []byte) {
	return func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(v))
	}
}

func TestBrowserLocaleRequests(t *testing.T) {
	f := newFakeAPI(t)
	cases := []struct {
		args []string
		want map[string]any
	}{
		{[]string{"b1", "--locale", "ru-RU", "--timezone", "Europe/Moscow"},
			map[string]any{"browser": map[string]any{"locale": "ru-RU", "timezone": "Europe/Moscow"}}},
		{[]string{"b1", "--locale", "", "--timezone="},
			map[string]any{"browser": map[string]any{"locale": "", "timezone": ""}}},
		{[]string{"b1", "--languages", "ru-RU, ru,en"},
			map[string]any{"browser": map[string]any{"languages": []any{"ru-RU", "ru", "en"}}}},
		{[]string{"b1", "--languages", ""},
			map[string]any{"browser": map[string]any{"languages": []any{}}}},
		{[]string{"b1", "--geolocation", "off"},
			map[string]any{"browser": map[string]any{"geolocation": map[string]any{
				"mode": "off", "latitude": nil, "longitude": nil, "accuracy": nil}}}},
		{[]string{"b1", "--geolocation", "default"},
			map[string]any{"browser": map[string]any{"geolocation": map[string]any{
				"mode": "prompt", "latitude": nil, "longitude": nil, "accuracy": nil}}}},
		{[]string{"b1", "--geolocation", "55.75,37.62,50"},
			map[string]any{"browser": map[string]any{"geolocation": map[string]any{
				"mode": "fixed", "latitude": 55.75, "longitude": 37.62, "accuracy": float64(50)}}}},
	}
	for _, c := range cases {
		f.seen = nil
		if err := cmdBrowser(append([]string{"locale"}, c.args...)); err != nil {
			t.Fatalf("%v: %v", c.args, err)
		}
		r := f.last("PATCH", "/v1/workloads/b1")
		if r == nil {
			t.Fatalf("%v: no PATCH", c.args)
		}
		var got map[string]any
		_ = json.Unmarshal(r.body, &got)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%v: sent %v, want %v", c.args, got, c.want)
		}
	}
	for _, bad := range []string{"91,0", "1,2,3,4", "north,south", "1,2,0"} {
		if err := cmdBrowser([]string{"locale", "b1", "--geolocation", bad}); err == nil {
			t.Errorf("--geolocation %s was taken", bad)
		}
	}
	// No flags: it shows what is set, and changes nothing.
	f.answers["GET /v1/workspace"] = answerJSON(`{"spec":{"workloads":[{"id":"b1","type":"browser","browser":{"locale":"ru-RU","storage":"5Gi"}}]}}`)
	f.seen = nil
	if err := cmdBrowser([]string{"locale", "b1"}); err != nil {
		t.Fatal(err)
	}
	if f.last("PATCH", "/v1/workloads/b1") != nil {
		t.Error("showing the locale wrote")
	}
}

// What is there now: two proxies, b with a login and a change-IP link.
const currentProxy = `{"proxy":{"upstreams":[
  {"name":"a","server":"http://proxy-a:3128","hasAuth":false,"hasChangeIp":false},
  {"name":"b","server":"socks5://proxy-b:1080","hasAuth":true,"hasChangeIp":true,"changeIpMethod":"POST","minChangeIpSeconds":120}],
  "rotation":{"mode":"off","order":"sequential"},"checkUrl":"http://echo/ip"},
  "status":{"mode":"proxy","exitIp":"203.0.113.9","upstream":{"name":"a"}}}`

func putBody(t *testing.T, f *fakeAPI) map[string]any {
	t.Helper()
	r := f.last("PUT", "/v1/workloads/b1/proxy")
	if r == nil {
		t.Fatal("no PUT")
	}
	var m map[string]any
	if err := json.Unmarshal(r.body, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// A change to the rotation alone carries the proxies as they are and sends
// no login: the stored ones stay.
func TestProxySetKeepsWhatIsThere(t *testing.T) {
	f := newFakeAPI(t)
	f.answers["GET /v1/workloads/b1/proxy"] = answerJSON(currentProxy)
	if err := cmdBrowser([]string{"proxy", "set", "b1", "--rotation", "session"}); err != nil {
		t.Fatal(err)
	}
	got := putBody(t, f)
	want := map[string]any{
		"upstreams": []any{
			map[string]any{"name": "a", "server": "http://proxy-a:3128"},
			map[string]any{"name": "b", "server": "socks5://proxy-b:1080", "changeIpMethod": "POST", "minChangeIpSeconds": float64(120)},
		},
		"rotation": map[string]any{"mode": "session", "order": "sequential"},
		"checkUrl": "http://echo/ip",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sent %v\nwant %v", got, want)
	}
	// --every means interval; --check-url '' goes back to the platform's.
	if err := cmdBrowser([]string{"proxy", "set", "b1", "--every", "5", "--check-url", ""}); err != nil {
		t.Fatal(err)
	}
	got = putBody(t, f)
	if r := got["rotation"].(map[string]any); r["mode"] != "interval" || r["everyMinutes"] != float64(5) {
		t.Errorf("rotation %v", r)
	}
	if _, ok := got["checkUrl"]; ok {
		t.Errorf("checkUrl still sent: %v", got["checkUrl"])
	}
	// A login for one that is there, the rest kept.
	t.Setenv("B_PW", "s3cret-b")
	if err := cmdBrowser([]string{"proxy", "set", "b1", "--login", "a=alice", "--password-env", "B_PW", "--no-change-ip", "b"}); err != nil {
		t.Fatal(err)
	}
	ups := putBody(t, f)["upstreams"].([]any)
	if a := ups[0].(map[string]any); a["username"] != "alice" || a["password"] != "s3cret-b" {
		t.Errorf("a: %v", a)
	}
	if b := ups[1].(map[string]any); b["hasChangeIp"] != false || b["username"] != nil {
		t.Errorf("b: %v", b)
	}
}

func TestProxySetFromFlagsAndFile(t *testing.T) {
	f := newFakeAPI(t)
	f.answers["GET /v1/workloads/b1/proxy"] = func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"error":"no proxies set"}`))
	}
	t.Setenv("PW_M", "pw-mobile")
	t.Setenv("CHANGE_M", "http://changeip/rot?key=k1")
	err := cmdBrowser([]string{"proxy", "set", "b1",
		"--upstream", "m=socks5://mobile:1080", "--upstream", "d=http://dc:3128",
		"--login", "m=bob", "--password-env", "m=PW_M",
		"--change-ip-env", "m=CHANGE_M", "--change-ip-method", "m=post", "--min-change-ip", "m=90",
		"--rotation", "interval", "--every", "10", "--order", "random"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"upstreams": []any{
			map[string]any{"name": "m", "server": "socks5://mobile:1080", "username": "bob", "password": "pw-mobile",
				"changeIpUrl": "http://changeip/rot?key=k1", "changeIpMethod": "POST", "minChangeIpSeconds": float64(90)},
			map[string]any{"name": "d", "server": "http://dc:3128"},
		},
		"rotation": map[string]any{"mode": "interval", "everyMinutes": float64(10), "order": "random"},
	}
	if got := putBody(t, f); !reflect.DeepEqual(got, want) {
		t.Errorf("sent %v\nwant %v", got, want)
	}

	// A file whose secrets name variables, and the one password on stdin.
	dir := t.TempDir()
	file := filepath.Join(dir, "proxy.json")
	_ = os.WriteFile(file, []byte(`{"proxy":{"upstreams":[
	  {"name":"x","server":"https://x:443","username":"xu","changeIpUrl":"env:CHANGE_M"},
	  {"name":"y","server":"socks5://y:1080","hasAuth":true}],
	  "rotation":{"mode":"off"}}}`), 0o600)
	stdinReader = strings.NewReader("from-stdin\n")
	t.Cleanup(func() { stdinReader = os.Stdin })
	if err := cmdBrowser([]string{"proxy", "set", "b1", "-f", file, "--password-stdin"}); err != nil {
		t.Fatal(err)
	}
	ups := putBody(t, f)["upstreams"].([]any)
	x := ups[0].(map[string]any)
	if x["password"] != "from-stdin" || x["username"] != "xu" || x["changeIpUrl"] != "http://changeip/rot?key=k1" {
		t.Errorf("x: %v", x)
	}
	if y := ups[1].(map[string]any); y["hasAuth"] != true || y["password"] != nil {
		t.Errorf("y: %v", y)
	}

	refused := [][]string{
		{"--upstream", "m=socks5://bob:pw@mobile:1080"},              // a login in the address
		{"--upstream", "m=socks5://mobile:1080", "--login", "m=bob"}, // no password for it
		{"--login", "zz=bob", "--password-env", "PW_M"},              // no such proxy
		{"--upstream", "m=socks5://mobile:1080", "--password-env", "m=UNSET_VAR_X", "--login", "m=bob"},
		{"--upstream", "m=socks5://mobile:1080", "--change-ip-env", "m=UNSET_VAR_X"},
		{"--rotation", "hourly"},
		{"--every", "0"},
		{"--upstream", "m=socks5://mobile:1080", "--min-change-ip", "m=5"},
		{"--upstream", "m=socks5://mobile:1080", "--login", "m=bob", "--password-env", "m=PW_M", "--no-login", "m"},
	}
	for _, args := range refused {
		f.seen = nil
		if err := cmdBrowser(append([]string{"proxy", "set", "b1"}, args...)); err == nil {
			t.Errorf("%v was taken", args)
		}
		if f.last("PUT", "/v1/workloads/b1/proxy") != nil {
			t.Errorf("%v: refused, but it wrote", args)
		}
	}
}

func TestProxyVerbs(t *testing.T) {
	f := newFakeAPI(t)
	if err := cmdBrowser([]string{"proxy", "rotate", "b1", "--to", "b"}); err != nil {
		t.Fatal(err)
	}
	if r := f.last("POST", "/v1/workloads/b1/proxy/rotate"); r == nil || string(r.body) != `{"to":"b"}` {
		t.Errorf("rotate sent %v", r)
	}
	if err := cmdBrowser([]string{"proxy", "clear", "b1"}); err != nil {
		t.Fatal(err)
	}
	if r := f.last("DELETE", "/v1/workloads/b1/proxy"); r == nil || r.query != "" {
		t.Errorf("clear sent %v", r)
	}
	if err := cmdBrowser([]string{"proxy", "remove", "b1"}); err != nil {
		t.Fatal(err)
	}
	if r := f.last("DELETE", "/v1/workloads/b1/proxy"); r == nil || r.query != "remove=true" {
		t.Errorf("remove sent %v", r)
	}
	f.answers["POST /v1/workloads/b1/proxy/rotate"] = func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		w.WriteHeader(409)
		_, _ = w.Write([]byte(`{"error":"Restart this browser once to turn on proxies.","code":"needs_restart"}`))
	}
	err := cmdBrowser([]string{"proxy", "rotate", "b1"})
	var p *problem
	if !asProblem(err, &p) || p.Status != 409 || !strings.Contains(p.Next, "livellm restart b1") {
		t.Errorf("needs_restart: %v", err)
	}
	if exitCode(err) != 4 {
		t.Errorf("exit code %d", exitCode(err))
	}
}

// pattern is a profile-sized stream that is the same every time.
func pattern(n int64) io.Reader {
	return io.LimitReader(&counter{}, n)
}

// counter's bytes depend only on their position, however they are read.
type counter struct{ pos uint64 }

func (c *counter) Read(p []byte) (int, error) {
	for k := range p {
		p[k] = byte(c.pos*7 + c.pos/251)
		c.pos++
	}
	return len(p), nil
}

func sum(r io.Reader) [32]byte {
	h := sha256.New()
	_, _ = io.Copy(h, r)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// An export is saved as it arrives, byte for byte, private to this user,
// and with a password the password goes in the body (never a query).
func TestProfileExportStreams(t *testing.T) {
	f := newFakeAPI(t)
	const size = 48 << 20
	f.answers["POST /v1/workloads/b1/profile/export"] = func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="../b1-2026-10-04.llcprofile.age"`)
		w.Header().Set("Content-Length", strconv.Itoa(size))
		_, _ = io.Copy(w, pattern(size))
	}
	dir := t.TempDir()
	wd, _ := os.Getwd()
	_ = os.Chdir(dir)
	t.Cleanup(func() { _ = os.Chdir(wd) })
	t.Setenv("EXPORT_PW", "export-secret")
	if err := cmdBrowser([]string{"profile", "export", "b1", "--snapshot", "s1", "--password-env", "EXPORT_PW"}); err != nil {
		t.Fatal(err)
	}
	r := f.last("POST", "/v1/workloads/b1/profile/export")
	var body map[string]any
	_ = json.Unmarshal(r.body, &body)
	if body["snapshot"] != "s1" || body["password"] != "export-secret" || r.query != "" {
		t.Errorf("export sent %v ?%s", body, r.query)
	}
	saved := filepath.Join(dir, "b1-2026-10-04.llcprofile.age")
	st, err := os.Stat(saved)
	if err != nil {
		t.Fatalf("not saved under the platform's name, kept in this folder: %v", err)
	}
	if st.Mode().Perm() != 0o600 || st.Size() != size {
		t.Errorf("saved %v, %d bytes", st.Mode(), st.Size())
	}
	fh, _ := os.Open(saved)
	defer fh.Close()
	if sum(fh) != sum(pattern(size)) {
		t.Error("the saved file differs from what was sent")
	}

	// An export cut short leaves nothing under the name asked for.
	f.answers["POST /v1/workloads/b1/profile/export"] = func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		w.Header().Set("Content-Length", strconv.Itoa(size))
		_, _ = io.Copy(w, pattern(1<<20))
	}
	out := filepath.Join(dir, "cut.llcprofile")
	if err := cmdBrowser([]string{"profile", "export", "b1", "-o", out}); err == nil {
		t.Error("a cut export was taken")
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("a cut export was saved")
	}
	left, _ := filepath.Glob(filepath.Join(dir, ".livellm-export-*"))
	if len(left) != 0 {
		t.Errorf("left behind %v", left)
	}
}

// An import goes up as it is read, byte for byte, with its length, the
// password in a header and --force as ?force=1.
func TestProfileImportStreams(t *testing.T) {
	f := newFakeAPI(t)
	const size = 40 << 20
	var got [32]byte
	var gotLen int64
	f.answers["POST /v1/workloads/b1/profile/import"] = func(w http.ResponseWriter, r *http.Request, _ []byte) {
		gotLen = r.ContentLength
		got = sum(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"imported":true,"chromeVersion":"154.0.8037.57"}`))
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "p.llcprofile.age")
	fh, _ := os.Create(file)
	_, _ = io.Copy(fh, pattern(size))
	fh.Close()
	t.Setenv("IMPORT_PW", "import-secret")
	if err := cmdBrowser([]string{"profile", "import", "b1", file, "--force", "--password-env", "IMPORT_PW", "-y"}); err != nil {
		t.Fatal(err)
	}
	r := f.last("POST", "/v1/workloads/b1/profile/import")
	if r.query != "force=1" || r.header.Get("X-Profile-Password") != "import-secret" ||
		r.header.Get("Content-Type") != "application/octet-stream" {
		t.Errorf("import sent ?%s %v", r.query, r.header)
	}
	if gotLen != size || got != sum(pattern(size)) {
		t.Errorf("the platform got %d bytes that differ from the file", gotLen)
	}
	// Without -y it asks, and with the password on stdin it can't.
	stdinReader = strings.NewReader("pw\n")
	t.Cleanup(func() { stdinReader = os.Stdin })
	if err := cmdBrowser([]string{"profile", "import", "b1", file, "--password-stdin"}); err == nil {
		t.Error("an import with the password on stdin went ahead without -y")
	}
	// A newer profile: the platform's words, and what to do.
	f.answers["POST /v1/workloads/b1/profile/import"] = func(w http.ResponseWriter, r *http.Request, _ []byte) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(409)
		_, _ = w.Write([]byte(`{"error":"This profile is from Chrome 155; this browser runs 154. Import anyway?","code":"profile_newer"}`))
	}
	err := cmdBrowser([]string{"profile", "import", "b1", file, "-y"})
	var p *problem
	if !asProblem(err, &p) || !strings.Contains(p.Msg, "Chrome 155") || !strings.Contains(p.Next, "--force") {
		t.Errorf("newer profile: %v", err)
	}
}

func TestProfileVerbs(t *testing.T) {
	f := newFakeAPI(t)
	run := func(args ...string) {
		t.Helper()
		if err := cmdBrowser(append([]string{"profile"}, args...)); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	run("list", "b1")
	if f.last("GET", "/v1/workloads/b1/profile") == nil {
		t.Error("list")
	}
	run("snapshot", "b1", "--name", "signed in")
	if r := f.last("POST", "/v1/workloads/b1/profile/snapshots"); r == nil || string(r.body) != `{"name":"signed in"}` {
		t.Errorf("snapshot sent %v", r)
	}
	run("restore", "b1", "s1", "--keep-current")
	if r := f.last("POST", "/v1/workloads/b1/profile/snapshots/s1/restore"); r == nil || string(r.body) != `{"keepCurrent":true}` {
		t.Errorf("restore sent %v", r)
	}
	run("delete", "b1", "s1", "-y")
	if f.last("DELETE", "/v1/workloads/b1/profile/snapshots/s1") == nil {
		t.Error("delete")
	}
	run("copy", "b2", "--from", "b1", "--snapshot", "s2", "-y")
	if r := f.last("POST", "/v1/workloads/b2/profile/copy"); r == nil || string(r.body) != `{"from":"b1","snapshot":"s2"}` {
		t.Errorf("copy sent %v", r)
	}
	// Restoring over the profile without --keep-current asks, and no answer
	// is no.
	stdin := os.Stdin
	devnull, _ := os.Open(os.DevNull)
	os.Stdin = devnull
	defer func() { os.Stdin = stdin; devnull.Close() }()
	f.seen = nil
	if err := cmdBrowser([]string{"profile", "restore", "b1", "s1"}); err == nil {
		t.Error("restore went ahead unasked")
	}
	if len(f.seen) != 0 {
		t.Errorf("an unconfirmed restore called %v", f.seen)
	}
}

func TestCookiesImport(t *testing.T) {
	f := newFakeAPI(t)
	dir := t.TempDir()
	list := filepath.Join(dir, "cookies.json")
	_ = os.WriteFile(list, []byte(`[{"name":"sid","value":"v1","domain":".example.com","path":"/"}]`), 0o600)
	state := filepath.Join(dir, "state.json")
	_ = os.WriteFile(state, []byte(`{"cookies":[{"name":"a","value":"1","domain":"x.test","path":"/"},{"name":"b","value":"2","domain":"x.test","path":"/"}],"origins":[]}`), 0o600)
	for file, n := range map[string]int{list: 1, state: 2} {
		if err := cmdBrowser([]string{"cookies", "import", "b1", file}); err != nil {
			t.Fatal(err)
		}
		r := f.last("POST", "/v1/workloads/b1/cookies")
		var sent []any
		if r == nil || json.Unmarshal(r.body, &sent) != nil || len(sent) != n {
			t.Errorf("%s: sent %v", file, r)
		}
	}
	empty := filepath.Join(dir, "empty.json")
	_ = os.WriteFile(empty, []byte(`{"origins":[]}`), 0o600)
	if err := cmdBrowser([]string{"cookies", "import", "b1", empty}); err == nil {
		t.Error("a file with no cookies was taken")
	}
}

// create browser --profile makes it, waits until it is up and imports the
// profile; a missing file is refused before anything is made.
func TestCreateBrowserWithProfile(t *testing.T) {
	f := newFakeAPI(t)
	waitPoll = time.Millisecond
	t.Cleanup(func() { waitPoll = 5 * time.Second })
	polls := 0
	f.answers["GET /v1/status"] = func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		polls++
		_, _ = w.Write([]byte(`{"workloads":[{"id":"b9","phase":"Running","ready":` + strconv.FormatBool(polls > 2) + `}]}`))
	}
	var imported []byte
	f.answers["POST /v1/workloads/b9/profile/import"] = func(w http.ResponseWriter, r *http.Request, _ []byte) {
		imported, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{"imported":true}`))
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "p.llcprofile")
	_ = os.WriteFile(file, []byte("profile-bytes"), 0o600)

	if err := cmdCreate([]string{"browser", "--id", "nope", "--profile", filepath.Join(dir, "missing")}); err == nil {
		t.Error("a missing profile file was taken")
	}
	if f.last("POST", "/v1/workloads/browser") != nil {
		t.Fatal("it made the browser before checking the file")
	}
	if err := cmdCreate([]string{"browser", "--id", "b9", "--locale", "ru-RU", "--timezone", "Europe/Moscow", "--profile", file}); err != nil {
		t.Fatal(err)
	}
	r := f.last("POST", "/v1/workloads/browser")
	var body map[string]any
	_ = json.Unmarshal(r.body, &body)
	if !reflect.DeepEqual(body, map[string]any{"id": "b9", "locale": "ru-RU", "timezone": "Europe/Moscow"}) {
		t.Errorf("create sent %v", body)
	}
	if !bytes.Equal(imported, []byte("profile-bytes")) || polls < 3 {
		t.Errorf("imported %q after %d polls", imported, polls)
	}
	// The flags belong to browsers.
	if err := cmdCreate([]string{"pod", "--locale", "ru-RU", "-f", file}); err == nil {
		t.Error("--locale was taken for an app")
	}
	// -f and --id together: the flag names it.
	settings := filepath.Join(dir, "b.json")
	_ = os.WriteFile(settings, []byte(`{"id":"from-file","storage":"10Gi"}`), 0o600)
	if err := cmdCreate([]string{"browser", "-f", settings, "--id", "b10"}); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(f.last("POST", "/v1/workloads/browser").body, &body)
	if body["id"] != "b10" || body["storage"] != "10Gi" {
		t.Errorf("create -f --id sent %v", body)
	}
}
