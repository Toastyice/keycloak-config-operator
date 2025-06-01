// Copyright 2025 toastyice
//
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"context"
	"fmt"
	"reflect"

	"github.com/keycloak/terraform-provider-keycloak/keycloak"
	keycloakTypes "github.com/keycloak/terraform-provider-keycloak/keycloak/types"
	keycloakv1alpha1 "github.com/toastyice/keycloak-config-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/types"
)

// get a Keycloak API client from the ClientManager
func (r *ClientReconciler) getKeycloakClient(ctx context.Context, clientObj *keycloakv1alpha1.Client) (*keycloak.KeycloakClient, error) {
	// Assuming your Realm spec has a reference to KeycloakInstanceConfig
	configName := clientObj.Spec.InstanceConfigRef.Name
	configNamespace := clientObj.Namespace
	if clientObj.Spec.InstanceConfigRef.Namespace != "" {
		configNamespace = clientObj.Spec.InstanceConfigRef.Namespace
	}

	var config keycloakv1alpha1.KeycloakInstanceConfig
	if err := r.Get(ctx, types.NamespacedName{
		Name:      configName,
		Namespace: configNamespace,
	}, &config); err != nil {
		return nil, fmt.Errorf("failed to get KeycloakInstanceConfig %s/%s: %w", configNamespace, configName, err)
	}

	// Check if the KeycloakInstanceConfig is ready
	if !r.isKeycloakInstanceConfigReady(&config) {
		return nil, fmt.Errorf("KeycloakInstanceConfig %s/%s is not ready", configNamespace, configName)
	}

	return r.ClientManager.GetOrCreateClient(ctx, &config)
}

// clearClientState clears stored client state when client is not found
func (r *ClientReconciler) clearClientState(clientObj *keycloakv1alpha1.Client) {
	clientObj.Status.ClientUUID = ""
	clientObj.Status.RoleUUIDs = make(map[string]string)
}

// buildClientFromSpec creates a new OpenidClient from the spec
func (r *ClientReconciler) buildClientFromSpec(clientObj *keycloakv1alpha1.Client, realm *keycloakv1alpha1.Realm) *keycloak.OpenidClient {
	attributes := &keycloak.OpenidClientAttributes{
		PostLogoutRedirectUris:                clientObj.Spec.PostLogoutRedirectUris,
		Oauth2DeviceAuthorizationGrantEnabled: keycloakTypes.KeycloakBoolQuoted(clientObj.Spec.Oauth2DeviceAuthorizationGrantEnabled),
		LoginTheme:                            clientObj.Spec.LoginTheme,
		DisplayOnConsentScreen:                keycloakTypes.KeycloakBoolQuoted(clientObj.Spec.DisplayOnConsentScreen),
		ConsentScreenText:                     clientObj.Spec.ConsentScreenText,
		FrontchannelLogoutUrl:                 clientObj.Spec.FrontchannelLogoutUrl,
		BackchannelLogoutUrl:                  clientObj.Spec.BackchannelLogoutUrl,
		BackchannelLogoutSessionRequired:      keycloakTypes.KeycloakBoolQuoted(clientObj.Spec.BackchannelLogoutSessionRequired),
		BackchannelLogoutRevokeOfflineTokens:  keycloakTypes.KeycloakBoolQuoted(clientObj.Spec.BackchannelLogoutRevokeOfflineTokens),
	}

	return &keycloak.OpenidClient{
		RealmId:                   realm.Name,
		ClientId:                  clientObj.Spec.ClientID,
		Name:                      clientObj.Spec.Name,
		Description:               clientObj.Spec.Description,
		Enabled:                   clientObj.Spec.Enabled,
		RootUrl:                   stringPtr(clientObj.Spec.RootUrl),
		BaseUrl:                   clientObj.Spec.BaseUrl,
		AdminUrl:                  clientObj.Spec.AdminUrl,
		AlwaysDisplayInConsole:    clientObj.Spec.AlwaysDisplayInConsole,
		ClientAuthenticatorType:   clientObj.Spec.ClientAuthenticatorType,
		PublicClient:              clientObj.Spec.PublicClient,
		ValidRedirectUris:         clientObj.Spec.RedirectUris,
		WebOrigins:                clientObj.Spec.WebOrigins,
		Attributes:                *attributes,
		StandardFlowEnabled:       clientObj.Spec.StandardFlowEnabled,
		DirectAccessGrantsEnabled: clientObj.Spec.DirectAccessGrantsEnabled,
		ImplicitFlowEnabled:       clientObj.Spec.ImplicitFlowEnabled,
		ServiceAccountsEnabled:    clientObj.Spec.ServiceAccountsEnabled,
		ConsentRequired:           clientObj.Spec.ConsentRequired,
		FrontChannelLogoutEnabled: clientObj.Spec.FrontchannelLogoutEnabled,
	}
}

