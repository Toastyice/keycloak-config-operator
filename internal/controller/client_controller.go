// Copyright 2025 toastyice
//
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/keycloak/terraform-provider-keycloak/keycloak"
	keycloakTypes "github.com/keycloak/terraform-provider-keycloak/keycloak/types"
	keycloakv1alpha1 "github.com/toastyice/keycloak-config-operator/api/v1alpha1"
	keycloakclientmanager "github.com/toastyice/keycloak-config-operator/internal/keycloak"
)

const (
	clientFinalizer  = "client.keycloak.schella.network/finalizer"
	requeueInterval  = 10 * time.Second
	maxStatusRetries = 3
)

// ClientReconciler reconciles a Client object
type ClientReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	ClientManager *keycloakclientmanager.ClientManager
}

// ReconcileResult represents the outcome of a reconciliation operation
type ReconcileResult struct {
	Ready      bool
	RealmReady bool
	Message    string
	Error      error
}

//+kubebuilder:rbac:groups=keycloak.schella.network,resources=clients,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=keycloak.schella.network,resources=clients/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=keycloak.schella.network,resources=clients/finalizers,verbs=update
//+kubebuilder:rbac:groups=keycloak.schella.network,resources=realms,verbs=get;list;watch

func (r *ClientReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("client", req.NamespacedName)

	clientObj, err := r.fetchClient(ctx, req.NamespacedName)
	if err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Unable to fetch Client")
		return ctrl.Result{}, err
	}

	if r.isMarkedForDeletion(clientObj) {
		keycloakClient, err := r.getKeycloakClient(ctx, clientObj)
		if err != nil {
			if strings.Contains(err.Error(), "is not ready") {
				logger.Info("KeycloakInstanceConfig not ready during deletion, waiting...", "client", clientObj.Name)

				// Optionally update status to indicate pending deletion
				result := ReconcileResult{
					Ready:      false,
					RealmReady: false,
					Message:    "Deletion pending: waiting for KeycloakInstanceConfig to become ready",
				}
				r.updateStatus(ctx, clientObj, result) // Ignore error during deletion

				return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
			}
			return ctrl.Result{}, fmt.Errorf("failed to get Keycloak client during deletion: %w", err)
		}
		return r.reconcileDelete(ctx, keycloakClient, clientObj)
	}

	// Get the KeycloakInstanceConfig for regular reconciliation
	keycloakClient, err := r.getKeycloakClient(ctx, clientObj)
	if err != nil {
		if strings.Contains(err.Error(), "is not ready") {
			// Don't update status, just log and requeue
			logger.Info("KeycloakInstanceConfig is not ready, requeuing", "client", clientObj.Name)
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
		logger.Error(err, "failed to get Keycloak client")
		// For actual errors (not dependency issues), update status if needed
		return ctrl.Result{RequeueAfter: 10 * time.Second}, fmt.Errorf("failed to get Keycloak client: %w", err)
	}

	if err := r.validateReconciler(keycloakClient); err != nil {
		logger.Error(err, "Controller not properly initialized")
		return ctrl.Result{}, err
	}

	if err := r.ensureFinalizer(ctx, clientObj); err != nil {
		return ctrl.Result{}, err
	}

	realm, result := r.validateRealm(ctx, clientObj)
	if result != nil {
		return r.updateStatus(ctx, clientObj, *result)
	}

	if err := r.setOwnerReference(ctx, clientObj, realm); err != nil {
		logger.Error(err, "Failed to set owner reference")
		result := &ReconcileResult{
			Ready:      false,
			RealmReady: true,
			Message:    fmt.Sprintf("Failed to set owner reference: %v", err),
		}
		return r.updateStatus(ctx, clientObj, *result)
	}

	return r.reconcileClient(ctx, keycloakClient, clientObj, realm)
}

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

func (r *ClientReconciler) isKeycloakInstanceConfigReady(config *keycloakv1alpha1.KeycloakInstanceConfig) bool {
	for _, condition := range config.Status.Conditions {
		if condition.Type == "Ready" {
			return condition.Status == "True"
		}
	}
	return false
}

// validateReconciler ensures the controller is properly initialized
func (r *ClientReconciler) validateReconciler(keycloakClient *keycloak.KeycloakClient) error {
	if keycloakClient == nil {
		return fmt.Errorf("KeycloakClient is nil - controller not properly initialized")
	}
	return nil
}

// fetchClient retrieves the Client object from the cluster
func (r *ClientReconciler) fetchClient(ctx context.Context, namespacedName types.NamespacedName) (*keycloakv1alpha1.Client, error) {
	var clientObj keycloakv1alpha1.Client
	if err := r.Get(ctx, namespacedName, &clientObj); err != nil {
		return nil, err
	}
	return &clientObj, nil
}

