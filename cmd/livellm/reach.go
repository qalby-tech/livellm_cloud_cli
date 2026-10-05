package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Inside the workspace: which other resources may connect to a resource.
// Every resource has one setting, reachableFrom: [] is nothing, ["*"] the
// whole workspace (also resources made later), or the ids it names (a
// Composable App's name, or any of its services, stands for the whole app).
// New resources start with nothing. Whatever the setting, a resource is
// reached by its own parts, the apps that link it or wait for it, and (a
// browser) the Browser API that drives it.
//
// Letting more in takes the Network permission for an API key or an agent,
// unless that key or agent made both resources; narrowing never does. The
// agent asks the person first.

const reachAll = "*"

// reachList reads a list of names: "a,b" names them, "*" is the whole
// workspace, "none" (or nothing at all) is an empty list.
func reachList(s string) ([]string, error) {
	out := []string{}
	if strings.TrimSpace(s) == "none" {
		return out, nil
	}
	seen := map[string]bool{}
	for _, n := range strings.Split(s, ",") {
		n = strings.TrimSpace(n)
		switch {
		case n == "":
			continue
		case n == "none":
			return nil, fmt.Errorf("none goes alone")
		case seen[n]:
			return nil, fmt.Errorf("%s is named twice", n)
		}
		seen[n] = true
		out = append(out, n)
	}
	if seen[reachAll] && len(out) > 1 {
		return nil, fmt.Errorf(`"*" (the whole workspace) goes alone`)
	}
	return out, nil
}

// reachFlag is --reachable-from on create: left out, nothing is sent (the
// resource starts closed); given, it is the list.
type reachFlag struct {
	set  bool
	list []string
}

func (r *reachFlag) String() string { return strings.Join(r.list, ",") }
func (r *reachFlag) Set(v string) error {
	l, err := reachList(v)
	if err != nil {
		return err
	}
	r.set, r.list = true, l
	return nil
}

func addReachFlag(fs *flag.FlagSet) *reachFlag {
	r := &reachFlag{}
	fs.Var(r, "reachable-from", `which other resources here may connect to it: ids, a Composable App's name, "*" for the whole workspace, or none (left out: none)`)
	return r
}

// withReach puts --reachable-from into a create body. A settings file that
// already says something else is refused rather than overridden.
func withReach(body map[string]any, r *reachFlag) error {
	if r == nil || !r.set {
		return nil
	}
	if had, ok := body["reachableFrom"]; ok && had != nil && !sameNames(had, r.list) {
		return fmt.Errorf("the settings say reachableFrom %v and --reachable-from says %s; pass one", had, describeReach(r.list))
	}
	body["reachableFrom"] = r.list
	return nil
}

func sameNames(v any, want []string) bool {
	l, ok := v.([]any)
	if !ok || len(l) != len(want) {
		return false
	}
	a := make([]string, 0, len(l))
	for _, x := range l {
		s, ok := x.(string)
		if !ok {
			return false
		}
		a = append(a, s)
	}
	b := append([]string(nil), want...)
	sort.Strings(a)
	sort.Strings(b)
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func describeReach(l []string) string {
	switch {
	case len(l) == 0:
		return "none"
	case len(l) == 1 && l[0] == reachAll:
		return `"*"`
	}
	return strings.Join(l, ",")
}

// reachMeans says a setting in words.
func reachMeans(l []string) string {
	switch {
	case len(l) == 0:
		return "nothing else in the workspace"
	case len(l) == 1 && l[0] == reachAll:
		return "the whole workspace"
	}
	return "only these"
}

// networkNext is what to do after a refusal for want of the Network
// permission: the person agrees first, and only a person turns it on.
func networkNext(err error) error {
	var p *problem
	if !asProblem(err, &p) || p.Next != "" || p.Code != "network_permission" {
		return err
	}
	where := "for this agent on the console's Agents page"
	if strings.TrimSpace(os.Getenv("LIVELLM_API_KEY")) != "" {
		where = "for this key on the console's Keys page"
	}
	p.Next = "letting resources reach each other needs the user's agreement: ask them first; only a person turns on Network " + where +
		" (no permission is needed for resources this key or agent made itself, nor to narrow)"
	return err
}

func cmdReach(args []string) error {
	id, rest, err := needArg(args, "resource")
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("reach", flag.ExitOnError)
	from := fs.String("from", "", `let these reach it: ids or Composable App names, comma-separated, or "*" for the whole workspace`)
	none := fs.Bool("none", false, "let nothing else in the workspace reach it")
	_ = fs.Parse(rest)
	given := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "from" {
			given = true
		}
	})
	if given && *none {
		return fmt.Errorf("pass --from or --none, not both")
	}
	if !given && !*none {
		return reachShow(id)
	}
	list := []string{}
	if given {
		if list, err = reachList(*from); err != nil {
			return err
		}
		if len(list) == 0 {
			return fmt.Errorf(`--from needs ids or "*"; --none lets nothing in`)
		}
	}
	ws, err := readWorkspace()
	if err != nil {
		return err
	}
	w := ws.find(id)
	if w == nil {
		return fmt.Errorf("there is nothing called %q here — try livellm ls", id)
	}
	if err := patchWorkload(id, map[string]any{"reachableFrom": list}); err != nil {
		return err
	}
	out := map[string]any{"id": id, "reachableFrom": list, "means": reachMeans(list)}
	if stack := stackOf(w); stack != "" {
		out["app"] = stack
		out["note"] = "the whole Composable App " + stack + " takes it"
	}
	return print(out)
}

