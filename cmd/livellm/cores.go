package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
)

// A machine's cores: shared (the default), or dedicated, every core reserved
// for it on its server. Where the servers' cores run at different clock
// speeds, dedicated cores are all of one speed: --core-speed asks for one
// (left out, any one speed that has the cores free), and --mixed-speeds
// keeps the count when no one speed has it. --cores is how many (sent as
// cpus). Each flag goes in place of the file's value, and only a flag given
// is sent: a machine saved without one keeps what it has. The speeds there
// are, and how many dedicated cores of each fit, come from GET
// /v1/machine-cores. A cluster whose servers all run at one speed never asks
// for one.

// coreFlags are a create's or a set's cores flags.
type coreFlags struct {
	fs               *flag.FlagSet
	cores            *int
	dedicated, mixed *bool
	speed            *string
}

func addCoreFlags(fs *flag.FlagSet) *coreFlags {
	return &coreFlags{
		fs:        fs,
		cores:     fs.Int("cores", 0, "a machine: how many cores (sent as cpus, in place of the file's)"),
		dedicated: fs.Bool("dedicated-cores", false, "a machine: every core reserved for it on its server (=false: shared again)"),
		speed:     fs.String("core-speed", "", "a machine with dedicated cores: their clock, 5.7GHz or 5700 (MHz); any: any one speed"),
		mixed:     fs.Bool("mixed-speeds", false, "a machine with dedicated cores: keep the count, at mixed core speeds"),
	}
}

// coreFlagNames are the flags, in the order the help gives them.
var coreFlagNames = []string{"cores", "dedicated-cores", "core-speed", "mixed-speeds"}

// changes are the settings the flags given set, by the API's names (cpus,
// dedicatedCores, coreSpeed, mixedSpeeds); empty when none was given. first
// is the first flag given, for a refusal to name.
func (c *coreFlags) changes() (out map[string]any, first string, err error) {
	given := flagsGiven(c.fs)
	out = map[string]any{}
	for _, n := range coreFlagNames {
		if given[n] && first == "" {
			first = "--" + n
		}
	}
	if given["cores"] {
		if *c.cores < 1 {
			return nil, "", fmt.Errorf("--cores is a whole number of cores, 1 or more")
		}
		out["cpus"] = *c.cores
	}
	if given["dedicated-cores"] {
		out["dedicatedCores"] = *c.dedicated
	}
	if given["core-speed"] {
		mhz, err := coreSpeedFlag(*c.speed)
		if err != nil {
			return nil, "", err
		}
		out["coreSpeed"] = mhz
	}
	if given["mixed-speeds"] {
		out["mixedSpeeds"] = *c.mixed
	}
	return out, first, nil
}

// coreSpeedFlag reads --core-speed: a clock in MHz (5700, 5700MHz) or in GHz
// (5.7GHz, 5.7 GHz), sent as MHz; 0 or any is any one speed (it clears one
// asked for before).
func coreSpeedFlag(v string) (int, error) {
	s := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(v), " ", ""))
	bad := fmt.Errorf("--core-speed takes a clock in GHz or MHz, e.g. 5.7GHz or 5700, or any; got %q (livellm cores lists the speeds)", v)
	switch {
	case s == "any" || s == "0":
		return 0, nil
	case strings.HasSuffix(s, "ghz"):
		g, err := strconv.ParseFloat(strings.TrimSuffix(s, "ghz"), 64)
		if err != nil || g <= 0 || g > 100 {
			return 0, bad
		}
		return int(math.Round(g * 1000)), nil
	}
	m, err := strconv.Atoi(strings.TrimSuffix(s, "mhz"))
	// A bare number that small is a clock in GHz said without its unit.
	if err != nil || m < 100 {
		return 0, bad
	}
	return m, nil
}

// withCores puts the cores flags' settings into what a create or a set sends
// for a machine of type kind: into body itself (a create's settings are
// flat, a set's are the vm block). dedicated is whether the machine has
// dedicated cores before this (a set's machine as stored; false for a new
// one). A speed or mixed speeds goes with dedicated cores only, which the
// platform would otherwise drop without a word.
func withCores(kind string, body map[string]any, c *coreFlags, dedicated bool) error {
	if err := coresOnMachines(kind, c); err != nil {
		return err
	}
	changes, _, err := c.changes()
	if err != nil || len(changes) == 0 {
		return err
	}
	for k, v := range changes {
		body[k] = v
	}
	if d, ok := body["dedicatedCores"].(bool); ok {
		dedicated = d
	}
	if !dedicated {
		if s, _ := changes["coreSpeed"].(int); s > 0 {
			return fmt.Errorf("--core-speed goes with dedicated cores: add --dedicated-cores")
		}
		if m, _ := changes["mixedSpeeds"].(bool); m {
			return fmt.Errorf("--mixed-speeds goes with dedicated cores: add --dedicated-cores")
		}
	}
	return nil
}

