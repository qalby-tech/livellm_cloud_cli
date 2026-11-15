package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// driver ID reads the state and says it in words; a state that needs
// something names the command for it.
func TestDriverShow(t *testing.T) {
	for _, c := range []struct {
		answer, summary, next string
	}{
		{`{"state":"working","detail":"620.70.06"}`, "The NVIDIA driver works: 620.70.06", ""},
		{`{"state":"restart-needed","detail":"installed; restart the machine to finish"}`, "The NVIDIA driver is installed. Restart the machine to finish: installed; restart the machine to finish", "livellm restart box"},
		{`{"state":"failed","detail":"the install stopped","log":["a","b"]}`, "The NVIDIA driver didn't install: the install stopped", "livellm driver install box tries again"},
		{`{"state":"not-installed"}`, "The NVIDIA driver program isn't on this machine.", "livellm driver install box"},
		{`{"state":"none","detail":"no NVIDIA card"}`, "No NVIDIA card in this machine: no NVIDIA card", ""},
		{`{"state":"unknown"}`, "The NVIDIA driver's state isn't known right now.", ""},
	} {
		f := &fakeAlerts{status: 200, answer: c.answer}
		f.serve(t)
		out, err := stdoutOf(t, func() error { return cmdDriver([]string{"box"}) })
		if err != nil {
			t.Fatal(err)
		}
		if f.method != "GET" || f.path != "/v1/workloads/box/gpu-driver" {
			t.Fatalf("sent %s %s", f.method, f.path)
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("not JSON: %v\n%s", err, out)
		}
		if got["summary"] != c.summary {
			t.Errorf("%s: summary %q, want %q", c.answer, got["summary"], c.summary)
		}
		if n, _ := got["next"].(string); n != c.next {
			t.Errorf("%s: next %q, want %q", c.answer, n, c.next)
		}
	}
}

// driver install ID posts with no body and says how to follow it.
func TestDriverInstall(t *testing.T) {
	f := &fakeAlerts{status: 202, answer: `{"state":"installing","detail":"started; it installs the driver when the machine has an NVIDIA card"}`}
	f.serve(t)
	out, err := stdoutOf(t, func() error { return cmdDriver([]string{"install", "box"}) })
	if err != nil {
		t.Fatal(err)
	}
	if f.method != "POST" || f.path != "/v1/workloads/box/gpu-driver" || f.body != nil {
		t.Fatalf("sent %s %s %v", f.method, f.path, f.body)
	}
	if !strings.Contains(out, `"livellm driver box shows how it goes`) || !strings.Contains(out, "Installing the NVIDIA driver") {
		t.Fatalf("printed %s", out)
	}
}

// The router's 404 (an API without the routes) is said as such; the API's
// own refusals keep its words.
func TestDriverRefusals(t *testing.T) {
	f := &fakeAlerts{status: 404, answer: "404 page not found\n"}
	f.serve(t)
	err := cmdDriver([]string{"box"})
	var p *problem
	if !asProblem(err, &p) || !strings.Contains(p.Msg, "isn't available on this server") || !strings.Contains(p.Next, "0.101.9") {
		t.Fatalf("router 404: %v", err)
	}
	f = &fakeAlerts{status: 422, answer: `{"error":"box's system has no automatic NVIDIA driver.","code":"gpu_driver_unsupported"}`}
	f.serve(t)
	err = cmdDriver([]string{"install", "box"})
	if !asProblem(err, &p) || p.Status != 422 || !strings.Contains(p.Msg, "no automatic NVIDIA driver") {
		t.Fatalf("422: %v", err)
	}
	if err := cmdDriver([]string{"box", "extra"}); err == nil || !strings.Contains(err.Error(), "takes nothing more") {
		t.Fatalf("extra args: %v", err)
	}
}
