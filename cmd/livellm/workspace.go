package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The workspace around the resources: templates, activity, monitoring, API
// keys, SSH keys, the plan, machines agents hold, and the files a resource
// hands out (a Remote Desktop file, a picture of its screen).

// --- templates -------------------------------------------------------------------

type template struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Kind        string         `json:"kind"`
	Config      map[string]any `json:"config"`
	CreatedAt   string         `json:"createdAt,omitempty"`
}

func listTemplates() ([]template, error) {
	var out struct {
		Templates []template `json:"templates"`
	}
	if err := call("GET", "/v1/templates", nil, &out); err != nil {
		return nil, err
	}
	return out.Templates, nil
}

// findTemplate takes a template's id or its name.
func findTemplate(ref string) (*template, error) {
	all, err := listTemplates()
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].ID == ref {
			return &all[i], nil
		}
	}
	var hits []*template
	for i := range all {
		if all[i].Name == ref {
			hits = append(hits, &all[i])
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return nil, fmt.Errorf("there is no template %q — livellm templates lists them", ref)
	}
	return nil, fmt.Errorf("%d templates are called %q: use the id (livellm templates)", len(hits), ref)
}

// kindBlock is where a resource type keeps its settings on the resource.
var kindBlock = map[string]string{
	"vm-ubuntu": "vm", "vm-ubuntu-desktop": "vm", "vm-windows": "vm",
	"pod": "pod", "browser": "browser", "desktop": "desktop", "controller": "controller", "storage": "storage",
}

// templateConfig is what a resource saves as a template: its settings, less
// what belongs to that one resource (logins, passwords, env values, a pull
// credential). The console keeps the same things out.
func templateConfig(w map[string]any) (kind string, config map[string]any, err error) {
	kind, _ = w["type"].(string)
	block := kindBlock[kind]
	if block == "" {
		return "", nil, fmt.Errorf("a %s can't be saved as a template", kind)
	}
	spec, _ := w[block].(map[string]any)
	clean := map[string]any{}
	for k, v := range spec {
		clean[k] = v
	}
	switch block {
	case "vm":
		delete(clean, "credentials")
	case "storage":
		delete(clean, "credentials")
		delete(clean, "restoreFrom") // a restore is this database's own history
	case "pod":
		delete(clean, "env")
		delete(clean, "secretEnv")
		delete(clean, "imageAuth")
		if src, ok := clean["source"].(map[string]any); ok {
			clean["source"] = map[string]any{"git": src["git"]}
		}
	}
	return kind, map[string]any{block: clean}, nil
}

func cmdTemplates(args []string) error {
	all, err := listTemplates()
	if err != nil {
		return err
	}
	if all == nil {
		all = []template{}
	}
	return print(map[string]any{"templates": all})
}

// cmdTemplate: save one (from a resource, or from a file), show one, delete one.
func cmdTemplate(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("template save NAME --from ID | template save NAME --kind TYPE -f FILE | template show T | template rm T")
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "save":
		name, rest, err := needArg(rest, "template name")
		if err != nil {
			return err
		}
		fs := flag.NewFlagSet("template save", flag.ExitOnError)
		from := fs.String("from", "", "save this resource's settings")
		kind := fs.String("kind", "", "with -f: the type the settings are for (vm-ubuntu, pod, browser…)")
		file := fs.String("f", "", "a JSON file with the settings, as a create takes them")
		desc := fs.String("description", "", "a line about what it is for")
		_ = fs.Parse(rest)
		body := map[string]any{"name": name}
		if *desc != "" {
			body["description"] = *desc
		}
		switch {
		case *from != "" && *file != "":
			return fmt.Errorf("pass --from or -f, not both")
		case *from != "":
			w, err := findWorkload(*from)
			if err != nil {
				return err
			}
			k, config, err := templateConfig(w)
			if err != nil {
				return err
			}
			body["kind"], body["config"] = k, config
		case *file != "":
			if kindBlock[*kind] == "" {
				return fmt.Errorf("with -f, say which type it is for: --kind vm-ubuntu, pod, browser, desktop, storage…")
			}
			raw, err := os.ReadFile(*file)
			if err != nil {
				return err
			}
			var settings map[string]any
			if err := json.Unmarshal(raw, &settings); err != nil {
				return fmt.Errorf("%s isn't a JSON object: %w", *file, err)
			}
			delete(settings, "id")
			body["kind"], body["config"] = *kind, map[string]any{kindBlock[*kind]: settings}
		default:
			return fmt.Errorf("save what? --from ID saves a resource's settings, or --kind TYPE -f FILE")
		}
		var out template
		if err := call("POST", "/v1/templates", body, &out); err != nil {
			return err
		}
		return print(out)
	case "show":
		ref, _, err := needArg(rest, "template")
		if err != nil {
			return err
		}
		t, err := findTemplate(ref)
		if err != nil {
			return err
		}
		return print(t)
	case "rm", "delete":
		ref, rest, err := needArg(rest, "template")
		if err != nil {
			return err
		}
		fs := flag.NewFlagSet("template rm", flag.ExitOnError)
		yes := fs.Bool("y", false, "don't ask")
		_ = fs.Parse(rest)
		t, err := findTemplate(ref)
		if err != nil {
			return err
		}
		if !*yes && !confirm(fmt.Sprintf("Delete the template %s?", t.Name)) {
			return fmt.Errorf("nothing was deleted")
		}
		if err := call("DELETE", "/v1/templates/"+url.PathEscape(t.ID), nil, nil); err != nil {
			return err
		}
		return print(map[string]any{"deleted": t.ID, "name": t.Name})
	}
	return fmt.Errorf("template %s? save, show or rm", verb)
}