// isMarkedForDeletion checks if the client is being deleted
func (r *ClientReconciler) isMarkedForDeletion(clientObj *keycloakv1alpha1.Client) bool {
	return !clientObj.ObjectMeta.DeletionTimestamp.IsZero()
}

// ensureFinalizer adds the finalizer if it doesn't exist
func (r *ClientReconciler) ensureFinalizer(ctx context.Context, clientObj *keycloakv1alpha1.Client) error {
	if !controllerutil.ContainsFinalizer(clientObj, clientFinalizer) {
		controllerutil.AddFinalizer(clientObj, clientFinalizer)
		return r.Update(ctx, clientObj)
	}
	return nil
}

// validateRealm validates and retrieves the referenced realm
func (r *ClientReconciler) validateRealm(ctx context.Context, clientObj *keycloakv1alpha1.Client) (*keycloakv1alpha1.Realm, *ReconcileResult) {
	realm, err := r.getRealm(ctx, clientObj)
	if err != nil {
		return nil, &ReconcileResult{
			Ready:      false,
			RealmReady: false,
			Message:    fmt.Sprintf("Failed to get realm: %v", err),
		}
	}

	if realm == nil {
		return nil, &ReconcileResult{
			Ready:      false,
			RealmReady: false,
			Message:    "Referenced realm not found",
		}
	}

	if !realm.Status.Ready {
		return nil, &ReconcileResult{
			Ready:      false,
			RealmReady: false,
			Message:    "Referenced realm is not ready",
		}
	}

	return realm, nil
}

// getRealm retrieves the realm object referenced by the client
func (r *ClientReconciler) getRealm(ctx context.Context, clientObj *keycloakv1alpha1.Client) (*keycloakv1alpha1.Realm, error) {
	realmNamespace := clientObj.Spec.RealmRef.Namespace
	if realmNamespace == "" {
		realmNamespace = clientObj.Namespace
	}

	var realm keycloakv1alpha1.Realm
	namespacedName := types.NamespacedName{
		Name:      clientObj.Spec.RealmRef.Name,
		Namespace: realmNamespace,
	}

	if err := r.Get(ctx, namespacedName, &realm); err != nil {
		if errors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}

	return &realm, nil
}

// setOwnerReference establishes an owner-dependent relationship between realm and client
func (r *ClientReconciler) setOwnerReference(ctx context.Context, clientObj *keycloakv1alpha1.Client, realm *keycloakv1alpha1.Realm) error {
	logger := log.FromContext(ctx)
	if r.hasCorrectOwnerReference(clientObj, realm) {
		return nil
	}

	if r.isCrossNamespaceReference(clientObj, realm) {
		logger.V(1).Info("Skipping owner reference - cross-namespace references not allowed")
		return nil
	}

	if err := controllerutil.SetOwnerReference(realm, clientObj, r.Scheme); err != nil {
		return fmt.Errorf("failed to set owner reference: %w", err)
	}

	if err := r.Update(ctx, clientObj); err != nil {
		return fmt.Errorf("failed to update client with owner reference: %w", err)
	}

	logger.Info("Owner reference set successfully")
	return nil
}

// hasCorrectOwnerReference checks if the correct owner reference already exists
func (r *ClientReconciler) hasCorrectOwnerReference(clientObj *keycloakv1alpha1.Client, realm *keycloakv1alpha1.Realm) bool {
	for _, ownerRef := range clientObj.GetOwnerReferences() {
		if ownerRef.Kind == "Realm" &&
			ownerRef.APIVersion == realm.APIVersion &&
			ownerRef.Name == realm.Name &&
			ownerRef.UID == realm.UID {
			return true
		}
	}
	return false
}

// isCrossNamespaceReference checks if this would be a cross-namespace reference
func (r *ClientReconciler) isCrossNamespaceReference(clientObj *keycloakv1alpha1.Client, realm *keycloakv1alpha1.Realm) bool {
	return clientObj.Namespace != realm.Namespace
}

