# ADR-0003: The agent runs as root, not merely privileged

**Status:** accepted, 2026-10-14

## Context

The agent creates `egr0`, adds the floating IP to it and (for host-routing) programs `nft` — all of
which need `CAP_NET_ADMIN`. The image is distroless and its default user is non-root (`USER 65532`).
The agent StatefulSet was given `privileged: true`, which on most runtimes is enough; here it crashed
on the first netlink call with `operation not permitted`.

The cause took two wrong turns to find (blamed on the `dummy` kernel module, then on SELinux) before
the evidence settled it: a **privileged container running as non-root has an empty *effective*
capability set**. `privileged` grants the capabilities in the permitted set, but a non-root process
does not raise them into the effective set on its own, so `CAP_NET_ADMIN` is held but not active, and
`ip link add` gets EPERM. A debug pod with `privileged + runAsUser 65532` showed `CapEff:
0000000000000000`; with `runAsUser 0` it showed the full set and the whole interface sequence
succeeded.

## Decision

The controller stamps the agent container with `runAsUser: 0` and `runAsGroup: 0` in addition to
`privileged: true`. It also sets `seLinuxOptions.type: spc_t` — a no-op under permissive SELinux (as on
Talos) but required if SELinux is enforcing. There is a unit test asserting the agent container runs as
root so a refactor cannot quietly drop it.

## Consequences

- The agent namespace must allow privileged, root pods (`egress-system` is labelled
  pod-security `privileged`). The controller itself stays non-root.
- A cluster policy that forces non-root (OpenShift SCC, a restrictive PSA, Kyverno) will reintroduce the
  crash; the [runbook](../runbook.md#an-agent-pod-crashloops) names the symptom.
- The misdiagnoses are recorded so they are not repeated: it was never the `dummy` module (built in) nor
  SELinux (permissive here). Earlier debug pods "worked" only because they used the Cilium image, which
  runs as root.