// applyChangesToClient applies spec changes to the Keycloak client
func (r *ClientReconciler) applyChangesToClient(clientObj *keycloakv1alpha1.Client, keycloakOpenIdClient *keycloak.OpenidClient) *keycloak.OpenidClient {
	updatedClient := *keycloakOpenIdClient

	// Apply basic fields
	updatedClient.Name = clientObj.Spec.Name
	updatedClient.Description = clientObj.Spec.Description
	updatedClient.Enabled = clientObj.Spec.Enabled
	updatedClient.RootUrl = stringPtr(clientObj.Spec.RootUrl)
	updatedClient.BaseUrl = clientObj.Spec.BaseUrl
	updatedClient.AdminUrl = clientObj.Spec.AdminUrl
	updatedClient.AlwaysDisplayInConsole = clientObj.Spec.AlwaysDisplayInConsole
	updatedClient.ClientAuthenticatorType = clientObj.Spec.ClientAuthenticatorType
	updatedClient.PublicClient = clientObj.Spec.PublicClient
	updatedClient.ValidRedirectUris = clientObj.Spec.RedirectUris
	updatedClient.WebOrigins = clientObj.Spec.WebOrigins
	updatedClient.StandardFlowEnabled = clientObj.Spec.StandardFlowEnabled
	updatedClient.DirectAccessGrantsEnabled = clientObj.Spec.DirectAccessGrantsEnabled
	updatedClient.ImplicitFlowEnabled = clientObj.Spec.ImplicitFlowEnabled
	updatedClient.ServiceAccountsEnabled = clientObj.Spec.ServiceAccountsEnabled
	updatedClient.ConsentRequired = clientObj.Spec.ConsentRequired
	updatedClient.FrontChannelLogoutEnabled = clientObj.Spec.FrontchannelLogoutEnabled

	// Apply attributes
	updatedClient.Attributes.PostLogoutRedirectUris = clientObj.Spec.PostLogoutRedirectUris
	updatedClient.Attributes.Oauth2DeviceAuthorizationGrantEnabled = keycloakTypes.KeycloakBoolQuoted(clientObj.Spec.Oauth2DeviceAuthorizationGrantEnabled)
	updatedClient.Attributes.LoginTheme = clientObj.Spec.LoginTheme
	updatedClient.Attributes.DisplayOnConsentScreen = keycloakTypes.KeycloakBoolQuoted(clientObj.Spec.DisplayOnConsentScreen)
	updatedClient.Attributes.ConsentScreenText = clientObj.Spec.ConsentScreenText
	updatedClient.Attributes.FrontchannelLogoutUrl = clientObj.Spec.FrontchannelLogoutUrl
	updatedClient.Attributes.BackchannelLogoutUrl = clientObj.Spec.BackchannelLogoutUrl
	updatedClient.Attributes.BackchannelLogoutSessionRequired = keycloakTypes.KeycloakBoolQuoted(clientObj.Spec.BackchannelLogoutSessionRequired)
	updatedClient.Attributes.BackchannelLogoutRevokeOfflineTokens = keycloakTypes.KeycloakBoolQuoted(clientObj.Spec.BackchannelLogoutRevokeOfflineTokens)

	return &updatedClient
}

