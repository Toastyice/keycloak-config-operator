package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:validation:XValidation:rule="self.standardFlowEnabled || size(self.redirectUris) == 0",message="redirectUris can only be set when standardFlowEnabled is true"
// +kubebuilder:validation:XValidation:rule="self.standardFlowEnabled || size(self.postLogoutRedirectUris) == 0",message="postLogoutRedirectUris can only be set when standardFlowEnabled is true"
// +kubebuilder:validation:XValidation:rule="self.standardFlowEnabled || size(self.webOrigins) == 0",message="webOrigins can only be set when standardFlowEnabled is true"
// +kubebuilder:validation:XValidation:rule="!self.publicClient || !self.serviceAccountsEnabled",message="serviceAccountsEnabled must be false when publicClient is true"
// +kubebuilder:validation:XValidation:rule="!self.displayOnConsentScreen || self.consentRequired",message="displayOnConsentScreen can only be true when consentRequired is true"
// +kubebuilder:validation:XValidation:rule=`!has(self.consentScreenText) || size(self.consentScreenText) == 0  || self.displayOnConsentScreen`,message="consentScreenText can only be set when displayOnConsentScreen is true"
// +kubebuilder:validation:XValidation:rule=`!has(self.frontchannelLogoutUrl) || size(self.frontchannelLogoutUrl) == 0 || self.frontchannelLogoutEnabled`,message="frontChannelLogoutUrl can only be set when frontchannelLogoutEnabled is true"
// +kubebuilder:validation:XValidation:rule=`!has(self.backchannelLogoutUrl) || size(self.backchannelLogoutUrl) == 0 || !self.frontchannelLogoutEnabled`,message="backchannelLogoutUrl can only be set when frontchannelLogoutEnabled is false"
// +kubebuilder:validation:XValidation:rule=`!self.frontchannelLogoutEnabled || !self.backchannelLogoutSessionRequired`,message="backchannelLogoutSessionRequired must be false when frontchannelLogoutEnabled is true"
type ClientSpec struct {
	// RealmRef specifies which realm this client belongs to
	// +kubebuilder:validation:Required
	RealmRef RealmReference `json:"realmRef"`

	// The client identifier registered with the identity provider.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="clientId is immutable"
	ClientID string `json:"clientId"`

	// Specifies display name of the client. For example 'My Client'. Supports keys for localized values as well. For example: ${my_client}.
	// +optional
	Name string `json:"name,omitempty"`

	// Specifies description of the client. For example 'My Client for TimeSheets'. Supports keys for localized values as well. For example: ${my_client_description}.
	// +optional
	Description string `json:"description,omitempty"`

	// Disabled clients cannot initiate a login or have obtained access tokens.
	// +optional
	// +kubebuilder:default=true
	Enabled bool `json:"enabled,omitempty"`

	// ClientAuthenticatorType specifies the client authenticator type
	// +optional
	// +kubebuilder:default="client-secret"
	ClientAuthenticatorType string `json:"clientAuthenticatorType,omitempty"`

	// Always list this client in the Account UI, even if the user does not have an active session.
	// +optional
	AlwaysDisplayInConsole bool `json:"alwaysDisplayInConsole,omitempty"`

	// Root URL appended to relative URLs
	// +optional
	RootUrl string `json:"rootUrl,omitempty"`

	// Default URL to use when the auth server needs to redirect or link back to the client.
	// +optional
	// +kubebuilder:validation:XValidation:rule=`size(self) == 0 || self.matches('^https?://[a-zA-Z0-9.-]+\\.[a-zA-Z]{2,}(/.*)?$')`,message="baseUrl must be a valid HTTP or HTTPS URL"
	BaseUrl string `json:"baseUrl,omitempty"`

	// Valid URI pattern a browser can redirect to after a successful login. Simple wildcards are allowed such as 'http://example.com/*'. Relative path can be specified too such as /my/relative/path/*. Relative paths are relative to the client root URL, or if none is specified the auth server root URL is used. For SAML, you must set valid URI patterns if you are relying on the consumer service URL embedded with the login request.
	// +optional
	// +kubebuilder:default={}
	RedirectUris []string `json:"redirectUris"`

	// Valid URI pattern a browser can redirect to after a successful logout. A value of '+' or an empty field will use the list of valid redirect uris. A value of '-' will not allow any post logout redirect uris. Simple wildcards are allowed such as 'http://example.com/*'. Relative path can be specified too such as /my/relative/path/*. Relative paths are relative to the client root URL, or if none is specified the auth server root URL is used.
	// +optional
	// +kubebuilder:default={}
	PostLogoutRedirectUris []string `json:"postLogoutRedirectUris"`

	// Allowed CORS origins. To permit all origins of Valid Redirect URIs, add '+'. This does not include the '*' wildcard though. To permit all origins, explicitly add '*'.
	// +optional
	// +kubebuilder:default={}
	WebOrigins []string `json:"webOrigins"`

	// URL to the admin interface of the client. Set this if the client supports the adapter REST API. This REST API allows the auth server to push revocation policies and other administrative tasks. Usually this is set to the base URL of the client.
	// +optional
	AdminUrl string `json:"adminUrl,omitempty"`

	// PublicClient indicates if this is a public client
	// +optional
	// +kubebuilder:default=true
	PublicClient bool `json:"publicClient"`

	// This enables standard OpenID Connect redirect based authentication with authorization code. In terms of OpenID Connect or OAuth2 specifications, this enables support of 'Authorization Code Flow' for this client.
	// +optional
	// +kubebuilder:default=true
	StandardFlowEnabled bool `json:"standardFlowEnabled"`

	// This enables support for Direct Access Grants, which means that client has access to username/password of user and exchange it directly with Keycloak server for access token. In terms of OAuth2 specification, this enables support of 'Resource Owner Password Credentials Grant' for this client.
	// +optional
	// +kubebuilder:default=true
	DirectAccessGrantsEnabled bool `json:"directAccessGrantsEnabled"`

	// This enables support for OpenID Connect redirect based authentication without authorization code. In terms of OpenID Connect or OAuth2 specifications, this enables support of 'Implicit Flow' for this client.
	// +optional
	ImplicitFlowEnabled bool `json:"implicitFlowEnabled"`

	// Allows you to authenticate this client to Keycloak and retrieve access token dedicated to this client. In terms of OAuth2 specification, this enables support of 'Client Credentials Grant' for this client.
	// +optional
	// +kubebuilder:default=false
	ServiceAccountsEnabled bool `json:"serviceAccountsEnabled"`

	// This enables support for OAuth 2.0 Device Authorization Grant, which means that client is an application on device that has limited input capabilities or lack a suitable browser.
	// +optional
	Oauth2DeviceAuthorizationGrantEnabled bool `json:"oauth2DeviceAuthorizationGrantEnabled"`

	// +optional
	Roles []RoleSpec `json:"roles,omitempty"`

	// The theme must exist in Keycloak. Select theme for login, OTP, grant, registration and forgot password pages.
	// +optional
	LoginTheme string `json:"loginTheme,omitempty"`

	// If enabled, users have to consent to client access.
	// +optional
	// +kubebuilder:default=false
	ConsentRequired bool `json:"consentRequired"`

	// Applicable only if 'Consent Required' is on for this client. If this switch is off, the consent screen will contain just the consents corresponding to configured client scopes. If on, there will be also one item on the consent screen about this client itself.
	// +optional
	// +kubebuilder:default=false
	DisplayOnConsentScreen bool `json:"displayOnConsentScreen"`

	// Text that will be shown on the consent screen when this client scope is added to some client with consent required. Defaults to name of client scope if it is not filled.
	// +optional
	ConsentScreenText string `json:"consentScreenText"`

	// When true, logout requires a browser to send the request to the client to configured Front-channel logout URL as specified in the OIDC Front-channel logout specification. When false, server can perform a background invocation for logout as long as either the Backchannel-logout URL is configured or Admin URL is configured.
	// +optional
	// +kubebuilder:default=false
	FrontchannelLogoutEnabled bool `json:"frontchannelLogoutEnabled"`

	// URL that will cause the client to log itself out when a logout request is sent to this realm (via end_session_endpoint). If not provided, it defaults to the base url.
	// +optional
	// +kubebuilder:validation:XValidation:rule=`size(self) == 0 || self.matches('^https?://[a-zA-Z0-9.-]+\\.[a-zA-Z]{2,}(/.*)?$')`,message="frontchannelLogoutUrl must be a valid HTTP or HTTPS URL"
	FrontchannelLogoutUrl string `json:"frontchannelLogoutUrl"`

	// URL that will cause the client to log itself out when a logout request is sent to this realm (via end_session_endpoint). The logout is done by sending logout token as specified in the OIDC Backchannel logout specification. If omitted, the logout request might be sent to the specified 'Admin URL' (if configured) in the format specific to Keycloak/RH-SSO adapters. If even 'Admin URL' is not configured, no logout request will be sent to the client.
	// +optional
	// +kubebuilder:validation:XValidation:rule=`size(self) == 0 || self.matches('^https?://[a-zA-Z0-9.-]+\\.[a-zA-Z]{2,}(/.*)?$')`,message="backchannelLogoutUrl must be a valid HTTP or HTTPS URL"
	BackchannelLogoutUrl string `json:"backchannelLogoutUrl"`

	// Specifying whether a sid (session ID) Claim is included in the Logout Token when the Backchannel Logout URL is used.
	// +optional
	// +kubebuilder:default=false
	BackchannelLogoutSessionRequired bool `json:"backchannelLogoutSessionRequired"`

	// This Option is also valid when FrontchannelLogoutEnabled is enabled. Specifying whether a "revoke_offline_access" event is included in the Logout Token when the Backchannel Logout URL is used. Keycloak will revoke offline sessions when receiving a Logout Token with this event.
	// +optional
	// +kubebuilder:default=false
	BackchannelLogoutRevokeOfflineTokens bool `json:"backchannelLogoutRevokeOfflineTokens"`
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
