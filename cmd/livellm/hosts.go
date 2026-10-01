package main

import (
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

// placementFlags turns --host or --region into a placement; neither is
// automatic (nil), both is refused.
func placementFlags(host, region string) (map[string]any, error) {
	host, region = strings.TrimSpace(host), strings.TrimSpace(region)
	switch {
	case host != "" && region != "":
		return nil, fmt.Errorf("--host or --region, not both: a host already sits in its region")
	case host != "":
		return map[string]any{"strategy": "host", "host": host}, nil
	case region != "":
		return map[string]any{"strategy": "region", "region": region}, nil
	}
	return nil, nil
}
