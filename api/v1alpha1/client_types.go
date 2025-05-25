package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ClientSpec defines the desired state of Client
// if standardFlowEnabled is false redirectUris, postLogoutRedirectUris, webOrigins are not allowed
// +kubebuilder:validation:XValidation:rule="self.standardFlowEnabled || size(self.redirectUris) == 0",message="redirectUris can only be set when standardFlowEnabled is true"
// +kubebuilder:validation:XValidation:rule="self.standardFlowEnabled || size(self.postLogoutRedirectUris) == 0",message="postLogoutRedirectUris can only be set when standardFlowEnabled is true"
// +kubebuilder:validation:XValidation:rule="self.standardFlowEnabled || size(self.webOrigins) == 0",message="webOrigins can only be set when standardFlowEnabled is true"
// if publicClient is true serviceAccountRoles can't be true
// +kubebuilder:validation:XValidation:rule="!self.publicClient || !self.serviceAccountsEnabled",message="serviceAccountsEnabled must be false when publicClient is true"
type ClientSpec struct {
	// RealmRef specifies which realm this client belongs to
	// +kubebuilder:validation:Required
	RealmRef RealmReference `json:"realmRef"`

	// ClientID is the unique identifier for this client
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="clientId is immutable"
	ClientID string `json:"clientId"`

	// Name is the display name of the client
	// +optional
	Name string `json:"name,omitempty"`

	// Description of the client
	// +optional
	Description string `json:"description,omitempty"`

	// Enabled indicates whether this client is enabled
	// +optional
	// +kubebuilder:default=true
	Enabled bool `json:"enabled,omitempty"`

	// ClientAuthenticatorType specifies the client authenticator type
	// +optional
	// +kubebuilder:default="client-secret"
	ClientAuthenticatorType string `json:"clientAuthenticatorType,omitempty"`

	// PublicClient indicates if this is a public client
	// +optional
	// +kubebuilder:default=true
	PublicClient bool `json:"publicClient"`

	// RedirectUris is a list of valid redirect URIs
	// +optional
	// +kubebuilder:default={}
	RedirectUris []string `json:"redirectUris"`

	// +optional
	// +kubebuilder:default={}
	PostLogoutRedirectUris []string `json:"postLogoutRedirectUris"`

	// WebOrigins is a list of allowed web origins
	// +optional
	// +kubebuilder:default={}
	WebOrigins []string `json:"webOrigins"`

	// +optional
	// +kubebuilder:default=true
	StandardFlowEnabled bool `json:"standardFlowEnabled"`

	// +optional
	// +kubebuilder:default=true
	DirectAccessGrantsEnabled bool `json:"directAccessGrantsEnabled"`

	// +optional
	ImplicitFlowEnabled bool `json:"implicitFlowEnabled"`

	// +optional
	// +kubebuilder:default=false
	ServiceAccountsEnabled bool `json:"serviceAccountsEnabled"`

	// +optional
	Oauth2DeviceAuthorizationGrantEnabled bool `json:"oauth2DeviceAuthorizationGrantEnabled"`

	// +optional
	Roles []RoleSpec `json:"roles,omitempty"`
}

// RealmReference defines a reference to a Realm resource
type RealmReference struct {
	// Name is the name of the Realm resource
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Namespace is the namespace of the Realm resource
	// If empty, the same namespace as the Client will be used
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

type RoleSpec struct {
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// +optional
	Description string `json:"description"`
}

// ClientStatus defines the observed state of Client
type ClientStatus struct {
	// Ready indicates if the client is ready
	Ready bool `json:"ready,omitempty"`

	// Message provides additional information about the current state
	Message string `json:"message,omitempty"`

	// LastSyncTime is the last time the client was synced
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// RealmReady indicates if the referenced realm is ready
	RealmReady bool `json:"realmReady,omitempty"`

	// ClientUUID is the internal Keycloak UUID for this client
	// This is set automatically and should not be modified
	// +optional
	ClientUUID string `json:"clientUUID,omitempty"`

	// RoleUUIDs maps role names to their Keycloak UUIDs
	// +optional
	RoleUUIDs map[string]string `json:"roleUUIDs,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="ClientID",type=string,JSONPath=`.spec.clientId`
// +kubebuilder:printcolumn:name="Realm",type=string,JSONPath=`.spec.realmRef.name`
// +kubebuilder:printcolumn:name="Enabled",type=boolean,JSONPath=`.spec.enabled`
// +kubebuilder:printcolumn:name="Ready",type=boolean,JSONPath=`.status.ready`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Client is the Schema for the clients API
type Client struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClientSpec   `json:"spec,omitempty"`
	Status ClientStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ClientList contains a list of Client
type ClientList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Client `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Client{}, &ClientList{})
}
