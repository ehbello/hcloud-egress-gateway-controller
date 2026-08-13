/*
Copyright 2026 Maarlab Rethinking.
Licensed under the Apache License, Version 2.0 (the "License").
*/

package agent

import "testing"

func TestOrdinalOf(t *testing.T) {
	cases := map[string]struct {
		in      string
		want    int
		wantErr bool
	}{
		"zero":       {"egress-agent-partner-api-0", 0, false},
		"nonzero":    {"egress-agent-partner-api-3", 3, false},
		"no suffix":  {"noordinal", 0, true},
		"bad suffix": {"egress-agent-x", 0, true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := ordinalOf(c.in)
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
				t.Fatalf("ordinalOf(%q) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}

func TestParseIPList(t *testing.T) {
	got := parseIPList("101:203.0.113.1, 102:203.0.113.2 ,,bad,7:x")
	want := []ipEntry{
		{id: 101, addr: "203.0.113.1"},
		{id: 102, addr: "203.0.113.2"},
		{id: 7, addr: "x"},
	}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}
