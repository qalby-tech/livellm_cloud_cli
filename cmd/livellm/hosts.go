package main

import (
	"flag"
	"fmt"
	"strings"
)

// Where resources run. Every resource is automatic unless its settings carry
// a placement: {"strategy": "region", "region": R} for any host in a region,
// or {"strategy": "host", "host": H} to pin one host. The hosts, their
// regions and their free room come from GET /v1/fleet/hosts.

// cmdHosts prints the hosts resources can run on, as the API answers.
func cmdHosts([]string) error {
	var out map[string]any
	if err := call("GET", "/v1/fleet/hosts", nil, &out); err != nil {
		return err
	}
	return print(out)
}

// placementFlags turns the parsed --host or --region of fs into a placement;
// neither is automatic (nil), both is refused, and one given with no value is
// refused rather than quietly meaning automatic.
func placementFlags(fs *flag.FlagSet) (map[string]any, error) {
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	host := strings.TrimSpace(fs.Lookup("host").Value.String())
	region := strings.TrimSpace(fs.Lookup("region").Value.String())
	switch {
	case given["host"] && given["region"]:
		return nil, fmt.Errorf("--host or --region, not both: a host already sits in its region")
	case given["host"] && host == "":
		return nil, fmt.Errorf("--host needs a host id (livellm hosts lists them); leave it out for automatic")
	case given["region"] && region == "":
		return nil, fmt.Errorf("--region needs a region (livellm hosts lists them); leave it out for automatic")
	case host != "":
		return map[string]any{"strategy": "host", "host": host}, nil
	case region != "":
		return map[string]any{"strategy": "region", "region": region}, nil
	}
	return nil, nil
}
