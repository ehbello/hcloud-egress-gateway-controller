# API Reference

## Packages
- [egress.maarlab.dev/v1alpha1](#egressmaarlabdevv1alpha1)


## egress.maarlab.dev/v1alpha1

Package v1alpha1 contains the API types for the egress.maarlab.dev group.

The scheme is registered through apimachinery's own SchemeBuilder rather than
controller-runtime's helper, so that importing these types costs nothing beyond
apimachinery.

### Resource Types
- [HetznerEgressGateway](#hetzneregressgateway)
- [HetznerEgressGatewayList](#hetzneregressgatewaylist)



#### EgressBackend

_Underlying type:_ _string_

EgressBackend selects how the egress redirect (send selected pods' traffic out
via the gateway nodes, SNAT'd to their floating IP) is programmed. The floating-IP
lifecycle, node assignment and interface/label handling are backend-independent;
only this redirect step is CNI-specific.

_Validation:_
- Enum: [cilium host-routing]

_Appears in:_
- [HetznerEgressGatewaySpec](#hetzneregressgatewayspec)

| Field | Description |
| --- | --- |
| `cilium` | BackendCilium renders a CiliumEgressGatewayPolicy with one gateway per floating<br />IP. Requires Cilium >= 1.18 with egress gateway enabled; Cilium spreads the pods<br />across the gateways and fails over between them.<br /> |
| `host-routing` | BackendHostRouting is CNI-independent: the agent programs policy routing +<br />SNAT (nftables) in the node's host netns itself. Works on any CNI that permits<br />host-level SNAT, at the cost of the HA/spreading niceties a CNI egress gateway<br />gives for free.<br /> |


#### FloatingIPStatus



FloatingIPStatus reports a floating IP's live state — the address to whitelist.



_Appears in:_
- [HetznerEgressGatewayStatus](#hetzneregressgatewaystatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `address` _string_ |  |  |  |
| `hcloudID` _integer_ |  |  |  |
| `assignedNode` _string_ |  |  |  |


#### HetznerEgressGateway



HetznerEgressGateway pins the egress source IP(s) of selected pods to stable
Hetzner floating IPs, spread across the nodes where the controller's agents run.



_Appears in:_
- [HetznerEgressGatewayList](#hetzneregressgatewaylist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `egress.maarlab.dev/v1alpha1` | | |
| `kind` _string_ | `HetznerEgressGateway` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.37/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[HetznerEgressGatewaySpec](#hetzneregressgatewayspec)_ |  |  |  |


#### HetznerEgressGatewayList



HetznerEgressGatewayList contains a list of HetznerEgressGateway.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `egress.maarlab.dev/v1alpha1` | | |
| `kind` _string_ | `HetznerEgressGatewayList` | | |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.37/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[HetznerEgressGateway](#hetzneregressgateway) array_ |  |  |  |


#### HetznerEgressGatewaySpec



HetznerEgressGatewaySpec defines a stable-source-IP egress path for a set of pods.



_Appears in:_
- [HetznerEgressGateway](#hetzneregressgateway)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `floatingIPs` _string array_ | FloatingIPs is a BYO list of existing Hetzner floating IP addresses to adopt<br />(already created + whitelisted at the provider). Mutually exclusive with Managed. |  | Optional: \{\} <br /> |
| `managed` _[ManagedFloatingIPs](#managedfloatingips)_ | Managed lets the controller create/own the floating IPs. Mutually exclusive<br />with FloatingIPs. |  | Optional: \{\} <br /> |
| `podSelector` _[LabelSelector](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.37/#labelselector-v1-meta)_ | PodSelector selects the pods whose egress must use the fixed source IPs.<br />Services opt in by carrying these labels — no node pinning. |  |  |
| `destinationCIDRs` _string array_ | DestinationCIDRs restricts which destinations are routed through the gateway.<br />Empty => 0.0.0.0/0 (any destination), with the cluster-internal ranges added<br />to ExcludedCIDRs automatically so in-cluster traffic is never redirected. |  | Optional: \{\} <br /> |
| `excludedCIDRs` _string array_ | ExcludedCIDRs are destinations NOT routed through the gateway (extra to the<br />cluster-internal ranges the controller always excludes). |  | Optional: \{\} <br /> |
| `egressInterface` _string_ | EgressInterface is the name of the dummy interface the agent creates on each<br />gateway node to hold that node's floating IP as a local address, so each gateway<br />node can SNAT to its own floating IP. Traffic never leaves through it: the cilium<br />backend routes out the node's uplink. | egr0 | Optional: \{\} <br /> |
| `backend` _[EgressBackend](#egressbackend)_ | Backend programs the egress redirect. Defaults to cilium. | cilium | Enum: [cilium host-routing] <br />Optional: \{\} <br /> |




#### ManagedFloatingIPs



ManagedFloatingIPs asks the controller to create and own the floating IPs in
Hetzner Cloud (idempotently, keyed by label), instead of adopting pre-existing
ones listed in Spec.FloatingIPs.



_Appears in:_
- [HetznerEgressGatewaySpec](#hetzneregressgatewayspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `regions` _[ManagedRegion](#managedregion) array_ | Regions is the set of Hetzner locations across which floating IPs are<br />provisioned. Spread them across the locations where your workloads run so each<br />region has a fixed egress IP. NOTE: Cilium's OSS egress gateway is not<br />topology-aware — the CR's pods are spread across all of its floating IPs, whatever<br />their region, so allow-list every address; for guaranteed same-region egress use<br />one CR per region with a region-scoped podSelector (and region-affine workloads). |  | MinItems: 1 <br /> |
| `type` _string_ | Type of floating IP. Defaults to ipv4. | ipv4 | Enum: [ipv4 ipv6] <br />Optional: \{\} <br /> |
| `reclaimPolicy` _[ReclaimPolicy](#reclaimpolicy)_ | ReclaimPolicy on CR deletion. Defaults to Retain (protects the whitelisted addresses). | Retain | Enum: [Retain Delete] <br />Optional: \{\} <br /> |
| `labels` _object (keys:string, values:string)_ | Labels are extra Hetzner labels stamped on the created floating IPs (the<br />controller always adds its own managed-by/gateway/index labels for adoption). |  | Optional: \{\} <br /> |


#### ManagedRegion



ManagedRegion asks for a number of floating IPs in one Hetzner location. A floating
IP can only be assigned to a server in its home location, so the controller pins
this region's agents to nodes in that location (topology.kubernetes.io/region).



_Appears in:_
- [ManagedFloatingIPs](#managedfloatingips)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `location` _string_ | Location is the Hetzner location, e.g. "nbg1", "fsn1", "hel1". |  | MinLength: 1 <br /> |
| `count` _integer_ | Count is the number of floating IPs to provision in this location (= gateway<br />nodes in the region). Keep it small and stable: every address must be<br />whitelisted at the upstream provider. |  | Minimum: 1 <br /> |


#### ReclaimPolicy

_Underlying type:_ _string_

ReclaimPolicy controls what happens to managed Hetzner floating IPs when the
HetznerEgressGateway is deleted.

_Validation:_
- Enum: [Retain Delete]

_Appears in:_
- [ManagedFloatingIPs](#managedfloatingips)

| Field | Description |
| --- | --- |
| `Retain` | ReclaimRetain leaves the floating IPs in Hetzner on CR deletion. Default:<br />the addresses are whitelisted at the upstream provider, so losing them is<br />far more costly than an orphaned (cheap) floating IP. They are re-adopted<br />by name/label if the CR is recreated.<br /> |
| `Delete` | ReclaimDelete deletes the floating IPs in Hetzner on CR deletion. Only for<br />ephemeral/test gateways whose addresses are not whitelisted anywhere.<br /> |


