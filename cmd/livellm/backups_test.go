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

// Backups, back up now and restore send what the API expects, and restore
// refuses what can't work before anything is sent.
func TestBackupRequests(t *testing.T) {
	type got struct {
		method, path string
		body         map[string]any
	}
	var last got
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/workspace" {
			_, _ = w.Write([]byte(`{"spec":{"workloads":[
				{"id":"db","type":"storage"},{"id":"box","type":"vm-ubuntu"},{"id":"web","type":"pod"}]}}`))
			return
		}
		raw, _ := io.ReadAll(r.Body)
		last = got{method: r.Method, path: r.URL.Path}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &last.body)
		}
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
		{"backups", func() error { return cmdBackups([]string{"db"}) },
			got{"GET", "/v1/workloads/db/backups", nil}},
		{"backup a database", func() error { return cmdBackup([]string{"db"}) },
			got{"POST", "/v1/workloads/db/backups", map[string]any{}}},
		{"backup a machine, clean and named", func() error {
			return cmdBackup([]string{"box", "--clean", "--name", "before-upgrade"})
		}, got{"POST", "/v1/workloads/box/backups", map[string]any{"mode": "clean", "name": "before-upgrade"}}},
		{"restore a database into a new one", func() error {
			return cmdRestore([]string{"db", "db-20260925", "--as", "db-copy"})
		}, got{"POST", "/v1/workloads/db/backups/db-20260925/restore", map[string]any{"id": "db-copy"}}},
		{"restore a database to a minute", func() error {
			return cmdRestore([]string{"db", "db-20260925", "--as", "db-copy", "--at", "2026-09-25T14:05:00Z"})
		}, got{"POST", "/v1/workloads/db/backups/db-20260925/restore", map[string]any{
			"id": "db-copy", "at": "2026-09-25T14:05:00Z"}}},
		{"restore a machine in place", func() error {
			return cmdRestore([]string{"box", "snap-1", "-y"})
		}, got{"POST", "/v1/workloads/box/backups/snap-1/restore", map[string]any{}}},
		{"restart a database", func() error { return cmdRestart([]string{"db"}) },
			got{"POST", "/v1/workloads/db/restart", map[string]any{}}},
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

	refused := map[string][]string{
		"a database without --as":  {"db", "b1"},
		"a machine with --as":      {"box", "snap-1", "--as", "box2", "-y"},
		"an app":                   {"web", "b1", "--as", "x"},
		"a time that isn't a time": {"db", "b1", "--as", "x", "--at", "yesterday"},
		"no backup named":          {"db"},
		"nothing that exists":      {"nope", "b1", "--as", "x"},
	}
	for name, args := range refused {
		last = got{}
		if err := cmdRestore(args); err == nil {
			t.Errorf("restore %s should be refused", name)
		}
		if last.method != "" {
			t.Errorf("restore %s sent %s %s before refusing", name, last.method, last.path)
		}
	}
}
