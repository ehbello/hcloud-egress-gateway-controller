# Architecture

How a `HetznerEgressGateway` becomes a set of pods egressing from fixed Hetzner floating IPs, in the
order the controller does it and with the reasons for that order. The [decision records](decisions/)
hold the alternatives that were considered and why they lost.

## The shape

```
HetznerEgressGateway/egress-gateway-dev            (cluster-scoped)
  ├─ reconciles ─► StatefulSet/egress-agent-egress-gateway-dev-nbg1   (one per region, hostNetwork)
  │                 └─ agent pod on a nbg1 node:
  │                     ├─ Hetzner API: assign floating IP ──► the node's server
  │                     ├─ egr0 (dummy) holds the floating IP /32
  │                     └─ labels the node  egress.maarlab.dev/gateway=egress-gateway-dev
  ├─ reconciles ─► CiliumEgressGatewayPolicy/egress-gateway-dev       (cilium backend only)
  └─ status ─────► readyGateways, floatingIPs[].{address,assignedNode}
```

The controller owns one cluster-scoped CRD, `HetznerEgressGateway`. From it, it creates one **agent
StatefulSet per region** and, for the default backend, one **`CiliumEgressGatewayPolicy`**; it sets
owner references on both, so deleting the CR garbage-collects them. The agents are the data plane:
each one claims a floating IP, puts it on the node, and labels the node so the backend's redirect can
find it.

It talks to two APIs. **Kubernetes**, through controller-runtime, for the CR, the StatefulSets it
owns, the backend object, and the Nodes it labels and reads. **Hetzner Cloud**, through
`hcloud-go`, to create/adopt floating IPs (controller) and assign them to servers (agent). The
floating IP is the one thing that only a cloud API can provide and the reason this controller exists
rather than a generic egress gateway ([ADR-0001](decisions/0001-floating-ip-controller-not-datapath.md)).

## One binary, two modes

`cmd` is a single binary dispatched by its first argument:

- **`controller`** — the cluster controller, a leader-elected Deployment. Reconciles the CR.
- **`agent`** — the per-node data plane, a StatefulSet pod with `hostNetwork` and `privileged`,
  running as **root** ([ADR-0003](decisions/0003-agent-runs-as-root.md)). The controller stamps the
  same image in `agent` mode onto the pods it generates.

The agent needs `CAP_NET_ADMIN` in its *effective* set to create `egr0` and add the floating IP; a
privileged container running as non-root has an empty effective set, so the controller sets
`runAsUser: 0`.

## One reconcile, in order

A pass runs when the CR's spec changes, when a Node this CR is a gateway for changes (the controller
watches Nodes and maps a labelled node back to its CR), when an owned StatefulSet changes, and every
**60 seconds** regardless — the flat requeue is what catches floating-IP reassignment and node churn.

