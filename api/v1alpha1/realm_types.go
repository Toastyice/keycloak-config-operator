// Copyright 2025 toastyice
//
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RealmSpec defines the desired state of Realm
// +kubebuilder:validation:XValidation:rule="!(self.loginWithEmailAllowed == true || self.registrationEmailAsUsername == true) || self.duplicateEmailsAllowed == false",message="duplicateEmailsAllowed must be false when loginWithEmailAllowed or registrationEmailAsUsername is true"
type RealmSpec struct {
	// Reference to the KeycloakInstanceConfig
	// +kubebuilder:validation:Required
	InstanceConfigRef InstanceConfigReference `json:"instanceConfigRef"`

	// +optional
	// +kubebuilder:default=true
	// +kubebuilder:validation:Type=boolean
	Enabled bool `json:"enabled,omitempty"`

	// +optional
	DisplayName string `json:"displayName,omitempty"`

	// +optional
	DisplayNameHtml string `json:"displayNameHtml,omitempty"`

	// +optional
	// +kubebuilder:validation:Enum=none;external;all
	// +kubebuilder:default=external
	SslRequired string `json:"sslRequired,omitempty"`

	// +optional
	// +kubebuilder:validation:Type=boolean
	UserManagedAccess bool `json:"userManagedAccess,omitempty"`

	// +optional
	// +kubebuilder:validation:Type=boolean
	OrganizationsEnabled bool `json:"organizationsEnabled,omitempty"`

	// +optional
	// +kubebuilder:validation:Type=boolean
	RegistrationAllowed bool `json:"registrationAllowed,omitempty"`

	// +optional
	// +kubebuilder:validation:Type=boolean
	ResetPasswordAllowed bool `json:"resetPasswordAllowed,omitempty"`

	// +optional
	// +kubebuilder:validation:Type=boolean
	RememberMe bool `json:"rememberMe,omitempty"`

	// +optional
	// +kubebuilder:validation:Type=boolean
	RegistrationEmailAsUsername bool `json:"registrationEmailAsUsername,omitempty"`

	// +optional
	// +kubebuilder:default=true
	// +kubebuilder:validation:Type=boolean
	LoginWithEmailAllowed bool `json:"loginWithEmailAllowed,omitempty"`

	// +optional
	// +kubebuilder:validation:Type=boolean
	DuplicateEmailsAllowed bool `json:"duplicateEmailsAllowed"`

	// +optional
	// +kubebuilder:validation:Type=boolean
	VerifyEmail bool `json:"verifyEmail,omitempty"`

	// +optional
	// +kubebuilder:validation:Type=boolean
	EditUsernameAllowed bool `json:"editUsernameAllowed,omitempty"`
}

// RealmStatus defines the observed state of Realm
type RealmStatus struct {
	// Ready indicates if the realm is ready
	Ready bool `json:"ready,omitempty"`

	// Message provides additional information about the current state
	Message string `json:"message,omitempty"`

	// LastSyncTime is the last time the realm was synced
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Enabled",type=boolean,JSONPath=`.spec.enabled`
// +kubebuilder:printcolumn:name="Ready",type=boolean,JSONPath=`.status.ready`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Realm is the Schema for the realms API
type Realm struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RealmSpec   `json:"spec,omitempty"`
	Status RealmStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RealmList contains a list of Realm
type RealmList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Realm `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Realm{}, &RealmList{})
}