// fromTemplate is the create body a template gives: its settings, the new
// id, and whatever the file adds (a login, env values) on top.
func fromTemplate(ref, id string, extra map[string]any) (string, map[string]any, error) {
	t, err := findTemplate(ref)
	if err != nil {
		return "", nil, err
	}
	block := kindBlock[t.Kind]
	settings, _ := t.Config[block].(map[string]any)
	if settings == nil {
		settings = map[string]any{}
	}
	body := map[string]any{}
	for k, v := range settings {
		body[k] = v
	}
	for k, v := range extra {
		body[k] = v
	}
	body["id"] = id
	return t.Kind, body, nil
}

// findWorkload is one resource as the workspace holds it.
func findWorkload(id string) (map[string]any, error) {
	var ws struct {
		Spec struct {
			Workloads []map[string]any `json:"workloads"`
		} `json:"spec"`
	}
	if err := call("GET", "/v1/workspace", nil, &ws); err != nil {
		return nil, err
	}
	for _, w := range ws.Spec.Workloads {
		if w["id"] == id {
			return w, nil
		}
	}
	return nil, fmt.Errorf("there is nothing called %q here — try livellm ls", id)
}

// --- activity and monitoring ---------------------------------------------------

func cmdActivity(args []string) error {
	fs := flag.NewFlagSet("activity", flag.ExitOnError)
	actor := fs.String("actor", "", "you (people and keys), platform, or all")
	object := fs.String("object", "", "one resource: its id, or TYPE:ID such as workload:web")
	limit := fs.Int("limit", 0, "how many, up to 200 (default 50)")
	before := fs.Int64("before", 0, "the next page: events older than this event id")
	_ = fs.Parse(args)
	q := url.Values{}
	if *actor != "" {
		q.Set("actor", *actor)
	}
	if o := *object; o != "" {
		if !strings.Contains(o, ":") {
			o = "workload:" + o
		}
		q.Set("object", o)
	}
	if *limit > 0 {
		q.Set("limit", strconv.Itoa(*limit))
	}
	if *before > 0 {
		q.Set("before", strconv.FormatInt(*before, 10))
	}
	path := "/v1/activity"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out map[string]any
	if err := call("GET", path, nil, &out); err != nil {
		return err
	}
	return print(out)
}

// cmdMonitoring is the workspace's monitoring: every resource up or down, its
// uptime and use, and the alerts. With a machine's id, that machine in
// detail: its system, disks, network and a history of its use.
func cmdMonitoring(args []string) error {
	fs := flag.NewFlagSet("monitoring", flag.ExitOnError)
	rng := fs.String("range", "", "with a machine's id: how far back its history goes: 15m, 1h (default), 6h, 24h or 7d")
	id := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		id, args = args[0], args[1:]
	}
	_ = fs.Parse(args)
	if id == "" {
		id = fs.Arg(0) // the id may follow the flags
	}
	var out map[string]any
	if id == "" {
		if err := call("GET", "/v1/monitoring", nil, &out); err != nil {
			return err
		}
		return print(out)
	}
	path := "/v1/workloads/" + url.PathEscape(id) + "/monitor"
	if *rng != "" {
		path += "?range=" + url.QueryEscape(*rng)
	}
	if err := call("GET", path, nil, &out); err != nil {
		return err
	}
	return print(out)
}

