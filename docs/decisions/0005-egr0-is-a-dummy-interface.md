# ADR-0005: egr0 is a dummy interface

**Status:** accepted, 2026-10-14

## Context

The agent needs an interface to hold the floating IP as a `/32`, so that Cilium's `interface`-mode
egress gateway (and the host-routing SNAT) uses the floating IP as the source address. The interface
only has to own an address; no traffic ingresses it.

During the crashloop investigation the `dummy` link type was briefly suspected of being unavailable and
the agent was switched to a `veth` pair as a workaround, which also meant adding
`machine.kernel.modules: dummy` to the Talos config and rolling the worker nodes. That turned out to be
a misdiagnosis — the real cause was capabilities ([ADR-0003](0003-agent-runs-as-root.md)), the `dummy`
module is built in on essentially all kernels, and the `veth` change and the kernel-module patch were
reverted.

## Decision

`egr0` (name configurable via `spec.egressInterface`, default `egr0`) is a **dummy** link. The agent
creates it if missing, brings it up, and puts the floating IP on it with `AddrReplace`. A dummy is the
idiomatic single-purpose interface for holding an address and needs no peer, no module install and no
routing of its own.

## Consequences

- No Talos `machine.kernel.modules` entry and no worker roll are needed for the interface; the
  `dummy`-module patch was removed.
- `egr0` carries only the floating IP; it is a black hole for anything routed *to* it, which is fine
  because the datapath only uses it as a SNAT source, and is why its `rp_filter` is irrelevant
  ([ADR-0004](0004-never-raise-conf-all-rp-filter.md)).
- Because `egr0` is a black hole, the cilium backend must **not** use Cilium's `interface` mode (which
  would forward egress traffic out `egr0`); it uses `egressIP` mode, where the floating IP only needs to
  be a local source address and the datapath routes out the real uplink
  ([ADR-0009](0009-cilium-egressip-not-interface.md)).
