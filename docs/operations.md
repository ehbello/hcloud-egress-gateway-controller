# Operations

What to look at, what the controller will and will not do for you, and the procedures that are yours
to run.

## Reading the status

```console
$ kubectl get hetzneregressgateway
NAME                 READY   AGE
egress-gateway-dev   3       4d
```

The `READY` column is `status.readyGateways`: the number of floating IPs currently live on a node
that is running an agent. Compare it with the number you asked for — `sum(spec.managed.regions[].count)`
or `len(spec.floatingIPs)`. Equal means every requested source IP is up.

`kubectl get hetzneregressgateway <name> -o yaml` shows `status.floatingIPs[]`, each with its
`address` and the `assignedNode` it is currently live on. **Those addresses are what you allow-list
upstream.** There are no `Ready`-style conditions; the gap between wanted and ready, plus the agent
pods, is the whole picture. The [runbook](runbook.md) works from that gap.

## Metrics

The controller serves controller-runtime's own metrics on `--metrics-bind-address` (`:8080` by
default, plain HTTP): `controller_runtime_reconcile_total`,
`controller_runtime_reconcile_errors_total`, `controller_runtime_reconcile_time_seconds`,
`workqueue_*`, `rest_client_*` and leader election. A health probe is on `--health-probe-bind-address`
(`:8081`). It publishes no metrics of its own; `readyGateways` on each CR is the state. A sustained
rise in `controller_runtime_reconcile_errors_total` means the Hetzner API or the Kubernetes API is
failing calls.

## Installing and upgrading

```console
helm upgrade --install hcloud-egress-gateway charts/hcloud-egress-gateway-controller \
  --namespace egress-system --create-namespace
```

The namespace must allow privileged pods — the agents need `hostNetwork` and `privileged`. Every value
is in `values.yaml`; the Hetzner token is read from the Secret named by `hcloud.tokenSecretName`
(key `hcloud.tokenSecretKey`), which the controller mounts and passes to the agents. The CRD, the
ClusterRole and the API reference are generated from the Go source and shipped under `templates/`, so
`helm upgrade` updates them ([ADR-0008](decisions/0008-generate-crd-rbac-api-reference.md)); set
`installCRD=false` or `rbac.create=false` to manage either out of band.

The controller holds no state outside the API server and Hetzner. An upgrade replaces the pod and the
new version picks up every CR on its first pass; while it is down nothing is reconciled, but existing
gateways keep working — the floating IPs stay assigned and the agents keep self-healing every 30 s.
With more than one replica, leader election makes the others hot standbys.

`v1alpha1` makes no compatibility promise across minor versions. New fields arrive with defaults, so
an existing CR stays valid.

## Compatibility

| Component | Tested |
|---|---|
| Kubernetes | 1.36 |
| Cilium (default backend) | 1.18–1.20.x, egress gateway enabled; the datapath e2e runs the newest patch of each and the next pre-release weekly ([ADR-0010](decisions/0010-cilium-datapath-e2e-matrix.md)) |
| Talos | SELinux permissive; `dummy` module built in |
| Hetzner Cloud | floating IPs (IPv4), API token with read/write |

The default `cilium` backend requires Cilium with `egressGateway.enabled: true`. The `host-routing`
backend needs `nft` in the agent image and works on any CNI that permits host-level SNAT, but its
spreading and failover are whatever the routing gives you, not a CNI feature.

## The Hetzner token

One Hetzner Cloud API token, read/write on floating IPs and servers, provided as a Secret (key
`token` by default). On this platform it is the cluster's existing token, materialised by External
Secrets from OpenBao; reuse it rather than minting a new one. The controller and every agent mount the
same Secret. Rotating it is a normal Secret update followed by restarting the controller and the agent
StatefulSets so the new value is read.

## Floating IP lifecycle

Two mutually exclusive modes:

- **Managed** (`spec.managed`): the controller creates the floating IPs in Hetzner, one per
  `regions[].count`, idempotently keyed by the labels `managed-by=hcloud-egress-gateway-controller`,
  `egress-gateway=<cr>`, `egress-region=<location>`, `egress-index=<i>`. They are named
  `<cr>-<location>-<index>` (cosmetic; adoption is by label). An existing one is **adopted, never
  recreated** ([ADR-0006](decisions/0006-managed-floating-ips-adopt-never-recreate.md)), so an
  allow-listed address survives deleting and recreating the CR.
