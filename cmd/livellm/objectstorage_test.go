package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// create storage --servers sends an object storage's servers as instances,
// and only there.
func TestCreateObjectStorageServers(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		_ = os.WriteFile(p, []byte(body), 0o600)
		return p
	}
	s3 := write("s3.json", `{"id":"files","engine":"s3","storageSize":"20Gi"}`)
	s3three := write("s3-3.json", `{"id":"files","engine":"s3","instances":3}`)
	pg := write("pg.json", `{"id":"db","engine":"postgres"}`)

	for _, c := range []struct {
		name string
		args []string
		want map[string]any
	}{
		{"3 servers", []string{"storage", "-f", s3, "--servers", "3"},
			map[string]any{"id": "files", "engine": "s3", "storageSize": "20Gi", "instances": float64(3)}},
		{"1 server", []string{"storage", "-f", s3, "--servers", "1"},
			map[string]any{"id": "files", "engine": "s3", "storageSize": "20Gi", "instances": float64(1)}},
		{"left out: the file as written", []string{"storage", "-f", s3},
			map[string]any{"id": "files", "engine": "s3", "storageSize": "20Gi"}},
		{"the file says 3 too", []string{"storage", "-f", s3three, "--servers", "3"},
			map[string]any{"id": "files", "engine": "s3", "instances": float64(3)}},
	} {
		f := newFakeAPI(t)
		if err := cmdCreate(c.args); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		w := writesOf(f)
		if len(w) != 1 || w[0].method != "POST" || w[0].path != "/v1/workloads/storage" {
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
		"2 servers":          {[]string{"storage", "-f", s3, "--servers", "2"}, "--servers is 1 or 3"},
		"not a number":       {[]string{"storage", "-f", s3, "--servers", "three"}, "--servers is 1 or 3"},
		"on Postgres":        {[]string{"storage", "-f", pg, "--servers", "3"}, `for an object storage ("engine": "s3")`},
		"on an app":          {[]string{"pod", "-f", s3, "--servers", "3"}, "--servers goes with create storage"},
		"the file says 3":    {[]string{"storage", "-f", s3three, "--servers", "1"}, "the settings say instances 3 and --servers says 1"},
		"on a browser by id": {[]string{"browser", "--id", "b", "--servers", "3"}, "--servers goes with create storage"},
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

// ls shows how many of an object storage's 3 servers are ready; nothing for
// anything else.
func TestListShowsServers(t *testing.T) {
	f := newFakeAPI(t)
	f.answers["GET /v1/workspace"] = answerJSON(`{"spec":{"workloads":[
		{"id":"files","type":"storage","storage":{"engine":"s3","instances":3}},
		{"id":"one","type":"storage","storage":{"engine":"s3"}},
		{"id":"db","type":"storage","storage":{"engine":"postgres"}}]}}`)
	f.answers["GET /v1/status"] = answerJSON(`{"workloads":[
		{"id":"files","type":"storage","phase":"Running","ready":true,"servers":{"desired":3,"ready":2}},
		{"id":"one","type":"storage","phase":"Running","ready":true},
		{"id":"db","type":"storage","phase":"Running","ready":true}]}`)
	out, _, err := captured(t, func() error { return cmdList(nil) })
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]any{}
	for _, raw := range printed(t, out)["resources"].([]any) {
		m := raw.(map[string]any)
		got[m["id"].(string)] = m["servers"]
	}
	want := map[string]any{"files": map[string]any{"desired": float64(3), "ready": float64(2)}, "one": nil, "db": nil}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("servers: %v, want %v", got, want)
	}
}
