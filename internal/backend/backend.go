/*
Copyright 2026 Maarlab Rethinking.
Licensed under the Apache License, Version 2.0 (the "License").
*/

// Package backend renders the CNI-specific egress-redirect resource. Only this step
// is CNI-specific; the rest of the controller is backend-independent.
package backend

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	egressv1alpha1 "github.com/maarlab-rethinking/hcloud-egress-gateway-controller/api/v1alpha1"
	"github.com/maarlab-rethinking/hcloud-egress-gateway-controller/internal/names"
)

// Renderer builds the egress-redirect object for a HetznerEgressGateway, or nil if the
// backend needs no cluster resource (it's handled node-side by the agent).
type Renderer interface {
	Render(cr *egressv1alpha1.HetznerEgressGateway) (client.Object, error)
}

// For returns the Renderer for the CR's backend (defaults to cilium).
func For(cr *egressv1alpha1.HetznerEgressGateway) Renderer {
	if cr.Spec.Backend == egressv1alpha1.BackendHostRouting {
		return hostRouting{}
	}
	return cilium{}
}

// defaultExcluded keeps in-cluster / private traffic off the egress path when
// destinationCIDRs is the catch-all.
var defaultExcluded = []string{
	"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
	"100.64.0.0/10", "169.254.0.0/16", "fd00::/8", "fe80::/10",
}

type cilium struct{}

func (cilium) Render(cr *egressv1alpha1.HetznerEgressGateway) (client.Object, error) {
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
		"egressGateway": map[string]interface{}{
			"nodeSelector": map[string]interface{}{
				"matchLabels": map[string]interface{}{names.NodeLabelKey: cr.Name},
			},
			"interface": egressInterface(cr),
		},
	}, "spec")
	return u, nil
}

type hostRouting struct{}

// Render returns nil: the agent programs policy-routing + SNAT in the node netns, so
// there is no cluster-level redirect object.
func (hostRouting) Render(*egressv1alpha1.HetznerEgressGateway) (client.Object, error) {
	return nil, nil
}

func egressInterface(cr *egressv1alpha1.HetznerEgressGateway) string {
	if cr.Spec.EgressInterface != "" {
		return cr.Spec.EgressInterface
	}
	return "egr0"
}

func toIfaceSlice(s []string) []interface{} {
	out := make([]interface{}, len(s))
	for i, v := range s {
		out[i] = v
	}
	return out
}