// reconcileClient handles the main client reconciliation logic
func (r *ClientReconciler) reconcileClient(ctx context.Context, keycloakClient *keycloak.KeycloakClient, clientObj *keycloakv1alpha1.Client, realm *keycloakv1alpha1.Realm) (ctrl.Result, error) {
	keycloakOpenIdClient, err := r.getOrCreateClient(ctx, keycloakClient, clientObj, realm)
	if err != nil {
		result := ReconcileResult{
			Ready:      false,
			RealmReady: true,
			Message:    fmt.Sprintf("Failed to reconcile client: %v", err),
		}
		return r.updateStatus(ctx, clientObj, result)
	}

	clientChanged := keycloakOpenIdClient == nil // If nil, it means we created
	if keycloakOpenIdClient != nil {
		clientChanged = r.updateClientIfNeeded(ctx, keycloakClient, clientObj, keycloakOpenIdClient)
	}

	if clientObj.Status.ClientUUID != "" {
		if err := r.reconcileClientRoles(ctx, keycloakClient, clientObj, realm); err != nil {
			result := ReconcileResult{
				Ready:      false,
				RealmReady: true,
				Message:    fmt.Sprintf("Client ready but failed to reconcile roles: %v", err),
			}
			return r.updateStatus(ctx, clientObj, result)
		}

		// Always fetch the current client state before reconciling secrets
		// This ensures we have the latest client data including the secret
		currentClient, err := keycloakClient.GetOpenidClient(context.Background(), realm.Name, clientObj.Status.ClientUUID)
		if err != nil {
			result := ReconcileResult{
				Ready:      false,
				RealmReady: true,
				Message:    fmt.Sprintf("Failed to fetch current client state: %v", err),
			}
			return r.updateStatus(ctx, clientObj, result)
		}

		// Reconcile client secret with the current client state
		if err := r.reconcileClientSecret(ctx, clientObj, keycloakClient, realm, currentClient); err != nil {
			// Update secret status to reflect failure
			r.updateSecretStatus(clientObj, false, "", "")

			result := ReconcileResult{
				Ready:      false,
				RealmReady: true,
				Message:    fmt.Sprintf("Client ready but failed to reconcile secret: %v", err),
			}
			return r.updateStatus(ctx, clientObj, result)
		}
	}

	// Update secret status to reflect success (if applicable)
	if !clientObj.Spec.PublicClient && (clientObj.Spec.Secret == nil || clientObj.Spec.Secret.CreateSecret) {
		secretName := r.getSecretName(clientObj)
		secretNamespace := r.getSecretNamespace(clientObj)
		r.updateSecretStatus(clientObj, true, secretName, secretNamespace)
	}

	message := r.getSuccessMessage(clientChanged)
	result := ReconcileResult{
		Ready:      true,
		RealmReady: true,
		Message:    message,
	}
	return r.updateStatus(ctx, clientObj, result)
}

// getOrCreateClient retrieves an existing client or creates a new one
func (r *ClientReconciler) getOrCreateClient(ctx context.Context, keycloakClient *keycloak.KeycloakClient, clientObj *keycloakv1alpha1.Client, realm *keycloakv1alpha1.Realm) (*keycloak.OpenidClient, error) {
	if clientObj.Status.ClientUUID != "" {
		client, err := r.fetchExistingClient(ctx, keycloakClient, clientObj, realm)
		if err != nil {
			return nil, err
		}
		if client != nil {
			return client, nil
		}
	}

	return nil, r.createNewClient(ctx, keycloakClient, clientObj, realm)
}

