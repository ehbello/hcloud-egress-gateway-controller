/*
Copyright 2026 Maarlab Rethinking.
Licensed under the Apache License, Version 2.0 (the "License").
*/

// Package names holds label keys and naming shared by the controller, agent and backends.
package names

const (
	// NodeLabelKey marks a node as an active egress gateway for a given CR
	// (value = CR name). The egress backend's nodeSelector targets it.
	NodeLabelKey = "egress.maarlab.dev/gateway"
	// FloatingIPLabelKey records which floating IP the agent put on the node
	// (for status reporting).
	FloatingIPLabelKey = "egress.maarlab.dev/floating-ip"
	// Finalizer guards floating-IP reclaim on CR deletion.
	Finalizer = "egress.maarlab.dev/finalizer"
)

// AgentName is the StatefulSet name the controller creates for a CR's agents.
func AgentName(cr string) string { return "egress-agent-" + cr }
