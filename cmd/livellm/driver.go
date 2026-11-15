package main

import (
	"fmt"
	"net/url"
	"strings"
)

// A machine's NVIDIA driver. Every machine made on LiveLLM API 0.101.9 or
// newer carries the program that installs it when the machine has an NVIDIA
// card; `driver ID` reads where it is, `driver install ID` puts the program
// on a machine made before it (or tries a failed install again) and starts
// it. The install runs in the machine's background: the API answers at once
// and the platform never restarts the machine for it.

func cmdDriver(args []string) error {
	if len(args) > 0 && args[0] == "install" {
		return cmdDriverInstall(args[1:])
	}
	id, rest, err := needArg(args, "machine")
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return fmt.Errorf("livellm driver %s takes nothing more; to install it: livellm driver install %s", id, id)
	}
	var out map[string]any
	if err := call("GET", driverPath(id), nil, &out); err != nil {
		return driverErr(err)
	}
	return print(withDriverSummary(id, out))
}

func cmdDriverInstall(args []string) error {
	id, rest, err := needArg(args, "machine")
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return fmt.Errorf("livellm driver install %s takes nothing more", id)
	}
	var out map[string]any
	if err := call("POST", driverPath(id), nil, &out); err != nil {
		return driverErr(err)
	}
	out = withDriverSummary(id, out)
	out["next"] = "livellm driver " + id + " shows how it goes (an install takes up to 20 minutes)"
	return print(out)
}

func driverPath(id string) string { return "/v1/workloads/" + url.PathEscape(id) + "/gpu-driver" }

// driverWords say each state plainly; what to do next goes with the states
// that need something.
var driverWords = map[string]struct{ say, next string }{
	"none":           {"No NVIDIA card in this machine.", ""},
	"off":            {"Turned off inside the machine.", ""},
	"working":        {"The NVIDIA driver works.", ""},
	"restart-needed": {"The NVIDIA driver is installed. Restart the machine to finish.", "livellm restart %s"},
	"installing":     {"Installing the NVIDIA driver. This can take up to 20 minutes.", ""},
	"pending":        {"The NVIDIA driver program hasn't run yet.", ""},
	"failed":         {"The NVIDIA driver didn't install.", "livellm driver install %s tries again"},
	"unsupported":    {"This machine's system gets no automatic NVIDIA driver.", ""},
	"not-installed":  {"The NVIDIA driver program isn't on this machine.", "livellm driver install %s"},
}

// withDriverSummary adds "summary" (the state in words, with its detail)
// and, where something is to be done, "next".
func withDriverSummary(id string, out map[string]any) map[string]any {
	if out == nil {
		out = map[string]any{}
	}
	state, _ := out["state"].(string)
	detail, _ := out["detail"].(string)
	w, ok := driverWords[state]
	say := w.say
	if !ok {
		say = "The NVIDIA driver's state isn't known right now."
	}
	if detail != "" {
		say = strings.TrimSuffix(say, ".") + ": " + detail
	}
	out["summary"] = say
	if w.next != "" {
		out["next"] = fmt.Sprintf(w.next, id)
	}
	return out
}

// driverErr says the API's refusals plainly: a 404 from the router (an API
// without the driver routes) is not "no such machine". Everything else, an
// agent's 403 and a stopped machine's 409 included, is passed on in the
// API's words.
func driverErr(err error) error {
	var p *problem
	if !asProblem(err, &p) {
		return err
	}
	if p.Status == 404 && strings.HasPrefix(p.Msg, "404 page not found") {
		return &problem{Status: 404, Msg: "The NVIDIA driver isn't available on this server.", Next: "it needs LiveLLM API 0.101.9 or newer"}
	}
	return err
}
