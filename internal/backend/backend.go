/*
Copyright 2026 Maarlab Rethinking.
Licensed under the Apache License, Version 2.0 (the "License").
*/

// Package backend renders the CNI-specific egress-redirect resource. Only this step
// is CNI-specific; the rest of the controller is backend-independent.
package backend

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	egressv1alpha1 "github.com/maarlab-rethinking/hcloud-egress-gateway-controller/api/v1alpha1"
	"github.com/maarlab-rethinking/hcloud-egress-gateway-controller/internal/names"
)

// Renderer builds the egress-redirect object for a HetznerEgressGateway from its resolved
// egress IPs, or nil if the backend needs no cluster resource (it's handled node-side by
// the agent, or no IP is resolved yet). The object is unstructured because its CRD belongs
// to the CNI and is not in the scheme.
type Renderer interface {
	Render(cr *egressv1alpha1.HetznerEgressGateway, egressIPs []string) (*unstructured.Unstructured, error)
}

// For returns the Renderer for the CR's backend (defaults to cilium).
func For(cr *egressv1alpha1.HetznerEgressGateway) Renderer {
	if cr.Spec.Backend == egressv1alpha1.BackendHostRouting {
		return hostRouting{}
	}
	return cilium{}
}

// defaultExcluded keeps in-cluster / private traffic off the egress path when
// destinationCIDRs is the catch-all. IPv4 only: the default destination is 0.0.0.0/0
// and the floating IPs are IPv4, so an IPv6 entry would never match — and mixing address
// families into an IPv4 egressIP policy trips Cilium 1.20's address-family checks and
// black-holes the IPv4 traffic (cilium/cilium#48660).
var defaultExcluded = []string{
	"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
	"100.64.0.0/10", "169.254.0.0/16",
}

// maxGateways is the CRD's limit on egressGateways.
const maxGateways = 64

type cilium struct{}

// Render emits one CiliumEgressGatewayPolicy in egressIP mode, pinned to the node that
// owns the egress IP.
//
// egressIP mode (not interface mode) is deliberate: Cilium programs egress_ifindex=0 for
// an egressIP policy and resolves the outgoing device per packet with a datapath FIB
// lookup (`ip route get <dst> from <egressIP>`), which lands on the node's real uplink.
// interface mode instead programs egress_ifindex = the named interface's index and sends
// the packet out THAT device; pointed at egr0 (a dummy, ADR-0005) it is a black hole —
// the regression this fixes on Cilium >= 1.20 (ADR-0009).
//
// Every floating IP is one entry of egressGateways (Cilium >= 1.18), all in a single
// policy. Cilium hashes each selected pod to one live entry, so the pods spread across the
// floating IPs, and an entry whose nodeSelector matches no node is skipped, so its pods
// fail over to the others as soon as the agent's labels leave the node. Each entry is
// pinned to the node that owns its address by both the gateway label and the per-address
// floating-IP label the agent sets: egressIP lives on exactly one node, and an entry that
// elected any other node would drop its traffic with "No Egress IP configured". One policy
// rather than one per IP, because policies selecting the same pods and destinations collide
// in Cilium's egress map (ADR-0009).
//
// egressGateway, required by the CRD, repeats the first entry; Cilium ignores it while
// egressGateways has entries.
func (cilium) Render(cr *egressv1alpha1.HetznerEgressGateway, egressIPs []string) (*unstructured.Unstructured, error) {
	if len(egressIPs) == 0 {
		return nil, nil // no floating IP resolved yet; nothing to program
	}
	if len(egressIPs) > maxGateways {
		return nil, fmt.Errorf("%d floating IPs, but a CiliumEgressGatewayPolicy takes at most %d gateways",
			len(egressIPs), maxGateways)
	}
	gateways := make([]interface{}, len(egressIPs))
	for i, ip := range egressIPs {
		gateways[i] = map[string]interface{}{
			"nodeSelector": map[string]interface{}{
				"matchLabels": map[string]interface{}{
					names.NodeLabelKey:       cr.Name,
					names.FloatingIPLabelKey: ip,
				},
			},
			"egressIP": ip,
		}
	}

	podSel, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&cr.Spec.PodSelector)
	if err != nil {
		return nil, err
	}

	dst := cr.Spec.DestinationCIDRs
	excluded := cr.Spec.ExcludedCIDRs
	if len(dst) == 0 {
		dst = []string{"0.0.0.0/0"}
		excluded = append(append([]string{}, excluded...), defaultExcluded...)
	}

	u := &unstructured.Unstructured{}
	u.SetAPIVersion("cilium.io/v2")
	u.SetKind("CiliumEgressGatewayPolicy")
	u.SetName(cr.Name)
	_ = unstructured.SetNestedMap(u.Object, map[string]interface{}{
		"selectors": []interface{}{
			map[string]interface{}{"podSelector": podSel},
		},
		"destinationCIDRs": toIfaceSlice(dst),
		"excludedCIDRs":    toIfaceSlice(excluded),
		"egressGateway":    gateways[0],
		"egressGateways":   gateways,
	}, "spec")
	return u, nil
}

type hostRouting struct{}

// Render returns nil: the agent programs policy-routing + SNAT in the node netns, so
// there is no cluster-level redirect object.
func (hostRouting) Render(*egressv1alpha1.HetznerEgressGateway, []string) (*unstructured.Unstructured, error) {
	return nil, nil
}

func toIfaceSlice(s []string) []interface{} {
	out := make([]interface{}, len(s))
	for i, v := range s {
		out[i] = v
	}
	return out
}