// fetchExistingClient retrieves an existing client by UUID
func (r *ClientReconciler) fetchExistingClient(ctx context.Context, keycloakClient *keycloak.KeycloakClient, clientObj *keycloakv1alpha1.Client, realm *keycloakv1alpha1.Realm) (*keycloak.OpenidClient, error) {
	logger := log.FromContext(ctx)
	logger.V(1).Info("Fetching client by UUID", "uuid", clientObj.Status.ClientUUID)

	client, err := keycloakClient.GetOpenidClient(ctx, realm.Name, clientObj.Status.ClientUUID)
	if err != nil {
		if keycloak.ErrorIs404(err) {
			logger.Info("Client UUID not found in Keycloak, will recreate")
			r.clearClientState(clientObj)
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get client by UUID: %w", err)
	}

	return client, nil
}

// clearClientState clears stored client state when client is not found
func (r *ClientReconciler) clearClientState(clientObj *keycloakv1alpha1.Client) {
	clientObj.Status.ClientUUID = ""
	clientObj.Status.RoleUUIDs = make(map[string]string)
}

// createNewClient creates a new client in Keycloak
func (r *ClientReconciler) createNewClient(ctx context.Context, keycloakClient *keycloak.KeycloakClient, clientObj *keycloakv1alpha1.Client, realm *keycloakv1alpha1.Realm) error {
	logger := log.FromContext(ctx)
	logger.Info("Creating client in Keycloak")

	newClient := r.buildClientFromSpec(clientObj, realm)

	if err := keycloakClient.NewOpenidClient(ctx, newClient); err != nil {
		if keycloak.ErrorIs409(err) {
			return fmt.Errorf("client exists in Keycloak but UUID not tracked - manual intervention required")
		}
		return fmt.Errorf("failed to create client: %w", err)
	}

	return r.storeClientUUID(ctx, keycloakClient, clientObj, realm)
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

// storeClientUUID fetches and stores the UUID of the created client
func (r *ClientReconciler) storeClientUUID(ctx context.Context, keycloakClient *keycloak.KeycloakClient, clientObj *keycloakv1alpha1.Client, realm *keycloakv1alpha1.Realm) error {
	logger := log.FromContext(ctx)
	createdClient, err := keycloakClient.GetOpenidClientByClientId(ctx, realm.Name, clientObj.Spec.ClientID)
	if err != nil {
		return fmt.Errorf("client created but failed to retrieve UUID: %w", err)
	}

	clientObj.Status.ClientUUID = createdClient.Id
	clientObj.Status.RoleUUIDs = make(map[string]string)

	logger.Info("Client created successfully", "uuid", createdClient.Id)
	return nil
}

// updateClientIfNeeded checks for differences and updates the client if needed
func (r *ClientReconciler) updateClientIfNeeded(ctx context.Context, keycloakClient *keycloak.KeycloakClient, clientObj *keycloakv1alpha1.Client, keycloakOpenIdClient *keycloak.OpenidClient) bool {
	logger := log.FromContext(ctx)
	diffs := r.getClientDiffs(clientObj, keycloakOpenIdClient)
	if len(diffs) == 0 {
		return false
	}

	logger.Info("Client configuration changes detected", "changes", strings.Join(diffs, ", "))

	updatedClient := r.applyChangesToClient(clientObj, keycloakOpenIdClient)
	if err := keycloakClient.UpdateOpenidClient(ctx, updatedClient); err != nil {
		logger.Error(err, "Failed to update client")
		return false
	}

	logger.Info("Client updated successfully")
	return true
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

// getClientRolesForSingleClient retrieves roles for a specific client
func (r *ClientReconciler) getClientRolesForSingleClient(ctx context.Context, keycloakClient *keycloak.KeycloakClient, realmName, clientUUID string) ([]*keycloak.Role, error) {
	mockClients := []*keycloak.OpenidClient{{Id: clientUUID}}
	return keycloakClient.GetClientRoles(ctx, realmName, mockClients)
}

// reconcileClientRoles handles role synchronization for the client
func (r *ClientReconciler) reconcileClientRoles(ctx context.Context, keycloakClient *keycloak.KeycloakClient, clientObj *keycloakv1alpha1.Client, realm *keycloakv1alpha1.Realm) error {
	logger := log.FromContext(ctx)
	r.initializeRoleUUIDs(clientObj)

	existingRoles, err := r.getClientRolesForSingleClient(ctx, keycloakClient, realm.Name, clientObj.Status.ClientUUID)
	if err != nil {
		return fmt.Errorf("failed to get existing client roles: %w", err)
	}

	existingRoleMap := r.buildRoleMap(existingRoles)
	desiredRoles := r.buildDesiredRoleMap(clientObj)

	logger.V(1).Info("Role reconciliation starting",
		"existingRoles", len(existingRoles),
		"desiredRoles", len(desiredRoles))

	r.cleanupTrackedRoles(ctx, clientObj, desiredRoles)

	if err := r.deleteUnwantedRoles(ctx, keycloakClient, existingRoles, desiredRoles, realm.Name); err != nil {
		return err
	}

	if err := r.createOrUpdateRoles(ctx, keycloakClient, existingRoleMap, desiredRoles, clientObj, realm); err != nil {
		return err
	}

	logger.V(1).Info("Role reconciliation completed", "finalTrackedRoles", len(clientObj.Status.RoleUUIDs))
	return nil
}

// initializeRoleUUIDs ensures the RoleUUIDs map is initialized
func (r *ClientReconciler) initializeRoleUUIDs(clientObj *keycloakv1alpha1.Client) {
	if clientObj.Status.RoleUUIDs == nil {
		clientObj.Status.RoleUUIDs = make(map[string]string)
	}
}

// buildRoleMap creates a map of role names to role objects
func (r *ClientReconciler) buildRoleMap(roles []*keycloak.Role) map[string]*keycloak.Role {
	roleMap := make(map[string]*keycloak.Role)
	for _, role := range roles {
		roleMap[role.Name] = role
	}
	return roleMap
}

// buildDesiredRoleMap creates a map of desired roles from the spec
func (r *ClientReconciler) buildDesiredRoleMap(clientObj *keycloakv1alpha1.Client) map[string]keycloakv1alpha1.RoleSpec {
	desiredRoles := make(map[string]keycloakv1alpha1.RoleSpec)
	for _, roleSpec := range clientObj.Spec.Roles {
		desiredRoles[roleSpec.Name] = roleSpec
	}
	return desiredRoles
}

// cleanupTrackedRoles removes tracking for roles that are no longer desired
func (r *ClientReconciler) cleanupTrackedRoles(ctx context.Context, clientObj *keycloakv1alpha1.Client, desiredRoles map[string]keycloakv1alpha1.RoleSpec) {
	logger := log.FromContext(ctx)
	for trackedRoleName := range clientObj.Status.RoleUUIDs {
		if _, stillDesired := desiredRoles[trackedRoleName]; !stillDesired {
			logger.Info("Removing role UUID from tracking", "role", trackedRoleName, "uuid", clientObj.Status.RoleUUIDs[trackedRoleName])
			delete(clientObj.Status.RoleUUIDs, trackedRoleName)
		}
	}
}

// deleteUnwantedRoles deletes roles that exist in Keycloak but are not desired
func (r *ClientReconciler) deleteUnwantedRoles(ctx context.Context, keycloakClient *keycloak.KeycloakClient, existingRoles []*keycloak.Role, desiredRoles map[string]keycloakv1alpha1.RoleSpec, realmName string) error {
	logger := log.FromContext(ctx)
	for _, existingRole := range existingRoles {
		if _, stillDesired := desiredRoles[existingRole.Name]; !stillDesired {
			logger.Info("Deleting unwanted client role", "role", existingRole.Name, "uuid", existingRole.Id)

			if err := keycloakClient.DeleteRole(ctx, realmName, existingRole.Id); err != nil {
				if !keycloak.ErrorIs404(err) {
					return fmt.Errorf("failed to delete role %s: %w", existingRole.Name, err)
				}
				logger.Info("Role was already deleted from Keycloak", "role", existingRole.Name)
			} else {
				logger.Info("Successfully deleted client role", "role", existingRole.Name)
			}
		}
	}
	return nil
}

// createOrUpdateRoles creates new roles or updates existing ones
func (r *ClientReconciler) createOrUpdateRoles(ctx context.Context, keycloakClient *keycloak.KeycloakClient, existingRoleMap map[string]*keycloak.Role, desiredRoles map[string]keycloakv1alpha1.RoleSpec, clientObj *keycloakv1alpha1.Client, realm *keycloakv1alpha1.Realm) error {
	for roleName, roleSpec := range desiredRoles {
		if existingRole, exists := existingRoleMap[roleName]; exists {
			if err := r.updateRoleIfNeeded(ctx, keycloakClient, existingRole, roleSpec); err != nil {
				return err
			}
			clientObj.Status.RoleUUIDs[roleName] = existingRole.Id
		} else {
			if err := r.createRole(ctx, keycloakClient, roleName, roleSpec, clientObj, realm); err != nil {
				return err
			}
		}
	}
	return nil
}

// updateRoleIfNeeded updates a role if its description has changed
func (r *ClientReconciler) updateRoleIfNeeded(ctx context.Context, keycloakClient *keycloak.KeycloakClient, existingRole *keycloak.Role, roleSpec keycloakv1alpha1.RoleSpec) error {
	logger := log.FromContext(ctx)
	if existingRole.Description != roleSpec.Description {
		logger.Info("Updating client role", "role", existingRole.Name)

		updatedRole := &keycloak.Role{
			Id:          existingRole.Id,
			RealmId:     existingRole.RealmId,
			ClientId:    existingRole.ClientId,
			Name:        existingRole.Name,
			Description: roleSpec.Description,
			ClientRole:  true,
			ContainerId: existingRole.ContainerId,
			Composite:   existingRole.Composite,
			Attributes:  existingRole.Attributes,
		}

		if err := keycloakClient.UpdateRole(ctx, updatedRole); err != nil {
			return fmt.Errorf("failed to update role %s: %w", existingRole.Name, err)
		}
		logger.Info("Successfully updated client role", "role", existingRole.Name)
	}
	return nil
}

// createRole creates a new role in Keycloak
func (r *ClientReconciler) createRole(ctx context.Context, keycloakClient *keycloak.KeycloakClient, roleName string, roleSpec keycloakv1alpha1.RoleSpec, clientObj *keycloakv1alpha1.Client, realm *keycloakv1alpha1.Realm) error {
	logger := log.FromContext(ctx)
	logger.Info("Creating client role", "role", roleName)

	newRole := &keycloak.Role{
		RealmId:     realm.Name,
		ClientId:    clientObj.Status.ClientUUID,
		Name:        roleName,
		Description: roleSpec.Description,
		ClientRole:  true,
		ContainerId: clientObj.Status.ClientUUID,
		Composite:   false,
		Attributes:  make(map[string][]string),
	}

	if err := keycloakClient.CreateRole(ctx, newRole); err != nil {
		return fmt.Errorf("failed to create role %s: %w", roleName, err)
	}

	clientObj.Status.RoleUUIDs[roleName] = newRole.Id
	logger.Info("Successfully created client role", "role", roleName, "uuid", newRole.Id)
	return nil
}

// getSuccessMessage returns an appropriate success message
func (r *ClientReconciler) getSuccessMessage(clientChanged bool) string {
	if clientChanged {
		return "Client and roles reconciled successfully"
	}
	return "Client and roles reconciled"
}

// reconcileDelete handles client deletion
func (r *ClientReconciler) reconcileDelete(ctx context.Context, keycloakClient *keycloak.KeycloakClient, clientObj *keycloakv1alpha1.Client) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(clientObj, clientFinalizer) {
		return ctrl.Result{}, nil
	}

	if err := r.deleteClientFromKeycloak(ctx, keycloakClient, clientObj); err != nil {
		return ctrl.Result{}, err
	}

	controllerutil.RemoveFinalizer(clientObj, clientFinalizer)
	return ctrl.Result{}, r.Update(ctx, clientObj)
}

// deleteClientFromKeycloak handles the actual deletion from Keycloak
func (r *ClientReconciler) deleteClientFromKeycloak(ctx context.Context, keycloakClient *keycloak.KeycloakClient, clientObj *keycloakv1alpha1.Client) error {
	logger := log.FromContext(ctx)
	if clientObj.Status.ClientUUID == "" {
		logger.Info("No UUID stored - skipping Keycloak deletion")
		return nil
	}

	realm, err := r.getRealm(ctx, clientObj)
	if err != nil {
		logger.Error(err, "Failed to get realm for client deletion")
		return err
	}

	if realm == nil {
		logger.Info("Realm not found - skipping Keycloak deletion")
		return nil
	}

	r.deleteClientRoles(ctx, keycloakClient, clientObj, realm)
	return r.deleteClient(ctx, keycloakClient, clientObj, realm)
}

// deleteClientRoles deletes all client roles
func (r *ClientReconciler) deleteClientRoles(ctx context.Context, keycloakClient *keycloak.KeycloakClient, clientObj *keycloakv1alpha1.Client, realm *keycloakv1alpha1.Realm) {
	logger := log.FromContext(ctx)
	if len(clientObj.Status.RoleUUIDs) == 0 {
		return
	}

	logger.Info("Deleting client roles from Keycloak", "roleCount", len(clientObj.Status.RoleUUIDs))
	for roleName, roleUUID := range clientObj.Status.RoleUUIDs {
		if err := keycloakClient.DeleteRole(ctx, realm.Name, roleUUID); err != nil {
			if !keycloak.ErrorIs404(err) {
				logger.Error(err, "Failed to delete client role", "role", roleName)
			}
		}
	}
}

// deleteClient deletes the client itself
func (r *ClientReconciler) deleteClient(ctx context.Context, keycloakClient *keycloak.KeycloakClient, clientObj *keycloakv1alpha1.Client, realm *keycloakv1alpha1.Realm) error {
	logger := log.FromContext(ctx)
	logger.Info("Deleting client from Keycloak", "uuid", clientObj.Status.ClientUUID)

	if err := keycloakClient.DeleteOpenidClient(ctx, realm.Name, clientObj.Status.ClientUUID); err != nil {
		if keycloak.ErrorIs404(err) {
			logger.Info("Client already deleted from Keycloak")
			return nil
		}
		logger.Error(err, "Failed to delete client from Keycloak")
		return err
	}

	logger.Info("Client and roles deleted from Keycloak")
	return nil
}

// updateStatus updates the client status with retry logic
func (r *ClientReconciler) updateStatus(ctx context.Context, clientObj *keycloakv1alpha1.Client, result ReconcileResult) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("client", clientObj.Name)

	for i := range maxStatusRetries {
		if err := r.performStatusUpdate(ctx, clientObj, result); err != nil {
			if errors.IsConflict(err) && i < maxStatusRetries-1 {
				logger.V(1).Info("Status update conflict, retrying", "attempt", i+1)
				time.Sleep(time.Duration(i+1) * 100 * time.Millisecond)
				continue
			}
			logger.Error(err, "Failed to update status after retries")
			return ctrl.Result{}, err
		}
		break
	}

	// Handle different requeue scenarios based on the result
	if result.Error != nil {
		return ctrl.Result{}, result.Error
	}

	// Different requeue intervals based on the result state
	switch {
	case !result.RealmReady:
		// Shorter interval when waiting for realm to be ready
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	case result.Ready:
		// Longer interval for ready clients (periodic sync)
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil //change to 10 min later!
	default:
		// Default interval for other cases
		return ctrl.Result{RequeueAfter: requeueInterval}, nil
	}
}

// performStatusUpdate performs a single status update attempt
func (r *ClientReconciler) performStatusUpdate(ctx context.Context, clientObj *keycloakv1alpha1.Client, result ReconcileResult) error {
	logger := log.FromContext(ctx).WithValues("client", clientObj.Name)

	var latestClient keycloakv1alpha1.Client
	if err := r.Get(ctx, client.ObjectKeyFromObject(clientObj), &latestClient); err != nil {
		return err
	}

	// Optional: Check if update is needed to avoid unnecessary API calls
	if r.statusUnchanged(&latestClient, result) {
		logger.V(2).Info("Status unchanged, skipping update")
		clientObj.Status = latestClient.Status
		return nil
	}

	r.applyStatusUpdate(&latestClient, clientObj, result)

	if err := r.Status().Update(ctx, &latestClient); err != nil {
		return err
	}

	clientObj.Status = latestClient.Status
	logger.V(1).Info("Status updated successfully",
		"ready", latestClient.Status.Ready,
		"realmReady", latestClient.Status.RealmReady,
		"message", latestClient.Status.Message,
		"clientUUID", latestClient.Status.ClientUUID,
		"roleCount", len(latestClient.Status.RoleUUIDs))

	return nil
}

// applyStatusUpdate applies the status changes to the latest client object
func (r *ClientReconciler) applyStatusUpdate(latestClient, originalClient *keycloakv1alpha1.Client, result ReconcileResult) {
	latestClient.Status.Ready = result.Ready
	latestClient.Status.RealmReady = result.RealmReady
	latestClient.Status.Message = result.Message
	now := metav1.NewTime(time.Now())
	latestClient.Status.LastSyncTime = &now

	// Preserve operational data from the original client
	if originalClient.Status.ClientUUID != "" {
		latestClient.Status.ClientUUID = originalClient.Status.ClientUUID
	}

	if len(originalClient.Status.RoleUUIDs) > 0 {
		latestClient.Status.RoleUUIDs = originalClient.Status.RoleUUIDs
	} else if latestClient.Status.RoleUUIDs == nil {
		// Only initialize if it doesn't already exist
		latestClient.Status.RoleUUIDs = make(map[string]string)
	}
}

// statusUnchanged checks if the status would actually change
func (r *ClientReconciler) statusUnchanged(latest *keycloakv1alpha1.Client, result ReconcileResult) bool {
	return latest.Status.Ready == result.Ready &&
		latest.Status.RealmReady == result.RealmReady &&
		latest.Status.Message == result.Message
}

// Reconcile secret
func (r *ClientReconciler) reconcileClientSecret(ctx context.Context, clientObj *keycloakv1alpha1.Client, keycloakClient *keycloak.KeycloakClient, realm *keycloakv1alpha1.Realm, keycloakOpenIdClient *keycloak.OpenidClient) error {
	logger := log.FromContext(ctx).WithValues("client", clientObj.Name)

	// Only create secrets for confidential clients
	if clientObj.Spec.PublicClient {
		// If this was previously a confidential client, clean up the secret
		if clientObj.Status.Secret != nil && clientObj.Status.Secret.SecretCreated {
			if err := r.deleteClientSecret(ctx, clientObj); err != nil {
				logger.Error(err, "Failed to delete secret for now-public client")
				return err
			}
		}
		return nil
	}

	// Check if secret creation is disabled
	if clientObj.Spec.Secret != nil && !clientObj.Spec.Secret.CreateSecret {
		return nil
	}

	// Verify we have a client object
	if keycloakOpenIdClient == nil {
		return fmt.Errorf("keycloak client object is nil")
	}

	// Get the client secret
	clientSecret, err := r.getClientSecret(clientObj, keycloakOpenIdClient)
	if err != nil {
		return fmt.Errorf("failed to get client secret: %w", err)
	}

	// Create or update the Kubernetes secret
	if err := r.createOrUpdateClientSecret(ctx, clientObj, clientSecret); err != nil {
		return fmt.Errorf("failed to create/update client secret: %w", err)
	}

	return nil
}

func (r *ClientReconciler) getClientSecret(clientObj *keycloakv1alpha1.Client, keycloakOpenIdClient *keycloak.OpenidClient) (string, error) {
	if clientObj.Status.ClientUUID == "" {
		return "", fmt.Errorf("client UUID not available")
	}

	if keycloakOpenIdClient == nil {
		return "", fmt.Errorf("keycloak client is nil")
	}

	// For public clients, there shouldn't be a secret
	if keycloakOpenIdClient.PublicClient {
		return "", fmt.Errorf("cannot get secret for public client")
	}

	secret := keycloakOpenIdClient.ClientSecret
	if secret == "" {
		return "", fmt.Errorf("client secret is empty - ensure this is a confidential client")
	}

	return secret, nil
}

func (r *ClientReconciler) createOrUpdateClientSecret(ctx context.Context, clientObj *keycloakv1alpha1.Client, clientSecret string) error {
	logger := log.FromContext(ctx).WithValues("client", clientObj.Name)

	secretName := r.getSecretName(clientObj)
	secretNamespace := r.getSecretNamespace(clientObj)

	// Create the desired secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        secretName,
			Namespace:   secretNamespace,
			Labels:      r.getSecretLabels(clientObj),
			Annotations: r.getSecretAnnotations(clientObj),
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"client-id":     []byte(clientObj.Spec.ClientID),
			"client-secret": []byte(clientSecret),
		},
	}

	// Set owner reference
	if err := ctrl.SetControllerReference(clientObj, secret, r.Scheme); err != nil {
		return fmt.Errorf("failed to set controller reference: %w", err)
	}

	// Check if secret already exists
	existingSecret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: secretNamespace}, existingSecret)

	if err != nil {
		if errors.IsNotFound(err) {
			// Secret doesn't exist, create it
			if err := r.Create(ctx, secret); err != nil {
				return fmt.Errorf("failed to create kubernetes secret: %w", err)
			}
			logger.Info("Created kubernetes client secret",
				"secretName", secretName,
				"secretNamespace", secretNamespace)
			return nil
		}
		return fmt.Errorf("failed to get existing secret: %w", err)
	}

	// Secret exists, check if it needs updating
	needsUpdate := false

	// Check if client-id changed
	if string(existingSecret.Data["client-id"]) != clientObj.Spec.ClientID {
		needsUpdate = true
	}

	// Check if client-secret changed
	if string(existingSecret.Data["client-secret"]) != clientSecret {
		needsUpdate = true
	}

	// Check if labels changed
	if !maps.Equal(existingSecret.Labels, secret.Labels) {
		needsUpdate = true
	}

	// Check if annotations changed
	if !maps.Equal(existingSecret.Annotations, secret.Annotations) {
		needsUpdate = true
	}

	if needsUpdate {
		// Update the existing secret
		existingSecret.Data = secret.Data
		existingSecret.Labels = secret.Labels           // Update labels
		existingSecret.Annotations = secret.Annotations // Update annotations

		if err := r.Update(ctx, existingSecret); err != nil {
			return fmt.Errorf("failed to update kubernetes secret: %w", err)
		}
		logger.Info("Updated kubernetes client secret",
			"secretName", secretName,
			"secretNamespace", secretNamespace,
			"reason", "secret content changed")
	} else {
		logger.V(1).Info("Kubernetes Secret already up to date",
			"secretName", secretName,
			"secretNamespace", secretNamespace)
	}

	return nil
}

