//go:build e2e

/*
Copyright 2026 Maarlab Rethinking.
Licensed under the Apache License, Version 2.0 (the "License").
*/

// Package e2e checks the controller's Cilium backend against a real Cilium datapath, the one
// part the unit tests cannot see (ADR-0009: the policy was right, the datapath it produced
// on Cilium 1.20 was a black hole). It runs against the environment hack/e2e/up.sh brings up
// and is the test a new Cilium version has to pass before the platform moves to it.
package e2e

import (
	"context"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	egressv1alpha1 "github.com/maarlab-rethinking/hcloud-egress-gateway-controller/api/v1alpha1"
	"github.com/maarlab-rethinking/hcloud-egress-gateway-controller/internal/backend"
	"github.com/maarlab-rethinking/hcloud-egress-gateway-controller/internal/names"
)

// What hack/e2e/up.sh sets up: the upstream echo server and one simulated floating IP per
// worker, already on that worker's egr0 and routed back to it from the upstream.
const (
	crName    = "e2e"
	namespace = "default"
	upstream  = "http://198.18.0.200:8080/clientip"
	clients   = 24 // enough that each of 3 gateways gets a pod: P(one gets none) ≈ 3·(2/3)^24 < 1e-4
)

var gateways = []struct{ node, ip string }{
	{"heg-e2e-worker", "203.0.113.10"},
	{"heg-e2e-worker2", "203.0.113.11"},
	{"heg-e2e-worker3", "203.0.113.12"},
}

func TestCiliumEgress(t *testing.T) {
	ctx := context.Background()
	cfg, err := ctrl.GetConfig()
	if err != nil {
		t.Fatalf("kubeconfig: %v", err)
	}
	c, err := client.New(cfg, client.Options{})
	if err != nil {
		t.Fatal(err)
	}

	var fips []string
	for _, g := range gateways {
		fips = append(fips, g.ip)
		setGatewayLabels(ctx, t, c, g.node, g.ip)
	}
	t.Cleanup(func() {
		for _, g := range gateways {
			setGatewayLabels(ctx, t, c, g.node, "")
		}
	})

	cr := &egressv1alpha1.HetznerEgressGateway{
		ObjectMeta: metav1.ObjectMeta{Name: crName},
		Spec: egressv1alpha1.HetznerEgressGatewaySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{"egress-via": crName}},
		},
	}
	policy, err := backend.For(cr).Render(cr, fips)
	if err != nil || policy == nil {
		t.Fatalf("render: %v (policy %v)", err, policy)
	}
	if err := c.Apply(ctx, client.ApplyConfigurationFromUnstructured(policy),
		client.FieldOwner("e2e"), client.ForceOwnership); err != nil {
		t.Fatalf("apply policy: %v", err)
	}
	t.Cleanup(func() { _ = c.Delete(ctx, policy) })

	var opted []string
	for i := range clients {
		opted = append(opted, runClient(ctx, t, c, fmt.Sprintf("opted-%d", i), true))
	}
	plain := runClient(ctx, t, c, "plain", false)
	waitReady(ctx, t, c, append([]string{plain}, opted...))

	t.Run("a pod without the label egresses from its own node", func(t *testing.T) {
		var pod corev1.Pod
		if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: plain}, &pod); err != nil {
			t.Fatal(err)
		}
		if got := sourceIP(t, plain); got != pod.Status.HostIP {
			t.Fatalf("source IP = %q, want its node's %q", got, pod.Status.HostIP)
		}
	})

	t.Run("labelled pods egress from the floating IPs, spread across all of them", func(t *testing.T) {
		eventually(t, "every labelled pod egresses from a floating IP, and every floating IP is used", func() error {
			return expectSources(t, opted, fips, fips)
		})
	})

	t.Run("a gateway that goes away fails over to the others", func(t *testing.T) {
		gone := gateways[0]
		setGatewayLabels(ctx, t, c, gone.node, "")
		eventually(t, "no labelled pod egresses from "+gone.ip, func() error {
			return expectSources(t, opted, fips[1:], fips[1:])
		})
	})
}

