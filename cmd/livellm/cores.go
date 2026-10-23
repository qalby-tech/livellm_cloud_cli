package main

import (
	"encoding/json"
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
// cpus), and must agree with the file's cpus when it has one (owner,
// 2026-10-22: nothing is made from two counts). Each other flag goes in
// place of the file's value, and only a flag given is sent: a machine saved
// without one keeps what it has. The speeds there are, and how many
// dedicated cores of each fit, come from GET /v1/machine-cores. A cluster
// whose servers all run at one speed never asks for one.

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
		cores:     fs.Int("cores", 0, "a machine: how many cores (sent as cpus; a file's cpus must agree)"),
		dedicated: fs.Bool("dedicated-cores", false, "a machine, an app, a Desktop App, a database, a browser or a Browser API: all its cores reserved for it on its server (=false: shared again)"),
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
// flat, a set's are the vm block), read from file ("" for a set without
// one). --cores must agree with the cpus the file says, if it says any.
// dedicated is whether the machine has dedicated cores before this (a set's
// machine as stored; false for a new one). A speed or mixed speeds goes with
// dedicated cores only, which the platform would otherwise drop without a
// word.
func withCores(kind string, body map[string]any, file string, c *coreFlags, dedicated bool) error {
	if err := coresOnMachines(kind, c); err != nil {
		return err
	}
	changes, _, err := c.changes()
	if err != nil || len(changes) == 0 {
		return err
	}
	if !isMachine(kind) {
		if err := noObjectStorageCores(c, body["engine"]); err != nil {
			return err
		}
		body["dedicatedCores"] = changes["dedicatedCores"]
		return nil
	}
	if n, ok := changes["cpus"].(int); ok {
		if err := coresAgree(file, body["cpus"], n); err != nil {
			return err
		}
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

// coresAgree refuses --cores n beside another count in file (had: the cpus
// it says, nil for none): which one is meant is the user's to say, so nothing
// is sent.
func coresAgree(file string, had any, n int) error {
	if had == nil {
		return nil
	}
	raw, _ := json.Marshal(had)
	says := `"cpus": ` + string(raw)
	if f, ok := had.(float64); ok {
		if f == float64(n) {
			return nil
		}
		// A count past any whole number of cores is said as the file says it.
		if f >= 0 && f <= math.MaxInt32 && f == math.Trunc(f) {
			says = coresWord(int(f))
		}
	}
	return fmt.Errorf("%s says %s and --cores says %d: change one of them", file, says, n)
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
// anything but a machine, before anything is sent: --dedicated-cores alone
// goes with an app, a Desktop App, a database, a browser and a Browser API
// too (sharedCoresBlock).
func coresOnMachines(kind string, c *coreFlags) error {
	_, first, err := c.changes()
	if err != nil {
		return err
	}
	if first == "" || isMachine(kind) {
		return nil
	}
	given := flagsGiven(c.fs)
	if sharedCoresBlock(kind) != "" {
		for _, n := range []string{"cores", "core-speed", "mixed-speeds"} {
			if given[n] {
				return fmt.Errorf("--%s goes with a machine: vm-ubuntu, vm-ubuntu-desktop or vm-windows (an app, a Desktop App, a database, a browser or a Browser API takes --dedicated-cores alone; its cpu is set in the file)", n)
			}
		}
		return nil
	}
	if kind == "apps" {
		return fmt.Errorf("%s goes with one resource: for an app of several services, set \"dedicatedCores\": true on each service in the file", first)
	}
	return fmt.Errorf("%s goes with a machine: vm-ubuntu, vm-ubuntu-desktop or vm-windows", first)
}

// sharedCoresBlock is the settings block that holds dedicatedCores on an
// app, a Desktop App, a database, a browser and a Browser API: shared cores
// (the default) reserve a quarter of the cpu, dedicated ones all of it. ""
// for any other kind (a machine's are its vm block's, with more settings).
func sharedCoresBlock(kind string) string {
	switch kind {
	case "pod", "desktop", "storage", "browser", "controller":
		return kind
	}
	return ""
}

// noObjectStorageCores refuses --dedicated-cores on an object storage
// (engine s3, as the file or the stored database says): it has none.
func noObjectStorageCores(c *coreFlags, engine any) error {
	if flagsGiven(c.fs)["dedicated-cores"] && engine == "s3" {
		return fmt.Errorf("an object storage has no dedicated cores: --dedicated-cores goes with Postgres and Redis")
	}
	return nil
}

// restartNote is the line a set of a machine's cores, dedicated cores, speed
// or mixed speeds prints on stderr: each takes effect at its next restart.
// The count too: a running machine keeps the cores it started with, and
// nothing restarts it on its own.
func restartNote(c *coreFlags, kind, id string) {
	if !isMachine(kind) {
		return
	}
	given := flagsGiven(c.fs)
	if given["cores"] || given["dedicated-cores"] || given["core-speed"] || given["mixed-speeds"] {
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
// unless the API named a next step: the cores that fit ("Use N cores", said
// as again says it), or the count kept at mixed speeds ("Continue with mixed
// speeds"). The cores that fit are offered only when there are at least
// floor of them (the fewest the machine runs on, coreFloor; asked only
// then): fewer would be refused. A server with no room for the machine
// (server_full) has nothing to run again, and neither has a refusal that
// names another machine than the one the command sent (machine; its flags
// would change this one), as the console's.
func coresNext(err error, machine string, floor func() int, again coresAgain) error {
	var p *problem
	if !asProblem(err, &p) || p.Next != "" || p.Cores == nil {
		return err
	}
	c := p.Cores
	if c.Workload != "" && machine != "" && c.Workload != machine {
		return err
	}
	mixed := "continue with mixed speeds: run it again with --mixed-speeds"
	if c.Cores > 0 {
		mixed = fmt.Sprintf("continue with mixed speeds (%s): run it again with --mixed-speeds", coresWord(c.Cores))
	}
	switch p.Code {
	case "core_speed_short", "core_speeds_mixed":
		p.Next = mixed
		if c.Fit > 0 && c.Fit >= floor() {
			p.Next = fmt.Sprintf("use %s: %s; or %s", coresWord(c.Fit), again.use(c.Fit), mixed)
		}
	case "core_speed_unknown":
		p.Next = "livellm cores lists the speeds there are; or run it again with --core-speed any (any one speed)"
	}
	return err
}

// coresAgain is where a command refused for its cores takes another count:
// the "cpus" its file says (file; block, the file's block that holds them:
// "" for a create's flat settings), as a create from a file always does
// (owner, 2026-10-22), with --cores again beside it when the command gave
// one (flag), which must agree; or, with no count in a file, --cores N.
type coresAgain struct {
	file, block string
	flag        bool
}

// use is how to run the command again with n cores.
func (a coresAgain) use(n int) string {
	if a.file == "" {
		return fmt.Sprintf("run it again with --cores %d", n)
	}
	in := a.file
	if a.block != "" {
		in = fmt.Sprintf("%s's %q block", a.file, a.block)
	}
	if a.flag {
		return fmt.Sprintf(`set "cpus": %d in %s and run it again with --cores %d`, n, in, n)
	}
	return fmt.Sprintf(`set "cpus": %d in %s and run it again`, n, in)
}

// coreFloor is the fewest cores a machine of type kind, with its settings vm
// (a create's flat settings, a set's vm block as stored), runs on, as the
// API holds them: a desktop machine and Windows 11 (the edition a vm-windows
// gets when it names none) 2; a server and Windows Server 1. A kind not
// known ("": the machine couldn't be read) gets the most any machine needs,
// so nothing under a floor is offered.
func coreFloor(kind string, vm map[string]any) int {
	edition, _ := vm["windowsEdition"].(string)
	if kind == "" || kind == "vm-ubuntu-desktop" || (kind == "vm-windows" && edition != "server") {
		return 2
	}
	return 1
}

// coresWord is a count of cores as the console words it: "1 core", "6 cores".
func coresWord(n int) string {
	if n == 1 {
		return "1 core"
	}
	return fmt.Sprintf("%d cores", n)
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
