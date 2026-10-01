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

// hosts prints the API's answer as it is, every field kept.
func TestHostsPassesTheAnswerThrough(t *testing.T) {
	const answer = `{"hosts":[{"id":"selangor","region":"ru-mow","zone":"","nodeGroup":"",
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

// --host and --region make a placement; neither is automatic; both is refused.
func TestPlacementFlags(t *testing.T) {
	cases := []struct {
		host, region string
		want         map[string]any
	}{
		{"", "", nil},
		{"selangor", "", map[string]any{"strategy": "host", "host": "selangor"}},
		{"", "ru-mow", map[string]any{"strategy": "region", "region": "ru-mow"}},
		{" ", " ", nil},
	}
	for _, c := range cases {
		got, err := placementFlags(c.host, c.region)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("placementFlags(%q, %q) = %v, %v; want %v", c.host, c.region, got, err, c.want)
		}
	}
	if _, err := placementFlags("selangor", "ru-mow"); err == nil {
		t.Error("--host with --region should be refused")
	}
}
