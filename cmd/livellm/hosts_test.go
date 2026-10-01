package main

import (
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
)

// hosts prints the API's answer as it is, every field kept.
func TestHostsPassesTheAnswerThrough(t *testing.T) {
	const answer = `{"hosts":[{"id":"host-a","region":"region-1","zone":"","nodeGroup":"",
		"cpuTotal":16,"cpuFree":5.5,"memTotalGi":62,"memFreeGi":31,"gpuType":"","gpuTotal":0,"gpuFree":0,
		"utilization":0.4,"ready":true}]}`
	var path, method string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(answer))
	}))
	defer srv.Close()
	t.Setenv("LIVELLM_API_URL", srv.URL)
	t.Setenv("LIVELLM_API_KEY", "llc_test")

	r, w, _ := os.Pipe()
	stdout := os.Stdout
	os.Stdout = w
	err := cmdHosts(nil)
	w.Close()
	os.Stdout = stdout
	printed, _ := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if method != "GET" || path != "/v1/fleet/hosts" {
		t.Errorf("sent %s %s, want GET /v1/fleet/hosts", method, path)
	}
	var got, want any
	if err := json.Unmarshal(printed, &got); err != nil {
		t.Fatalf("printed %q, not JSON: %v", printed, err)
	}
	_ = json.Unmarshal([]byte(answer), &want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("printed %v, want the answer %v", got, want)
	}
}

// --host and --region make a placement; neither is automatic; both, or one
// given with no value, is refused.
func TestPlacementFlags(t *testing.T) {
	parse := func(args ...string) *flag.FlagSet {
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		fs.String("host", "", "")
		fs.String("region", "", "")
		if err := fs.Parse(args); err != nil {
			t.Fatal(err)
		}
		return fs
	}
	cases := []struct {
		args []string
		want map[string]any
	}{
		{nil, nil},
		{[]string{"--host", "host-a"}, map[string]any{"strategy": "host", "host": "host-a"}},
		{[]string{"--region", "region-1"}, map[string]any{"strategy": "region", "region": "region-1"}},
	}
	for _, c := range cases {
		got, err := placementFlags(parse(c.args...))
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("placementFlags(%q) = %v, %v; want %v", c.args, got, err, c.want)
		}
	}
	for _, args := range [][]string{
		{"--host", "host-a", "--region", "region-1"},
		{"--host", ""},
		{"--host", " "},
		{"--region", ""},
		{"--host=", "--region=region-1"},
	} {
		if got, err := placementFlags(parse(args...)); err == nil {
			t.Errorf("placementFlags(%q) = %v; want it refused", args, got)
		}
	}
}