// --- API keys ------------------------------------------------------------------

// permissionList reads --permissions: names separated by commas; "none" is
// an empty list.
func permissionList(s string) []string {
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" && p != "none" {
			out = append(out, p)
		}
	}
	return out
}

func cmdAPIKeys(args []string) error {
	verb := "ls"
	if len(args) > 0 {
		verb, args = args[0], args[1:]
	}
	switch verb {
	case "ls", "list":
		var out map[string]any
		if err := call("GET", "/v1/keys", nil, &out); err != nil {
			return err
		}
		return print(out)
	case "create":
		name, rest, err := needArg(args, "key name")
		if err != nil {
			return err
		}
		fs := flag.NewFlagSet("api-keys create", flag.ExitOnError)
		perms := fs.String("permissions", "", "what it may do beyond its workspace, e.g. billing (a person gives these, in the console)")
		_ = fs.Parse(rest)
		body := map[string]any{"name": name}
		if *perms != "" {
			body["permissions"] = permissionList(*perms)
		}
		var out map[string]any
		if err := call("POST", "/v1/keys", body, &out); err != nil {
			return err
		}
		// The secret is shown only now.
		return print(out)
	case "set":
		id, rest, err := needArg(args, "key")
		if err != nil {
			return err
		}
		fs := flag.NewFlagSet("api-keys set", flag.ExitOnError)
		perms := fs.String("permissions", "", "the key's permissions, all of them: billing, or none")
		_ = fs.Parse(rest)
		if *perms == "" {
			return fmt.Errorf("pass --permissions billing, or --permissions none to take them all away")
		}
		var out map[string]any
		if err := call("PATCH", "/v1/keys/"+url.PathEscape(id), map[string]any{"permissions": permissionList(*perms)}, &out); err != nil {
			return err
		}
		return print(out)
	case "rm", "revoke", "delete":
		id, rest, err := needArg(args, "key")
		if err != nil {
			return err
		}
		fs := flag.NewFlagSet("api-keys rm", flag.ExitOnError)
		yes := fs.Bool("y", false, "don't ask")
		_ = fs.Parse(rest)
		if !*yes && !confirm(fmt.Sprintf("Revoke the key %s? Anything using it stops working.", id)) {
			return fmt.Errorf("nothing was revoked")
		}
		if err := call("DELETE", "/v1/keys/"+url.PathEscape(id), nil, nil); err != nil {
			return err
		}
		return print(map[string]any{"revoked": id})
	}
	return fmt.Errorf("api-keys %s? ls, create, set or rm", verb)
}

// --- SSH keys ------------------------------------------------------------------

func cmdKeys(args []string) error {
	if len(args) == 0 || args[0] == "ls" || args[0] == "list" {
		var out map[string]any
		if err := call("GET", "/v1/ssh-keys", nil, &out); err != nil {
			return err
		}
		return print(out)
	}
	if args[0] != "set" {
		return fmt.Errorf("keys %s? keys lists them; keys set -f FILE replaces them", args[0])
	}
	fs := flag.NewFlagSet("keys set", flag.ExitOnError)
	file := fs.String("f", "", `a JSON file: [{"name": "laptop", "key": "ssh-ed25519 …"}], or public key lines, one per line`)
	_ = fs.Parse(args[1:])
	if *file == "" {
		return fmt.Errorf("pass the keys with -f keys.json (or a file of .pub lines)")
	}
	raw, err := os.ReadFile(*file)
	if err != nil {
		return err
	}
	keys, err := readSSHKeys(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", *file, err)
	}
	var out map[string]any
	if err := call("PUT", "/v1/ssh-keys", map[string]any{"sshKeys": keys}, &out); err != nil {
		return err
	}
	return print(out)
}

// readSSHKeys takes the list the API keeps ([{name, key}] or {"sshKeys": [...]})
// or plain public key lines, as in an authorized_keys file.
func readSSHKeys(raw []byte) ([]map[string]any, error) {
	var list []map[string]any
	if json.Unmarshal(raw, &list) == nil {
		return list, nil
	}
	var wrapped struct {
		SSHKeys []map[string]any `json:"sshKeys"`
	}
	if json.Unmarshal(raw, &wrapped) == nil && wrapped.SSHKeys != nil {
		return wrapped.SSHKeys, nil
	}
	out := []map[string]any{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "{") || strings.HasPrefix(line, "[") {
			return nil, fmt.Errorf("isn't valid JSON")
		}
		out = append(out, map[string]any{"key": line})
	}
	return out, nil
}

