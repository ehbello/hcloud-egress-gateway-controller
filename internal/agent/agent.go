/*
Copyright 2026 Maarlab Rethinking.
Licensed under the Apache License, Version 2.0 (the "License").
*/

// Package agent runs on each gateway node (StatefulSet pod, hostNetwork + privileged).
// It owns exactly one floating IP (its StatefulSet ordinal indexes the IP list) and
// keeps it live on the node it currently runs on.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/vishvananda/netlink"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/maarlab-rethinking/hcloud-egress-gateway-controller/internal/hcloud"
	"github.com/maarlab-rethinking/hcloud-egress-gateway-controller/internal/names"
)

type config struct {
	podName  string
	nodeName string
	cr       string
	iface    string
	backend  string
	ipID     int64
	ipAddr   string
	ipMask   string // "32" (ipv4) or "128" (ipv6)
	token    string
}

// Run is invoked by `... agent`.
func Run(args []string) {
	ctrl := zap.New(zap.UseDevMode(false))
	log.SetLogger(ctrl)
	l := log.Log.WithName("agent")

	cfg, err := loadConfig()
	if err != nil {
		l.Error(err, "invalid agent configuration")
		os.Exit(1)
	}
	l = l.WithValues("node", cfg.nodeName, "cr", cfg.cr, "ip", cfg.ipAddr)

	k8s, err := k8sClient()
	if err != nil {
		l.Error(err, "kubernetes client")
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	hc := hcloud.New(cfg.token)

	if err := cfg.assertOnce(ctx, l, k8s, hc); err != nil {
		l.Error(err, "initial assert failed")
		os.Exit(1)
	}
	l.Info("egress gateway active")

	// Re-assert periodically (self-heal) until told to stop.
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			cfg.cleanup(l, k8s)
			return
		case <-ticker.C:
			if err := cfg.assertOnce(context.Background(), l, k8s, hc); err != nil {
				l.Error(err, "re-assert failed")
			}
		}
	}
}

// assertOnce makes the desired node state true: FIP assigned → on the egress interface
// → node labelled. Idempotent.
func (c *config) assertOnce(ctx context.Context, l logr, k8s kubernetes.Interface, hc hcloud.Client) error {
	node, err := k8s.CoreV1().Nodes().Get(ctx, c.nodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get node: %w", err)
	}
	serverID, err := hcloud.ServerIDFromProviderID(node.Spec.ProviderID)
	if err != nil {
		return err
	}
	if err := hc.Assign(ctx, c.ipID, serverID); err != nil {
		return fmt.Errorf("assign floating IP: %w", err)
	}
	if err := c.ensureInterface(); err != nil {
		return fmt.Errorf("ensure interface: %w", err)
	}
	if c.backend == "host-routing" {
		if err := c.programHostRouting(l); err != nil {
			return fmt.Errorf("host-routing: %w", err)
		}
	}
	if err := c.labelNode(ctx, k8s); err != nil {
		return fmt.Errorf("label node: %w", err)
	}
	return nil
}

// ensureInterface creates the egress link (if missing) and puts the floating IP on it,
// so interface-mode SNAT (or host routing) uses the floating IP as source.
//
// The link is a dummy (the idiomatic single interface for holding an IP; the module is
// built-in on essentially all kernels). Creating a link in the host netns needs
// CAP_NET_ADMIN in the process's EFFECTIVE set — i.e. the pod must run as ROOT: a
// privileged container running as non-root has an empty effective-cap set and gets
// EPERM. The controller runs the agent with runAsUser 0 + privileged (+ spc_t SELinux).
func (c *config) ensureInterface() error {
	link, err := netlink.LinkByName(c.iface)
	if err != nil {
		attrs := netlink.NewLinkAttrs()
		attrs.Name = c.iface
		if err := netlink.LinkAdd(&netlink.Dummy{LinkAttrs: attrs}); err != nil {
			return fmt.Errorf("create dummy %s: %w", c.iface, err)
		}
		if link, err = netlink.LinkByName(c.iface); err != nil {
			return err
		}
	}
	if err := netlink.LinkSetUp(link); err != nil {
		return fmt.Errorf("link up %s: %w", c.iface, err)
	}
	addr, err := netlink.ParseAddr(c.ipAddr + "/" + c.ipMask)
	if err != nil {
		return err
	}
	if err := netlink.AddrReplace(link, addr); err != nil {
		return fmt.Errorf("add %s to %s: %w", c.ipAddr, c.iface, err)
	}
	// Deliberately do NOT touch rp_filter. The effective reverse-path filter of any
	// interface is max(conf.all.rp_filter, conf.<iface>.rp_filter), so raising conf.all
	// to 1/2 forces RPF onto the CNI's own interfaces — Cilium's cilium_host and the
	// lxc* pod veths require rp_filter=0 — and silently drops pod traffic on the gateway
	// node (pods lose ClusterIP/pod-to-pod connectivity while the host netns still works).
	// egr0 is a dummy that only holds the floating IP; nothing ingresses it, so no
	// rp_filter tuning is needed for egress SNAT to work.
	return nil
}

