package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

// What the commands do. Each one is a call or two to the public API; the
// printing is what makes it a tool rather than curl.

func cmdLogin(args []string) error {
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	access := fs.String("access", "full", "how far this sign-in reaches: use, create or full")
	_ = fs.Parse(args)

	name := "livellm on " + hostname()
	var start struct {
		DeviceCode string `json:"device_code"`
		UserCode   string `json:"user_code"`
		Verify     string `json:"verification_uri_complete"`
		Interval   int    `json:"interval"`
		ExpiresIn  int    `json:"expires_in"`
	}
	if err := form("/v1/oauth/device/code", url.Values{
		"client_name": {name}, "scope": {*access},
	}, &start); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Open this and press Allow:\n\n  %s\n\n(code %s)\nWaiting…\n",
		start.Verify, start.UserCode)

	interval := start.Interval
	if interval < 1 {
		interval = 5
	}
	deadline := time.Now().Add(time.Duration(start.ExpiresIn) * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(time.Duration(interval) * time.Second)
		var out tokenAnswer
		err := form("/v1/oauth/token", url.Values{
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
			"device_code": {start.DeviceCode},
		}, &out)
		if err == nil {
			c := out.creds()
			if err := saveCreds(c); err != nil {
				return err
			}
			return print(map[string]any{
				"signedIn": true, "workspace": c.Workspace, "access": c.Access,
			})
		}
		var p *problem
		if asProblem(err, &p) {
			switch {
			case strings.Contains(p.Msg, "waiting"), strings.Contains(p.Msg, "pending"):
				continue
			case strings.Contains(p.Msg, "poll"), strings.Contains(p.Msg, "slow"):
				interval += 5
				continue
			}
		}
		return err
	}
	return fmt.Errorf("nobody allowed it in time — run livellm login again")
}

func cmdLogout([]string) error {
	c, _ := loadCreds()
	if c != nil && c.RefreshToken != "" {
		_ = form("/v1/oauth/revoke", url.Values{"token": {c.RefreshToken}}, nil)
	}
	if err := saveCreds(nil); err != nil {
		return err
	}
	return print(map[string]any{"signedOut": true})
}

func cmdWhoami([]string) error {
	var ws struct {
		Name string `json:"name"`
		Plan string `json:"plan"`
	}
	if err := call("GET", "/v1/workspace", nil, &ws); err != nil {
		return err
	}
	out := map[string]any{"workspace": ws.Name, "plan": ws.Plan}
	if os.Getenv("LIVELLM_API_KEY") != "" {
		out["signedInWith"] = "api key"
	} else if c, _ := loadCreds(); c != nil {
		out["signedInWith"] = "sign-in"
		out["access"] = c.Access
	}
	var billing map[string]any
	if err := call("GET", "/v1/billing", nil, &billing); err == nil {
		if u, ok := billing["usage"]; ok {
			out["usage"] = u
		}
	}
	return print(out)
}

// resource is one line of `ls`: what the workspace holds and how it is doing.
type resource struct {
	ID        string   `json:"id"`
	Type      string   `json:"type"`
	State     string   `json:"state"`
	Ready     bool     `json:"ready"`
	CreatedBy string   `json:"createdBy,omitempty"`
	Endpoints []string `json:"endpoints,omitempty"`
	StopsAt   string   `json:"stopsAt,omitempty"`
}

