// Copyright 2025 toastyice
//
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

type InstanceConfigReference struct {
	// Name of the KeycloakInstanceConfig
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Namespace of the KeycloakInstanceConfig. If empty, uses the same namespace as the resource.
	// +optional
	Namespace string `json:"namespace,omitempty"`
}