// programHostRouting SNATs pod egress to the floating IP without a CNI egress feature.
// Requires `nft` in the image (the default cilium backend does not need this).
func (c *config) programHostRouting(l logr) error {
	uplink, err := defaultRouteLink()
	if err != nil {
		return err
	}
	rules := [][]string{
		{"add", "table", "ip", "egress"},
		{"add", "chain", "ip", "egress", "postrouting",
			"{", "type", "nat", "hook", "postrouting", "priority", "100", ";", "}"},
		{"add", "rule", "ip", "egress", "postrouting",
			"oifname", uplink, "ip", "saddr", "!=", c.ipAddr, "counter", "snat", "to", c.ipAddr},
	}
	for _, r := range rules {
		if out, err := runNft(r...); err != nil {
			l.Info("nft rule failed (host-routing)", "args", strings.Join(r, " "), "out", out, "err", err.Error())
		}
	}
	return nil
}

func (c *config) labelNode(ctx context.Context, k8s kubernetes.Interface) error {
	patch := map[string]interface{}{"metadata": map[string]interface{}{"labels": map[string]string{
		names.NodeLabelKey:       c.cr,
		names.FloatingIPLabelKey: c.ipAddr,
	}}}
	b, _ := json.Marshal(patch)
	_, err := k8s.CoreV1().Nodes().Patch(ctx, c.nodeName, types.MergePatchType, b, metav1.PatchOptions{})
	return err
}

// cleanup runs on graceful shutdown: drop the node labels and tear the interface down
// (best-effort; the controller GC covers ungraceful node loss).
func (c *config) cleanup(l logr, k8s kubernetes.Interface) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	patch := map[string]interface{}{"metadata": map[string]interface{}{"labels": map[string]interface{}{
		names.NodeLabelKey:       nil,
		names.FloatingIPLabelKey: nil,
	}}}
	b, _ := json.Marshal(patch)
	if _, err := k8s.CoreV1().Nodes().Patch(ctx, c.nodeName, types.MergePatchType, b, metav1.PatchOptions{}); err != nil {
		l.Info("cleanup: unlabel node failed", "err", err.Error())
	}
	if link, err := netlink.LinkByName(c.iface); err == nil {
		_ = netlink.LinkDel(link)
	}
	l.Info("cleaned up egress gateway")
}

// --- config / helpers ---

func loadConfig() (*config, error) {
	c := &config{
		podName:  os.Getenv("POD_NAME"),
		nodeName: os.Getenv("NODE_NAME"),
		cr:       os.Getenv("EGRESS_CR"),
		iface:    os.Getenv("EGRESS_INTERFACE"),
		backend:  os.Getenv("EGRESS_BACKEND"),
		token:    os.Getenv("HCLOUD_TOKEN"),
	}
	if c.iface == "" {
		c.iface = "egr0"
	}
	for _, must := range []struct{ k, v string }{
		{"POD_NAME", c.podName}, {"NODE_NAME", c.nodeName},
		{"EGRESS_CR", c.cr}, {"HCLOUD_TOKEN", c.token},
	} {
		if must.v == "" {
			return nil, fmt.Errorf("%s is required", must.k)
		}
	}

	ordinal, err := ordinalOf(c.podName)
	if err != nil {
		return nil, err
	}
	list := parseIPList(os.Getenv("EGRESS_FLOATING_IPS"))
	if ordinal >= len(list) {
		return nil, fmt.Errorf("ordinal %d out of range for %d floating IPs", ordinal, len(list))
	}
	c.ipID, c.ipAddr = list[ordinal].id, list[ordinal].addr
	c.ipMask = "32"
	if strings.Contains(c.ipAddr, ":") {
		c.ipMask = "128"
	}
	return c, nil
}

type ipEntry struct {
	id   int64
	addr string
}

func parseIPList(s string) []ipEntry {
	var out []ipEntry
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, addr, ok := strings.Cut(part, ":")
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			continue
		}
		out = append(out, ipEntry{id: n, addr: addr})
	}
	return out
}

// ordinalOf extracts the StatefulSet ordinal from the pod name suffix ("...-<n>").
func ordinalOf(podName string) (int, error) {
	i := strings.LastIndex(podName, "-")
	if i < 0 {
		return 0, fmt.Errorf("pod name %q has no ordinal suffix", podName)
	}
	return strconv.Atoi(podName[i+1:])
}

func k8sClient() (kubernetes.Interface, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(cfg)
}

// logr is the minimal logger surface used here (matches controller-runtime's logr.Logger).
type logr interface {
	Info(msg string, kv ...interface{})
	Error(err error, msg string, kv ...interface{})
}