func resources() ([]resource, error) {
	var ws struct {
		Spec struct {
			Workloads []struct {
				ID        string `json:"id"`
				Type      string `json:"type"`
				CreatedBy *struct {
					Name string `json:"name"`
				} `json:"createdBy"`
			} `json:"workloads"`
		} `json:"spec"`
	}
	if err := call("GET", "/v1/workspace", nil, &ws); err != nil {
		return nil, err
	}
	var live struct {
		Workloads []struct {
			ID        string `json:"id"`
			Phase     string `json:"phase"`
			Ready     bool   `json:"ready"`
			ExpiresAt string `json:"expiresAt"`
			SSH       string `json:"ssh"`
			Endpoints []struct {
				URL  string `json:"url"`
				Addr string `json:"addr"`
			} `json:"endpoints"`
		} `json:"workloads"`
	}
	_ = call("GET", "/v1/status", nil, &live)
	byID := map[string]int{}
	for i, w := range live.Workloads {
		byID[w.ID] = i
	}
	out := make([]resource, 0, len(ws.Spec.Workloads))
	for _, w := range ws.Spec.Workloads {
		r := resource{ID: w.ID, Type: w.Type, State: "unknown", CreatedBy: "a person"}
		if w.CreatedBy != nil && w.CreatedBy.Name != "" {
			r.CreatedBy = w.CreatedBy.Name
		}
		if i, ok := byID[w.ID]; ok {
			l := live.Workloads[i]
			r.State, r.Ready, r.StopsAt = strings.ToLower(l.Phase), l.Ready, l.ExpiresAt
			for _, e := range l.Endpoints {
				if e.URL != "" {
					r.Endpoints = append(r.Endpoints, e.URL)
				} else if e.Addr != "" {
					r.Endpoints = append(r.Endpoints, e.Addr)
				}
			}
			if l.SSH != "" {
				r.Endpoints = append(r.Endpoints, "ssh "+l.SSH)
			}
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func cmdList(args []string) error {
	fs := flag.NewFlagSet("ls", flag.ExitOnError)
	kind := fs.String("type", "", "only this kind (vm-ubuntu, pod, storage, browser…)")
	_ = fs.Parse(args)
	all, err := resources()
	if err != nil {
		return err
	}
	out := make([]resource, 0, len(all))
	for _, r := range all {
		if *kind == "" || r.Type == *kind {
			out = append(out, r)
		}
	}
	return print(map[string]any{"resources": out})
}

func cmdStatus(args []string) error {
	var live map[string]any
	if err := call("GET", "/v1/status", nil, &live); err != nil {
		return err
	}
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return print(live)
	}
	id := args[0]
	if list, ok := live["workloads"].([]any); ok {
		for _, raw := range list {
			if w, ok := raw.(map[string]any); ok && w["id"] == id {
				return print(w)
			}
		}
	}
	return fmt.Errorf("there is nothing called %q here — try livellm ls", id)
}

func cmdLogs(args []string) error {
	id, rest, err := needArg(args, "resource")
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("logs", flag.ExitOnError)
	lines := fs.Int("lines", 100, "how many lines per container")
	_ = fs.Parse(rest)
	var out map[string]any
	if err := call("GET", fmt.Sprintf("/v1/workloads/%s/observe?tailLines=%d",
		url.PathEscape(id), *lines), nil, &out); err != nil {
		return err
	}
	return print(out)
}

func cmdConnect(args []string) error {
	id, rest, err := needArg(args, "resource")
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("connect", flag.ExitOnError)
	tool := fs.String("tool", "", "cdp, view, api or computer, when a resource offers several")
	_ = fs.Parse(rest)
	body := map[string]any{}
	if *tool != "" {
		body["tool"] = *tool
	}
	var out map[string]any
	if err := call("POST", "/v1/workloads/"+url.PathEscape(id)+"/connect", body, &out); err != nil {
		return err
	}
	// A machine is reached over SSH, and its address lives in the status.
	if t, _ := out["type"].(string); strings.HasPrefix(t, "vm-") {
		var live struct {
			Workloads []struct {
				ID  string `json:"id"`
				SSH string `json:"ssh"`
			} `json:"workloads"`
		}
		if call("GET", "/v1/status", nil, &live) == nil {
			for _, w := range live.Workloads {
				if w.ID == id && w.SSH != "" {
					out["ssh"] = map[string]any{"address": w.SSH}
				}
			}
		}
	}
	return print(out)
}

func cmdKeys([]string) error {
	var out map[string]any
	if err := call("GET", "/v1/ssh-keys", nil, &out); err != nil {
		return err
	}
	return print(out)
}

func cmdCreate(args []string) error {
	kind, rest, err := needArg(args, "kind of resource")
	if err != nil {
		return fmt.Errorf("which kind? vm-ubuntu, vm-ubuntu-desktop, vm-windows, pod, storage or browser")
	}
	fs := flag.NewFlagSet("create", flag.ExitOnError)
	file := fs.String("f", "", "a JSON file with the resource's settings")
	_ = fs.Parse(rest)
	if *file == "" {
		return fmt.Errorf("pass the settings with -f file.json")
	}
	raw, err := os.ReadFile(*file)
	if err != nil {
		return err
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return fmt.Errorf("%s isn't valid JSON: %w", *file, err)
	}
	if body["id"] == nil {
		return fmt.Errorf("the settings need an id")
	}
	if err := call("POST", "/v1/workloads/"+url.PathEscape(kind), body, nil); err != nil {
		return err
	}
	return print(map[string]any{"created": body["id"], "type": kind})
}

func cmdRemove(args []string) error {
	id, rest, err := needArg(args, "resource")
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("rm", flag.ExitOnError)
	yes := fs.Bool("y", false, "don't ask")
	_ = fs.Parse(rest)
	if !*yes && !confirm(fmt.Sprintf("Delete %s and its disk? This can't be undone.", id)) {
		return fmt.Errorf("nothing was deleted")
	}
	if err := call("DELETE", "/v1/workloads/"+url.PathEscape(id), nil, nil); err != nil {
		return err
	}
	return print(map[string]any{"deleted": id})
}

func cmdRestart(args []string) error {
	id, _, err := needArg(args, "resource")
	if err != nil {
		return err
	}
	if err := call("POST", "/v1/workloads/"+url.PathEscape(id)+"/restart", map[string]any{}, nil); err != nil {
		return err
	}
	return print(map[string]any{"restarting": id})
}

func cmdBuild(args []string) error {
	id, _, err := needArg(args, "app")
	if err != nil {
		return err
	}
	var out map[string]any
	if err := call("POST", "/v1/workloads/"+url.PathEscape(id)+"/build", map[string]any{}, &out); err != nil {
		return err
	}
	if out == nil {
		out = map[string]any{"building": id}
	}
	return print(out)
}

func cmdBuilds(args []string) error {
	id, _, err := needArg(args, "app")
	if err != nil {
		return err
	}
	var out map[string]any
	if err := call("GET", "/v1/workloads/"+url.PathEscape(id)+"/builds", nil, &out); err != nil {
		return err
	}
	return print(out)
}

func cmdDeploy(args []string) error {
	id, rest, err := needArg(args, "app")
	if err != nil {
		return err
	}
	build, _, err := needArg(rest, "build")
	if err != nil {
		return fmt.Errorf("which build? livellm builds %s lists them", id)
	}
	path := fmt.Sprintf("/v1/workloads/%s/builds/%s/deploy", url.PathEscape(id), url.PathEscape(build))
	if err := call("POST", path, map[string]any{}, nil); err != nil {
		return err
	}
	return print(map[string]any{"deploying": build, "to": id})
}

func confirm(question string) bool {
	fmt.Fprintf(os.Stderr, "%s [y/N] ", question)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "a terminal"
	}
	return h
}
