package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// What the commands do. Each one is a call or two to the public API; the
// printing is what makes it a tool rather than curl.

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
	// Databases are an app's links to databases, as its settings hold them;
	// UsedBy the apps that link a database or wait for it.
	Databases []map[string]any `json:"databases,omitempty"`
	UsedBy    []string         `json:"usedBy,omitempty"`
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
				Pod *struct {
					Databases []map[string]any `json:"databases"`
				} `json:"pod"`
			} `json:"workloads"`
		} `json:"spec"`
	}
	if err := call("GET", "/v1/workspace", nil, &ws); err != nil {
		return nil, err
	}
	var live struct {
		Workloads []struct {
			ID        string   `json:"id"`
			Phase     string   `json:"phase"`
			Ready     bool     `json:"ready"`
			ExpiresAt string   `json:"expiresAt"`
			SSH       string   `json:"ssh"`
			UsedBy    []string `json:"usedBy"`
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
		if w.Pod != nil {
			r.Databases = w.Pod.Databases
		}
		if i, ok := byID[w.ID]; ok {
			l := live.Workloads[i]
			r.State, r.Ready, r.StopsAt = strings.ToLower(l.Phase), l.Ready, l.ExpiresAt
			r.UsedBy = l.UsedBy
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

// execPoll is how long one call waits for a command (the API's most) and
// execSlack how much past the command's own time limit exec keeps looking.
const (
	execPoll  = 55
	execSlack = 2 * time.Minute
)

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
	body := map[string]any{"command": command, "timeout": *timeout, "wait": execPoll}
	if *session != "" {
		body["session"] = *session
	}
	if *desktop >= 0 {
		body["desktop"] = *desktop
	}
	// Each call waits up to execPoll seconds for the command; getting onto
	// the computer comes on top of that.
	if t := time.Duration(execPoll+35) * time.Second; t > client.Timeout {
		client.Timeout = t
	}
	var out map[string]any
	if err := call("POST", "/v1/workloads/"+url.PathEscape(id)+"/exec", body, &out); err != nil {
		return err
	}
	// A command still going when the call answered keeps going on the
	// computer: look again until it ends. It ends by its time limit at the
	// latest (the platform stops it then).
	deadline := time.Now().Add(time.Duration(*timeout)*time.Second + execSlack)
	for !execDone(out) {
		run, _ := out["runId"].(string)
		if run == "" {
			return fmt.Errorf("LiveLLM answered a command still going without its run id")
		}
		if time.Now().After(deadline) {
			_ = print(out)
			return fmt.Errorf("%s is still running after its time limit; it was left as it is (run %s)", id, run)
		}
		var next map[string]any
		path := "/v1/workloads/" + url.PathEscape(id) + "/exec/" + url.PathEscape(run) + "?wait=" + strconv.Itoa(execPoll)
		if err := callRetrying("GET", path, &next); err != nil {
			return err
		}
		out = next
	}
	return print(out)
}

// execDone reads whether a command's answer is its end. An answer without
// done is from before runs, and is always the end.
func execDone(out map[string]any) bool {
	done, ok := out["done"].(bool)
	return !ok || done
}

// callRetrying makes a read that may be repeated, trying twice more when the
// network, not LiveLLM, failed it.
func callRetrying(method, path string, out any) error {
	var err error
	for try := 0; try < 3; try++ {
		if err = call(method, path, nil, out); err == nil {
			return nil
		}
		var p *problem
		if asProblem(err, &p) || try == 2 {
			return err
		}
		time.Sleep(time.Duration(try+1) * 2 * time.Second)
	}
	return err
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

func cmdCreate(args []string) error {
	if len(args) > 0 && strings.HasPrefix(args[0], "--template") {
		return createFromTemplate(args)
	}
	kind, rest, err := needArg(args, "kind of resource")
	if err != nil {
		return fmt.Errorf("which kind? vm-ubuntu, vm-ubuntu-desktop, vm-windows, pod, storage, browser, desktop, browser-api — or apps, several at once; or --template T --id NEW")
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
	// settings (or {"apps": [...]}), the same bodies `create pod` takes, and
	// the databases to make with them ({"apps": [...], "databases": [...]},
	// each what `create storage` takes, a password optional).
	if kind == "apps" {
		var list []map[string]any
		var dbs []map[string]any
		if err := json.Unmarshal(raw, &list); err != nil {
			var wrapped struct {
				Apps      []map[string]any `json:"apps"`
				Databases []map[string]any `json:"databases"`
			}
			if err := json.Unmarshal(raw, &wrapped); err != nil || len(wrapped.Apps) == 0 {
				return fmt.Errorf("%s should hold a list of apps, or {\"apps\": [...], \"databases\": [...]}", *file)
			}
			list, dbs = wrapped.Apps, wrapped.Databases
		}
		body := map[string]any{"apps": list}
		if len(dbs) > 0 {
			body["databases"] = dbs
		}
		var out struct {
			Created   []string `json:"created"`
			Databases []string `json:"databases"`
		}
		if err := call("POST", "/v1/workloads", body, &out); err != nil {
			return err
		}
		answer := map[string]any{"created": out.Created}
		if len(out.Databases) > 0 {
			answer["databases"] = out.Databases
		}
		return print(answer)
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

// templateBodyKeys are what a create from a template takes besides the new
// id: the secrets the template left out. Anything else in -f would be
// dropped without a word, so it is refused instead.
var templateBodyKeys = map[string]bool{
	"secretEnv": true, "imagePassword": true, "gitToken": true, "portPasswords": true,
	"credentials": true, "services": true,
}

// createFromTemplate makes a resource from a saved template (a Composable App
// from a stack template, with its databases): the new id, and the secrets the
// template left out, from -f, --secret PATH=VALUE and --secret-env PATH=VAR.
func createFromTemplate(args []string) error {
	fs := flag.NewFlagSet("create", flag.ExitOnError)
	ref := fs.String("template", "", "a saved template's id or name")
	id := fs.String("id", "", "the new resource's id (a Composable App's name)")
	name := fs.String("name", "", "the same as --id")
	file := fs.String("f", "", `a JSON file with the secrets: {"secretEnv": {"API_KEY": "…"}, "credentials": {…}, "services": {…}}`)
	var secrets, secretEnvs repeated
	fs.Var(&secrets, "secret", "a secret the template needs, PATH=VALUE (repeatable): API_KEY=…, imagePassword=…, credentials.password=…, portPasswords.http.alice=…, services.web.secretEnv.API_KEY=…")
	fs.Var(&secretEnvs, "secret-env", "the same, with the value read from an environment variable: PATH=VAR (repeatable)")
	_ = fs.Parse(args)
	if *id == "" {
		*id = *name
	}
	if *ref == "" || *id == "" {
		return fmt.Errorf("pass --template T and --id NEW")
	}
	t, err := findTemplate(*ref)
	if err != nil {
		return err
	}
	body := map[string]any{}
	if *file != "" {
		raw, err := os.ReadFile(*file)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			return fmt.Errorf("%s isn't a JSON object: %w", *file, err)
		}
		for k := range body {
			switch {
			case !templateBodyKeys[k]:
				return fmt.Errorf("%s: %q can't be given here — a create from a template takes only the secrets it needs "+
					"(secretEnv, imagePassword, gitToken, portPasswords, credentials, services); change settings after, with livellm set %s -f", *file, k, *id)
			case t.Kind == "stack" && k != "services":
				return fmt.Errorf("%s: %s is a Composable App's template: each service's secrets go under services.<name>.%s", *file, t.Name, k)
			case t.Kind != "stack" && k == "services":
				return fmt.Errorf("%s: services is for a Composable App's template; %s is a %s", *file, t.Name, t.Kind)
			}
		}
	}
	given := make([]string, 0, len(secrets)+len(secretEnvs))
	given = append(given, secrets...)
	for _, s := range secretEnvs {
		path, env, ok := strings.Cut(s, "=")
		if !ok || path == "" || env == "" {
			return fmt.Errorf("--secret-env takes PATH=VAR (the variable holding the value), got %q", s)
		}
		v := os.Getenv(env)
		if v == "" {
			return fmt.Errorf("--secret-env %s: the variable %s is empty or unset", path, env)
		}
		given = append(given, path+"="+v)
	}
	for _, s := range given {
		path, value, ok := strings.Cut(s, "=")
		if !ok || path == "" {
			return fmt.Errorf("--secret takes PATH=VALUE, got %q", strings.SplitN(s, "=", 2)[0])
		}
		if err := putSecret(body, t, path, value); err != nil {
			return err
		}
	}
	if t.Kind == "stack" {
		body["name"] = *id
	} else {
		body["id"] = *id
	}
	var out map[string]any
	if err := call("POST", "/v1/templates/"+url.PathEscape(t.ID)+"/create", body, &out); err != nil {
		var p *problem
		if asProblem(err, &p) && len(p.Missing) > 0 && p.Next == "" {
			p.Next = "add " + secretFlags(p.Missing)
		}
		return err
	}
	answer := map[string]any{"template": t.Name, "type": t.Kind, "created": out["created"]}
	if t.Kind != "stack" {
		answer["created"] = *id
	}
	if dbs, ok := out["databases"]; ok {
		answer["databases"] = dbs
	}
	return print(answer)
}

// putSecret puts one --secret into the create body. A path is what a refusal
// lists as missing (secretEnv.API_KEY, imagePassword, credentials.password,
// portPasswords.http.alice, services.web.secretEnv.API_KEY…), and a bare name
// is a secret env value. In a Composable App's template every secret belongs
// to a service: a path without services.<name> goes to each service that has
// that secret env name, or to the only service there is.
func putSecret(body map[string]any, t *template, path, value string) error {
	parts := strings.Split(path, ".")
	switch {
	case len(parts) == 1 && (path == "imagePassword" || path == "gitToken"):
	case len(parts) == 1 && templateBodyKeys[path]:
		return fmt.Errorf("--secret %s: say what in it, e.g. %s", path, map[string]string{
			"secretEnv": "secretEnv.API_KEY=…", "portPasswords": "portPasswords.http.alice=…",
			"credentials": "credentials.password=…", "services": "services.web.secretEnv.API_KEY=…"}[path])
	case len(parts) == 1:
		parts = []string{"secretEnv", path}
	}
	if t.Kind != "stack" {
		if parts[0] == "services" {
			return fmt.Errorf("--secret %s: %s isn't a Composable App's template; leave out services.<name>", path, t.Name)
		}
		return setPath(body, parts, value)
	}
	if parts[0] == "services" {
		return setPath(body, parts, value)
	}
	var svcs []string
	if parts[0] == "secretEnv" && len(parts) == 2 {
		if svcs = servicesWithSecret(t, parts[1]); len(svcs) == 0 {
			return fmt.Errorf("--secret %s: no service of the template %s has a secret %s (livellm template show %s)", path, t.Name, parts[1], t.ID)
		}
	} else if all := stackServices(t); len(all) == 1 {
		svcs = all
	} else {
		return fmt.Errorf("--secret %s: say which service it is for, services.<name>.%s (the template's services: %s)", path, path, strings.Join(all, ", "))
	}
	for _, svc := range svcs {
		if err := setPath(body, append([]string{"services", svc}, parts...), value); err != nil {
			return err
		}
	}
	return nil
}

// stackServices are the names of a stack template's services.
func stackServices(t *template) []string {
	st, _ := t.Config["stack"].(map[string]any)
	list, _ := st["services"].([]any)
	var out []string
	for _, raw := range list {
		sv, _ := raw.(map[string]any)
		if n, _ := sv["name"].(string); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// servicesWithSecret are the services of a stack template that have the secret env name.
func servicesWithSecret(t *template, name string) []string {
	st, _ := t.Config["stack"].(map[string]any)
	list, _ := st["services"].([]any)
	var out []string
	for _, raw := range list {
		sv, _ := raw.(map[string]any)
		pod, _ := sv["pod"].(map[string]any)
		env, _ := pod["secretEnv"].([]any)
		for _, e := range env {
			if m, _ := e.(map[string]any); m["name"] == name {
				if n, _ := sv["name"].(string); n != "" {
					out = append(out, n)
				}
			}
		}
	}
	return out
}

// setPath sets body[a][b]…= value, making the objects on the way.
func setPath(body map[string]any, parts []string, value string) error {
	m := body
	for i, k := range parts {
		if k == "" {
			return fmt.Errorf("--secret %s: an empty name in the path", strings.Join(parts, "."))
		}
		if i == len(parts)-1 {
			m[k] = value
			return nil
		}
		next, ok := m[k].(map[string]any)
		if !ok {
			if _, taken := m[k]; taken {
				return fmt.Errorf("--secret %s: %s already holds a value", strings.Join(parts, "."), strings.Join(parts[:i+1], "."))
			}
			next = map[string]any{}
			m[k] = next
		}
		m = next
	}
	return nil
}

// secretFlags spells the secrets a template still needs as the flags that give them.
func secretFlags(missing []string) string {
	flags := make([]string, 0, len(missing))
	for _, m := range missing {
		if name, ok := strings.CutPrefix(m, "secretEnv."); ok && !strings.Contains(name, ".") {
			m = name
		}
		flags = append(flags, "--secret "+m+"=…")
	}
	return strings.Join(flags, " ")
}

func cmdRemove(args []string) error {
	id, rest, err := needArg(args, "resource")
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("rm", flag.ExitOnError)
	yes := fs.Bool("y", false, "don't ask")
	withDBs := fs.Bool("with-databases", false, "an app: also delete the databases made with it that no other app uses")
	force := fs.Bool("force", false, "delete even though another app's settings name it (an app that links it or waits for it has to change first)")
	_ = fs.Parse(rest)
	question := fmt.Sprintf("Delete %s and its disk? This can't be undone.", id)
	if *withDBs {
		question = fmt.Sprintf("Delete %s and its disk, and the databases made with it that no other app uses, with their data? This can't be undone.", id)
	}
	if !*yes && !confirm(question) {
		return fmt.Errorf("nothing was deleted")
	}
	q := url.Values{}
	if *withDBs {
		q.Set("withDatabases", "true")
	}
	if *force {
		q.Set("force", "true")
	}
	path := "/v1/workloads/" + url.PathEscape(id)
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out map[string]any
	if err := call("DELETE", path, nil, &out); err != nil {
		return err
	}
	answer := map[string]any{"deleted": id}
	if dbs, ok := out["databases"]; ok {
		answer["databases"] = dbs
	}
	return print(answer)
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
	id, rest, err := needArg(args, "app")
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("build", flag.ExitOnError)
	wait := fs.Bool("wait", false, "wait until the new build is live, or has failed")
	timeout := fs.Duration("timeout", 30*time.Minute, "with --wait: give up after this long")
	_ = fs.Parse(rest)
	var out map[string]any
	if err := call("POST", "/v1/workloads/"+url.PathEscape(id)+"/build", map[string]any{}, &out); err != nil {
		return err
	}
	if out == nil {
		out = map[string]any{"building": id}
	}
	if !*wait {
		return print(out)
	}
	started, _ := out["buildId"].(string)
	return waitBuild(id, started, *timeout, buildPoll)
}

// buildPoll is how often build --wait asks; tests shorten it.
var buildPoll = 10 * time.Second

// buildProgress is where an app's newest build stands.
type buildProgress struct {
	Stage   string `json:"stage"`
	Message string `json:"message"`
	Done    bool   `json:"done"`
	Failed  bool   `json:"failed"`
	BuildID string `json:"buildId"`
	Commit  string `json:"commit"`
	Logs    []struct {
		Body string `json:"body"`
	} `json:"logs"`
}

// waitBuild follows the newest build until it is live or has failed. When
// the build's id is known, an older build's answer is never taken for it.
func waitBuild(id, buildID string, timeout, every time.Duration) error {
	deadline := time.Now().Add(timeout)
	last := ""
	for {
		var p buildProgress
		if err := call("GET", "/v1/workloads/"+url.PathEscape(id)+"/build-progress", nil, &p); err != nil {
			return err
		}
		ours := buildID == "" || p.BuildID == "" || p.BuildID == buildID
		if ours && p.Failed {
			tail := make([]string, 0, len(p.Logs))
			for _, l := range p.Logs {
				tail = append(tail, l.Body)
			}
			if len(tail) > 20 {
				tail = tail[len(tail)-20:]
			}
			fmt.Fprintln(os.Stderr, strings.Join(tail, "\n"))
			return &problem{Status: 422, Msg: "the build failed: " + p.Message, Next: "livellm builds " + id + " has its logs"}
		}
		if ours && p.Done {
			return print(map[string]any{"id": id, "live": true, "buildId": p.BuildID, "commit": p.Commit})
		}
		if ours && p.Stage == "none" {
			return fmt.Errorf("%s isn't built from a repository", id)
		}
		if line := p.Stage + ": " + p.Message; line != last {
			fmt.Fprintln(os.Stderr, line)
			last = line
		}
		if time.Now().After(deadline) {
			return &problem{Status: 409, Msg: fmt.Sprintf("the build isn't live after %s (%s)", timeout, p.Stage),
				Next: "livellm builds " + id + " shows where it is"}
		}
		time.Sleep(every)
	}
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
