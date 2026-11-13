package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
)

// fakeAlerts is a stand-in API for the alert settings routes: it records the
// last PUT body and answers what the test says.
type fakeAlerts struct {
	status int
	answer string
	method string
	path   string
	body   map[string]any
}

func (f *fakeAlerts) serve(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.method, f.path, f.body = r.Method, r.URL.EscapedPath(), nil
		raw, _ := io.ReadAll(r.Body)
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &f.body)
		}
		if strings.HasPrefix(f.answer, "{") {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(f.status)
		_, _ = w.Write([]byte(f.answer))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("LIVELLM_API_URL", srv.URL)
	t.Setenv("LIVELLM_API_KEY", "llc_test")
}

const alertsAnswer = `{"usual":{"cpu":90,"memory":90,"disk":90},"kinds":["down","cpu","memory","disk","gpu"],
 "settings":{"down":{"mode":"off"},"cpu":{"mode":"usual"},"memory":{"mode":"level","level":97},"disk":{"mode":"usual"},"gpu":{"mode":"usual"}}}`

// stdoutOf runs fn and hands back what it printed.
func stdoutOf(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	err := fn()
	w.Close()
	os.Stdout = old
	b, _ := io.ReadAll(r)
	return string(b), err
}

func TestAlertsShow(t *testing.T) {
	f := &fakeAlerts{status: 200, answer: alertsAnswer}
	f.serve(t)
	out, err := stdoutOf(t, func() error { return cmdAlerts([]string{"web"}) })
	if err != nil {
		t.Fatal(err)
	}
	if f.method != "GET" || f.path != "/v1/workloads/web/alert-settings" {
		t.Fatalf("sent %s %s", f.method, f.path)
	}
	var got struct {
		Summary  []string       `json:"summary"`
		Settings map[string]any `json:"settings"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	want := []string{"Down: off", "CPU: usual (90%)", "Memory: at 97%", "Disk: usual (90%)", "GPU: on"}
	if !reflect.DeepEqual(got.Summary, want) {
		t.Fatalf("summary %q, want %q", got.Summary, want)
	}
	if got.Settings["memory"] == nil {
		t.Fatalf("the API's answer is not kept whole: %s", out)
	}
}

// Only the flags given are sent; on is usual for down and gpu; a number is
// the resource's own level.
func TestAlertsSetSendsOnlyWhatIsGiven(t *testing.T) {
	cases := []struct {
		args []string
		want map[string]any
	}{
		{[]string{"web", "--cpu", "off"}, map[string]any{"cpu": map[string]any{"mode": "off"}}},
		{[]string{"web", "--memory", "97"}, map[string]any{"memory": map[string]any{"mode": "level", "level": 97.0}}},
		{[]string{"web", "--disk=80%"}, map[string]any{"disk": map[string]any{"mode": "level", "level": 80.0}}},
		{[]string{"web", "--cpu", "usual", "--down", "on", "--gpu", "off"}, map[string]any{
			"cpu": map[string]any{"mode": "usual"}, "down": map[string]any{"mode": "usual"}, "gpu": map[string]any{"mode": "off"}}},
		// out of range: sent, for the API to judge
		{[]string{"web", "--cpu", "10"}, map[string]any{"cpu": map[string]any{"mode": "level", "level": 10.0}}},
	}
	for _, c := range cases {
		f := &fakeAlerts{status: 200, answer: alertsAnswer}
		f.serve(t)
		if _, err := stdoutOf(t, func() error { return cmdAlerts(append([]string{"set"}, c.args...)) }); err != nil {
			t.Fatalf("%v: %v", c.args, err)
		}
		if f.method != "PUT" || f.path != "/v1/workloads/web/alert-settings" {
			t.Fatalf("%v: sent %s %s", c.args, f.method, f.path)
		}
		if want := map[string]any{"settings": c.want}; !reflect.DeepEqual(f.body, want) {
			t.Fatalf("%v: body %v, want %v", c.args, f.body, want)
		}
	}
}

// What can't be sent is refused before anything goes out.
func TestAlertsSetRefusesBeforeSending(t *testing.T) {
	for _, args := range [][]string{
		{"web"},                       // nothing to change
		{"web", "--down", "97"},       // down takes no level
		{"web", "--gpu", "usual-ish"}, // not on or off
		{"web", "--cpu", "high"},      // not a number
		{"web", "--cpu", "on"},        // cpu has no "on"
		{"--cpu", "off"},              // no id
	} {
		f := &fakeAlerts{status: 200, answer: alertsAnswer}
		f.serve(t)
		if err := cmdAlerts(append([]string{"set"}, args...)); err == nil {
			t.Fatalf("%v: accepted", args)
		}
		if f.method != "" {
			t.Fatalf("%v: sent %s %s", args, f.method, f.path)
		}
	}
}

func TestAlertsRefusals(t *testing.T) {
	cases := []struct {
		name, answer string
		status, exit int
		says         []string
	}{
		{"a refused level names its part and flag", `{"error":"the level is a whole percent from 50 to 99","code":"alert_settings","field":"settings.cpu.level"}`,
			422, 1, []string{"settings.cpu.level (--cpu): the level is a whole percent from 50 to 99", "(422)"}},
		{"an agent sign-in is refused in the API's words", `{"error":"Agents can't do this. Do it in the LiveLLM console."}`,
			403, 2, []string{"Agents can't do this. Do it in the LiveLLM console. (403)"}},
		{"an API without the route", "404 page not found\n",
			404, 1, []string{"Alert settings aren't available on this server.", "0.101.5"}},
		{"a resource that isn't there is the API's own 404", `{"error":"workload not found: web"}`,
			404, 1, []string{"workload not found: web (404)"}},
	}
	for _, c := range cases {
		f := &fakeAlerts{status: c.status, answer: c.answer}
		f.serve(t)
		err := cmdAlerts([]string{"set", "web", "--cpu", "10"})
		if err == nil {
			t.Fatalf("%s: no error", c.name)
		}
		for _, s := range c.says {
			if !strings.Contains(err.Error(), s) {
				t.Fatalf("%s: %q doesn't say %q", c.name, err.Error(), s)
			}
		}
		if got := exitCode(err); got != c.exit {
			t.Fatalf("%s: exit %d, want %d", c.name, got, c.exit)
		}
		if c.status == 404 && c.answer[0] != '{' {
			// the read says the same
			if err := cmdAlerts([]string{"web"}); err == nil || !strings.Contains(err.Error(), "aren't available") {
				t.Fatalf("%s: the read said %v", c.name, err)
			}
		}
	}
}

// field is read only from an alert settings refusal.
func TestFieldOnlyFromAlertSettings(t *testing.T) {
	if p := problemFrom(422, []byte(`{"error":"x","code":"other","field":"a.b"}`)); p.Field != "" {
		t.Fatalf("field read from another refusal: %q", p.Field)
	}
}
