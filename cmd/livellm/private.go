package main

import (
	"errors"
	"flag"
	"fmt"
	"strings"
)

// A machine's private address: a fixed address of its own on a second
// network card, in a network only the workspace's machines share. It is
// chosen when the machine is made (--private-address, else the next free
// one wherever the cluster offers the private network) or left out
// (--no-private-network), and fixed for the machine's life. Neither flag
// sends nothing: the API gives one wherever it offers the network, and an
// explicit "privateNetwork": true would be refused where it doesn't. The
// API judges the address (one IPv4 address, inside the range, not one of
// its first ten nor its last, not another machine's); its refusals are
// passed on in its words.

type privateFlags struct {
	fs      *flag.FlagSet
	address *string
	off     *bool
}

func addPrivateFlags(fs *flag.FlagSet) *privateFlags {
	return &privateFlags{
		fs:      fs,
		address: fs.String("private-address", "", "a machine: its private address, e.g. 172.16.0.12 (left out: the next free one); fixed once made"),
		off:     fs.Bool("no-private-network", false, "a machine: make it without a private address; fixed once made"),
	}
}

// given is whether either flag asks for something (--no-private-network=false
// asks for nothing).
func (p *privateFlags) given() bool {
	return flagsGiven(p.fs)["private-address"] || *p.off
}

// privateLeftovers refuses `--no-private-network false`: the false is left
// over as an argument, and the machine would be made without one.
func privateLeftovers(p *privateFlags) error {
	if p.fs.NArg() > 0 && flagsGiven(p.fs)["no-private-network"] {
		return fmt.Errorf("%q is left over: a yes-or-no flag takes its value after =, e.g. --no-private-network=false", p.fs.Arg(0))
	}
	return nil
}

const privateOnMachines = "--private-address and --no-private-network go with a machine: vm-ubuntu, vm-ubuntu-desktop or vm-windows"

// withPrivateNetwork puts the flags into a machine's create body, agreeing
// with what the file says.
func withPrivateNetwork(kind string, body map[string]any, file string, p *privateFlags) error {
	if !p.given() {
		return nil
	}
	if !isMachine(kind) {
		return errors.New(privateOnMachines)
	}
	addr := strings.TrimSpace(*p.address)
	if flagsGiven(p.fs)["private-address"] && addr == "" {
		return fmt.Errorf("--private-address takes one address, e.g. 172.16.0.12 (leave it out for the next free one)")
	}
	if addr != "" && *p.off {
		return fmt.Errorf("--private-address and --no-private-network don't go together: a machine without a private network has no private address")
	}
	had, _ := body["privateAddress"].(string)
	on, said := body["privateNetwork"].(bool)
	switch {
	case addr != "" && had != "" && had != addr:
		return fmt.Errorf("%s says the private address %s and --private-address says %s: change one of them", file, had, addr)
	case addr != "" && said && !on:
		return fmt.Errorf("%s says \"privateNetwork\": false and --private-address says %s: change one of them", file, addr)
	case *p.off && had != "":
		return fmt.Errorf("%s says the private address %s and --no-private-network says none: change one of them", file, had)
	case *p.off && said && on:
		return fmt.Errorf("%s says \"privateNetwork\": true and --no-private-network says none: change one of them", file)
	}
	if addr != "" {
		body["privateAddress"] = addr
		return nil
	}
	body["privateNetwork"] = false
	return nil
}

// setPrivateRefusal is a set's --private-address or --no-private-network,
// refused before anything is sent: both are chosen when the machine is made.
func setPrivateRefusal(kind string) error {
	if !isMachine(kind) {
		return errors.New(privateOnMachines + ", when it is made")
	}
	return fmt.Errorf("a machine's private address is chosen when it is made (livellm create %s -f FILE --private-address 172.16.0.12, or --no-private-network for none) and fixed afterwards", kind)
}

// privateNexts are what to do after a refusal of a machine's private
// address, by the API's code, where there is something to run (the API's
// words already name the range, the holder, or to try again in a minute).
// A taken address stays its machine's: never delete one to free it.
var privateNexts = map[string]string{
	"private_network_not_offered": "leave --private-address (and the file's \"privateAddress\") out: the machine is made without one",
	"private_address_outside":     "leave --private-address out for the next free one",
	"private_address_reserved":    "leave --private-address out for the next free one",
	"private_address_taken":       "pick another (livellm ls shows the machines' private addresses), or leave --private-address out for the next free one",
	"private_address_fixed":       "leave \"privateAddress\" and \"privateNetwork\" out of the file",
	"private_range_full":          "tell the user, or make the machine with --no-private-network",
}

func privateNext(err error) error {
	var p *problem
	if !asProblem(err, &p) || p.Next != "" {
		return err
	}
	if next, ok := privateNexts[p.Code]; ok {
		p.Next = next
	}
	return err
}

// privateAddressOf is a machine's private address as the workspace read
// answers it ("" without one).
func privateAddressOf(w map[string]any) string {
	a, _ := block(w, "vm")["privateAddress"].(string)
	return a
}
