package main

import (
	"flag"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// A resource's alert settings: which alerts it gets (down, cpu, memory, disk,
// gpu) and at what level. Each kind is usual (the platform's level; on, for
// down and gpu), the resource's own level (cpu, memory and disk: a whole
// percent from 50 to 99) or off.

// alertKinds are the kinds in the order they are shown, with their names.
var alertKinds = []struct{ kind, label string }{
	{"down", "Down"}, {"cpu", "CPU"}, {"memory", "Memory"}, {"disk", "Disk"}, {"gpu", "GPU"},
}

// alertLevelled are the kinds that take a level of their own.
var alertLevelled = map[string]bool{"cpu": true, "memory": true, "disk": true}

func alertLabel(kind string) string {
	for _, k := range alertKinds {
		if k.kind == kind {
			return k.label
		}
	}
	return kind
}

func cmdAlerts(args []string) error {
	if len(args) > 0 && args[0] == "set" {
		return cmdAlertsSet(args[1:])
	}
	id, rest, err := needArg(args, "resource")
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return fmt.Errorf("livellm alerts %s takes nothing more; to change them: livellm alerts set %s --cpu usual|off|50-99 …", id, id)
	}
	var out map[string]any
	if err := call("GET", alertsPath(id), nil, &out); err != nil {
		return alertsErr(err)
	}
	return print(withAlertSummary(out))
}

func cmdAlertsSet(args []string) error {
	id, rest, err := needArg(args, "resource")
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("alerts set", flag.ExitOnError)
	fs.String("cpu", "", "usual (the platform's level), off, or a level of its own: a whole percent from 50 to 99")
	fs.String("memory", "", "usual, off, or a level from 50 to 99")
	fs.String("disk", "", "usual, off, or a level from 50 to 99")
	fs.String("down", "", "on (usual) or off")
	fs.String("gpu", "", "on (usual) or off")
	_ = fs.Parse(rest)
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected %q: the flags go after the resource's id", fs.Arg(0))
	}
	settings := map[string]any{}
	var bad error
	// Only the flags given are sent: a kind left out keeps what it has.
	fs.Visit(func(f *flag.Flag) {
		if bad != nil {
			return
		}
		s, err := alertSetting(f.Name, f.Value.String())
		if err != nil {
			bad = err
			return
		}
		settings[f.Name] = s
	})
	if bad != nil {
		return bad
	}
	if len(settings) == 0 {
		return fmt.Errorf("pass what to change: --cpu, --memory, --disk (usual, off or 50-99), --down or --gpu (on or off)")
	}
	var out map[string]any
	if err := call("PUT", alertsPath(id), map[string]any{"settings": settings}, &out); err != nil {
		return alertsErr(err)
	}
	return print(withAlertSummary(out))
}

func alertsPath(id string) string { return "/v1/workloads/" + url.PathEscape(id) + "/alert-settings" }

// alertSetting reads one flag's value. A number goes as the resource's own
// level whatever it is: the API judges the range and says which part it
// refused.
func alertSetting(kind, v string) (map[string]any, error) {
	v = strings.ToLower(strings.TrimSpace(v))
	switch v {
	case "usual":
		return map[string]any{"mode": "usual"}, nil
	case "off":
		return map[string]any{"mode": "off"}, nil
	case "on":
		if !alertLevelled[kind] {
			return map[string]any{"mode": "usual"}, nil
		}
	}
	if !alertLevelled[kind] {
		return nil, fmt.Errorf("--%s is on or off, not %q", kind, v)
	}
	n, err := strconv.Atoi(strings.TrimSuffix(v, "%"))
	if err != nil {
		return nil, fmt.Errorf("--%s is usual, off or a whole percent from 50 to 99, not %q", kind, v)
	}
	return map[string]any{"mode": "level", "level": n}, nil
}

// withAlertSummary adds "summary": one line a kind, in the order the API
// lists them ("CPU: usual (90%)", "Memory: at 97%", "Down: off").
func withAlertSummary(out map[string]any) map[string]any {
	if out == nil {
		return out
	}
	usual, _ := out["usual"].(map[string]any)
	settings, _ := out["settings"].(map[string]any)
	kinds, _ := out["kinds"].([]any)
	lines := []string{}
	for _, k := range kinds {
		kind, _ := k.(string)
		s, _ := settings[kind].(map[string]any)
		mode, _ := s["mode"].(string)
		label := alertLabel(kind)
		switch mode {
		case "off":
			lines = append(lines, label+": off")
		case "level":
			lines = append(lines, fmt.Sprintf("%s: at %s%%", label, num(s["level"])))
		default: // usual, and a kind with nothing set
			if !alertLevelled[kind] {
				lines = append(lines, label+": on")
			} else if u, ok := usual[kind]; ok {
				lines = append(lines, fmt.Sprintf("%s: usual (%s%%)", label, num(u)))
			} else {
				lines = append(lines, label+": usual")
			}
		}
	}
	out["summary"] = lines
	return out
}

func num(v any) string {
	if f, ok := v.(float64); ok {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return fmt.Sprint(v)
}

// alertsErr says the API's refusals plainly: a 404 from the router (an API
// without alert settings) is not "no such resource", and a refused value
// names the part (settings.cpu.level) and its flag. Everything else, an
// agent's 403 included, is passed on in the API's words.
func alertsErr(err error) error {
	var p *problem
	if !asProblem(err, &p) {
		return err
	}
	switch {
	case p.Status == 404 && strings.HasPrefix(p.Msg, "404 page not found"):
		return &problem{Status: 404, Msg: "Alert settings aren't available on this server.", Next: "they need LiveLLM API 0.101.5 or newer"}
	case p.Code == "alert_settings" && p.Field != "":
		where := p.Field
		if parts := strings.Split(p.Field, "."); len(parts) >= 2 && parts[0] == "settings" {
			where += " (--" + parts[1] + ")"
		}
		p.Msg = where + ": " + p.Msg
	}
	return err
}
