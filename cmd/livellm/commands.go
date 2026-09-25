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

// statusWarning carries why the live state is missing, when it is: a resource
// list that silently says "unknown" for everything is worse than one that says
// what went wrong.
var statusWarning string

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
				TCP  bool   `json:"tcp"`
				UDP  bool   `json:"udp"`
			} `json:"endpoints"`
		} `json:"workloads"`
	}
	statusWarning = ""
	if err := call("GET", "/v1/status", nil, &live); err != nil {
		statusWarning = "couldn't read how things are running: " + err.Error()
	}
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
				switch {
				case e.URL != "":
					r.Endpoints = append(r.Endpoints, e.URL)
				case e.Addr != "" && e.UDP:
					r.Endpoints = append(r.Endpoints, "udp "+e.Addr)
				case e.Addr != "" && e.TCP:
					r.Endpoints = append(r.Endpoints, "tcp "+e.Addr)
				case e.Addr != "":
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
	answer := map[string]any{"resources": out}
	if statusWarning != "" {
		answer["warning"] = statusWarning
	}
	return print(answer)
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
	desktop := fs.Int("desktop", -1, "for a Desktop App: which desktop, from 0")
	width := fs.Int("screen-width", 0, "computer: shrink screenshots to this many pixels wide (320-3840)")
	format := fs.String("format", "", "computer: png or jpeg")
	_ = fs.Parse(rest)
	body := map[string]any{}
	if *tool != "" {
		body["tool"] = *tool
	}
	if *desktop >= 0 {
		body["desktop"] = *desktop
	}
	screen := map[string]any{}
	if *width > 0 {
		screen["width"] = *width
	}
	if *format != "" {
		screen["format"] = *format
	}
	if len(screen) > 0 {
		body["screen"] = screen
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
	fillRawAddresses(id, out)
	return print(out)
}

// fillRawAddresses gives an app's raw TCP/UDP ports their host:port, which
// lives in the status like a machine's SSH address.
func fillRawAddresses(id string, out map[string]any) {
	urls, _ := out["urls"].([]any)
	var raw []map[string]any
	for _, u := range urls {
		if m, ok := u.(map[string]any); ok && m["raw"] == true && m["address"] == nil {
			raw = append(raw, m)
		}
	}
	if len(raw) == 0 {
		return
	}
	var live struct {
		Workloads []struct {
			ID        string `json:"id"`
			Endpoints []struct {
				Name string `json:"name"`
				Addr string `json:"addr"`
				UDP  bool   `json:"udp"`
			} `json:"endpoints"`
		} `json:"workloads"`
	}
	if call("GET", "/v1/status", nil, &live) != nil {
		return
	}
	for _, w := range live.Workloads {
		if w.ID != id {
			continue
		}
		for _, m := range raw {
			for _, e := range w.Endpoints {
				if e.Name == m["port"] && e.Addr != "" {
					m["address"] = e.Addr
					m["protocol"] = "tcp"
					if e.UDP {
						m["protocol"] = "udp"
					}
				}
			}
		}
	}
}

func cmdExec(args []string) error {
	id, rest, err := needArg(args, "machine")
	if err != nil {
		return err
	}
	if len(rest) == 0 || strings.HasPrefix(rest[0], "-") {
		return fmt.Errorf("which command? livellm exec %s \"uname -a\"", id)
	}
	command, rest := rest[0], rest[1:]
	fs := flag.NewFlagSet("exec", flag.ExitOnError)
	session := fs.String("session", "", "commands in the same session share a working folder")
	timeout := fs.Int("timeout", 60, "seconds, up to 600")
	desktop := fs.Int("desktop", -1, "for a Desktop App: which desktop, from 0")
	_ = fs.Parse(rest)
	body := map[string]any{"command": command, "timeout": *timeout}
	if *session != "" {
		body["session"] = *session
	}
	if *desktop >= 0 {
		body["desktop"] = *desktop
	}
	// The answer comes when the command ends: wait a little longer than it may run.
	if t := time.Duration(*timeout+30) * time.Second; t > client.Timeout {
		client.Timeout = t
	}
	var out map[string]any
	if err := call("POST", "/v1/workloads/"+url.PathEscape(id)+"/exec", body, &out); err != nil {
		return err
	}
	return print(out)
}

func cmdShare(args []string) error {
	id, rest, err := needArg(args, "machine")
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("share", flag.ExitOnError)
	control := fs.Bool("control", false, "let whoever opens it use the screen, not only watch")
	life := fs.String("for", "", "1h, 24h or 7d (default 24h)")
	desktop := fs.Int("desktop", -1, "for a Desktop App: which desktop, from 0")
	_ = fs.Parse(rest)
	body := map[string]any{"mode": "view"}
	if *control {
		body["mode"] = "control"
	}
	if *life != "" {
		body["for"] = *life
	}
	if *desktop >= 0 {
		body["desktop"] = *desktop
	}
	var out map[string]any
	if err := call("POST", "/v1/workloads/"+url.PathEscape(id)+"/shares", body, &out); err != nil {
		return err
	}
	// The link is shown only now.
	return print(out)
}

func cmdShares(args []string) error {
	id, _, err := needArg(args, "machine")
	if err != nil {
		return err
	}
	var out map[string]any
	if err := call("GET", "/v1/workloads/"+url.PathEscape(id)+"/shares", nil, &out); err != nil {
		return err
	}
	return print(out)
}

func cmdUnshare(args []string) error {
	id, rest, err := needArg(args, "machine")
	if err != nil {
		return err
	}
	share, _, err := needArg(rest, "link")
	if err != nil {
		return fmt.Errorf("which link? livellm shares %s lists them", id)
	}
	path := fmt.Sprintf("/v1/workloads/%s/shares/%s", url.PathEscape(id), url.PathEscape(share))
	if err := call("DELETE", path, nil, nil); err != nil {
		return err
	}
	return print(map[string]any{"closed": share})
}

func cmdRelease(args []string) error {
	id, _, err := needArg(args, "machine")
	if err != nil {
		return err
	}
	var out map[string]any
	if err := call("DELETE", "/v1/workloads/"+url.PathEscape(id)+"/reservation", nil, &out); err != nil {
		return err
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
		return fmt.Errorf("which kind? vm-ubuntu, vm-ubuntu-desktop, vm-windows, pod, storage, browser, browser-api — or apps, several at once")
	}
	if kind == "browser-api" {
		kind = browserAPIType
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
	// Several apps at once, all or nothing: the file holds a list of app
	// settings (or {"apps": [...]}), the same bodies `create pod` takes.
	if kind == "apps" {
		var list []map[string]any
		if err := json.Unmarshal(raw, &list); err != nil {
			var wrapped struct {
				Apps []map[string]any `json:"apps"`
			}
			if err := json.Unmarshal(raw, &wrapped); err != nil || len(wrapped.Apps) == 0 {
				return fmt.Errorf("%s should hold a list of apps, or {\"apps\": [...]}", *file)
			}
			list = wrapped.Apps
		}
		var out struct {
			Created []string `json:"created"`
		}
		if err := call("POST", "/v1/workloads", map[string]any{"apps": list}, &out); err != nil {
			return err
		}
		return print(map[string]any{"created": out.Created})
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

func cmdStop(args []string) error { return setStopped(args, true) }

// stoppable are the resource types that honour "stopped": machines, desktops
// and apps. A browser or a database keeps running whatever the flag says, so
// stop refuses them rather than report a stop that never happens.
var stoppable = map[string]bool{
	"vm-ubuntu": true, "vm-ubuntu-desktop": true, "vm-windows": true, "desktop": true, "pod": true,
}

func cmdStart(args []string) error { return setStopped(args, false) }

// setStopped stops or starts a resource. It reads the resource only to refuse
// what can't be stopped and to say when nothing would change; the write is a
// patch of "stopped" alone, so a change made meanwhile (in the console, by
// another client) is kept.
func setStopped(args []string, stop bool) error {
	id, _, err := needArg(args, "resource")
	if err != nil {
		return err
	}
	var ws struct {
		Spec struct {
			Workloads []map[string]any `json:"workloads"`
		} `json:"spec"`
	}
	if err := call("GET", "/v1/workspace", nil, &ws); err != nil {
		return err
	}
	var w map[string]any
	for _, x := range ws.Spec.Workloads {
		if x["id"] == id {
			w = x
			break
		}
	}
	if w == nil {
		return fmt.Errorf("there is nothing called %q here — try livellm ls", id)
	}
	if t, _ := w["type"].(string); !stoppable[t] {
		return fmt.Errorf("%q can't be stopped or started: only machines and apps can; livellm rm %s deletes it", id, id)
	}
	verb := "starting"
	if stop {
		verb = "stopping"
	}
	if was, _ := w["stopped"].(bool); was == stop {
		state := "running"
		if stop {
			state = "stopped"
		}
		return print(map[string]any{"id": id, "already": state})
	}
	if err := patchWorkload(id, map[string]any{"stopped": stop}); err != nil {
		return err
	}
	return print(map[string]any{verb: id})
}

// cmdSet changes some of a resource's settings: the file holds only what
// changes, in the shape the resource has (a JSON merge patch), and null
// removes a setting.
func cmdSet(args []string) error {
	id, rest, err := needArg(args, "resource")
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("set", flag.ExitOnError)
	file := fs.String("f", "", "a JSON file with the settings that change")
	_ = fs.Parse(rest)
	if *file == "" {
		return fmt.Errorf("pass the changes with -f changes.json, e.g. {\"pod\": {\"cpu\": \"1\"}}")
	}
	raw, err := os.ReadFile(*file)
	if err != nil {
		return err
	}
	var patch map[string]any
	if err := json.Unmarshal(raw, &patch); err != nil {
		return fmt.Errorf("%s isn't a JSON object: %w", *file, err)
	}
	if len(patch) == 0 {
		return fmt.Errorf("%s changes nothing", *file)
	}
	if err := patchWorkload(id, patch); err != nil {
		return err
	}
	return print(map[string]any{"changed": id})
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
