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

// stop and start write the resource back whole, with only "stopped" changed:
// the settings the command knows nothing about go back as they came.
func TestStopAndStart(t *testing.T) {
	workloads := []map[string]any{
		{"id": "web", "type": "pod", "pod": map[string]any{
			"image":   "nginx",
			"ports":   []any{map[string]any{"name": "game", "port": float64(25565), "tcp": true}},
			"volumes": []any{map[string]any{"name": "data", "size": "10Gi", "mountPath": "/data"}},
		}, "createdBy": map[string]any{"name": "a person"}},
		{"id": "box", "type": "vm-ubuntu", "stopped": true, "vm": map[string]any{"cpus": float64(2)}},
	}
	var puts []struct {
		path string
		body map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/workspace":
			_ = json.NewEncoder(w).Encode(map[string]any{"name": "ws", "spec": map[string]any{"workloads": workloads}})
		case r.Method == "PUT":
			var b map[string]any
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &b)
			puts = append(puts, struct {
				path string
				body map[string]any
			}{r.URL.Path, b})
			_, _ = w.Write([]byte(`{"status":"accepted"}`))
		default:
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":"no such route"}`))
		}
	}))
	defer srv.Close()
	t.Setenv("LIVELLM_API_URL", srv.URL)
	t.Setenv("LIVELLM_API_KEY", "llc_test")
	stdout := os.Stdout
	devnull, _ := os.Open(os.DevNull)
	os.Stdout = devnull
	defer func() { os.Stdout = stdout }()

	if err := cmdStop([]string{"web"}); err != nil {
		t.Fatal(err)
	}
	if len(puts) != 1 || puts[0].path != "/v1/workloads/web" {
		t.Fatalf("stop sent %v", puts)
	}
	want := map[string]any{}
	b, _ := json.Marshal(workloads[0])
	_ = json.Unmarshal(b, &want)
	want["stopped"] = true
	if !reflect.DeepEqual(puts[0].body, want) {
		t.Errorf("stop wrote\n%v\nwant\n%v", puts[0].body, want)
	}

	if err := cmdStart([]string{"box"}); err != nil {
		t.Fatal(err)
	}
	if len(puts) != 2 || puts[1].path != "/v1/workloads/box" || puts[1].body["stopped"] != false || puts[1].body["vm"] == nil {
		t.Errorf("start sent %v", puts[1:])
	}

	// already in that state: nothing is written
	if err := cmdStop([]string{"box"}); err != nil {
		t.Fatal(err)
	}
	if len(puts) != 2 {
		t.Errorf("stopping a stopped machine wrote %v", puts[2:])
	}
	if err := cmdStop([]string{"nope"}); err == nil {
		t.Error("stopping something that isn't there should be refused")
	}
	if err := cmdStart(nil); err == nil {
		t.Error("start without an id should be refused")
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
