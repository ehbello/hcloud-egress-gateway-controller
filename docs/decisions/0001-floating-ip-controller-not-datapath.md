# ADR-0001: A floating-IP controller that delegates the redirect, not an egress datapath of its own

**Status:** accepted, 2026-10-14

## Context

Some OceanFlights workloads call upstreams that allow-list source IPs. On Kubernetes a pod's egress
is SNAT'd to its node's IP, which changes as nodes are recycled by Cluster API and autoscaling, so any
upstream filter breaks. We need a **stable, allow-listable public source IP** that survives node
churn, without pinning workloads to a fixed node pool or giving up the dynamic CAPI platform.

Two facts frame the options:

- On **Hetzner Cloud** you cannot just raise an arbitrary secondary IP on a server's NIC and have it
  route. A routable, stable public egress address is a **floating IP**, provisioned and assigned to a
  server through the Hetzner API. Nothing egresses from an address the cloud has not attached.
- The cluster already runs **Cilium with its egress gateway feature**, which can redirect selected
  pods out through chosen nodes and SNAT to an address — but it has no notion of a cloud floating IP
  and will not create or move one.

## Options considered

**A general egress-gateway product (e.g. spidernet-io/egressgateway).** Mature, CNI-agnostic, with its
own VXLAN datapath, EIP pools, and topology-aware active/standby failover — everything the redirect
half needs, and more than Cilium's OSS egress gateway offers. But it manages EIPs as addresses it
expects to already be usable on the node's L2/underlay; it does **not** call a cloud API to provision
and attach a real routable IP. On Hetzner Cloud its EIP would not be an allow-listable public source
IP. It solves the redirect half, not the half we are missing.

**OpenTofu / imperative scripts to provision the floating IPs.** Creates the IPs fine, but re-homing
an IP to whichever node an agent lands on, after every node recycle, is a reconciliation loop, not a
one-shot apply. Drift (an IP left on a dead server) stays until the next run.

## Decision

A small controller with one cluster-scoped CRD, `HetznerEgressGateway`, that owns only the half the
ecosystem is missing: the **floating-IP lifecycle** (create/adopt by label, assign to the agent's
node, re-home on churn, reclaim) and the **node labelling** that lets a redirect find the gateway
nodes. The actual redirect is **delegated to the CNI** through a pluggable backend — Cilium's egress
gateway by default ([ADR-0002](0002-cilium-default-host-routing-fallback.md)). The controller runs its
data plane as a per-node agent that assigns the IP and holds it on a dummy interface
([ADR-0005](0005-egr0-is-a-dummy-interface.md)).

## Consequences

- The controller stays small: no overlay, no SNAT engine, no EIP datapath to maintain. It is a Hetzner
  cloud-IP provisioner with a thin node-labelling contract.
- It inherits Cilium's egress-gateway limitations, notably that OSS Cilium is **not topology-aware**
  (a pod may egress through another region's gateway), which is why every address is allow-listed and
  why region-scoped CRs exist ([operations](../operations.md#regions-and-locality)).
- If we ever outgrow Cilium's HA, the split leaves the door open: keep this controller for the floating
  IPs and point a richer datapath (a general egress gateway) at the same labelled nodes, instead of the
  Cilium backend.
- Compatibility with Hetzner and with the CNI is ours: a change on either side shows up as agents
  failing to assign or pods failing to egress, and the fix is in this repository.