- **BYO** (`spec.floatingIPs`): a list of addresses you created and allow-listed by hand; the
  controller adopts them and groups them by their API-reported home location.

**Whitelist every address across all regions upstream.** `status.floatingIPs[].address` lists them.
With the `cilium` backend a CR's pods are spread across all of its addresses, whatever region they
run in (see below), so one address is not enough even for a single-region workload.

### Reclaim on deletion

`spec.managed.reclaimPolicy` (default **`Retain`**) decides what deleting the CR does to managed
floating IPs:

- **`Retain`** leaves them in Hetzner. The next CR with the same name and regions re-adopts them by
  label. This is the default because the address is allow-listed and a cheap orphaned IP is far better
  than losing it.
- **`Delete`** removes them. Ephemeral and test gateways only; never for an address an upstream filters
  on.

The finalizer `egress.maarlab.dev/finalizer` also strips the `gateway`/`floating-ip` node labels on
deletion, so a CR never leaves a node looking like a gateway.

## Regions and locality

A Hetzner floating IP only assigns to a server in its **home location**, so the controller runs one
agent StatefulSet per region, pinned to that location's nodes. List every location your workloads run
in under `spec.managed.regions`, each with a `count`, so each has at least one fixed egress IP.

**Cilium's OSS egress gateway is not topology-aware.** The policy lists every floating IP as a gateway,
and Cilium assigns each selected pod to one of them by a hash of its endpoint, so a pod in `hel1` may
egress through the `nbg1` address ([ADR-0009](decisions/0009-cilium-egressip-not-interface.md)). This is
why every address must be allow-listed. For guaranteed same-region egress, use one CR per region with a
region-scoped `podSelector` and region-affine workloads.

**Failover is Cilium's.** When a gateway node goes away, its labels leave the node — the agent removes
them on shutdown, and the controller as soon as the node stops being `Ready` (an unreachable node turns
`Unknown` once the node-monitor grace period passes, under a minute by default) or has no agent pod
left — and Cilium moves that gateway's pods to the remaining ones within seconds. Any change to the set
of live gateways re-hashes the pods, so some whose gateway did not fail move too, and **their open
egress connections break** (cilium/cilium#39245); new connections work at once. The floating IP itself
moves only when its agent pod is rescheduled: at once for a drained node, and for a dead one when its
Node object is removed (Cluster API remediation); the agent then claims it on another node of its region
and the gateway rejoins.

## Opting a workload in

A pod egresses through the gateway by carrying the labels in `spec.podSelector` — nothing is pinned to
a node, and pods without the label are untouched and egress from their node's own IP. Verify it with a
pod that carries the label and one that does not, each hitting an IP echo service: the labelled one
reports a floating IP, the other its node's address.

## Backends

- **`cilium`** (default): the controller renders a `CiliumEgressGatewayPolicy` with one gateway per
  floating IP, in `egressIP` mode, each pinned to the node that owns its address; Cilium does the
  redirect and SNAT, spreads the pods across the gateways and fails over between them, and the datapath
  routes out the node's real uplink via a per-packet FIB lookup
  ([ADR-0009](decisions/0009-cilium-egressip-not-interface.md)). On Cilium 1.20.x the policy must stay a
  single address family (IPv4 here); the controller keeps it so.
- **`host-routing`**: the agent programs policy routing and an `nft` SNAT rule itself, in the node's
  host netns. No CNI feature required; no cross-node spreading beyond what the routing provides.
  See [ADR-0002](decisions/0002-cilium-default-host-routing-fallback.md).

## Moving or retiring a gateway

With `reclaimPolicy: Retain`, deleting the CR leaves the floating IPs, so moving a gateway to another
cluster is: create the CR there with the same name and regions (it re-adopts the IPs by label), then
delete the old one. To retire a gateway for good, set `reclaimPolicy: Delete` before deleting the CR,
or delete the CR and then remove the floating IPs in the Hetzner Console — and remove the addresses
from the upstream allow-list.