func (r *ClientReconciler) getSecretName(clientObj *keycloakv1alpha1.Client) string {
	if clientObj.Spec.Secret != nil && clientObj.Spec.Secret.SecretName != "" {
		return clientObj.Spec.Secret.SecretName
	}
	return fmt.Sprintf("%s-client-secret", clientObj.Name)
}

func (r *ClientReconciler) getSecretNamespace(clientObj *keycloakv1alpha1.Client) string {
	if clientObj.Spec.Secret != nil && clientObj.Spec.Secret.SecretNamespace != "" {
		return clientObj.Spec.Secret.SecretNamespace
	}
	return clientObj.Namespace
}

func (r *ClientReconciler) getSecretLabels(clientObj *keycloakv1alpha1.Client) map[string]string {
	labels := map[string]string{
		"app.kubernetes.io/name":       "keycloak-client",
		"app.kubernetes.io/instance":   clientObj.Name,
		"app.kubernetes.io/managed-by": "keycloak-operator",
    "app.kubernetes.io/version":    "v1alpha1",
    "app.kubernetes.io/component":  "client-secret",
    "app.kubernetes.io/part-of":    "keycloak",
	}

	// Add additional labels if specified
	if clientObj.Spec.Secret != nil && clientObj.Spec.Secret.AdditionalLabels != nil {
		maps.Copy(labels, clientObj.Spec.Secret.AdditionalLabels)
	}

	return labels
}

