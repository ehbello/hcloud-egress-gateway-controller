/*
Copyright 2026 Maarlab Rethinking.
Licensed under the Apache License, Version 2.0 (the "License").
*/

package controller

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	egressv1alpha1 "github.com/maarlab-rethinking/hcloud-egress-gateway-controller/api/v1alpha1"
	"github.com/maarlab-rethinking/hcloud-egress-gateway-controller/internal/backend"
	"github.com/maarlab-rethinking/hcloud-egress-gateway-controller/internal/hcloud"
	"github.com/maarlab-rethinking/hcloud-egress-gateway-controller/internal/names"
)

const crLabelKey = "egress.maarlab.dev/cr"

// Reconciler turns a HetznerEgressGateway into a running egress path.
type Reconciler struct {
	client.Client
	Scheme          *runtime.Scheme
	HCloud          hcloud.Client
	AgentImage      string
	AgentSA         string // ServiceAccount the agent pods run under (has node RBAC)
	Namespace       string // where agent StatefulSets are created (controller ns)
	TokenSecretName string
	TokenSecretKey  string
}

// Reconcile drives the CR to its desired state:
//
//  1. Resolve the floating IP set (BYO adopt / managed create-if-missing, keyed by
//     label, never recreated).
//  2. Ensure the agent StatefulSet (replicas = len(IPs), anti-affinity, ordinal→IP env).
//  3. Ensure the egress-redirect backend resource (cilium policy / none for host-routing).
//  4. GC node labels of recycled gateways + publish status from live labels.
//  5. Finalizer honoring spec.managed.reclaimPolicy on deletion.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := log.FromContext(ctx)

	var heg egressv1alpha1.HetznerEgressGateway
	if err := r.Get(ctx, req.NamespacedName, &heg); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !heg.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, r.finalize(ctx, &heg)
	}
	if controllerutil.AddFinalizer(&heg, names.Finalizer) {
		if err := r.Update(ctx, &heg); err != nil {
			return ctrl.Result{}, err
		}
	}

	regions, err := r.resolveIPs(ctx, &heg)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("resolve floating IPs: %w", err)
	}
	if err := r.ensureStatefulSets(ctx, &heg, regions); err != nil {
		return ctrl.Result{}, fmt.Errorf("ensure statefulsets: %w", err)
	}
	if err := r.ensureBackend(ctx, &heg); err != nil {
		return ctrl.Result{}, fmt.Errorf("ensure backend: %w", err)
	}
	if err := r.gcAndStatus(ctx, &heg); err != nil {
		return ctrl.Result{}, err
	}

	total := 0
	for _, reg := range regions {
		total += len(reg.ips)
	}
	l.Info("reconciled", "floatingIPs", total, "regions", len(regions), "ready", heg.Status.ReadyGateways)
	return ctrl.Result{RequeueAfter: 60 * time.Second}, nil
}

// regionIPs is a Hetzner location and the floating IPs homed there.
type regionIPs struct {
	location string
	ips      []hcloud.FloatingIP
}

func (r *Reconciler) resolveIPs(ctx context.Context, heg *egressv1alpha1.HetznerEgressGateway) ([]regionIPs, error) {
	if m := heg.Spec.Managed; m != nil {
		out := make([]regionIPs, 0, len(m.Regions))
		for _, reg := range m.Regions {
			ri := regionIPs{location: reg.Location}
			for i := 0; i < reg.Count; i++ {
				fip, err := r.HCloud.EnsureManaged(ctx, heg.Name, reg.Location, i, m.Type, m.Labels)
				if err != nil {
					return nil, err
				}
				ri.ips = append(ri.ips, fip)
			}
			out = append(out, ri)
		}
		return out, nil
	}
	// BYO: adopt each address and group by its (API-discovered) home location so each
	// region's agents can be pinned to nodes there.
	byLoc := map[string]*regionIPs{}
	var order []string
	for _, addr := range heg.Spec.FloatingIPs {
		fip, err := r.HCloud.GetByAddress(ctx, addr)
		if err != nil {
			return nil, err
		}
		loc := fip.Location
		if _, ok := byLoc[loc]; !ok {
			byLoc[loc] = &regionIPs{location: loc}
			order = append(order, loc)
		}
		byLoc[loc].ips = append(byLoc[loc].ips, fip)
	}
	out := make([]regionIPs, 0, len(order))
	for _, loc := range order {
		out = append(out, *byLoc[loc])
	}
	return out, nil
}

// ensureStatefulSets ensures one agent StatefulSet per region (pinned to that region's
// nodes) and deletes StatefulSets for regions no longer in the spec.
func (r *Reconciler) ensureStatefulSets(ctx context.Context, heg *egressv1alpha1.HetznerEgressGateway, regions []regionIPs) error {
	wanted := map[string]bool{}
	for _, reg := range regions {
		wanted[names.AgentName(heg.Name, reg.location)] = true
		if err := r.ensureStatefulSet(ctx, heg, reg); err != nil {
			return err
		}
	}
	var list appsv1.StatefulSetList
	if err := r.List(ctx, &list, client.InNamespace(r.Namespace),
		client.MatchingLabels{crLabelKey: heg.Name}); err != nil {
		return err
	}
	for i := range list.Items {
		s := &list.Items[i]
		if !wanted[s.Name] {
			if err := r.Delete(ctx, s); err != nil && !apierrors.IsNotFound(err) {
				return err
			}
		}
	}
	return nil
}

