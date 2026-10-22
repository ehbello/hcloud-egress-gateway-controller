/*
Copyright 2026 Maarlab Rethinking.
Licensed under the Apache License, Version 2.0 (the "License").
*/

package backend

import (
	"fmt"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	egressv1alpha1 "github.com/maarlab-rethinking/hcloud-egress-gateway-controller/api/v1alpha1"
	"github.com/maarlab-rethinking/hcloud-egress-gateway-controller/internal/names"
)

func TestCiliumRenderDefaults(t *testing.T) {
	cr := &egressv1alpha1.HetznerEgressGateway{
		ObjectMeta: metav1.ObjectMeta{Name: "partner-api"},
		Spec: egressv1alpha1.HetznerEgressGatewaySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{"egress-via": "partner-api"}},
		},
	}
	u, err := For(cr).Render(cr, []string{"203.0.113.10"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if u == nil {
		t.Fatal("render returned nil with a resolved IP")
	}
	if u.GetKind() != "CiliumEgressGatewayPolicy" || u.GetAPIVersion() != "cilium.io/v2" {
		t.Fatalf("unexpected GVK: %s %s", u.GetAPIVersion(), u.GetKind())
	}
	if u.GetName() != "partner-api" {
		t.Fatalf("name = %q", u.GetName())
	}

	dst, _, _ := unstructured.NestedStringSlice(u.Object, "spec", "destinationCIDRs")
	if len(dst) != 1 || dst[0] != "0.0.0.0/0" {
		t.Fatalf("destinationCIDRs = %v, want [0.0.0.0/0]", dst)
	}
	// The excluded defaults must be IPv4-only: an IPv6 CIDR in an IPv4 egressIP policy
	// black-holes the traffic on Cilium 1.20 (cilium/cilium#48660, ADR-0009).
	excl, _, _ := unstructured.NestedStringSlice(u.Object, "spec", "excludedCIDRs")
	if len(excl) != len(defaultExcluded) {
		t.Fatalf("excludedCIDRs = %v, want the %d defaults", excl, len(defaultExcluded))
	}
	for _, c := range excl {
		if strings.Contains(c, ":") {
			t.Fatalf("excludedCIDRs must be IPv4-only, got %q", c)
		}
	}

	// egressIP mode, not interface mode: the datapath does a per-packet FIB lookup from
	// the floating IP (ADR-0009). An interface field would make Cilium ignore the policy.
	// Pinned to the one node that owns the address: both the gateway label and the
	// per-address floating-IP label the agent sets.
	gws := gateways(t, u)
	if len(gws) != 1 {
		t.Fatalf("egressGateways has %d entries, want 1", len(gws))
	}
	expectGateway(t, gws[0], "partner-api", "203.0.113.10")
	// egressGateway is required by the CRD and repeats the first entry.
	first, _, _ := unstructured.NestedMap(u.Object, "spec", "egressGateway")
	expectGateway(t, first, "partner-api", "203.0.113.10")
}

func TestCiliumRenderCustomCIDRs(t *testing.T) {
	cr := &egressv1alpha1.HetznerEgressGateway{
		ObjectMeta: metav1.ObjectMeta{Name: "gw"},
		Spec: egressv1alpha1.HetznerEgressGatewaySpec{
			PodSelector:      metav1.LabelSelector{MatchLabels: map[string]string{"egress-via": "gw"}},
			DestinationCIDRs: []string{"198.51.100.0/24"},
			ExcludedCIDRs:    []string{"198.51.100.5/32"},
		},
	}
	u, err := For(cr).Render(cr, []string{"203.0.113.10"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	dst, _, _ := unstructured.NestedStringSlice(u.Object, "spec", "destinationCIDRs")
	if len(dst) != 1 || dst[0] != "198.51.100.0/24" {
		t.Fatalf("destinationCIDRs = %v", dst)
	}
	// With an explicit destination, the private-range defaults must NOT be injected.
	excl, _, _ := unstructured.NestedStringSlice(u.Object, "spec", "excludedCIDRs")
	if len(excl) != 1 || excl[0] != "198.51.100.5/32" {
		t.Fatalf("excludedCIDRs = %v, want only the explicit one (no defaults)", excl)
	}
}

func TestCiliumRenderOneGatewayPerIP(t *testing.T) {
	cr := &egressv1alpha1.HetznerEgressGateway{
		ObjectMeta: metav1.ObjectMeta{Name: "gw"},
		Spec: egressv1alpha1.HetznerEgressGatewaySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{"egress-via": "gw"}},
		},
	}
	// Several resolved IPs: one policy, one egressGateways entry per IP, in order, so Cilium
	// spreads the pods across all of them and fails over between them.
	ips := []string{"116.203.14.77", "78.47.226.21", "65.109.255.19"}
	u, err := For(cr).Render(cr, ips)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	gws := gateways(t, u)
	if len(gws) != len(ips) {
		t.Fatalf("egressGateways has %d entries, want %d", len(gws), len(ips))
	}
	for i, ip := range ips {
		expectGateway(t, gws[i], "gw", ip)
	}
	first, _, _ := unstructured.NestedMap(u.Object, "spec", "egressGateway")
	expectGateway(t, first, "gw", ips[0])
}

func TestCiliumRenderRejectsMoreIPsThanTheCRDAllows(t *testing.T) {
	cr := &egressv1alpha1.HetznerEgressGateway{ObjectMeta: metav1.ObjectMeta{Name: "gw"}}
	ips := make([]string, maxGateways+1)
	for i := range ips {
		ips[i] = fmt.Sprintf("203.0.113.%d", i)
	}
	if _, err := For(cr).Render(cr, ips); err == nil {
		t.Fatalf("rendering %d gateways should fail, the CRD allows %d", len(ips), maxGateways)
	}
}

func TestCiliumRenderNoIPsYet(t *testing.T) {
	cr := &egressv1alpha1.HetznerEgressGateway{ObjectMeta: metav1.ObjectMeta{Name: "gw"}}
	u, err := For(cr).Render(cr, nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if u != nil {
		t.Fatalf("no floating IP resolved yet should render nothing, got %v", u)
	}
}

func TestHostRoutingRendersNothing(t *testing.T) {
	cr := &egressv1alpha1.HetznerEgressGateway{
		ObjectMeta: metav1.ObjectMeta{Name: "x"},
		Spec:       egressv1alpha1.HetznerEgressGatewaySpec{Backend: egressv1alpha1.BackendHostRouting},
	}
	u, err := For(cr).Render(cr, []string{"203.0.113.10"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if u != nil {
		t.Fatalf("host-routing should render no cluster object, got %v", u)
	}
}

func gateways(t *testing.T, u *unstructured.Unstructured) []map[string]interface{} {
	t.Helper()
	raw, _, _ := unstructured.NestedSlice(u.Object, "spec", "egressGateways")
	out := make([]map[string]interface{}, len(raw))
	for i, g := range raw {
		out[i] = g.(map[string]interface{})
	}
	return out
}

func expectGateway(t *testing.T, gw map[string]interface{}, cr, ip string) {
	t.Helper()
	if got, _, _ := unstructured.NestedString(gw, "egressIP"); got != ip {
		t.Fatalf("egressIP = %q, want %q", got, ip)
	}
	if _, found, _ := unstructured.NestedString(gw, "interface"); found {
		t.Fatal("interface must not be set alongside egressIP")
	}
	ml, _, _ := unstructured.NestedStringMap(gw, "nodeSelector", "matchLabels")
	if ml[names.NodeLabelKey] != cr || ml[names.FloatingIPLabelKey] != ip {
		t.Fatalf("nodeSelector matchLabels = %v, want %s=%s and %s=%s",
			ml, names.NodeLabelKey, cr, names.FloatingIPLabelKey, ip)
	}
}
