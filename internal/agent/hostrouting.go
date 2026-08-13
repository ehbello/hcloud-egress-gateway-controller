/*
Copyright 2026 Maarlab Rethinking.
Licensed under the Apache License, Version 2.0 (the "License").
*/

package agent

import (
	"fmt"
	"os/exec"

	"github.com/vishvananda/netlink"
)

// defaultRouteLink returns the name of the interface carrying the IPv4 default route
// (the node uplink egress traffic leaves through).
func defaultRouteLink() (string, error) {
	routes, err := netlink.RouteList(nil, netlink.FAMILY_V4)
	if err != nil {
		return "", err
	}
	for _, r := range routes {
		if r.Dst == nil { // default route
			link, err := netlink.LinkByIndex(r.LinkIndex)
			if err != nil {
				return "", err
			}
			return link.Attrs().Name, nil
		}
	}
	return "", fmt.Errorf("no default route found")
}

// runNft runs the nft binary with the given arguments (host-routing backend only).
func runNft(args ...string) (string, error) {
	out, err := exec.Command("nft", args...).CombinedOutput()
	return string(out), err
}