func (r *Reconciler) ensureStatefulSet(ctx context.Context, heg *egressv1alpha1.HetznerEgressGateway, reg regionIPs) error {
	name := names.AgentName(heg.Name, reg.location)
	replicas := int32(len(reg.ips))
	sel := map[string]string{
		"app.kubernetes.io/name": "egress-agent",
		crLabelKey:               heg.Name,
		names.RegionSelectorKey:  reg.location,
	}

	// Ordered "id:addr" list the agent indexes by its StatefulSet ordinal.
	parts := make([]string, len(reg.ips))
	for i, ip := range reg.ips {
		parts[i] = fmt.Sprintf("%d:%s", ip.ID, ip.Address)
	}
	ipList := strings.Join(parts, ",")

	sts := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: r.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, sts, func() error {
		priv := true
		sts.Labels = map[string]string{crLabelKey: heg.Name, names.RegionSelectorKey: reg.location}
		sts.Spec.Replicas = &replicas
		sts.Spec.ServiceName = name
		sts.Spec.Selector = &metav1.LabelSelector{MatchLabels: sel}
		sts.Spec.Template.ObjectMeta.Labels = sel
		sts.Spec.Template.Spec.HostNetwork = true
		sts.Spec.Template.Spec.DNSPolicy = corev1.DNSClusterFirstWithHostNet
		sts.Spec.Template.Spec.ServiceAccountName = r.AgentSA
		// Pin this region's agents to nodes in that location — a floating IP only
		// assigns to a server in its home location.
		sts.Spec.Template.Spec.NodeSelector = map[string]string{names.RegionLabelKey: reg.location}
		sts.Spec.Template.Spec.Affinity = &corev1.Affinity{PodAntiAffinity: &corev1.PodAntiAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{{
				TopologyKey:   "kubernetes.io/hostname",
				LabelSelector: &metav1.LabelSelector{MatchLabels: sel},
			}},
		}}
		sts.Spec.Template.Spec.Containers = []corev1.Container{{
			Name:            "agent",
			Image:           r.AgentImage,
			Args:            []string{"agent"},
			SecurityContext: &corev1.SecurityContext{Privileged: &priv},
			Env: []corev1.EnvVar{
				{Name: "POD_NAME", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"}}},
				{Name: "NODE_NAME", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "spec.nodeName"}}},
				{Name: "EGRESS_CR", Value: heg.Name},
				{Name: "EGRESS_INTERFACE", Value: ifaceOf(heg)},
				{Name: "EGRESS_BACKEND", Value: string(backendOf(heg))},
				{Name: "EGRESS_FLOATING_IPS", Value: ipList},
				{Name: "HCLOUD_TOKEN", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: r.TokenSecretName},
					Key:                  r.TokenSecretKey,
				}}},
			},
		}}
		return controllerutil.SetControllerReference(heg, sts, r.Scheme)
	})
	return err
}

func (r *Reconciler) ensureBackend(ctx context.Context, heg *egressv1alpha1.HetznerEgressGateway) error {
	obj, err := backend.For(heg).Render(heg)
	if err != nil || obj == nil {
		return err
	}
	if err := controllerutil.SetControllerReference(heg, obj, r.Scheme); err != nil {
		return err
	}
	return r.Patch(ctx, obj, client.Apply,
		client.FieldOwner("hcloud-egress-gateway-controller"), client.ForceOwnership)
}

func (r *Reconciler) gcAndStatus(ctx context.Context, heg *egressv1alpha1.HetznerEgressGateway) error {
	var nodes corev1.NodeList
	if err := r.List(ctx, &nodes, client.MatchingLabels{names.NodeLabelKey: heg.Name}); err != nil {
		return err
	}
	var pods corev1.PodList
	if err := r.List(ctx, &pods, client.InNamespace(r.Namespace),
		client.MatchingLabels{crLabelKey: heg.Name}); err != nil {
		return err
	}
	agentNodes := map[string]bool{}
	for i := range pods.Items {
		if n := pods.Items[i].Spec.NodeName; n != "" {
			agentNodes[n] = true
		}
	}

	var status []egressv1alpha1.FloatingIPStatus
	for i := range nodes.Items {
		n := &nodes.Items[i]
		if !agentNodes[n.Name] {
			// Node recycled without graceful cleanup → drop the stale labels.
			patch := client.MergeFrom(n.DeepCopy())
			delete(n.Labels, names.NodeLabelKey)
			delete(n.Labels, names.FloatingIPLabelKey)
			if err := r.Patch(ctx, n, patch); err != nil {
				return err
			}
			continue
		}
		status = append(status, egressv1alpha1.FloatingIPStatus{
			Address:      n.Labels[names.FloatingIPLabelKey],
			AssignedNode: n.Name,
		})
	}

	heg.Status.FloatingIPs = status
	heg.Status.ReadyGateways = len(status)
	return r.Status().Update(ctx, heg)
}

