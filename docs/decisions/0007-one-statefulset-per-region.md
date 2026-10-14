# ADR-0007: One agent StatefulSet per region, pinned to that location's nodes

**Status:** accepted, 2026-10-14

## Context

A Hetzner floating IP can only be assigned to a server in its **home location**: an IP homed in `nbg1`
will not attach to a server in `fsn1`. The agent assigns its IP to the node it runs on, so an agent for
an `nbg1` IP must only ever run on an `nbg1` node. A single cluster-wide agent DaemonSet would schedule
agents onto nodes in the wrong location, where the assignment fails.

The desired egress identity is also "N stable IPs spread across N nodes" within a region, not one IP per
node — the count is a property of the region, not of the node fleet.

## Decision

The controller creates **one StatefulSet per region**, `egress-agent-<cr>-<location>`, with:

- `replicas = regions[].count` (BYO: the number of IPs homed in that location);
- a `nodeSelector` on `topology.kubernetes.io/region=<location>`, so agents only land on nodes there;
- required host-level pod anti-affinity, so the N replicas spread onto N distinct nodes;
- each pod's floating-IP list passed as `id:addr,...`, indexed by the pod's **StatefulSet ordinal** —
  which is why it is a StatefulSet (stable ordinals) and not a Deployment.

StatefulSets for regions dropped from the spec are deleted on the next reconcile.

## Consequences

- A region with fewer schedulable nodes than its `count` leaves some agent pods `Pending` and its
  `readyGateways` below target; the [runbook](../runbook.md#readygateways-is-lower-than-the-count-you-asked-for)
  names it.
- The ordinal→IP mapping is stable across pod restarts, so an agent always re-claims the same address.
- Topology pinning is the controller's; it does not make the floating IP follow a pod to another
  location. Cross-region locality of the *redirect* is a separate, CNI-level concern
  ([ADR-0002](0002-cilium-default-host-routing-fallback.md),
  [operations](../operations.md#regions-and-locality)).
