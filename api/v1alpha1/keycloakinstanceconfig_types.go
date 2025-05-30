// Copyright 2025 toastyice
//
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:validation:XValidation:rule=`!(has(self.clientSecret) && (has(self.username) || has(self.password)))`,message="clientSecret cannot be used together with username or password"
// +kubebuilder:validation:XValidation:rule=`!(has(self.username) && !has(self.password)) && !(has(self.password) && !has(self.username))`,message="username and password must be provided together"
type KeycloakInstanceConfigSpec struct {
	// The URL of the Keycloak instance, before /auth/admin
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule=`self.matches('^https?://([a-zA-Z0-9-]+\\.)*[a-zA-Z0-9-]+(:[0-9]{1,5})?(/.*)?$')`,message="url must be a valid HTTP or HTTPS URL"
	Url string `json:"url"`

	// The admin URL of the Keycloak instance, before /auth/admin
	// +optional
	// +kubebuilder:validation:XValidation:rule=`size(self) == 0 || self.matches('^https?://([a-zA-Z0-9-]+\\.)*[a-zA-Z0-9-]+(:[0-9]{1,5})?(/.*)?$')`,message="adminUrl must be a valid HTTP or HTTPS URL"
	AdminUrl string `json:"adminUrl,omitempty"`

	// The base path used for accessing the Keycloak REST API. Defaults to an empty string. Note that users of the legacy distribution of Keycloak will need to set this attribute to /auth
	// +optional
	// +kubebuilder:validation:XValidation:rule=`size(self) == 0 || self.startsWith('/')`,message="basePath must start with /"
	// +kubebuilder:validation:XValidation:rule=`size(self) == 0 || self == '/' || !self.endsWith('/')`,message="basePath must not end with / (except for root path)"
	BasePath string `json:"basePath"`

	// The realm used by the provider for authentication. Defaults to master.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	// +kubebuilder:default=master
	Realm string `json:"realm"`

	// The client_id for the client that was created in the "Keycloak Setup" section. Use the admin-cli client if you are using the password grant.
	// +kubebuilder:validation:Required
	// +kubebuilder:default=admin-cli
	ClientId string `json:"clientId"`

	// The secret for the client used by the provider for authentication via the client credentials grant. This can be found or changed using the "Credentials" tab in the client settings. This attribute is required when using the client credentials grant, and cannot be set when using the password grant.
	// +optional
	ClientSecret string `json:"clientSecret,omitempty"`

	// The username of the user used by the provider for authentication via the password grant. Required when using the password grant, and cannot be set when using the client credentials grant.
	// +optional
	Username string `json:"username,omitempty"`

	// The password of the user used by the provider for authentication via the password grant. Required when using the password grant, and cannot be set when using the client credentials grant.
	// +optional
	Password string `json:"password,omitempty"`

	// Sets the timeout of the client when addressing Keycloak, in seconds. Defaults to 15.
	// +optional
	// +kubebuilder:default=15
	Timeout int `json:"timeout"`

	// Indicates if Keycloak is Red Hat Single Sign-On distribution
	// +optional
	// +kubebuilder:default=false
	RedHatSso bool `json:"redHatSso"`

	// Allows ignoring insecure certificates when set to true. Defaults to false. Disabling this security check is dangerous and should only be done in local or test environments!
	// +optional
	// +kubebuilder:default=false
	TlsInsecureSkipVerify bool `json:"tlsInsecureSkipVerify"`

	// Allows x509 calls using a CA certificate
	// +optional
	CaCert string `json:"caCert,omitempty"`

	// A map of custom HTTP headers to add to each request to the Keycloak API.
	// +optional
	AdditionalHeaders map[string]string `json:"additionalHeaders,omitempty"`
}

// KeycloakInstanceConfigStatus defines the observed state of KeycloakInstanceConfig
type KeycloakInstanceConfigStatus struct {
	// Conditions represent the latest available observations of the connection state
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// LastConnected represents the last time a successful connection was made
	// +optional
	LastConnected *metav1.Time `json:"lastConnected,omitempty"`

	// ServerVersion represents the version of the connected Keycloak server
	// +optional
	ServerVersion string `json:"serverVersion,omitempty"`

	// +optional
	Themes map[string][]Theme `json:"themes,omitempty"`
}

type Theme struct {
	Name    string   `json:"name"`
	Locales []string `json:"locales,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="Connected",type="string",JSONPath=".status.conditions[?(@.type=='Connected')].status"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Version",type="string",JSONPath=".status.serverVersion"
// +kubebuilder:printcolumn:name="Last Connected",type="date",JSONPath=".status.lastConnected"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// KeycloakInstanceConfig is the Schema for the keycloakinstanceconfigs API
type KeycloakInstanceConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   KeycloakInstanceConfigSpec   `json:"spec,omitempty"`
	Status KeycloakInstanceConfigStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// KeycloakInstanceConfigList contains a list of KeycloakInstanceConfig
type KeycloakInstanceConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []KeycloakInstanceConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&KeycloakInstanceConfig{}, &KeycloakInstanceConfigList{})
}