1. **Deletion.** A CR with a deletion timestamp runs the finalizer
   ([step below](#finalizer-and-reclaim)) and returns. Otherwise the finalizer
   `egress.maarlab.dev/finalizer` is added on the first pass.
2. **Resolve the floating IPs.** In **managed** mode, for each region and each index `0..count-1`,
   `EnsureManaged` lists Hetzner floating IPs by label (`managed-by`, `egress-gateway`,
   `egress-region`, `egress-index`) and **adopts the match or creates one**, never recreating an
   address that already exists ([ADR-0006](decisions/0006-managed-floating-ips-adopt-never-recreate.md)).
   In **BYO** mode, each address in `spec.floatingIPs` is looked up and grouped by its
   API-reported home location. Either way the result is a list of regions, each with its ordered
   floating IPs.
3. **Ensure one StatefulSet per region.** Each region gets `egress-agent-<cr>-<location>` with
   `replicas = len(ips)`, pinned with a `nodeSelector` on `topology.kubernetes.io/region=<location>`
   and host-level pod anti-affinity, so the *N* agents land on *N* distinct nodes in that location —
   a floating IP only assigns to a server in its home location
   ([ADR-0007](decisions/0007-one-statefulset-per-region.md)). Each pod carries its region's
   floating-IP list as `id:addr,...`; the agent indexes it by its StatefulSet ordinal. StatefulSets
   for regions no longer in the spec are deleted.
4. **Ensure the backend object.** The selected backend renders the egress redirect
   ([ADR-0002](decisions/0002-cilium-default-host-routing-fallback.md)). `cilium` server-side-applies
   a `CiliumEgressGatewayPolicy` selecting `spec.podSelector`, with `destinationCIDRs` defaulting to
   `0.0.0.0/0` minus the cluster-internal (IPv4) ranges, and one `egressGateways` entry per floating IP
   in `egressIP` mode, each `nodeSelector` pinned to the node that owns its address
   ([ADR-0009](decisions/0009-cilium-egressip-not-interface.md)). Cilium spreads the pods across the
   entries whose node exists and fails over between them. `host-routing` renders nothing
   cluster-level; its data plane is entirely in the agent.
5. **GC node labels and publish status.** Every node labelled `egress.maarlab.dev/gateway=<cr>` is
   checked against the agent pods actually running and its `Ready` condition: a labelled node with no
   agent pod was recycled without graceful cleanup, and a node that is not `Ready` is dead or
   unreachable — its agent pod stays bound to it in the API until the Node object goes — so in both
   cases its `gateway` and `floating-ip` labels are removed, and the policy's other gateways take its
   pods. The survivors become `status.floatingIPs[]` (`address` from the node's `floating-ip` label,
   `assignedNode`), and `status.readyGateways` their count.
6. **Requeue after 60 s.**

Steps 2 and 4 are the controller's only writes to Hetzner and to the backend; steps 3 and 5 are its
only writes to Kubernetes besides the CR's status and finalizer. It creates no ConfigMaps, Services or
Secrets.

## The agent loop

Each agent, on the node it was scheduled to, runs `assertOnce` at startup and then every **30
seconds** (self-heal), and makes the node's state true and idempotent:

1. Read the node, derive the Hetzner **server ID** from `spec.providerID` (`hcloud://<id>`).
2. **Assign** its floating IP to that server (no-op if already there).
3. **`ensureInterface`**: create `egr0` as a **dummy** link if missing
   ([ADR-0005](decisions/0005-egr0-is-a-dummy-interface.md)), bring it up, and put the floating IP on
   it as a `/32`. It deliberately does **not** touch `rp_filter`
   ([ADR-0004](decisions/0004-never-raise-conf-all-rp-filter.md)).
4. For the `host-routing` backend only, program policy routing + an `nft` SNAT rule to the floating IP
   on the node's default-route uplink.
5. **Label the node** `egress.maarlab.dev/gateway=<cr>` and `egress.maarlab.dev/floating-ip=<addr>` —
   only while the node is `Ready`; otherwise remove the labels, so the agent agrees with the
   controller's GC instead of re-adding what it removed every 30 s.

On `SIGTERM` the agent removes the two node labels and deletes `egr0` (best-effort); ungraceful node
loss is covered by the controller's label GC in step 5 above.

## Status, not conditions

This controller publishes its state as a count, not a set of `Ready`-style conditions. The signal is
**`status.readyGateways`** — the number of floating IPs currently live on a node that is running an
agent — against the number you asked for (`sum(regions[].count)` or `len(floatingIPs)`). When they
match, every requested source IP is up; when `readyGateways` is lower, some region's agent has not
yet claimed its IP, and `status.floatingIPs[].assignedNode` shows which are placed. The addresses to
allow-list upstream are in `status.floatingIPs[].address`. The [runbook](runbook.md) reads the gap
between wanted and ready.

## Failure philosophy

The controller never recreates a floating IP it already created: an allow-listed address is an asset,
and losing it is worse than an orphaned IP ([ADR-0006](decisions/0006-managed-floating-ips-adopt-never-recreate.md)).
It does not run its own egress datapath, SNAT engine or overlay; it delegates the redirect to the CNI
and only guarantees that a stable, cloud-provisioned IP is live on a node and that the node is
labelled ([ADR-0001](decisions/0001-floating-ip-controller-not-datapath.md)). What it does without
asking is re-assign a floating IP after its node is recycled and drop node labels left behind by a
node that vanished.

## What the tests cover, and cannot

- **Unit tests** run the reconciler against a fake Kubernetes client and a fake Hetzner client:
  managed create/adopt, BYO grouping by location, per-region StatefulSets and their GC, the backend
  renderers, finalizer reclaim, and naming.
- **The datapath e2e** ([ADR-0010](decisions/0010-cilium-datapath-e2e-matrix.md)) runs the rendered
  policy against a real Cilium on kind, with simulated gateway nodes and an upstream outside the
  cluster, and asserts the source IPs the upstream sees: unlabelled pods from their node, labelled pods
  spread across every floating IP, and failover when a gateway goes away. It runs on every supported
  Cilium minor and the next pre-release, weekly as well as on changes.
- **Not covered automatically**: the agent's netlink path (`egr0`, addresses, `nft`) and the real
  Hetzner API client run only on a real node against the real API; they are exercised by deploying,
  not in CI (the e2e sets up `egr0` from the shell). Interactions with the rest of a production node —
  the one [ADR-0004](decisions/0004-never-raise-conf-all-rp-filter.md) is about — and any CNI other
  than Cilium are still only seen on a real cluster.
