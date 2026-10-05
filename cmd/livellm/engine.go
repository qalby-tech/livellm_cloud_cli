package main

import (
	"fmt"
	"os"
	"strings"
)

// A browser's engine: Chrome (the default, driven over CDP) or Camoufox
// (Firefox-based, driven with Playwright). It is chosen when the browser is
// made and can't change afterwards. Nothing about a Chrome browser changes:
// no engine is sent unless it is camoufox, and the engine is shown only when
// it is camoufox. A Browser API has no engine: one holds browsers of both,
// and its start_session takes an engine to pick one.

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

// cookiesOver is how a browser's cookies reach a browser of the other
// engine: there is no cookies export, so they are saved with Playwright from
// the old browser's contexts[0] and added to the new one.
const cookiesOver = "save the old browser's cookies with Playwright (livellm connect OLD, then contexts[0].storage_state(path=\"cookies.json\")) and add them: livellm browser cookies import NEW cookies.json"

// engineNexts are what to do after an engine refusal, by the API's code.
var engineNexts = map[string]string{
	"engine_fixed":           "make a new browser with the other --engine; to bring its sign-ins over, " + cookiesOver,
	"engine_unavailable":     "leave out --engine for a Chrome browser; livellm browser engines lists what this platform offers",
	"extensions_unsupported": "leave \"extensions\" out of a Camoufox browser's settings",
	"engine_mismatch":        cookiesOver,
	"profile_engine":         cookiesOver,
}

// engineNext gives an engine refusal its next step, unless the API named
// one. Only a refusal carrying one of the engine codes gets one; any other
// error (a database's engine refusal among them) comes back as it was.
func engineNext(err error) error {
	var p *problem
	if !asProblem(err, &p) || p.Next != "" {
		return err
	}
	next, ok := engineNexts[p.Code]
	if !ok {
		return err
	}
	p.Next = next
	return err
}
