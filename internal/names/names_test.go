/*
Copyright 2026 Maarlab Rethinking.
Licensed under the Apache License, Version 2.0 (the "License").
*/

package names

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestNodeReady(t *testing.T) {
	withReady := func(s corev1.ConditionStatus) *corev1.Node {
		return &corev1.Node{Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{
			{Type: corev1.NodeMemoryPressure, Status: corev1.ConditionFalse},
			{Type: corev1.NodeReady, Status: s},
		}}}
	}
	for _, tc := range []struct {
		name string
		node *corev1.Node
		want bool
	}{
		{"Ready", withReady(corev1.ConditionTrue), true},
		{"NotReady", withReady(corev1.ConditionFalse), false},
		{"unreachable", withReady(corev1.ConditionUnknown), false},
		{"no Ready condition yet", &corev1.Node{}, false},
	} {
		if got := NodeReady(tc.node); got != tc.want {
			t.Errorf("%s: NodeReady = %v, want %v", tc.name, got, tc.want)
		}
	}
}