// getClientDiffs compares client specifications and returns a list of differences
func (r *ClientReconciler) getClientDiffs(clientObj *keycloakv1alpha1.Client, keycloakOpenIdClient *keycloak.OpenidClient) []string {
	var diffs []string

	fields := []FieldDiff{
		{"name", keycloakOpenIdClient.Name, clientObj.Spec.Name},
		{"description", keycloakOpenIdClient.Description, clientObj.Spec.Description},
		{"enabled", keycloakOpenIdClient.Enabled, clientObj.Spec.Enabled},
		{"rootUrl", derefStringPtr(keycloakOpenIdClient.RootUrl), clientObj.Spec.RootUrl},
		{"baseUrl", keycloakOpenIdClient.BaseUrl, clientObj.Spec.BaseUrl},
		{"adminUrl", keycloakOpenIdClient.AdminUrl, clientObj.Spec.AdminUrl},
		{"alwaysDisplayInConsole", keycloakOpenIdClient.AlwaysDisplayInConsole, clientObj.Spec.AlwaysDisplayInConsole},
		{"clientAuthenticatorType", keycloakOpenIdClient.ClientAuthenticatorType, clientObj.Spec.ClientAuthenticatorType},
		{"publicClient", keycloakOpenIdClient.PublicClient, clientObj.Spec.PublicClient},
		{"standardFlowEnabled", keycloakOpenIdClient.StandardFlowEnabled, clientObj.Spec.StandardFlowEnabled},
		{"directAccessGrantsEnabled", keycloakOpenIdClient.DirectAccessGrantsEnabled, clientObj.Spec.DirectAccessGrantsEnabled},
		{"implicitFlowEnabled", keycloakOpenIdClient.ImplicitFlowEnabled, clientObj.Spec.ImplicitFlowEnabled},
		{"serviceAccountsEnabled", keycloakOpenIdClient.ServiceAccountsEnabled, clientObj.Spec.ServiceAccountsEnabled},
		{"oauth2DeviceAuthorizationGrantEnabled", keycloakOpenIdClient.Attributes.Oauth2DeviceAuthorizationGrantEnabled, keycloakTypes.KeycloakBoolQuoted(clientObj.Spec.Oauth2DeviceAuthorizationGrantEnabled)},
		{"loginTheme", keycloakOpenIdClient.Attributes.LoginTheme, clientObj.Spec.LoginTheme},
		{"consentRequired", keycloakOpenIdClient.ConsentRequired, clientObj.Spec.ConsentRequired},
		{"displayOnConsentScreen", keycloakOpenIdClient.Attributes.DisplayOnConsentScreen, keycloakTypes.KeycloakBoolQuoted(clientObj.Spec.DisplayOnConsentScreen)},
		{"consentScreenText", keycloakOpenIdClient.Attributes.ConsentScreenText, clientObj.Spec.ConsentScreenText},
		{"frontchannelLogoutEnabled", keycloakOpenIdClient.FrontChannelLogoutEnabled, clientObj.Spec.FrontchannelLogoutEnabled},
		{"frontchannelLogoutUrl", keycloakOpenIdClient.Attributes.FrontchannelLogoutUrl, clientObj.Spec.FrontchannelLogoutUrl},
		{"backchannelLogoutUrl", keycloakOpenIdClient.Attributes.BackchannelLogoutUrl, clientObj.Spec.BackchannelLogoutUrl},
		{"backchannelLogoutSessionRequired", keycloakOpenIdClient.Attributes.BackchannelLogoutSessionRequired, keycloakTypes.KeycloakBoolQuoted(clientObj.Spec.BackchannelLogoutSessionRequired)},
		{"backchannelLogoutRevokeOfflineTokens", keycloakOpenIdClient.Attributes.BackchannelLogoutRevokeOfflineTokens, keycloakTypes.KeycloakBoolQuoted(clientObj.Spec.BackchannelLogoutRevokeOfflineTokens)},
	}

	for _, field := range fields {
		if !reflect.DeepEqual(field.Old, field.New) {
			diffs = append(diffs, r.formatFieldDiff(field))
		}
	}

	// Handle slice fields
	if !r.slicesEqual(keycloakOpenIdClient.ValidRedirectUris, clientObj.Spec.RedirectUris) {
		diffs = append(diffs, fmt.Sprintf("validRedirectUris: %v -> %v", keycloakOpenIdClient.ValidRedirectUris, clientObj.Spec.RedirectUris))
	}

	if !r.slicesEqual(keycloakOpenIdClient.WebOrigins, clientObj.Spec.WebOrigins) {
		diffs = append(diffs, fmt.Sprintf("webOrigins: %v -> %v", keycloakOpenIdClient.WebOrigins, clientObj.Spec.WebOrigins))
	}

	if !r.slicesEqual(keycloakOpenIdClient.Attributes.PostLogoutRedirectUris, clientObj.Spec.PostLogoutRedirectUris) {
		diffs = append(diffs, fmt.Sprintf("postLogoutRedirectUris: %v -> %v", keycloakOpenIdClient.Attributes.PostLogoutRedirectUris, clientObj.Spec.PostLogoutRedirectUris))
	}

	return diffs
}

// formatFieldDiff formats a field difference for logging
func (r *ClientReconciler) formatFieldDiff(field FieldDiff) string {
	if reflect.TypeOf(field.Old).Kind() == reflect.String {
		return fmt.Sprintf("%s: '%v' -> '%v'", field.Name, field.Old, field.New)
	}
	return fmt.Sprintf("%s: %v -> %v", field.Name, field.Old, field.New)
}

// slicesEqual compares two string slices, treating nil and empty as equal
func (r *ClientReconciler) slicesEqual(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

// getSuccessMessage returns an appropriate success message
func (r *ClientReconciler) getSuccessMessage(clientChanged bool) string {
	if clientChanged {
		return "Client and roles reconciled successfully"
	}
	return "Client and roles reconciled"
}

func stringPtr(s string) *string {
	return &s
}

func derefStringPtr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
