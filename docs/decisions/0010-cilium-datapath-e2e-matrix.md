# ADR-0010: a Cilium datapath e2e on kind, run against every supported minor and the next pre-release

**Status:** accepted, 2026-10-22

## Context

The `cilium` backend's contract with Cilium is the `CiliumEgressGatewayPolicy` it renders, and the unit
tests check that object field by field. That was not enough: on Cilium 1.20 the interface-mode policy
was still accepted and still programmed the right egress IP, and the datapath sent the traffic into the
`egr0` dummy ([ADR-0009](0009-cilium-egressip-not-interface.md)). The break was in what Cilium *did*
with a valid policy, it only showed on a cluster, and the first cluster to run 1.20.3 was
oceanflights-dev, where it blocked every promotion until the controller was fixed.

Cilium ships a minor about every six months with pre-releases (`-pre.N`, `-rc.N`) for weeks before it,
and a patch release of each supported minor every few weeks. Waiting for the platform to adopt a version
to find out is the failure this ADR exists to avoid.

## Decision

An end-to-end test of the datapath runs on kind with a real Cilium:

- **Environment** (`hack/e2e/up.sh`): a kind cluster without kube-proxy, on its own docker network in
  198.18.0.0/24 (kind's default network is in 172.16.0.0/12, which the controller always excludes, so
  the default policy could not be tested on it), Cilium with `egressGateway.enabled`, an upstream echo
  server outside the cluster that reports the source IP it sees, and three workers set up as the agent
  sets up a gateway — an `egr0` dummy holding a TEST-NET-3 "floating IP" `/32` — with the route back to
  each address on the upstream standing in for the Hetzner floating-IP assignment.
- **Test** (`test/e2e`, build tag `e2e`): labels the workers as the agent does, renders the policy with
  the controller's own backend and applies it, then asserts what the upstream sees: a pod without the
  label egresses from its node's IP; 24 labelled pods egress only from the floating IPs and use all three;
  removing one gateway's labels moves its pods onto the other two.
- **Matrix** (`.github/workflows/e2e.yml`): resolved on every run from the GitHub releases of
  cilium/cilium by `hack/e2e/cilium-versions.sh` — the newest patch of every minor from 1.18 (the
  backend's minimum) must pass, and the newest pre-release of the next minor runs as experimental, so
  its failure warns without turning the build red. It runs on pull requests, on `main` and **weekly**;
  a scheduled failure opens an issue.

What it does not cover, on purpose: the agent's Hetzner calls and its netlink code (the shell stands in
for both), and the `host-routing` backend.

## Consequences

- A Cilium release that breaks the egress path is seen within a week of being published, and for a new
  minor during its pre-releases, before the platform moves to it. The test reproduces the 1.20 break: the
  pre-fix interface-mode policy egresses on 1.19.9 and times out on 1.20.3.
- Raising the platform's Cilium is gated on this matrix being green for that version; the compatibility
  range in [operations](../operations.md#compatibility) is the matrix's stable rows.
- The matrix follows Cilium on its own: when 1.21 is released, 1.21.z becomes a required row and the
  next pre-release the experimental one. Raising the minimum minor is a one-line change in
  `cilium-versions.sh`, with the docs.
- Each row costs a kind cluster and a Cilium install, about five minutes on a GitHub runner.