func (r *Reconciler) finalize(ctx context.Context, heg *egressv1alpha1.HetznerEgressGateway) error {
	if !controllerutil.ContainsFinalizer(heg, names.Finalizer) {
		return nil
	}
	if m := heg.Spec.Managed; m != nil && m.ReclaimPolicy == egressv1alpha1.ReclaimDelete {
		for _, reg := range m.Regions {
			for i := 0; i < reg.Count; i++ {
				fip, err := r.HCloud.EnsureManaged(ctx, heg.Name, reg.Location, i, m.Type, m.Labels)
				if err != nil {
					return err
				}
				if err := r.HCloud.Delete(ctx, fip.ID); err != nil {
					return err
				}
			}
		}
	}
	var nodes corev1.NodeList
	if err := r.List(ctx, &nodes, client.MatchingLabels{names.NodeLabelKey: heg.Name}); err == nil {
		for i := range nodes.Items {
			n := &nodes.Items[i]
			patch := client.MergeFrom(n.DeepCopy())
			delete(n.Labels, names.NodeLabelKey)
			delete(n.Labels, names.FloatingIPLabelKey)
			_ = r.Patch(ctx, n, patch)
		}
	}
	controllerutil.RemoveFinalizer(heg, names.Finalizer)
	return r.Update(ctx, heg)
}

// SetupWithManager wires the controller, its owned StatefulSet, and node watches.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.HCloud == nil {
		return fmt.Errorf("hcloud client not configured")
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&egressv1alpha1.HetznerEgressGateway{}).
		Owns(&appsv1.StatefulSet{}).
		Watches(&corev1.Node{}, handler.EnqueueRequestsFromMapFunc(r.nodeToCRs)).
		Complete(r)
}

// nodeToCRs re-reconciles the CR a node is (was) a gateway for, on node churn.
func (r *Reconciler) nodeToCRs(_ context.Context, obj client.Object) []ctrl.Request {
	if cr, ok := obj.GetLabels()[names.NodeLabelKey]; ok && cr != "" {
		return []ctrl.Request{{NamespacedName: types.NamespacedName{Name: cr}}}
	}
	return nil
}

func ifaceOf(heg *egressv1alpha1.HetznerEgressGateway) string {
	if heg.Spec.EgressInterface != "" {
		return heg.Spec.EgressInterface
	}
	return "egr0"
}

func backendOf(heg *egressv1alpha1.HetznerEgressGateway) egressv1alpha1.EgressBackend {
	if heg.Spec.Backend != "" {
		return heg.Spec.Backend
	}
	return egressv1alpha1.BackendCilium
}

// Run starts the controller manager (leader-elected). Invoked by `... controller`.
func Run(args []string) {
	fs := flag.NewFlagSet("controller", flag.ExitOnError)
	var (
		metricsAddr = fs.String("metrics-bind-address", ":8080", "metrics endpoint")
		probeAddr   = fs.String("health-probe-bind-address", ":8081", "health probe endpoint")
		leaderElect = fs.Bool("leader-elect", false, "enable leader election")
		agentImage  = fs.String("agent-image", "", "image for the per-node agent pods")
	)
	_ = fs.Parse(args)

	ctrl.SetLogger(zap.New(zap.UseDevMode(false)))
	l := ctrl.Log.WithName("setup")

	token := os.Getenv("HCLOUD_TOKEN")
	if token == "" {
		l.Error(fmt.Errorf("HCLOUD_TOKEN is empty"), "missing Hetzner Cloud token")
		os.Exit(1)
	}
	image := *agentImage
	if env := os.Getenv("AGENT_IMAGE"); image == "" && env != "" {
		image = env
	}

	scheme := runtime.NewScheme()
	utilruntimeMust(clientgoscheme.AddToScheme(scheme))
	utilruntimeMust(egressv1alpha1.AddToScheme(scheme))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: *metricsAddr},
		HealthProbeBindAddress: *probeAddr,
		LeaderElection:         *leaderElect,
		LeaderElectionID:       "hcloud-egress-gateway-controller.egress.maarlab.dev",
	})
	if err != nil {
		l.Error(err, "unable to start manager")
		os.Exit(1)
	}

	r := &Reconciler{
		Client:          mgr.GetClient(),
		Scheme:          mgr.GetScheme(),
		HCloud:          hcloud.New(token),
		AgentImage:      image,
		AgentSA:         envOr("AGENT_SERVICE_ACCOUNT", "hcloud-egress-gateway-controller"),
		Namespace:       envOr("POD_NAMESPACE", "egress-system"),
		TokenSecretName: envOr("HCLOUD_TOKEN_SECRET_NAME", "hcloud-egress-token"),
		TokenSecretKey:  envOr("HCLOUD_TOKEN_SECRET_KEY", "token"),
	}
	if err := r.SetupWithManager(mgr); err != nil {
		l.Error(err, "unable to set up controller")
		os.Exit(1)
	}

	l.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		l.Error(err, "manager exited with error")
		os.Exit(1)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func utilruntimeMust(err error) {
	if err != nil {
		panic(err)
	}
}
