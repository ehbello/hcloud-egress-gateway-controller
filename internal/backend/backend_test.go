/*
Copyright 2026 Maarlab Rethinking.
Licensed under the Apache License, Version 2.0 (the "License").
*/

package backend

import (
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
	obj, err := For(cr).Render(cr)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		t.Fatalf("expected *unstructured.Unstructured, got %T", obj)
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
	excl, _, _ := unstructured.NestedStringSlice(u.Object, "spec", "excludedCIDRs")
	if len(excl) != len(defaultExcluded) {
		t.Fatalf("excludedCIDRs = %v, want the %d defaults", excl, len(defaultExcluded))
	}

	iface, _, _ := unstructured.NestedString(u.Object, "spec", "egressGateway", "interface")
	if iface != "egr0" {
		t.Fatalf("interface = %q, want egr0", iface)
	}
	nodeLabel, _, _ := unstructured.NestedString(u.Object, "spec", "egressGateway", "nodeSelector", "matchLabels", names.NodeLabelKey)
	if nodeLabel != "partner-api" {
		t.Fatalf("nodeSelector matchLabels[%s] = %q, want partner-api", names.NodeLabelKey, nodeLabel)
	}
}

func TestHostRoutingRendersNothing(t *testing.T) {
	cr := &egressv1alpha1.HetznerEgressGateway{
		ObjectMeta: metav1.ObjectMeta{Name: "x"},
		Spec:       egressv1alpha1.HetznerEgressGatewaySpec{Backend: egressv1alpha1.BackendHostRouting},
	}
	obj, err := For(cr).Render(cr)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if obj != nil {
		t.Fatalf("host-routing should render no cluster object, got %v", obj)
	}
}
