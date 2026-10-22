/*
Copyright 2026 Maarlab Rethinking.
Licensed under the Apache License, Version 2.0 (the "License").
*/

// Package names holds label keys, naming and the node-liveness rule shared by the
// controller, agent and backends.
package names

import corev1 "k8s.io/api/core/v1"

const (
	// NodeLabelKey marks a node as an active egress gateway for a given CR
	// (value = CR name). The egress backend's nodeSelector targets it.
	NodeLabelKey = "egress.maarlab.dev/gateway"
	// FloatingIPLabelKey records which floating IP the agent put on the node
	// (for status reporting).
	FloatingIPLabelKey = "egress.maarlab.dev/floating-ip"
	// Finalizer guards floating-IP reclaim on CR deletion.
	Finalizer = "egress.maarlab.dev/finalizer"
	// RegionLabelKey is the well-known node label the controller pins each region's
	// agents to (a floating IP only assigns to a server in its home location).
	RegionLabelKey = "topology.kubernetes.io/region"
	// RegionSelectorKey marks a StatefulSet/pod with its Hetzner region, so the
	// controller can find and GC per-region agent StatefulSets.
	RegionSelectorKey = "egress.maarlab.dev/region"
)

// NodeReady reports whether the node's Ready condition is True. A gateway node that is
// not Ready must not carry the gateway labels: the controller drops them and the agent
// does not set them, so the egress policy fails over to the other gateways.
func NodeReady(n *corev1.Node) bool {
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// AgentName is the per-region StatefulSet name the controller creates for a CR.
func AgentName(cr, location string) string { return "egress-agent-" + cr + "-" + location }