func (r *ClientReconciler) getSecretAnnotations(clientObj *keycloakv1alpha1.Client) map[string]string {
	annotations := map[string]string{
		"keycloak.org/client-name": clientObj.Name,
		"keycloak.org/realm":       clientObj.Spec.RealmRef.Name,
	}

	// Add additional annotations if specified
	if clientObj.Spec.Secret != nil && clientObj.Spec.Secret.AdditionalAnnotations != nil {
		maps.Copy(annotations, clientObj.Spec.Secret.AdditionalAnnotations)
	}

	return annotations
}

func (r *ClientReconciler) deleteClientSecret(ctx context.Context, clientObj *keycloakv1alpha1.Client) error {
	if clientObj.Status.Secret == nil || !clientObj.Status.Secret.SecretCreated {
		return nil
	}

	secret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      clientObj.Status.Secret.SecretName,
		Namespace: clientObj.Status.Secret.SecretNamespace,
	}, secret)

	if err != nil {
		if errors.IsNotFound(err) {
			return nil // Already deleted
		}
		return err
	}

	return r.Delete(ctx, secret)
}

func (r *ClientReconciler) updateSecretStatus(clientObj *keycloakv1alpha1.Client, created bool, secretName, secretNamespace string) {
	if clientObj.Status.Secret == nil {
		clientObj.Status.Secret = &keycloakv1alpha1.ClientSecretStatus{}
	}

	now := metav1.Now()
	clientObj.Status.Secret.SecretCreated = created
	clientObj.Status.Secret.SecretName = secretName
	clientObj.Status.Secret.SecretNamespace = secretNamespace
	clientObj.Status.Secret.LastSecretUpdate = &now
}

// Reconcile secret end

// SetupWithManager sets up the controller with the Manager
func (r *ClientReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&keycloakv1alpha1.Client{}).
		Complete(r)
}