// setGatewayLabels puts the labels the agent sets on a gateway node, or removes them when ip
// is empty, as the agent does on shutdown and the controller does for a dead node.
func setGatewayLabels(ctx context.Context, t *testing.T, c client.Client, node, ip string) {
	t.Helper()
	var n corev1.Node
	if err := c.Get(ctx, client.ObjectKey{Name: node}, &n); err != nil {
		t.Fatal(err)
	}
	patch := client.MergeFrom(n.DeepCopy())
	if ip == "" {
		delete(n.Labels, names.NodeLabelKey)
		delete(n.Labels, names.FloatingIPLabelKey)
	} else {
		n.Labels[names.NodeLabelKey] = crName
		n.Labels[names.FloatingIPLabelKey] = ip
	}
	if err := c.Patch(ctx, &n, patch); err != nil {
		t.Fatal(err)
	}
}

func runClient(ctx context.Context, t *testing.T, c client.Client, name string, optIn bool) string {
	t.Helper()
	labels := map[string]string{"app": "e2e-client"}
	if optIn {
		labels["egress-via"] = crName
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labels},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:    "client",
				Image:   "busybox:1.37",
				Command: []string{"sleep", "infinity"},
			}},
			TerminationGracePeriodSeconds: new(int64),
		},
	}
	if err := c.Create(ctx, pod); err != nil {
		t.Fatalf("create pod %s: %v", name, err)
	}
	t.Cleanup(func() { _ = c.Delete(context.Background(), pod) })
	return name
}

func waitReady(ctx context.Context, t *testing.T, c client.Client, pods []string) {
	t.Helper()
	err := wait.PollUntilContextTimeout(ctx, 2*time.Second, 3*time.Minute, true, func(ctx context.Context) (bool, error) {
		for _, name := range pods {
			var pod corev1.Pod
			if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &pod); err != nil {
				return false, err
			}
			if pod.Status.Phase != corev1.PodRunning {
				return false, nil
			}
		}
		return true, nil
	})
	if err != nil {
		t.Fatalf("client pods not running: %v", err)
	}
}

// sourceIP is the source address the upstream saw for a request from the pod, or the error.
func sourceIP(t *testing.T, pod string) string {
	t.Helper()
	out, err := exec.Command("kubectl", "exec", "-n", namespace, pod, "--",
		"wget", "-qO-", "-T", "3", upstream).CombinedOutput()
	if err != nil {
		return fmt.Sprintf("error: %v: %s", err, strings.TrimSpace(string(out)))
	}
	host, _, _ := strings.Cut(strings.TrimSpace(string(out)), ":")
	return host
}

// expectSources checks every pod egresses from one of allowed and that, together, they use
// every address in used.
func expectSources(t *testing.T, pods, allowed, used []string) error {
	t.Helper()
	got := make([]string, len(pods))
	var wg sync.WaitGroup
	for i, pod := range pods {
		wg.Go(func() { got[i] = sourceIP(t, pod) })
	}
	wg.Wait()

	seen := map[string]int{}
	for i, ip := range got {
		if !slices.Contains(allowed, ip) {
			return fmt.Errorf("pod %s egresses from %q, want one of %v", pods[i], ip, allowed)
		}
		seen[ip]++
	}
	for _, ip := range used {
		if seen[ip] == 0 {
			return fmt.Errorf("no pod egresses from %s (spread: %v)", ip, seen)
		}
	}
	t.Logf("spread across floating IPs: %v", seen)
	return nil
}

// eventually retries check until it passes: Cilium programs a policy (and reacts to a
// gateway going away) asynchronously, within seconds.
func eventually(t *testing.T, what string, check func() error) {
	t.Helper()
	var last error
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if last = check(); last == nil {
			return
		}
		time.Sleep(3 * time.Second)
	}
	t.Fatalf("%s: %v", what, last)
}
