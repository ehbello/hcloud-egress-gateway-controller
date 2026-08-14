/*
Copyright 2026 Maarlab Rethinking.
Licensed under the Apache License, Version 2.0 (the "License").
*/

package hcloud

import (
	"strings"
	"testing"
)

func TestFloatingIPNameHasNoForcedPrefix(t *testing.T) {
	got := floatingIPName("egress-gateway-dev", "nbg1", 0)
	if got != "egress-gateway-dev-nbg1-0" {
		t.Fatalf("floatingIPName = %q, want egress-gateway-dev-nbg1-0", got)
	}
	// A generic CR name must not gain any hidden prefix.
	if got := floatingIPName("partner-api", "fsn1", 2); got != "partner-api-fsn1-2" {
		t.Fatalf("floatingIPName = %q, want partner-api-fsn1-2", got)
	}
}

func TestManagedSelectorIncludesRegion(t *testing.T) {
	got := managedSelector("partner-api", "nbg1", 2)
	for _, want := range []string{
		LabelManagedBy + "==" + ManagedByValue,
		LabelGateway + "==partner-api",
		LabelRegion + "==nbg1",
		LabelIndex + "==2",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("selector %q missing %q", got, want)
		}
	}
}

func TestServerIDFromProviderID(t *testing.T) {
	cases := map[string]struct {
		in      string
		want    int64
		wantErr bool
	}{
		"valid":        {"hcloud://12345", 12345, false},
		"empty":        {"", 0, true},
		"wrong prefix": {"aws:///eu/i-abc", 0, true},
		"not a number": {"hcloud://abc", 0, true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := ServerIDFromProviderID(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", c.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Fatalf("ServerIDFromProviderID(%q) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}
