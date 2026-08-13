/*
Copyright 2026 Maarlab Rethinking.
Licensed under the Apache License, Version 2.0 (the "License").
*/

// Single binary, two modes:
//
//	hcloud-egress-gateway-controller controller   # cluster controller (Deployment, leader-elected)
//	hcloud-egress-gateway-controller agent        # per-node agent (StatefulSet, hostNetwork, privileged)
//
// The controller reconciles HetznerEgressGateway CRs into an agent StatefulSet and
// the selected egress-redirect backend resource. The agent, on the node it runs on,
// assigns its floating IP (Hetzner API), configures the egress interface, and labels
// its node.
package main

import (
	"fmt"
	"os"

	"github.com/maarlab-rethinking/hcloud-egress-gateway-controller/internal/agent"
	"github.com/maarlab-rethinking/hcloud-egress-gateway-controller/internal/controller"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: hcloud-egress-gateway-controller <controller|agent> [flags]")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "controller":
		controller.Run(os.Args[2:])
	case "agent":
		agent.Run(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown mode %q (want controller|agent)\n", os.Args[1])
		os.Exit(2)
	}
}
