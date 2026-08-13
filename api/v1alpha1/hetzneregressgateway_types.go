/*
Copyright 2026 Maarlab Rethinking.
Licensed under the Apache License, Version 2.0 (the "License").
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ReclaimPolicy controls what happens to managed Hetzner floating IPs when the
// HetznerEgressGateway is deleted.
// +kubebuilder:validation:Enum=Retain;Delete
type ReclaimPolicy string

const (
	// ReclaimRetain leaves the floating IPs in Hetzner on CR deletion. Default:
	// the addresses are whitelisted at the upstream provider, so losing them is
	// far more costly than an orphaned (cheap) floating IP. They are re-adopted
	// by name/label if the CR is recreated.
	ReclaimRetain ReclaimPolicy = "Retain"
	// ReclaimDelete deletes the floating IPs in Hetzner on CR deletion. Only for
	// ephemeral/test gateways whose addresses are not whitelisted anywhere.
	ReclaimDelete ReclaimPolicy = "Delete"
)

// EgressBackend selects how the egress redirect (send selected pods' traffic out
// via the gateway nodes, SNAT'd to their floating IP) is programmed. The floating-IP
// lifecycle, node assignment and interface/label handling are backend-independent;
// only this redirect step is CNI-specific.
// +kubebuilder:validation:Enum=cilium;host-routing
type EgressBackend string

const (
	// BackendCilium renders a CiliumEgressGatewayPolicy. Requires Cilium with
	// egress gateway enabled; provides multi-gateway HA + per-endpoint spreading.
	BackendCilium EgressBackend = "cilium"
	// BackendHostRouting is CNI-independent: the agent programs policy routing +
	// SNAT (nftables) in the node's host netns itself. Works on any CNI that permits
	// host-level SNAT, at the cost of the HA/spreading niceties a CNI egress gateway
	// gives for free.
	BackendHostRouting EgressBackend = "host-routing"
)

// ManagedFloatingIPs asks the controller to create and own the floating IPs in
// Hetzner Cloud (idempotently, keyed by label), instead of adopting pre-existing
// ones listed in Spec.FloatingIPs.
type ManagedFloatingIPs struct {
	// Count is the number of floating IPs to provision (= number of gateway nodes
	// traffic is spread across). Keep it small and stable: every address must be
	// whitelisted at the upstream provider.
	// +kubebuilder:validation:Minimum=1
	Count int `json:"count"`

	// HomeLocation is the Hetzner home location for the floating IPs, e.g. "fsn1".
	// +kubebuilder:validation:MinLength=1
	HomeLocation string `json:"homeLocation"`

	// Type of floating IP. Defaults to ipv4.
	// +kubebuilder:validation:Enum=ipv4;ipv6
	// +kubebuilder:default=ipv4
	// +optional
	Type string `json:"type,omitempty"`

	// ReclaimPolicy on CR deletion. Defaults to Retain (protects the whitelisted addresses).
	// +kubebuilder:default=Retain
	// +optional
	ReclaimPolicy ReclaimPolicy `json:"reclaimPolicy,omitempty"`

	// Labels are extra Hetzner labels stamped on the created floating IPs (the
	// controller always adds its own managed-by/gateway/index labels for adoption).
	// +optional
	Labels map[string]string `json:"labels,omitempty"`
}

// HetznerEgressGatewaySpec defines a stable-source-IP egress path for a set of pods.
type HetznerEgressGatewaySpec struct {
	// FloatingIPs is a BYO list of existing Hetzner floating IP addresses to adopt
	// (already created + whitelisted at the provider). Mutually exclusive with Managed.
	// +optional
	FloatingIPs []string `json:"floatingIPs,omitempty"`

	// Managed lets the controller create/own the floating IPs. Mutually exclusive
	// with FloatingIPs.
	// +optional
	Managed *ManagedFloatingIPs `json:"managed,omitempty"`

	// PodSelector selects the pods whose egress must use the fixed source IPs.
	// Services opt in by carrying these labels — no node pinning.
	PodSelector metav1.LabelSelector `json:"podSelector"`

	// DestinationCIDRs restricts which destinations are routed through the gateway.
	// Empty => 0.0.0.0/0 (any destination), with the cluster-internal ranges added
	// to ExcludedCIDRs automatically so in-cluster traffic is never redirected.
	// +optional
	DestinationCIDRs []string `json:"destinationCIDRs,omitempty"`

	// ExcludedCIDRs are destinations NOT routed through the gateway (extra to the
	// cluster-internal ranges the controller always excludes).
	// +optional
	ExcludedCIDRs []string `json:"excludedCIDRs,omitempty"`

	// EgressInterface is the name of the dummy interface the agent creates on each
	// gateway node to hold that node's floating IP; the egress backend SNATs to this
	// interface's address, so N gateway nodes each SNAT to their own floating IP and
	// traffic spreads across them.
	// +kubebuilder:default=egr0
	// +optional
	EgressInterface string `json:"egressInterface,omitempty"`

	// Backend programs the egress redirect. Defaults to cilium.
	// +kubebuilder:default=cilium
	// +optional
	Backend EgressBackend `json:"backend,omitempty"`
}

// FloatingIPStatus reports a floating IP's live state — the address to whitelist.
type FloatingIPStatus struct {
	Address      string `json:"address"`
	HCloudID     int64  `json:"hcloudID,omitempty"`
	AssignedNode string `json:"assignedNode,omitempty"`
}

// HetznerEgressGatewayStatus is the observed state.
type HetznerEgressGatewayStatus struct {
	// FloatingIPs are the actual addresses (BYO + managed) and where they are assigned.
	// Whitelist these at the upstream provider.
	// +optional
	FloatingIPs []FloatingIPStatus `json:"floatingIPs,omitempty"`

	// ReadyGateways is the count of floating IPs currently assigned to a healthy node.
	// +optional
	ReadyGateways int `json:"readyGateways,omitempty"`

	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=heg
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.readyGateways`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// HetznerEgressGateway pins the egress source IP(s) of selected pods to stable
// Hetzner floating IPs, spread across the nodes where the controller's agents run.
type HetznerEgressGateway struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HetznerEgressGatewaySpec   `json:"spec,omitempty"`
	Status HetznerEgressGatewayStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HetznerEgressGatewayList contains a list of HetznerEgressGateway.
type HetznerEgressGatewayList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HetznerEgressGateway `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HetznerEgressGateway{}, &HetznerEgressGatewayList{})
}
