package main

import (
	"fmt"
	"strings"
)

// Database links. A database has no setting of its own: it is reached only
// by what links it — an app (pod.databases, or dependsOn, each with its whole
// Composable App), a machine (vm.databases) or a Desktop App
// (desktop.databases). A link that names no variables only lets the resource
// reach the database: nothing goes into its environment and nothing
// restarts. A machine or a Desktop App takes only such links.

const maxDatabaseLinks = 8

// linkBlock is the block of a resource that holds its database links: "pod"
// for an app, "vm" for a machine, "desktop" for a Desktop App; "" when the
// resource can't link one.
func linkBlock(kind string) string {
	switch {
	case kind == "pod":
		return "pod"
	case strings.HasPrefix(kind, "vm-"):
		return "vm"
	case kind == "desktop":
		return "desktop"
	}
	return ""
}

// linksOf are a resource's database links as its settings hold them.
func linksOf(w map[string]any) []map[string]any {
	b := linkBlock(fmt.Sprint(w["type"]))
	if b == "" {
		return nil
	}
	l, _ := block(w, b)["databases"].([]any)
	out := make([]map[string]any, 0, len(l))
	for _, x := range l {
		if m, ok := x.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func linkID(l map[string]any) string { s, _ := l["id"].(string); return s }

func hasVars(l map[string]any) bool {
	env, _ := l["env"].(map[string]any)
	return len(env) > 0
}

// databaseReachRefusal is the API's own refusal of a setting on a database.
func databaseReachRefusal(id string) error {
	return fmt.Errorf("reachableFrom: a database is reached only by what links it: add %s to the databases of the app, machine or Desktop App that uses it (livellm link APP %s)", id, id)
}

// machineLinkRefusal is the API's refusal of variables on a machine's or a
// Desktop App's link.
func machineLinkRefusal(at, kind, id string) error {
	what := "a machine"
	if kind == "desktop" {
		what = "a Desktop App"
	}
	return fmt.Errorf("%s (%s): %s gets no variables from a link: it only lets it reach the database", at, id, what)
}

// withMachineLinks puts --database into the body of a machine or a Desktop
// App, next to the links its settings file names. They link reach only: an
// env in the file is refused as the API would.
func withMachineLinks(kind string, body map[string]any, add []string) error {
	b := linkBlock(kind)
	if len(add) > 0 && (b == "" || b == "pod") {
		return fmt.Errorf("--database goes with create vm-… or desktop (an app's links are in its settings: \"databases\" in -f, or livellm link APP DB after)")
	}
	if b != "vm" && b != "desktop" {
		return nil
	}
	have, _ := body["databases"].([]any)
	seen := map[string]bool{}
	for j, x := range have {
		m, ok := x.(map[string]any)
		if !ok {
			return fmt.Errorf("databases[%d]: each is {\"id\": \"DATABASE\"}", j)
		}
		id := linkID(m)
		if _, ok := m["env"]; ok && hasVars(m) {
			return machineLinkRefusal(fmt.Sprintf("databases[%d]", j), kind, id)
		}
		seen[id] = true
	}
	for _, db := range add {
		for _, id := range strings.Split(db, ",") {
			id = strings.TrimSpace(id)
			switch {
			case id == "":
				continue
			case seen[id]:
				return fmt.Errorf("%s is linked twice", id)
			}
			seen[id] = true
			have = append(have, map[string]any{"id": id})
		}
	}
	if len(have) > maxDatabaseLinks {
		return fmt.Errorf("at most %d databases per resource", maxDatabaseLinks)
	}
	if len(add) > 0 {
		body["databases"] = have
	}
	return nil
}

// refuseDatabaseReach refuses a setting on a database in a create body; an
// empty one (nothing) is left out, as the API would drop it.
func refuseDatabaseReach(body map[string]any) error {
	v, ok := body["reachableFrom"]
	if !ok {
		return nil
	}
	if l, isList := v.([]any); v == nil || (isList && len(l) == 0) {
		delete(body, "reachableFrom")
		return nil
	}
	return databaseReachRefusal(fmt.Sprint(body["id"]))
}

// cmdLink links databases to an app, a machine or a Desktop App, or takes
// links out (--remove). A new link names no variables: it only lets the
// resource (an app with its whole Composable App) reach the database, and
// nothing restarts. It reads the resource first and sends its whole list.
func cmdLink(args []string) error {
	id, rest, err := needArg(args, "app, machine or Desktop App")
	if err != nil {
		return err
	}
	remove := false
	var dbs []string
	seen := map[string]bool{}
	for _, a := range rest {
		switch {
		case a == "--remove" || a == "-remove":
			remove = true
			continue
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("link takes databases and --remove, not %s", a)
		}
		for _, d := range strings.Split(a, ",") {
			d = strings.TrimSpace(d)
			switch {
			case d == "":
				continue
			case seen[d]:
				return fmt.Errorf("%s is named twice", d)
			}
			seen[d] = true
			dbs = append(dbs, d)
		}
	}
	if len(dbs) == 0 {
		return fmt.Errorf("which databases? livellm link %s DB... (--remove takes links out)", id)
	}
	ws, err := readWorkspace()
	if err != nil {
		return err
	}
	w := ws.find(id)
	if w == nil {
		return fmt.Errorf("there is nothing called %q here — try livellm ls", id)
	}
	kind, _ := w["type"].(string)
	b := linkBlock(kind)
	if b == "" {
		if kind == "storage" {
			return fmt.Errorf("%s is a database: link it from what uses it, livellm link APP %s", id, id)
		}
		return fmt.Errorf("an app, a machine or a Desktop App links a database; %s is a %s", id, kindName(kind))
	}
	for _, d := range dbs {
		dw := ws.find(d)
		switch {
		case dw == nil:
			return fmt.Errorf("there is no database %q here — try livellm ls", d)
		case dw["type"] != "storage":
			return fmt.Errorf("%s is not a database", d)
		}
	}
	current := linksOf(w)
	has := map[string]map[string]any{}
	for _, l := range current {
		has[linkID(l)] = l
	}
	var changed, already []string
	var notes []string
	next := make([]any, 0, len(current)+len(dbs))
	if remove {
		drop := map[string]bool{}
		for _, d := range dbs {
			l, ok := has[d]
			if !ok {
				already = append(already, d)
				continue
			}
			drop[d] = true
			changed = append(changed, d)
			if hasVars(l) {
				notes = append(notes, d+"'s variables leave "+id+": it restarts")
			}
		}
		for _, l := range current {
			if !drop[linkID(l)] {
				next = append(next, l)
			}
		}
	} else {
		for _, l := range current {
			next = append(next, l)
		}
		for _, d := range dbs {
			if _, ok := has[d]; ok {
				already = append(already, d)
				continue
			}
			changed = append(changed, d)
			next = append(next, map[string]any{"id": d})
		}
		if len(next) > maxDatabaseLinks {
			return fmt.Errorf("%s would link %d databases: at most %d", id, len(next), maxDatabaseLinks)
		}
	}
	out := map[string]any{"id": id}
	if len(changed) > 0 {
		if err := patchWorkload(id, map[string]any{b: map[string]any{"databases": next}}); err != nil {
			return err
		}
	}
	ids := make([]string, 0, len(next))
	for _, l := range next {
		ids = append(ids, linkID(l.(map[string]any)))
	}
	out["databases"] = ids
	switch {
	case len(changed) == 0 && remove:
		out["note"] = "nothing to take out: not linked: " + strings.Join(already, ", ")
		return print(out)
	case len(changed) == 0:
		out["note"] = "nothing to add: already linked"
		return print(out)
	case remove:
		out["removed"] = changed
		if len(already) > 0 {
			out["notLinked"] = already
		}
	default:
		out["linked"] = changed
		if len(already) > 0 {
			out["already"] = already
		}
		switch b {
		case "pod":
			notes = append(notes, "reach only: no variables, the app doesn't restart")
		case "vm":
			notes = append(notes, "reach only: nothing goes into the machine")
		default:
			notes = append(notes, "reach only: nothing goes into the Desktop App")
		}
	}
	if stack := stackOf(w); stack != "" {
		out["app"] = stack
		if !remove {
			notes = append(notes, "the whole Composable App "+stack+" reaches "+strings.Join(changed, ", "))
		}
	}
	if remove {
		// What still reaches a database after the link went: a wait, or
		// another service of the same Composable App that links it.
		p := block(w, "pod")
		for _, d := range changed {
			for _, dep := range strs(p["dependsOn"]) {
				if dep == d {
					notes = append(notes, id+" still waits for "+d+" (dependsOn), which reaches it too")
				}
			}
			if stack := stackOf(w); stack != "" {
				for _, x := range ws.Spec.Workloads {
					if xid, _ := x["id"].(string); xid != id && stackOf(x) == stack && linksTo(x, d) {
						notes = append(notes, "still reached: "+xid+" of "+stack+" links "+d)
						break
					}
				}
			}
		}
	}
	if len(notes) > 0 {
		out["note"] = strings.Join(notes, "; ")
	}
	return print(out)
}

// linksTo says whether a resource links the database d.
func linksTo(w map[string]any, d string) bool {
	for _, l := range linksOf(w) {
		if linkID(l) == d {
			return true
		}
	}
	return false
}

// kindName says a resource's type in words.
func kindName(kind string) string {
	switch kind {
	case "browser":
		return "browser"
	case browserAPIType:
		return "Browser API"
	}
	return kind
}
