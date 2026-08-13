/*
Copyright 2026 Maarlab Rethinking.
Licensed under the Apache License, Version 2.0 (the "License").
*/

// Package hcloud wraps the Hetzner Cloud API for floating-IP lifecycle + assignment.
package hcloud

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	hcloudapi "github.com/hetznercloud/hcloud-go/v2/hcloud"
)

// Managed label keys the controller stamps on floating IPs it creates, so they are
// re-adopted (never duplicated/recreated) across CR recreation.
const (
	LabelManagedBy = "managed-by"
	LabelGateway   = "egress-gateway"
	LabelRegion    = "egress-region"
	LabelIndex     = "egress-index"

	ManagedByValue = "hcloud-egress-gateway-controller"
)

// FloatingIP is the minimal view the controller/agent need.
type FloatingIP struct {
	ID           int64
	Address      string
	Location     string // Hetzner home location (e.g. "nbg1")
	AssignedToID int64  // 0 = unassigned
}

// Client is the Hetzner Cloud surface this project uses.
type Client interface {
	EnsureManaged(ctx context.Context, gateway, location string, index int, ipType string, labels map[string]string) (FloatingIP, error)
	GetByAddress(ctx context.Context, address string) (FloatingIP, error)
	Assign(ctx context.Context, ipID, serverID int64) error
	Delete(ctx context.Context, ipID int64) error
}

type apiClient struct{ c *hcloudapi.Client }

// New returns a Client backed by the Hetzner Cloud API.
func New(token string) Client {
	return &apiClient{c: hcloudapi.NewClient(hcloudapi.WithToken(token))}
}

func toView(fip *hcloudapi.FloatingIP) FloatingIP {
	v := FloatingIP{ID: fip.ID, Address: fip.IP.String()}
	if fip.HomeLocation != nil {
		v.Location = fip.HomeLocation.Name
	}
	if fip.Server != nil {
		v.AssignedToID = fip.Server.ID
	}
	return v
}

func managedSelector(gateway, location string, index int) string {
	return fmt.Sprintf("%s==%s,%s==%s,%s==%s,%s==%d",
		LabelManagedBy, ManagedByValue, LabelGateway, gateway,
		LabelRegion, location, LabelIndex, index)
}

// EnsureManaged adopts the floating IP labelled for (gateway, location, index) or
// creates it in that location if none exists. It NEVER recreates: an existing labelled
// IP is reused as-is so the (allow-listed) address stays stable.
func (a *apiClient) EnsureManaged(ctx context.Context, gateway, location string, index int, ipType string, labels map[string]string) (FloatingIP, error) {
	existing, err := a.c.FloatingIP.AllWithOpts(ctx, hcloudapi.FloatingIPListOpts{
		ListOpts: hcloudapi.ListOpts{LabelSelector: managedSelector(gateway, location, index)},
	})
	if err != nil {
		return FloatingIP{}, fmt.Errorf("list floating IPs: %w", err)
	}
	if len(existing) > 0 {
		return toView(existing[0]), nil
	}

	all := map[string]string{
		LabelManagedBy: ManagedByValue,
		LabelGateway:   gateway,
		LabelRegion:    location,
		LabelIndex:     strconv.Itoa(index),
	}
	for k, v := range labels {
		all[k] = v
	}
	t := hcloudapi.FloatingIPTypeIPv4
	if strings.EqualFold(ipType, "ipv6") {
		t = hcloudapi.FloatingIPTypeIPv6
	}
	name := fmt.Sprintf("egress-%s-%s-%d", gateway, location, index)
	res, _, err := a.c.FloatingIP.Create(ctx, hcloudapi.FloatingIPCreateOpts{
		Type:         t,
		HomeLocation: &hcloudapi.Location{Name: location},
		Name:         &name,
		Labels:       all,
	})
	if err != nil {
		return FloatingIP{}, fmt.Errorf("create floating IP: %w", err)
	}
	return toView(res.FloatingIP), nil
}

func (a *apiClient) GetByAddress(ctx context.Context, address string) (FloatingIP, error) {
	all, err := a.c.FloatingIP.All(ctx)
	if err != nil {
		return FloatingIP{}, fmt.Errorf("list floating IPs: %w", err)
	}
	for _, fip := range all {
		if fip.IP.String() == address {
			return toView(fip), nil
		}
	}
	return FloatingIP{}, fmt.Errorf("floating IP %s not found", address)
}

// Assign points the floating IP at the server. Idempotent: a no-op if already there.
func (a *apiClient) Assign(ctx context.Context, ipID, serverID int64) error {
	fip, _, err := a.c.FloatingIP.GetByID(ctx, ipID)
	if err != nil {
		return fmt.Errorf("get floating IP %d: %w", ipID, err)
	}
	if fip == nil {
		return fmt.Errorf("floating IP %d not found", ipID)
	}
	if fip.Server != nil && fip.Server.ID == serverID {
		return nil
	}
	action, _, err := a.c.FloatingIP.Assign(ctx, fip, &hcloudapi.Server{ID: serverID})
	if err != nil {
		return fmt.Errorf("assign floating IP %d to server %d: %w", ipID, serverID, err)
	}
	return a.c.Action.WaitFor(ctx, action)
}

func (a *apiClient) Delete(ctx context.Context, ipID int64) error {
	_, err := a.c.FloatingIP.Delete(ctx, &hcloudapi.FloatingIP{ID: ipID})
	return err
}

// ServerIDFromProviderID parses a node's spec.providerID ("hcloud://<id>").
func ServerIDFromProviderID(providerID string) (int64, error) {
	const p = "hcloud://"
	if !strings.HasPrefix(providerID, p) {
		return 0, fmt.Errorf("providerID %q is not a hcloud:// id", providerID)
	}
	return strconv.ParseInt(strings.TrimPrefix(providerID, p), 10, 64)
}
