/*
Copyright 2026 Maarlab Rethinking.
Licensed under the Apache License, Version 2.0 (the "License").
*/

// Package v1alpha1 contains the API types for the egress.maarlab.dev group.
//
// The scheme is registered through apimachinery's own SchemeBuilder rather than
// controller-runtime's helper, so that importing these types costs nothing beyond
// apimachinery.
// +kubebuilder:object:generate=true
// +groupName=egress.maarlab.dev
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	// GroupVersion is group version used to register these objects.
	GroupVersion = schema.GroupVersion{Group: "egress.maarlab.dev", Version: "v1alpha1"}

	// SchemeBuilder collects the functions that add these types to a scheme.
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(GroupVersion, &HetznerEgressGateway{}, &HetznerEgressGatewayList{})
	metav1.AddToGroupVersion(scheme, GroupVersion)
	return nil
}
