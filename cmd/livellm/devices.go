package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
)

// A machine's devices, time zone and port groups.
//
// Devices are the cluster's own (a GPU, a USB controller), passed through to
// a machine by id; livellm devices lists the ones this workspace's machines
// may be given and how many are free. --devices a,b on a create or a set
// names them (on a set '' takes them all off); left out, a machine keeps its
// own. They change at the machine's next restart.
//
// A machine's time zone is written into it when it is made (--timezone on
// create, sent as timeZone); after that it is the machine's own to change,
// so a set never takes one.
//
// Port groups are ports published together, in the file ("portGroups"): the
// platform publishes each port at its number plus the group's offset (or at
// the same number, for a mirrored group), on the machine's own address.
// connect lists where each one is reached.

// devicesFlag is a create's or a set's --devices.
type devicesFlag struct {
	fs  *flag.FlagSet
	val *string
}

func addDevicesFlag(fs *flag.FlagSet) *devicesFlag {
	return &devicesFlag{fs: fs, val: fs.String("devices", "", "a machine: the cluster's devices it has, by id, a,b (livellm devices lists them; a set's '' takes them all off)")}
}

func (d *devicesFlag) given() bool { return flagsGiven(d.fs)["devices"] }

// list is what --devices names, in its order: none for an empty value.
func (d *devicesFlag) list() ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, id := range strings.Split(*d.val, ",") {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if seen[id] {
			return nil, fmt.Errorf("--devices names %s twice", id)
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

// withDevices puts --devices into what a create (flat settings) or a set (the
// vm block) sends for a machine of type kind, read from file: the devices
// the file names, if any, must be the same ones (which are meant is the
// user's to say, so nothing is sent).
func withDevices(kind string, body map[string]any, file string, d *devicesFlag) error {
	if !d.given() {
		return nil
	}
	if !isMachine(kind) {
		return fmt.Errorf("--devices goes with a machine: vm-ubuntu, vm-ubuntu-desktop or vm-windows")
	}
	ids, err := d.list()
	if err != nil {
		return err
	}
	if had, ok := body["devices"]; ok && had != nil {
		if !sameDevices(had, ids) {
			return fmt.Errorf("%s says %s and --devices says %s: change one of them", file, devicesSaid(had), devicesWord(ids))
		}
	}
	body["devices"] = ids
	return nil
}

// sameDevices is whether a file's "devices" names the ids, in any order.
func sameDevices(had any, ids []string) bool {
	list, ok := had.([]any)
	if !ok || len(list) != len(ids) {
		return false
	}
	a := make([]string, 0, len(list))
	for _, x := range list {
		s, ok := x.(string)
		if !ok {
			return false
		}
		a = append(a, s)
	}
	b := append([]string(nil), ids...)
	sort.Strings(a)
	sort.Strings(b)
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// devicesSaid is a file's "devices" as a refusal says it.
func devicesSaid(had any) string {
	list, ok := had.([]any)
	if !ok {
		return fmt.Sprintf(`"devices": %v`, had)
	}
	ids := make([]string, 0, len(list))
	for _, x := range list {
		ids = append(ids, fmt.Sprint(x))
	}
	return devicesWord(ids)
}

func devicesWord(ids []string) string {
	if len(ids) == 0 {
		return "no devices"
	}
	return "devices " + strings.Join(ids, ", ")
}

// withMachineTimeZone puts --timezone into a machine's create as timeZone: a
// time zone the file says, if any, must be the same one.
func withMachineTimeZone(body map[string]any, file, zone string) error {
	zone = strings.TrimSpace(zone)
	if zone == "" {
		return nil
	}
	if had, ok := body["timeZone"]; ok && had != nil && had != zone {
		return fmt.Errorf("%s says the time zone %v and --timezone says %s: change one of them", file, had, zone)
	}
	body["timeZone"] = zone
	return nil
}

// setTimeZoneRefusal is a set's --timezone, refused before anything is sent:
// a machine's is written into it when it is made, and a browser's goes
// through browser locale.
func setTimeZoneRefusal(id, kind string) error {
	switch {
	case kind == "browser":
		return fmt.Errorf("a browser's time zone: livellm browser locale %s --timezone ZONE", id)
	case isMachine(kind):
		return fmt.Errorf("a machine's time zone is set when it is made (livellm create %s --timezone ZONE); change it on the machine itself", kind)
	}
	return fmt.Errorf("--timezone goes with create of a machine or a browser")
}

// restartNote is the line a set of what a machine takes at its next restart
// prints on stderr: its cores (the count, dedicated cores, their speed and
// mixed speeds) and its devices. A running machine keeps what it started
// with, and nothing restarts it on its own.
func restartNote(c *coreFlags, d *devicesFlag, kind, id string) {
	if !isMachine(kind) {
		return
	}
	given := flagsGiven(c.fs)
	cores := given["cores"] || given["dedicated-cores"] || given["core-speed"] || given["mixed-speeds"]
	devices := d != nil && d.given()
	what := ""
	switch {
	case cores && devices:
		what = "the cores and the devices change"
	case cores:
		what = "the cores change"
	case devices:
		what = "the devices change"
	default:
		return
	}
	fmt.Fprintf(os.Stderr, "%s at %s's next restart (livellm restart %s)\n", what, id, id)
}

// cmdDevices prints the cluster's devices a machine of this workspace may be
// given, as the API answers: what each is, how many the cluster has, how
// many are free, and this workspace's machines that have one.
func cmdDevices([]string) error {
	var out map[string]any
	if err := call("GET", "/v1/machine-devices", nil, &out); err != nil {
		var p *problem
		if asProblem(err, &p) && p.Status == 404 {
			return fmt.Errorf("this LiveLLM gives machines no devices yet (it has no list of devices)")
		}
		return err
	}
	if err := print(out); err != nil {
		return err
	}
	if list, _ := out["devices"].([]any); len(list) == 0 {
		fmt.Fprintln(os.Stderr, "no devices are offered to this workspace's machines")
	}
	return nil
}

// groupAddresses are where a machine's port groups are reached from
// outside: each port at its number plus its group's offset, on the host of
// the machine's own address (ssh, host:port; "" while it has none). A group
// whose ports the platform hasn't given yet (a mirrored one being made) is
// listed with none.
func groupAddresses(vm map[string]any, ssh string) []map[string]any {
	groups, _ := vm["portGroups"].([]any)
	if len(groups) == 0 {
		return nil
	}
	host := ssh
	if h, _, err := net.SplitHostPort(ssh); err == nil {
		host = h
	}
	out := []map[string]any{}
	for _, raw := range groups {
		g, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		off := intOf(g["offset"])
		ports := []map[string]any{}
		list, _ := g["ports"].([]any)
		for _, rp := range list {
			p, ok := rp.(map[string]any)
			if !ok {
				continue
			}
			n := intOf(p["port"])
			if n == 0 {
				continue
			}
			udp, _ := p["udp"].(bool)
			tcp, _ := p["tcp"].(bool)
			e := map[string]any{"port": n, "published": n + off, "tcp": tcp || !udp, "udp": udp}
			if host != "" {
				e["address"] = net.JoinHostPort(host, fmt.Sprint(n+off))
			}
			ports = append(ports, e)
		}
		entry := map[string]any{"name": g["name"], "offset": off, "ports": ports}
		if m, _ := g["mirrored"].(bool); m {
			entry["mirrored"] = true
		}
		if cidrs, _ := g["allowCIDRs"].([]any); len(cidrs) > 0 {
			entry["allowCIDRs"] = cidrs
		}
		out = append(out, entry)
	}
	return out
}

// intOf reads a JSON number as an int (0 for anything else).
func intOf(v any) int {
	if f, ok := v.(float64); ok {
		return int(f)
	}
	return 0
}

// The device refusals carry no code: they are known by the API's words.
const (
	deviceNotOffered = "isn't offered to this workspace"
	deviceInUse      = "is in use by another machine"
	deviceNoNode     = "no node of this cluster has one"
)

// machineNext gives a refusal of a machine's devices what to do next, unless
// the API named a next step: a device this workspace may not be given (no
// permission changes that), or one another machine has (livellm devices
// shows how many are free; the machine that has it keeps it).
func machineNext(err error) error {
	var p *problem
	if !asProblem(err, &p) || p.Next != "" {
		return err
	}
	switch {
	case p.Status == 403 && strings.Contains(p.Msg, deviceNotOffered):
		p.Next = "no permission changes that: tell the user, or leave the device out (livellm devices lists the ones offered here)"
	case p.Status == 409 && (strings.Contains(p.Msg, deviceInUse) || strings.Contains(p.Msg, deviceNoNode)):
		p.Next = "livellm devices shows how many are free; tell the user, or leave the device out (never take it off the machine that has it)"
	}
	return err
}
