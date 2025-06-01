// Copyright 2025 toastyice
//
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RealmRoleSpec defines the desired state of RealmRole
type RealmRoleSpec struct {
	// Reference to the KeycloakInstanceConfig
	// +kubebuilder:validation:Required
	InstanceConfigRef InstanceConfigReference `json:"instanceConfigRef"`

	// RealmRef specifies which realm this client belongs to
	// +kubebuilder:validation:Required
	RealmRef RealmReference `json:"realmRef"`

	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Specifies description of the realmRole. For example 'My role for TimeSheets'. Supports keys for localized values as well. For example: ${my_client_description}.
	// +optional
	Description string `json:"description,omitempty"`

	// Attributes are custom attributes for the group
	// +optional
	// +kubebuilder:default={}
	Attributes map[string][]string `json:"attributes"`
}

// RealmRoleStatus defines the observed state of RealmRole
type RealmRoleStatus struct {
	// Ready indicates if the group is ready
	Ready bool `json:"ready,omitempty"`

	// Message provides additional information about the current state
	Message string `json:"message,omitempty"`

	// LastSyncTime is the last time the realmRole was synced
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// RealmReady indicates if the referenced realm is ready
	RealmReady bool `json:"realmReady,omitempty"`

	RealmRoleUUID string `json:"realmRoleUUID,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// RealmRole is the Schema for the realmroles API
type RealmRole struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RealmRoleSpec   `json:"spec,omitempty"`
	Status RealmRoleStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RealmRoleList contains a list of RealmRole
type RealmRoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RealmRole `json:"items"`
}

func init() {
	SchemeBuilder.Register(&RealmRole{}, &RealmRoleList{})
}
