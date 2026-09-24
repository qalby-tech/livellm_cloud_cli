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
