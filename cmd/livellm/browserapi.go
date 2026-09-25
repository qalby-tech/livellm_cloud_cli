package main

import (
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"
)

// A Browser API is one address over several browsers (type "controller" on
// the platform). These are its API paths, in one place.
const browserAPIType = "controller"

func workloadPath(id string) string { return "/v1/workloads/" + url.PathEscape(id) }

func memberPath(api, browser string) string {
	return workloadPath(api) + "/browsers/" + url.PathEscape(browser)
}

// patchWorkload changes only the fields it sends (a JSON merge patch); the
// platform applies it to the resource as it is now.
func patchWorkload(id string, patch map[string]any) error {
	return call("PATCH", workloadPath(id), patch, nil)
}

func cmdBrowserAPI(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("browser-api what? create, show, add or remove")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "create":
		return browserAPICreate(rest)
	case "show":
		return browserAPIShow(rest)
	case "add":
		return browserAPIMember(rest, true)
	case "remove", "rm", "take-out":
		return browserAPIMember(rest, false)
	}
	return fmt.Errorf("browser-api has no %q: create, show, add or remove", sub)
}

// repeated collects a flag given several times.
type repeated []string

func (r *repeated) String() string     { return strings.Join(*r, ",") }
func (r *repeated) Set(v string) error { *r = append(*r, v); return nil }

// browserAPIBody is what `browser-api create` sends: the browsers it names,
// or every browser, and remote browsers as id=ws-address. auths maps a
// remote id to the environment variable holding its login header, so the
// header never appears on the command line or in the shell history.
func browserAPIBody(id, browsers string, all bool, remotes, auths []string) (map[string]any, error) {
	body := map[string]any{"id": id}
	var names []string
	for _, b := range strings.Split(browsers, ",") {
		if b = strings.TrimSpace(b); b != "" {
			names = append(names, b)
		}
	}
	if all && len(names) > 0 {
		return nil, fmt.Errorf("--all already means every browser in the workspace; leave out --browsers")
	}
	authVar := map[string]string{}
	for _, a := range auths {
		rid, env, ok := strings.Cut(a, "=")
		if !ok || rid == "" || env == "" {
			return nil, fmt.Errorf("--remote-auth takes name=ENV_VAR (the variable holding the header), got %q", a)
		}
		authVar[rid] = env
	}
	var ext []map[string]any
	for _, r := range remotes {
		rid, ws, ok := strings.Cut(r, "=")
		if !ok || rid == "" || !(strings.HasPrefix(ws, "ws://") || strings.HasPrefix(ws, "wss://")) {
			return nil, fmt.Errorf("--remote takes name=ws://address, got %q", r)
		}
		e := map[string]any{"id": rid, "wsUrl": ws}
		if env, ok := authVar[rid]; ok {
			v := strings.TrimSpace(os.Getenv(env))
			if v == "" {
				return nil, fmt.Errorf("--remote-auth %s: the variable %s is empty or unset", rid, env)
			}
			e["authHeader"] = v
			delete(authVar, rid)
		}
		ext = append(ext, e)
	}
	for rid := range authVar {
		return nil, fmt.Errorf("--remote-auth %s: no --remote %s=wss://… to go with it", rid, rid)
	}
	if !all && len(names) == 0 && len(ext) == 0 {
		return nil, fmt.Errorf("which browsers? --browsers a,b, --all for every browser in the workspace, or --remote name=wss://…")
	}
	body["autodiscover"] = all
	if len(names) > 0 {
		body["browsers"] = names
	}
	if len(ext) > 0 {
		body["externalBrowsers"] = ext
	}
	return body, nil
}

func browserAPICreate(args []string) error {
	id, rest, err := needArg(args, "Browser API (its name)")
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("browser-api create", flag.ExitOnError)
	browsers := fs.String("browsers", "", "the workspace browsers it drives, comma-separated")
	all := fs.Bool("all", false, "every browser in the workspace, including ones made later")
	var remotes repeated
	fs.Var(&remotes, "remote", "a browser running elsewhere: name=wss://address (repeatable)")
	var auths repeated
	fs.Var(&auths, "remote-auth", "a remote browser's login header, read from an environment variable: name=ENV_VAR (repeatable); \"Name: value\", or a bare value sent as Authorization; never shown again")
	_ = fs.Parse(rest)
	body, err := browserAPIBody(id, *browsers, *all, remotes, auths)
	if err != nil {
		return err
	}
	if err := call("POST", "/v1/workloads/"+browserAPIType, body, nil); err != nil {
		return err
	}
	return print(map[string]any{"created": id, "type": browserAPIType,
		"next": "livellm connect " + id + " gives its address and a key or token"})
}

// browserAPIShow is what the Browser API's page shows: which browsers it
// drives and how each is doing.
func browserAPIShow(args []string) error {
	id, _, err := needArg(args, "Browser API")
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
		}
	}
	if w == nil {
		return fmt.Errorf("there is nothing called %q here — try livellm ls --type %s", id, browserAPIType)
	}
	if w["type"] != browserAPIType {
		return fmt.Errorf("%q is a %v, not a Browser API", id, w["type"])
	}
	c, _ := w["controller"].(map[string]any)
	out := map[string]any{"id": id, "browsers": []any{}, "remoteBrowsers": []any{}}
	if all, _ := c["autodiscover"].(bool); all {
		out["drives"] = "every browser in the workspace"
	} else {
		out["drives"] = "only these"
	}
	if b, ok := c["browsers"].([]any); ok {
		out["browsers"] = b
	}
	if ext, ok := c["externalBrowsers"].([]any); ok {
		var names []any
		for _, e := range ext {
			if m, ok := e.(map[string]any); ok {
				names = append(names, m["id"])
			}
		}
		if names != nil {
			out["remoteBrowsers"] = names
		}
	}
	var live struct {
		Workloads []map[string]any `json:"workloads"`
	}
	if call("GET", "/v1/status", nil, &live) == nil {
		for _, l := range live.Workloads {
			if l["id"] == id {
				out["state"] = strings.ToLower(fmt.Sprint(l["phase"]))
				out["ready"] = l["ready"]
				if b, ok := l["browsers"]; ok {
					out["answering"] = b
				}
			}
		}
	}
	return print(out)
}

func browserAPIMember(args []string, add bool) error {
	api, rest, err := needArg(args, "Browser API")
	if err != nil {
		return err
	}
	browser, _, err := needArg(rest, "browser")
	if err != nil {
		return err
	}
	if add {
		if err := call("PUT", memberPath(api, browser), map[string]any{}, nil); err != nil {
			return err
		}
		return print(map[string]any{"added": browser, "to": api})
	}
	if err := call("DELETE", memberPath(api, browser), nil, nil); err != nil {
		return err
	}
	return print(map[string]any{"tookOut": browser, "of": api})
}