// --- plan ------------------------------------------------------------------------

// cmdPlan shows the plan and what the workspace uses of it; `plan set` and
// `plan metered` change it, which a person can always do and a key only when
// a person gave it the billing permission.
func cmdPlan(args []string) error {
	if len(args) == 0 {
		var out map[string]any
		if err := call("GET", "/v1/billing", nil, &out); err != nil {
			return err
		}
		return print(out)
	}
	switch args[0] {
	case "catalog", "plans":
		var out map[string]any
		if err := call("GET", "/v1/subscriptions/catalog", nil, &out); err != nil {
			return err
		}
		return print(out)
	case "set":
		plan, _, err := needArg(args[1:], "plan")
		if err != nil {
			return fmt.Errorf("which plan? livellm plan catalog lists them")
		}
		var out map[string]any
		if err := call("PUT", "/v1/subscription", map[string]any{"subscription": plan}, &out); err != nil {
			return err
		}
		return print(out)
	case "metered":
		if len(args) < 2 || (args[1] != "on" && args[1] != "off") {
			return fmt.Errorf("plan metered on (go past the plan and pay for it) or off (refuse what doesn't fit)")
		}
		var out map[string]any
		if err := call("PUT", "/v1/billing-mode", map[string]any{"metered": args[1] == "on"}, &out); err != nil {
			return err
		}
		return print(out)
	}
	return fmt.Errorf("plan %s? plan, plan catalog, plan set PLAN or plan metered on|off", args[0])
}

func cmdReservations([]string) error {
	var out map[string]any
	if err := call("GET", "/v1/reservations", nil, &out); err != nil {
		return err
	}
	return print(out)
}

// --- files a resource hands out --------------------------------------------------

// saveFile writes what the API answered to path, or to name in the current
// folder, and says where it went.
func saveFile(raw []byte, path, name string) error {
	if path == "" {
		path = name
	}
	if path == "-" {
		_, err := os.Stdout.Write(raw)
		return err
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return err
	}
	abs, _ := filepath.Abs(path)
	return print(map[string]any{"saved": abs, "bytes": len(raw)})
}

// cmdRDP saves a Windows or Ubuntu desktop machine's Remote Desktop file. It
// opens the machine through the platform with no other sign-in until it
// runs out, so it is written for this user alone.
func cmdRDP(args []string) error {
	id, rest, err := needArg(args, "machine")
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("rdp", flag.ExitOnError)
	ttl := fs.String("ttl", "", "how long the file works, e.g. 8h (default a day, 30 days at most)")
	out := fs.String("o", "", "where to save it (default ID.rdp; - for stdout)")
	_ = fs.Parse(rest)
	path := "/v1/workloads/" + url.PathEscape(id) + "/rdp-file"
	if *ttl != "" {
		if _, err := time.ParseDuration(*ttl); err != nil {
			return fmt.Errorf("--ttl %q isn't a length of time such as 8h or 90m", *ttl)
		}
		path += "?ttl=" + url.QueryEscape(*ttl)
	}
	raw, _, err := send("GET", path, nil)
	if err != nil {
		return err
	}
	return saveFile(raw, *out, id+".rdp")
}

// cmdScreenshot saves a picture of a machine's, a browser's or a Desktop
// App's screen, as the console shows it on the resource's card.
func cmdScreenshot(args []string) error {
	id, rest, err := needArg(args, "resource")
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("screenshot", flag.ExitOnError)
	desktop := fs.Int("desktop", -1, "for a Desktop App: which desktop, from 0")
	width := fs.Int("width", 0, "its width in pixels, 120 to 960 (default 480)")
	out := fs.String("o", "", "where to save it (default ID.jpg; - for stdout)")
	_ = fs.Parse(rest)
	q := url.Values{}
	if *desktop >= 0 {
		q.Set("desktop", strconv.Itoa(*desktop))
	}
	if *width > 0 {
		q.Set("width", strconv.Itoa(*width))
	}
	path := "/v1/workloads/" + url.PathEscape(id) + "/thumbnail"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	raw, _, err := send("GET", path, nil)
	if err != nil {
		return err
	}
	return saveFile(raw, *out, id+".jpg")
}