// coresLeftovers refuses what is left after the flags once a cores flag was
// given: `--dedicated-cores false` turns them on and leaves "false", and
// every flag after it, unread.
func coresLeftovers(c *coreFlags) error {
	if c.fs.NArg() == 0 {
		return nil
	}
	given := flagsGiven(c.fs)
	for _, n := range coreFlagNames {
		if given[n] {
			return fmt.Errorf("%q is left over: a yes-or-no flag takes its value after =, e.g. --dedicated-cores=false or --mixed-speeds=false", c.fs.Arg(0))
		}
	}
	return nil
}

// givesUpMixed is a set's new ask for a speed on a machine kept on mixed
// speeds (stored: its vm block as stored): it gives them up, as the
// console's does, unless --mixed-speeds or the file says otherwise. Kept,
// its cores would go on running at any speed, whatever the clock asked.
func givesUpMixed(c *coreFlags, vm, stored map[string]any) {
	if _, said := vm["mixedSpeeds"]; said || !flagsGiven(c.fs)["core-speed"] {
		return
	}
	if kept, _ := stored["mixedSpeeds"].(bool); kept {
		vm["mixedSpeeds"] = false
	}
}

// coresOnMachines refuses the cores flags, and a value they can't take, on
// anything but a machine, before anything is sent.
func coresOnMachines(kind string, c *coreFlags) error {
	_, first, err := c.changes()
	if err != nil {
		return err
	}
	if first != "" && !isMachine(kind) {
		return fmt.Errorf("%s goes with a machine: vm-ubuntu, vm-ubuntu-desktop or vm-windows", first)
	}
	return nil
}

// restartNote is the line a set of a machine's dedicated cores, speed or
// mixed speeds prints on stderr: they take effect at its next restart.
func restartNote(c *coreFlags, id string) {
	given := flagsGiven(c.fs)
	if given["dedicated-cores"] || given["core-speed"] || given["mixed-speeds"] {
		fmt.Fprintf(os.Stderr, "the cores change at %s's next restart (livellm restart %s)\n", id, id)
	}
}

// The refusals of a machine's cores, by the API's code. Their words are the
// API's own; these carry the numbers too.
var coresCodes = map[string]bool{"core_speed_short": true, "core_speeds_mixed": true, "core_speed_unknown": true}

// coresRefusal is what a cores refusal carries beside its words: the
// machine, the cores it asked for, how many of one speed fit (of the clock
// asked, when one was) and that clock in MHz.
type coresRefusal struct {
	Workload string `json:"workload"`
	Cores    int    `json:"cores"`
	Fit      int    `json:"fit"`
	SpeedMHz int    `json:"speedMHz"`
}

// coresNext gives a cores refusal of a create or a set what to run again,
// unless the API named a next step: the cores that fit ("Use N cores"), or
// the count kept at mixed speeds ("Continue with mixed speeds"). A server
// with no room for the machine (server_full) has nothing to run again.
func coresNext(err error) error {
	var p *problem
	if !asProblem(err, &p) || p.Next != "" || p.Cores == nil {
		return err
	}
	c := p.Cores
	mixed := "continue with mixed speeds: run it again with --mixed-speeds"
	if c.Cores > 0 {
		mixed = fmt.Sprintf("continue with mixed speeds (%d cores): run it again with --mixed-speeds", c.Cores)
	}
	switch p.Code {
	case "core_speed_short", "core_speeds_mixed":
		p.Next = mixed
		if c.Fit > 0 {
			p.Next = fmt.Sprintf("use %d cores: run it again with --cores %d; or %s", c.Fit, c.Fit, mixed)
		}
	case "core_speed_unknown":
		p.Next = "livellm cores lists the speeds there are; or run it again with --core-speed any (any one speed)"
	}
	return err
}

// cmdCores prints the clock speeds a machine's dedicated cores may ask for
// and how many of each fit on one server, as the API answers. showSpeed
// false: the servers run at one speed, and there is none to choose.
func cmdCores([]string) error {
	var out map[string]any
	if err := call("GET", "/v1/machine-cores", nil, &out); err != nil {
		var p *problem
		if asProblem(err, &p) && p.Status == 404 {
			return fmt.Errorf("this LiveLLM has no dedicated cores yet (it has no list of core speeds)")
		}
		return err
	}
	if err := print(out); err != nil {
		return err
	}
	if show, ok := out["showSpeed"].(bool); ok && !show {
		fmt.Fprintln(os.Stderr, "the servers run their cores at one speed: leave --core-speed out")
	}
	return nil
}
