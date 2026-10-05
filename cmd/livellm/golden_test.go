package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/format"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// The Chrome commands print and send exactly what they did before Camoufox
// came: create, connect, ls and status against a fake API, compared byte for
// byte with goldenWant in golden_data_test.go, written by the 0.5.0 code
// (go test -run Golden -update writes it again). It is a Go file so the
// package directory holds Go files only.

var updateGolden = flag.Bool("update", false, "write the golden files again")

// goldenAPI answers from fixed JSON and keeps every write's body as it came.
type goldenAPI struct {
	mu     sync.Mutex
	writes []string
}

func newGoldenAPI(t *testing.T, answers map[string]string) *goldenAPI {
	t.Helper()
	g := &goldenAPI{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if r.Method != "GET" {
			g.mu.Lock()
			g.writes = append(g.writes, r.Method+" "+r.URL.Path+" "+string(raw))
			g.mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/json")
		if a, ok := answers[r.Method+" "+r.URL.Path]; ok {
			_, _ = w.Write([]byte(a))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("LIVELLM_API_URL", srv.URL)
	t.Setenv("LIVELLM_API_KEY", "llc_test")
	return g
}

// captured runs fn and answers what it printed on stdout and on stderr.
func captured(t *testing.T, fn func() error) (string, string, error) {
	t.Helper()
	or, ow, _ := os.Pipe()
	er, ew, _ := os.Pipe()
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = ow, ew
	var out, errOut bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { _, _ = io.Copy(&out, or); wg.Done() }()
	go func() { _, _ = io.Copy(&errOut, er); wg.Done() }()
	err := fn()
	os.Stdout, os.Stderr = oldOut, oldErr
	_ = ow.Close()
	_ = ew.Close()
	wg.Wait()
	return out.String(), errOut.String(), err
}

const goldenWorkspace = `{"name":"ws","spec":{"workloads":[
	{"id":"shop","type":"browser","browser":{"locale":"ru-RU","timezone":"Europe/Moscow"}},
	{"id":"plain","type":"browser"},
	{"id":"scrapers","type":"controller","controller":{"autodiscover":false,"browsers":["shop"]}},
	{"id":"web","type":"pod","pod":{"image":"nginx"}}]}}`

const goldenStatus = `{"workloads":[
	{"id":"shop","type":"browser","phase":"Running","ready":true,"endpoints":[{"url":"https://shop-cdp.example"}]},
	{"id":"plain","type":"browser","phase":"Pending","ready":false},
	{"id":"scrapers","type":"controller","phase":"Running","ready":true,"browsers":[{"id":"shop","tabs":1}]},
	{"id":"web","type":"pod","phase":"Running","ready":true}]}`

const goldenConnect = `{"tool":"cdp","type":"browser","engine":"chrome","token":"llt_x","expiresAt":"2026-10-05T12:00:00Z",
	"cdp":{"url":"wss://shop-cdp.example/devtools/browser/default","headers":{"Authorization":"Bearer llt_x"}},
	"how":"connect_over_cdp(cdp.url, headers=cdp.headers)"}`

func TestGoldenChromeCommands(t *testing.T) {
	cases := []struct {
		name string
		run  func() error
	}{
		{"create-browser", func() error { return cmdCreate([]string{"browser", "--id", "shop"}) }},
		{"create-browser-locale", func() error {
			return cmdCreate([]string{"browser", "--id", "shop", "--locale", "ru-RU", "--timezone", "Europe/Moscow"})
		}},
		{"create-browser-file", func() error {
			dir := t.TempDir()
			f := filepath.Join(dir, "b.json")
			_ = os.WriteFile(f, []byte(`{"id":"shop","languages":["ru-RU","ru"]}`), 0o600)
			return cmdCreate([]string{"browser", "-f", f})
		}},
		{"browser-api-create-named", func() error {
			return cmdBrowserAPI([]string{"create", "scrapers", "--browsers", "shop,plain"})
		}},
		{"browser-api-create-all", func() error { return cmdBrowserAPI([]string{"create", "every", "--all"}) }},
		{"browser-api-create-remote", func() error {
			return cmdBrowserAPI([]string{"create", "mix", "--browsers", "shop", "--remote", "office=wss://o.example"})
		}},
		{"create-browser-api-file", func() error {
			dir := t.TempDir()
			f := filepath.Join(dir, "c.json")
			_ = os.WriteFile(f, []byte(`{"id":"scrapers","browsers":["shop"]}`), 0o600)
			return cmdCreate([]string{"browser-api", "-f", f})
		}},
		{"connect", func() error { return cmdConnect([]string{"shop"}) }},
		{"ls", func() error { return cmdList(nil) }},
		{"ls-browser", func() error { return cmdList([]string{"--type", "browser"}) }},
		{"status", func() error { return cmdStatus(nil) }},
		{"status-browser", func() error { return cmdStatus([]string{"shop"}) }},
		{"status-browser-api", func() error { return cmdStatus([]string{"scrapers"}) }},
		{"browser-api-show", func() error { return browserAPIShow([]string{"scrapers"}) }},
	}
	written := map[string]string{}
	for _, c := range cases {
		g := newGoldenAPI(t, map[string]string{
			"GET /v1/workspace":               goldenWorkspace,
			"GET /v1/status":                  goldenStatus,
			"POST /v1/workloads/shop/connect": goldenConnect,
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
		want, ok := goldenWant[c.name]
		if !ok {
			t.Fatalf("%s: no golden (go test -run Golden -update writes it)", c.name)
		}
		if got != want {
			t.Errorf("%s changed:\n--- got\n%s--- want\n%s", c.name, got, want)
		}
	}
	if *updateGolden {
		writeGoldenData(t, written, "golden_data_test.go", "goldenWant")
	}
}

// writeGoldenData writes a golden file: the map called name, in file.
func writeGoldenData(t *testing.T, data map[string]string, file, name string) {
	t.Helper()
	names := make([]string, 0, len(data))
	for n := range data {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("// Code generated by go test -run Golden -update; DO NOT EDIT.\n\npackage main\n\nvar " + name + " = map[string]string{\n")
	for _, n := range names {
		v := data[n]
		lit := strconv.Quote(v)
		if !strings.Contains(v, "`") {
			lit = "`" + v + "`"
		}
		fmt.Fprintf(&b, "\t%q: %s,\n", n, lit)
	}
	b.WriteString("}\n")
	src, err := format.Source([]byte(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, src, 0o644); err != nil {
		t.Fatal(err)
	}
}