// --- waiting, and a closer look ---------------------------------------------------

// waitPoll is how often wait asks; tests shorten it.
var waitPoll = 5 * time.Second

// cmdWait waits until a resource is ready, for a script that goes on to use
// it. A resource that fails, or doesn't come up in time, is an error.
func cmdWait(args []string) error {
	id, rest, err := needArg(args, "resource")
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("wait", flag.ExitOnError)
	timeout := fs.Duration("timeout", 15*time.Minute, "give up after this long (a Windows machine takes up to 35 minutes)")
	_ = fs.Parse(rest)
	deadline := time.Now().Add(*timeout)
	last := ""
	for {
		var live struct {
			Workloads []struct {
				ID      string `json:"id"`
				Phase   string `json:"phase"`
				Ready   bool   `json:"ready"`
				Message string `json:"message"`
			} `json:"workloads"`
		}
		if err := call("GET", "/v1/status", nil, &live); err != nil {
			return err
		}
		found := false
		for _, w := range live.Workloads {
			if w.ID != id {
				continue
			}
			found = true
			if w.Ready {
				return print(map[string]any{"id": id, "ready": true, "state": strings.ToLower(w.Phase)})
			}
			if w.Phase == "Failed" {
				return &problem{Status: 422, Msg: fmt.Sprintf("%s failed: %s", id, w.Message), Next: "livellm logs " + id}
			}
			// Stopped reads "not ready" for good, unless it was just started
			// (then it reads Stopped for a moment until it comes up).
			if w.Phase == "Stopped" {
				if spec, err := findWorkload(id); err == nil && spec["stopped"] == true {
					return &problem{Status: 409, Msg: id + " is stopped", Next: "livellm start " + id}
				}
			}
			if line := strings.TrimSpace(w.Phase + " " + w.Message); line != last && line != "" {
				fmt.Fprintln(os.Stderr, line)
				last = line
			}
		}
		if !found {
			if _, err := findWorkload(id); err != nil {
				return err
			}
		}
		if time.Now().After(deadline) {
			return &problem{Status: 409, Msg: fmt.Sprintf("%s isn't ready after %s (%s)", id, *timeout, last), Next: "livellm status " + id}
		}
		time.Sleep(waitPoll)
	}
}

// cmdDatabase is a database's instances as they are now: role, ready, use and disk.
func cmdDatabase(args []string) error {
	id, _, err := needArg(args, "database")
	if err != nil {
		return err
	}
	var out map[string]any
	if err := call("GET", "/v1/workloads/"+url.PathEscape(id)+"/database", nil, &out); err != nil {
		return err
	}
	return print(out)
}

// cmdInstall is where a new machine stands on its way to its first boot.
func cmdInstall(args []string) error {
	id, _, err := needArg(args, "machine")
	if err != nil {
		return err
	}
	var out map[string]any
	if err := call("GET", "/v1/workloads/"+url.PathEscape(id)+"/install-progress", nil, &out); err != nil {
		return err
	}
	return print(out)
}

// cmdAgents lists the agents signed in to the workspace; rm signs one out.
func cmdAgents(args []string) error {
	if len(args) == 0 || args[0] == "ls" || args[0] == "list" {
		var out map[string]any
		if err := call("GET", "/v1/agents", nil, &out); err != nil {
			return err
		}
		return print(out)
	}
	if args[0] != "rm" && args[0] != "sign-out" {
		return fmt.Errorf("agents %s? agents lists them; agents rm ID signs one out", args[0])
	}
	id, rest, err := needArg(args[1:], "agent")
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("agents rm", flag.ExitOnError)
	yes := fs.Bool("y", false, "don't ask")
	_ = fs.Parse(rest)
	if !*yes && !confirm(fmt.Sprintf("Sign out the agent %s? It stops on its next step.", id)) {
		return fmt.Errorf("nobody was signed out")
	}
	if err := call("DELETE", "/v1/agents/"+url.PathEscape(id), nil, nil); err != nil {
		return err
	}
	return print(map[string]any{"signedOut": id})
}

// cmdInvoices lists the monthly invoices, or shows one.
func cmdInvoices(args []string) error {
	path := "/v1/invoices"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		path += "/" + url.PathEscape(args[0])
	}
	var out map[string]any
	if err := call("GET", path, nil, &out); err != nil {
		return err
	}
	return print(out)
}