// workspaceSpec is what reach reads: the workspace's name and its resources'
// settings, as the API keeps them.
type workspaceSpec struct {
	Name string `json:"name"`
	Spec struct {
		Workloads []map[string]any `json:"workloads"`
	} `json:"spec"`
}

func readWorkspace() (*workspaceSpec, error) {
	var ws workspaceSpec
	if err := call("GET", "/v1/workspace", nil, &ws); err != nil {
		return nil, err
	}
	return &ws, nil
}

func (ws *workspaceSpec) find(id string) map[string]any {
	for _, w := range ws.Spec.Workloads {
		if w["id"] == id {
			return w
		}
	}
	return nil
}

func block(w map[string]any, name string) map[string]any {
	m, _ := w[name].(map[string]any)
	return m
}

func stackOf(w map[string]any) string {
	if w["type"] != "pod" {
		return ""
	}
	s, _ := block(w, "pod")["stack"].(string)
	return s
}

func strs(v any) []string {
	l, _ := v.([]any)
	out := make([]string, 0, len(l))
	for _, x := range l {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// storedReach is a resource's setting. One the API has never written is
// reached from the whole workspace, as before.
func storedReach(w map[string]any) ([]string, bool) {
	v, ok := w["reachableFrom"]
	if !ok || v == nil {
		return []string{reachAll}, false
	}
	return strs(v), true
}

// reachShow says who may connect to a resource inside the workspace: its
// setting, what reaches it whatever the setting, and its inside addresses.
// It reads the workspace only (connect would hold a machine or hand out a
// token).
func reachShow(id string) error {
	ws, err := readWorkspace()
	if err != nil {
		return err
	}
	w := ws.find(id)
	if w == nil {
		return fmt.Errorf("there is nothing called %q here — try livellm ls", id)
	}
	setting, stored := storedReach(w)
	out := map[string]any{"id": id, "reachableFrom": setting, "means": reachMeans(setting)}
	if !stored {
		out["note"] = "not set yet: reached from the whole workspace, as before"
	}
	if stack := stackOf(w); stack != "" {
		out["app"] = stack
	}
	also := ws.alsoFrom(w)
	out["alsoFrom"] = also
	if len(setting) > 0 || len(also) > 0 {
		out["addresses"] = insideAddresses(ws.Name+"-"+id, w)
	}
	out["change"] = "livellm reach " + id + ` --from a,b | --from "*" | --none`
	return print(out)
}

// group is the ids that count as one resource with w: a Composable App's
// services, or w alone.
func (ws *workspaceSpec) group(w map[string]any) map[string]bool {
	id, _ := w["id"].(string)
	g := map[string]bool{id: true}
	if s := stackOf(w); s != "" {
		for _, x := range ws.Spec.Workloads {
			if stackOf(x) == s {
				xid, _ := x["id"].(string)
				g[xid] = true
			}
		}
	}
	return g
}

// alsoFrom are the resources that reach w whatever its setting: the other
// services of its Composable App, the apps that link it or wait for it (with
// their whole Composable App), and the Browser APIs that drive it (and,
// through them, whatever reaches them).
func (ws *workspaceSpec) alsoFrom(w map[string]any) []map[string]any {
	target := ws.group(w)
	id, _ := w["id"].(string)
	out := []map[string]any{}
	seen := map[string]bool{}
	add := func(e map[string]any) {
		k := fmt.Sprint(e["id"], "|", e["why"])
		if seen[k] {
			return
		}
		seen[k] = true
		out = append(out, e)
	}
	for _, x := range ws.Spec.Workloads {
		xid, _ := x["id"].(string)
		if target[xid] {
			if xid != id {
				add(map[string]any{"id": xid, "why": "same app"})
			}
			continue
		}
		switch x["type"] {
		case "pod":
			p := block(x, "pod")
			var why string
			if ds, ok := p["databases"].([]any); ok {
				for _, d := range ds {
					if m, ok := d.(map[string]any); ok {
						if did, _ := m["id"].(string); target[did] {
							why = "links it"
						}
					}
				}
			}
			if why == "" {
				for _, dep := range strs(p["dependsOn"]) {
					if target[dep] {
						why = "waits for it"
					}
				}
			}
			if why == "" {
				continue
			}
			e := map[string]any{"id": xid, "why": why}
			if s := stackOf(x); s != "" {
				e["app"] = s
			}
			add(e)
		case browserAPIType:
			if w["type"] != "browser" {
				continue
			}
			c := block(x, "controller")
			drives, _ := c["autodiscover"].(bool)
			for _, b := range strs(c["browsers"]) {
				if b == id {
					drives = true
				}
			}
			if !drives {
				continue
			}
			through, _ := storedReach(x)
			add(map[string]any{"id": xid, "why": "drives it", "through": through})
		}
	}
	return out
}

// insideAddresses are the names a resource answers on inside the workspace
// (res is <workspace>-<id>). `livellm connect` gives the API's own list.
func insideAddresses(res string, w map[string]any) []map[string]any {
	t, _ := w["type"].(string)
	hp := func(host string, port int) map[string]any { return map[string]any{"host": host, "port": port} }
	switch {
	case t == "pod":
		p := block(w, "pod")
		host, _ := p["hostname"].(string)
		out := []map[string]any{}
		ports, _ := p["ports"].([]any)
		for _, x := range ports {
			pm, _ := x.(map[string]any)
			n, _ := pm["port"].(float64)
			if n == 0 {
				continue
			}
			// Raw TCP/UDP ports are on <res>-raw; the others on <res>, and
			// for the services of its Composable App at its hostname.
			internal, _ := pm["internal"].(bool)
			udp, _ := pm["udp"].(bool)
			tcp, _ := pm["tcp"].(bool)
			raw := !internal && (udp || tcp)
			e := hp(res, int(n))
			if raw {
				e = hp(res+"-raw", int(n))
				if udp {
					e["protocol"] = "udp"
				}
			}
			if name, _ := pm["name"].(string); name != "" {
				e["name"] = name
			}
			if host != "" && !raw {
				e["inStack"] = fmt.Sprintf("%s:%d", host, int(n))
			}
			out = append(out, e)
		}
		return out
	case strings.HasPrefix(t, "vm-"):
		// The machine's ports by role, as the platform names them: SSH and
		// raw ports on <res>, HTTP ports on <res>-http, internal ones on
		// <res>-internal, Remote Desktop on <res>-rdp.
		ssh := hp(res, 22)
		ssh["name"] = "ssh"
		out := []map[string]any{ssh}
		ports, _ := block(w, "vm")["ports"].([]any)
		for _, x := range ports {
			pm, _ := x.(map[string]any)
			n, _ := pm["port"].(float64)
			if n == 0 {
				continue
			}
			host := res + "-http"
			internal, _ := pm["internal"].(bool)
			udp, _ := pm["udp"].(bool)
			tcp, _ := pm["tcp"].(bool)
			switch {
			case internal:
				host = res + "-internal"
			case udp || tcp:
				host = res
			}
			e := hp(host, int(n))
			if name, _ := pm["name"].(string); name != "" {
				e["name"] = name
			}
			if udp && !internal {
				e["protocol"] = "udp"
			}
			out = append(out, e)
		}
		if t == "vm-windows" || t == "vm-ubuntu-desktop" {
			rdp := hp(res+"-rdp", 3389)
			rdp["name"] = "rdp"
			out = append(out, rdp)
		}
		return out
	case t == "storage":
		if e, _ := block(w, "storage")["engine"].(string); e == "redis" {
			return []map[string]any{hp(res, 6379)}
		}
		return []map[string]any{hp(res+"-rw", 5432)}
	case t == "browser":
		return []map[string]any{hp(res, 9222), hp(res, 9000)}
	case t == browserAPIType:
		return []map[string]any{hp(res, 8000)}
	case t == "desktop":
		return []map[string]any{hp(res+"-0."+res, 5900)}
	}
	return []map[string]any{}
}

// insideHint is the line connect prints on stderr when the API's answer has
// an inside block: who may connect to the resource from the workspace. The
// block itself is on stdout, as the API wrote it.
func insideHint(out map[string]any) string {
	in, ok := out["inside"].(map[string]any)
	if !ok {
		return ""
	}
	line := "inside the workspace: reachable from " + reachMeans(strs(in["reachableFrom"]))
	if l := strs(in["reachableFrom"]); len(l) > 0 && l[0] != reachAll {
		line = "inside the workspace: reachable from " + strings.Join(l, ", ")
	}
	if also, ok := in["alsoFrom"].([]any); ok && len(also) > 0 {
		var parts []string
		for _, a := range also {
			if m, ok := a.(map[string]any); ok {
				parts = append(parts, fmt.Sprintf("%v (%v)", m["id"], m["why"]))
			}
		}
		line += "; also by " + strings.Join(parts, ", ")
	}
	return line
}
