# hcloud-egress-gateway-controller

[![CI](https://github.com/maarlab-rethinking/hcloud-egress-gateway-controller/actions/workflows/ci.yml/badge.svg)](https://github.com/maarlab-rethinking/hcloud-egress-gateway-controller/actions/workflows/ci.yml)

Give selected Kubernetes pods a **stable, fixed egress source IP** — a set of
**Hetzner Cloud floating IPs** — spread across the nodes where the controller's
agents run, so upstream services that filter by source IP accept the traffic. No
fixed egress node pool, no external VMs: the egress "identity" follows the agent
pods wherever the scheduler puts them.

Apache-2.0. Maintained by Maarlab Rethinking.

## Why
On Kubernetes, a pod's egress is SNAT'd to the **node** IP, which changes as nodes
are recycled (Cluster API, autoscaling). Any upstream that allow-lists source IPs
then breaks. This controller decouples a **stable floating IP** from the node
lifecycle and lets services opt in by label, keeping the platform fully dynamic.

## How it works
For each `HetznerEgressGateway`:

1. The **controller** creates a `StatefulSet` of *N* **agent** pods (N = number of
   floating IPs), with pod anti-affinity so they land on *N* distinct nodes.
2. Each **agent** (`hostNetwork`, `NET_ADMIN`) on its node:
   - claims its floating IP (stable ordinal→IP mapping),
   - (managed mode) creates the floating IP in Hetzner if missing, idempotently by label,
   - **assigns** the floating IP to its node's Hetzner server (API),
   - creates the dummy interface (`egr0` by default) and adds the floating IP to it,
   - **labels its node** `egress.maarlab.dev/gateway=<cr-name>`.
3. The controller programs the **egress redirect** via the selected backend so that
   the CR's `podSelector` pods egress through the gateway nodes, each SNAT'ing to
   *its* floating IP.

Traffic spreads across the *N* stable IPs (no single-node bottleneck), with failover
to surviving gateways while an agent re-homes a floating IP after a node is recycled.

## CNI support (egress backends)
Only step 3 — the egress *redirect* — is CNI-specific; everything else (floating-IP
lifecycle, node assignment, interface, labels) is CNI-independent. It's a pluggable
backend (`spec.backend`):

- **`cilium`** (default): renders a `CiliumEgressGatewayPolicy`. Requires Cilium with
  egress gateway enabled. Cilium's egress-gateway HA distributes source endpoints
  across the gateway nodes (by CiliumEndpoint UID) and each SNATs to its floating IP
  via `interface` mode — so you get multi-IP spreading + failover for free.
- **`host-routing`**: CNI-independent — the agent programs policy routing + SNAT
  (nftables) in the node's host netns itself. Works on any CNI that permits host-level
  SNAT; spreading/HA semantics are whatever the routing setup provides, not a CNI feature.

Adding a backend for another CNI's egress-gateway construct is a matter of
implementing the redirect renderer; the rest of the controller is reused as-is.

## Floating IP lifecycle
Two modes (mutually exclusive):

- **BYO** (`spec.floatingIPs: [addr, ...]`): adopt pre-created, already-allow-listed IPs.
- **Managed** (`spec.managed`): the controller creates/owns them in Hetzner,
  idempotently keyed by label, and **never recreates** them (the address is
  allow-listed upstream and must stay stable).
  - `reclaimPolicy: Retain` (default) — leaves the IPs on CR deletion (a cheap
    orphaned floating IP is far better than losing an allow-listed address); re-adopted
    by label if the CR is recreated.
  - `reclaimPolicy: Delete` — deletes them (ephemeral/test only).

`status.floatingIPs[].address` lists the live addresses **to allow-list upstream**.

Managed floating IPs are named `<cr-name>-<region>-<index>` (the CR name drives it — no
forced prefix) and carry the Hetzner label `managed-by=hcloud-egress-gateway-controller`;
filter on that label to find every controller-owned IP. The name is cosmetic — adoption
is keyed on the labels, not the name.

## Example
```yaml
apiVersion: egress.maarlab.dev/v1alpha1
kind: HetznerEgressGateway
metadata:
  name: partner-api
spec:
  backend: cilium            # or host-routing
  managed:
    regions:                 # floating IPs per Hetzner location
      - { location: fsn1, count: 1 }
      - { location: nbg1, count: 1 }
    reclaimPolicy: Retain
  podSelector:
    matchLabels:
      egress-via: partner-api
  # destinationCIDRs omitted => any destination (0.0.0.0/0), cluster-internal excluded
```
A workload opts in by adding `egress-via: partner-api` to its pods.

### Regions & locality
A Hetzner floating IP only assigns to a server in its **home location**, so the
controller runs one agent StatefulSet per region, pinned to that location's nodes
(`topology.kubernetes.io/region`). List the locations where your workloads run under
`managed.regions` so each has a fixed egress IP (BYO IPs are grouped by their
API-reported location automatically). **Whitelist every address across all regions.**

Note: Cilium's OSS egress gateway is **not topology-aware** — with one CR spanning
several regions it distributes endpoints to gateway nodes by hash, not by locality, so
a pod may egress via another region's gateway. For guaranteed same-region egress, use
one CR per region with a region-scoped `podSelector` and region-affine workloads.

## Layout
- `api/v1alpha1/` — the `HetznerEgressGateway` CRD types.
- `cmd/` — single binary, two modes: `controller` and `agent`.
- `internal/controller/` — reconciles the CR → StatefulSet + egress-redirect resource + node-label GC.
- `internal/agent/` — per-node: hcloud assign + dummy interface + node label.
- `internal/hcloud/` — Hetzner Cloud API (create/adopt/assign floating IPs).
- `charts/hcloud-egress-gateway-controller/` — Helm chart (CRD + controller Deployment + RBAC).

## Requirements
- A Hetzner Cloud API token (read/write floating IPs + servers), provided as a Secret.
- Nodes whose `spec.providerID` is `hcloud://<id>` (Hetzner CCM / Cluster API).
- A namespace allowed to run privileged pods (the agent needs `hostNetwork` + `NET_ADMIN`).
- For `backend: cilium` (default): Cilium ≥ 1.14 with `egressGateway.enabled: true`
  (validated on 1.19). For `backend: host-routing`: no CNI feature required.

## Status
Alpha (`v1alpha1`). The controller, agent and both egress backends are implemented and
unit-tested; the API is not yet stable. The default `cilium` backend is the exercised
path; `host-routing` is a best-effort extension point (requires `nft` in the image).
