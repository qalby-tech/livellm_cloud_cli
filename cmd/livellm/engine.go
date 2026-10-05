package main

import (
	"fmt"
	"os"
	"strings"
)

// A browser's engine: Chrome (the default, driven over CDP) or Camoufox
// (Firefox-based, driven with Playwright). It is chosen when the browser or
// the Browser API is made and can't change afterwards. Nothing about a
// Chrome browser changes: no engine is sent unless it is camoufox, and the
// engine is shown only when it is camoufox.

const camoufox = "camoufox"

// engineFlag reads --engine: "" when left out or chrome (nothing is sent),
// "camoufox" for Camoufox.
func engineFlag(v string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "":
		return "", nil
	case "chrome":
		return "chrome", nil
	case camoufox:
		return camoufox, nil
	}
	return "", fmt.Errorf("--engine takes chrome or camoufox, got %q (livellm browser engines lists what this platform offers)", v)
}

// withEngine puts the --engine choice into a create body: only camoufox is
// sent. A settings file that already names another engine is refused rather
// than overridden.
func withEngine(body map[string]any, engine string) error {
	if engine == "" {
		return nil
	}
	if had, ok := body["engine"]; ok && had != nil {
		if s, _ := had.(string); !strings.EqualFold(s, engine) {
			return fmt.Errorf("the settings say engine %v and --engine says %s; pass one", had, engine)
		}
	}
	if engine == camoufox {
		body["engine"] = camoufox
	}
	return nil
}

// specEngine is the engine a workload's settings hold, "" for Chrome.
func specEngine(w map[string]any) string {
	for _, block := range []string{"browser", "controller"} {
		if b, ok := w[block].(map[string]any); ok {
			if e, _ := b["engine"].(string); e == camoufox {
				return camoufox
			}
		}
	}
	return ""
}

// markEngines adds "engine": "camoufox" to the live entries of Camoufox
// browsers and Browser APIs, read from the workspace's settings. A Chrome
// entry is left as it is, and nothing is read when no entry is a browser or
// a Browser API.
func markEngines(entries []map[string]any) {
	need := false
	for _, e := range entries {
		if t := e["type"]; (t == "browser" || t == browserAPIType) && e["engine"] == nil {
			need = true
		}
	}
	if !need {
		return
	}
	var ws struct {
		Spec struct {
			Workloads []map[string]any `json:"workloads"`
		} `json:"spec"`
	}
	if call("GET", "/v1/workspace", nil, &ws) != nil {
		return
	}
	camoufoxIDs := map[any]bool{}
	for _, w := range ws.Spec.Workloads {
		if specEngine(w) == camoufox {
			camoufoxIDs[w["id"]] = true
		}
	}
	for _, e := range entries {
		if e["engine"] == nil && camoufoxIDs[e["id"]] {
			e["engine"] = camoufox
		}
	}
}

// browserEngines lists the engines this platform offers; it needs no sign-in.
func browserEngines([]string) error {
	var out map[string]any
	if err := callPublic("/v1/browsers/engines", &out); err != nil {
		var p *problem
		if asProblem(err, &p) && p.Status == 404 {
			return fmt.Errorf("this platform offers Chrome browsers only (it has no engine list yet)")
		}
		return err
	}
	return print(out)
}

// connectHint is the one line a Camoufox browser's connect answer gets on
// stderr, so the JSON on stdout stays the API's answer.
func connectHint(out map[string]any) string {
	pw, ok := out["playwright"].(map[string]any)
	if !ok {
		return ""
	}
	v, _ := pw["version"].(string)
	if v == "" {
		v = "1.62"
	}
	return "Playwright " + v + ": firefox.connect(playwright.url, headers=playwright.headers)"
}

func printConnectHint(out map[string]any) {
	if h := connectHint(out); h != "" {
		fmt.Fprintln(os.Stderr, h)
	}
}

// engineNexts are what to do after an engine refusal, by the API's code.
var engineNexts = map[string]string{
	"engine_fixed":           "make a new browser with the other --engine and bring the cookies over: livellm browser cookies import NEW cookies.json",
	"engine_unavailable":     "leave out --engine for a Chrome browser; livellm browser engines lists what this platform offers",
	"extensions_unsupported": "leave \"extensions\" out of a Camoufox browser's settings",
	"engine_mismatch":        "a Browser API drives browsers of its own engine only (livellm ls shows engine camoufox), and remote browsers go only in a Chrome one",
	"profile_engine":         "profiles move only between browsers of one engine; import its cookies instead: livellm browser cookies import ID cookies.json",
}

// engineCodeOf is the engine code of a refusal: the API's code, or its
// wording when an answer came without one.
func engineCodeOf(p *problem) string {
	if _, ok := engineNexts[p.Code]; ok {
		return p.Code
	}
	if p.Code != "" {
		return ""
	}
	m := strings.ToLower(p.Msg)
	switch {
	case strings.Contains(m, "engine can't change"):
		return "engine_fixed"
	case strings.Contains(m, "doesn't offer camoufox"):
		return "engine_unavailable"
	case strings.Contains(m, "take no extensions"):
		return "extensions_unsupported"
	case strings.Contains(m, "drives camoufox browsers"), strings.Contains(m, "drives chrome browsers"),
		strings.Contains(m, "remote browsers go only in a chrome"):
		return "engine_mismatch"
	case strings.Contains(m, "profiles move only between browsers of one engine"):
		return "profile_engine"
	}
	return ""
}

// engineNext gives an engine refusal its next step, unless the API named
// one. Any other error comes back as it was.
func engineNext(err error) error {
	var p *problem
	if !asProblem(err, &p) || p.Next != "" {
		return err
	}
	code := engineCodeOf(p)
	if code == "" {
		return err
	}
	next := engineNexts[code]
	if code == "engine_mismatch" && strings.Contains(strings.ToLower(p.Msg), "profile") {
		next = engineNexts["profile_engine"]
	}
	p.Next = next
	return err
}
