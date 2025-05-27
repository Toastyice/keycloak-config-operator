package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

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
)

const (
	clientFinalizer  = "client.keycloak.schella.network/finalizer"
	requeueInterval  = 10 * time.Second
	maxStatusRetries = 3
)

// ClientReconciler reconciles a Client object
type ClientReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	KeycloakClient *keycloak.KeycloakClient
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

	if err := r.validateReconciler(); err != nil {
		logger.Error(err, "Controller not properly initialized")
		return ctrl.Result{}, err
	}

	clientObj, err := r.fetchClient(ctx, req.NamespacedName)
	if err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Unable to fetch Client")
		return ctrl.Result{}, err
	}

	if r.isMarkedForDeletion(clientObj) {
		return r.reconcileDelete(ctx, clientObj)
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

	return r.reconcileClient(ctx, clientObj, realm)
}

// validateReconciler ensures the controller is properly initialized
func (r *ClientReconciler) validateReconciler() error {
	if r.KeycloakClient == nil {
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
func (r *ClientReconciler) reconcileClient(ctx context.Context, clientObj *keycloakv1alpha1.Client, realm *keycloakv1alpha1.Realm) (ctrl.Result, error) {
	keycloakClient, err := r.getOrCreateClient(ctx, clientObj, realm)
	if err != nil {
		result := ReconcileResult{
			Ready:      false,
			RealmReady: true,
			Message:    fmt.Sprintf("Failed to reconcile client: %v", err),
		}
		return r.updateStatus(ctx, clientObj, result)
	}

	clientChanged := keycloakClient == nil // If nil, it means we created
	if keycloakClient != nil {
		clientChanged = r.updateClientIfNeeded(ctx, clientObj, keycloakClient)
	}

	if clientObj.Status.ClientUUID != "" {
		if err := r.reconcileClientRoles(ctx, clientObj, realm); err != nil {
			result := ReconcileResult{
				Ready:      false,
				RealmReady: true,
				Message:    fmt.Sprintf("Client ready but failed to reconcile roles: %v", err),
			}
			return r.updateStatus(ctx, clientObj, result)
		}
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
func (r *ClientReconciler) getOrCreateClient(ctx context.Context, clientObj *keycloakv1alpha1.Client, realm *keycloakv1alpha1.Realm) (*keycloak.OpenidClient, error) {
	if clientObj.Status.ClientUUID != "" {
		client, err := r.fetchExistingClient(ctx, clientObj, realm)
		if err != nil {
			return nil, err
		}
		if client != nil {
			return client, nil
		}
	}

	return nil, r.createNewClient(ctx, clientObj, realm)
}

// fetchExistingClient retrieves an existing client by UUID
func (r *ClientReconciler) fetchExistingClient(ctx context.Context, clientObj *keycloakv1alpha1.Client, realm *keycloakv1alpha1.Realm) (*keycloak.OpenidClient, error) {
	logger := log.FromContext(ctx)
	logger.V(1).Info("Fetching client by UUID", "uuid", clientObj.Status.ClientUUID)

	client, err := r.KeycloakClient.GetOpenidClient(ctx, realm.Name, clientObj.Status.ClientUUID)
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
func (r *ClientReconciler) createNewClient(ctx context.Context, clientObj *keycloakv1alpha1.Client, realm *keycloakv1alpha1.Realm) error {
	logger := log.FromContext(ctx)
	logger.Info("Creating client in Keycloak")

	newClient := r.buildClientFromSpec(clientObj, realm)

	if err := r.KeycloakClient.NewOpenidClient(ctx, newClient); err != nil {
		if keycloak.ErrorIs409(err) {
			return fmt.Errorf("client exists in Keycloak but UUID not tracked - manual intervention required")
		}
		return fmt.Errorf("failed to create client: %w", err)
	}

	return r.storeClientUUID(ctx, clientObj, realm)
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
func (r *ClientReconciler) storeClientUUID(ctx context.Context, clientObj *keycloakv1alpha1.Client, realm *keycloakv1alpha1.Realm) error {
	logger := log.FromContext(ctx)
	createdClient, err := r.KeycloakClient.GetOpenidClientByClientId(ctx, realm.Name, clientObj.Spec.ClientID)
	if err != nil {
		return fmt.Errorf("client created but failed to retrieve UUID: %w", err)
	}

	clientObj.Status.ClientUUID = createdClient.Id
	clientObj.Status.RoleUUIDs = make(map[string]string)

	logger.Info("Client created successfully", "uuid", createdClient.Id)
	return nil
}

// updateClientIfNeeded checks for differences and updates the client if needed
func (r *ClientReconciler) updateClientIfNeeded(ctx context.Context, clientObj *keycloakv1alpha1.Client, keycloakClient *keycloak.OpenidClient) bool {
	logger := log.FromContext(ctx)
	diffs := r.getClientDiffs(clientObj, keycloakClient)
	if len(diffs) == 0 {
		return false
	}

	logger.Info("Client configuration changes detected", "changes", strings.Join(diffs, ", "))

	updatedClient := r.applyChangesToClient(clientObj, keycloakClient)
	if err := r.KeycloakClient.UpdateOpenidClient(ctx, updatedClient); err != nil {
		logger.Error(err, "Failed to update client")
		return false
	}

	logger.Info("Client updated successfully")
	return true
}

// applyChangesToClient applies spec changes to the Keycloak client
func (r *ClientReconciler) applyChangesToClient(clientObj *keycloakv1alpha1.Client, keycloakClient *keycloak.OpenidClient) *keycloak.OpenidClient {
	updatedClient := *keycloakClient

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
func (r *ClientReconciler) getClientDiffs(clientObj *keycloakv1alpha1.Client, keycloakClient *keycloak.OpenidClient) []string {
	var diffs []string

	fields := []FieldDiff{
		{"name", keycloakClient.Name, clientObj.Spec.Name},
		{"description", keycloakClient.Description, clientObj.Spec.Description},
		{"enabled", keycloakClient.Enabled, clientObj.Spec.Enabled},
		{"rootUrl", derefStringPtr(keycloakClient.RootUrl), clientObj.Spec.RootUrl},
		{"baseUrl", keycloakClient.BaseUrl, clientObj.Spec.BaseUrl},
		{"adminUrl", keycloakClient.AdminUrl, clientObj.Spec.AdminUrl},
		{"alwaysDisplayInConsole", keycloakClient.AlwaysDisplayInConsole, clientObj.Spec.AlwaysDisplayInConsole},
		{"clientAuthenticatorType", keycloakClient.ClientAuthenticatorType, clientObj.Spec.ClientAuthenticatorType},
		{"publicClient", keycloakClient.PublicClient, clientObj.Spec.PublicClient},
		{"standardFlowEnabled", keycloakClient.StandardFlowEnabled, clientObj.Spec.StandardFlowEnabled},
		{"directAccessGrantsEnabled", keycloakClient.DirectAccessGrantsEnabled, clientObj.Spec.DirectAccessGrantsEnabled},
		{"implicitFlowEnabled", keycloakClient.ImplicitFlowEnabled, clientObj.Spec.ImplicitFlowEnabled},
		{"serviceAccountsEnabled", keycloakClient.ServiceAccountsEnabled, clientObj.Spec.ServiceAccountsEnabled},
		{"oauth2DeviceAuthorizationGrantEnabled", keycloakClient.Attributes.Oauth2DeviceAuthorizationGrantEnabled, keycloakTypes.KeycloakBoolQuoted(clientObj.Spec.Oauth2DeviceAuthorizationGrantEnabled)},
		{"loginTheme", keycloakClient.Attributes.LoginTheme, clientObj.Spec.LoginTheme},
		{"consentRequired", keycloakClient.ConsentRequired, clientObj.Spec.ConsentRequired},
		{"displayOnConsentScreen", keycloakClient.Attributes.DisplayOnConsentScreen, keycloakTypes.KeycloakBoolQuoted(clientObj.Spec.DisplayOnConsentScreen)},
		{"consentScreenText", keycloakClient.Attributes.ConsentScreenText, clientObj.Spec.ConsentScreenText},
		{"frontchannelLogoutEnabled", keycloakClient.FrontChannelLogoutEnabled, clientObj.Spec.FrontchannelLogoutEnabled},
		{"frontchannelLogoutUrl", keycloakClient.Attributes.FrontchannelLogoutUrl, clientObj.Spec.FrontchannelLogoutUrl},
		{"backchannelLogoutUrl", keycloakClient.Attributes.BackchannelLogoutUrl, clientObj.Spec.BackchannelLogoutUrl},
		{"backchannelLogoutSessionRequired", keycloakClient.Attributes.BackchannelLogoutSessionRequired, keycloakTypes.KeycloakBoolQuoted(clientObj.Spec.BackchannelLogoutSessionRequired)},
		{"backchannelLogoutRevokeOfflineTokens", keycloakClient.Attributes.BackchannelLogoutRevokeOfflineTokens, keycloakTypes.KeycloakBoolQuoted(clientObj.Spec.BackchannelLogoutRevokeOfflineTokens)},
	}

	for _, field := range fields {
		if !reflect.DeepEqual(field.Old, field.New) {
			diffs = append(diffs, r.formatFieldDiff(field))
		}
	}

	// Handle slice fields
	if !r.slicesEqual(keycloakClient.ValidRedirectUris, clientObj.Spec.RedirectUris) {
		diffs = append(diffs, fmt.Sprintf("validRedirectUris: %v -> %v", keycloakClient.ValidRedirectUris, clientObj.Spec.RedirectUris))
	}

	if !r.slicesEqual(keycloakClient.WebOrigins, clientObj.Spec.WebOrigins) {
		diffs = append(diffs, fmt.Sprintf("webOrigins: %v -> %v", keycloakClient.WebOrigins, clientObj.Spec.WebOrigins))
	}

	if !r.slicesEqual(keycloakClient.Attributes.PostLogoutRedirectUris, clientObj.Spec.PostLogoutRedirectUris) {
		diffs = append(diffs, fmt.Sprintf("postLogoutRedirectUris: %v -> %v", keycloakClient.Attributes.PostLogoutRedirectUris, clientObj.Spec.PostLogoutRedirectUris))
	}

	// Handle ExtraConfig with runtime.RawExtension
	if equal, diff, err := r.compareExtraConfig(keycloakClient, clientObj); err != nil {
		// Handle error - you might want to log this or add it as a diff
		diffs = append(diffs, fmt.Sprintf("extraConfig: error comparing - %v", err))
	} else if !equal {
		diffs = append(diffs, diff)
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

// getExtraConfigMap extracts map[string]interface{} from runtime.RawExtension
func (r *ClientReconciler) getExtraConfigMap(rawExt *runtime.RawExtension) (map[string]interface{}, error) {
	if rawExt == nil || rawExt.Raw == nil || len(rawExt.Raw) == 0 {
		return make(map[string]interface{}), nil
	}

	var result map[string]interface{}
	if err := json.Unmarshal(rawExt.Raw, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal extra config: %w", err)
	}

	if result == nil {
		result = make(map[string]interface{})
	}

	return result, nil
}

// compareExtraConfig compares ExtraConfig using the keycloak package's normalization logic
// This leverages the existing unmarshal/marshal functions which already separate
// structured fields from truly "extra" configuration
func (r *ClientReconciler) compareExtraConfig(keycloakClient *keycloak.OpenidClient, clientObj *keycloakv1alpha1.Client) (bool, string, error) {
	// Get the current ExtraConfig from Keycloak
	// This should already be normalized by the UnmarshalJSON function
	// which removes structured fields and only keeps truly "extra" config
	currentExtraConfig := keycloakClient.Attributes.ExtraConfig

	// Get the desired ExtraConfig from the client spec
	desiredExtraConfig, err := r.getExtraConfigMap(clientObj.Spec.ExtraConfig)
	if err != nil {
		return false, "", fmt.Errorf("failed to parse desired extra config: %w", err)
	}

	// The keycloak package's unmarshal function should have already filtered out
	// structured fields, so currentExtraConfig should only contain truly extra config

	// Handle nil maps by treating them as empty
	if currentExtraConfig == nil {
		currentExtraConfig = make(map[string]interface{})
	}
	if desiredExtraConfig == nil {
		desiredExtraConfig = make(map[string]interface{})
	}

	// If both are empty, they're equal
	if len(currentExtraConfig) == 0 && len(desiredExtraConfig) == 0 {
		return true, "", nil
	}

	// Use reflect.DeepEqual for comparison
	if reflect.DeepEqual(currentExtraConfig, desiredExtraConfig) {
		return true, "", nil
	}

	// Create diff string
	diff := r.formatExtraConfigDiff(currentExtraConfig, desiredExtraConfig)
	return false, diff, nil
}

// formatExtraConfigDiff creates a readable string representation of ExtraConfig differences
func (r *ClientReconciler) formatExtraConfigDiff(current, desired map[string]interface{}) string {
	var changes []string

	// Get all unique keys
	allKeys := make(map[string]bool)
	for k := range current {
		allKeys[k] = true
	}
	for k := range desired {
		allKeys[k] = true
	}

	// Convert to sorted slice for consistent output
	keys := make([]string, 0, len(allKeys))
	for k := range allKeys {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		currentVal, currentExists := current[key]
		desiredVal, desiredExists := desired[key]

		if !currentExists {
			changes = append(changes, fmt.Sprintf("  +%s: %v", key, desiredVal))
		} else if !desiredExists {
			changes = append(changes, fmt.Sprintf("  -%s: %v", key, currentVal))
		} else if !reflect.DeepEqual(currentVal, desiredVal) {
			changes = append(changes, fmt.Sprintf("  ~%s: %v -> %v", key, currentVal, desiredVal))
		}
	}

	if len(changes) == 0 {
		return "extraConfig: no changes"
	}

	return fmt.Sprintf("extraConfig:\n%s", strings.Join(changes, "\n"))
}

// Helper function to convert map[string]interface{} back to runtime.RawExtension
// Useful if you need to update the client spec with normalized values
func (r *ClientReconciler) mapToRawExtension(data map[string]interface{}) (*runtime.RawExtension, error) {
	if len(data) == 0 {
		return nil, nil
	}

	raw, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal extra config: %w", err)
	}

	return &runtime.RawExtension{Raw: raw}, nil
}

// getClientRolesForSingleClient retrieves roles for a specific client
func (r *ClientReconciler) getClientRolesForSingleClient(ctx context.Context, realmName, clientUUID string) ([]*keycloak.Role, error) {
	mockClients := []*keycloak.OpenidClient{{Id: clientUUID}}
	return r.KeycloakClient.GetClientRoles(ctx, realmName, mockClients)
}

// reconcileClientRoles handles role synchronization for the client
func (r *ClientReconciler) reconcileClientRoles(ctx context.Context, clientObj *keycloakv1alpha1.Client, realm *keycloakv1alpha1.Realm) error {
	logger := log.FromContext(ctx)
	r.initializeRoleUUIDs(clientObj)

	existingRoles, err := r.getClientRolesForSingleClient(ctx, realm.Name, clientObj.Status.ClientUUID)
	if err != nil {
		return fmt.Errorf("failed to get existing client roles: %w", err)
	}

	existingRoleMap := r.buildRoleMap(existingRoles)
	desiredRoles := r.buildDesiredRoleMap(clientObj)

	logger.V(1).Info("Role reconciliation starting",
		"existingRoles", len(existingRoles),
		"desiredRoles", len(desiredRoles))

	r.cleanupTrackedRoles(ctx, clientObj, desiredRoles)

	if err := r.deleteUnwantedRoles(ctx, existingRoles, desiredRoles, realm.Name); err != nil {
		return err
	}

	if err := r.createOrUpdateRoles(ctx, existingRoleMap, desiredRoles, clientObj, realm); err != nil {
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
func (r *ClientReconciler) deleteUnwantedRoles(ctx context.Context, existingRoles []*keycloak.Role, desiredRoles map[string]keycloakv1alpha1.RoleSpec, realmName string) error {
	logger := log.FromContext(ctx)
	for _, existingRole := range existingRoles {
		if _, stillDesired := desiredRoles[existingRole.Name]; !stillDesired {
			logger.Info("Deleting unwanted client role", "role", existingRole.Name, "uuid", existingRole.Id)

			if err := r.KeycloakClient.DeleteRole(ctx, realmName, existingRole.Id); err != nil {
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
func (r *ClientReconciler) createOrUpdateRoles(ctx context.Context, existingRoleMap map[string]*keycloak.Role, desiredRoles map[string]keycloakv1alpha1.RoleSpec, clientObj *keycloakv1alpha1.Client, realm *keycloakv1alpha1.Realm) error {
	for roleName, roleSpec := range desiredRoles {
		if existingRole, exists := existingRoleMap[roleName]; exists {
			if err := r.updateRoleIfNeeded(ctx, existingRole, roleSpec); err != nil {
				return err
			}
			clientObj.Status.RoleUUIDs[roleName] = existingRole.Id
		} else {
			if err := r.createRole(ctx, roleName, roleSpec, clientObj, realm); err != nil {
				return err
			}
		}
	}
	return nil
}

// updateRoleIfNeeded updates a role if its description has changed
func (r *ClientReconciler) updateRoleIfNeeded(ctx context.Context, existingRole *keycloak.Role, roleSpec keycloakv1alpha1.RoleSpec) error {
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

		if err := r.KeycloakClient.UpdateRole(ctx, updatedRole); err != nil {
			return fmt.Errorf("failed to update role %s: %w", existingRole.Name, err)
		}
		logger.Info("Successfully updated client role", "role", existingRole.Name)
	}
	return nil
}

// createRole creates a new role in Keycloak
func (r *ClientReconciler) createRole(ctx context.Context, roleName string, roleSpec keycloakv1alpha1.RoleSpec, clientObj *keycloakv1alpha1.Client, realm *keycloakv1alpha1.Realm) error {
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

	if err := r.KeycloakClient.CreateRole(ctx, newRole); err != nil {
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
func (r *ClientReconciler) reconcileDelete(ctx context.Context, clientObj *keycloakv1alpha1.Client) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(clientObj, clientFinalizer) {
		return ctrl.Result{}, nil
	}

	if err := r.deleteClientFromKeycloak(ctx, clientObj); err != nil {
		return ctrl.Result{}, err
	}

	controllerutil.RemoveFinalizer(clientObj, clientFinalizer)
	return ctrl.Result{}, r.Update(ctx, clientObj)
}

// deleteClientFromKeycloak handles the actual deletion from Keycloak
func (r *ClientReconciler) deleteClientFromKeycloak(ctx context.Context, clientObj *keycloakv1alpha1.Client) error {
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

	r.deleteClientRoles(ctx, clientObj, realm)
	return r.deleteClient(ctx, clientObj, realm)
}

// deleteClientRoles deletes all client roles
func (r *ClientReconciler) deleteClientRoles(ctx context.Context, clientObj *keycloakv1alpha1.Client, realm *keycloakv1alpha1.Realm) {
	logger := log.FromContext(ctx)
	if len(clientObj.Status.RoleUUIDs) == 0 {
		return
	}

	logger.Info("Deleting client roles from Keycloak", "roleCount", len(clientObj.Status.RoleUUIDs))
	for roleName, roleUUID := range clientObj.Status.RoleUUIDs {
		if err := r.KeycloakClient.DeleteRole(ctx, realm.Name, roleUUID); err != nil {
			if !keycloak.ErrorIs404(err) {
				logger.Error(err, "Failed to delete client role", "role", roleName)
			}
		}
	}
}

// deleteClient deletes the client itself
func (r *ClientReconciler) deleteClient(ctx context.Context, clientObj *keycloakv1alpha1.Client, realm *keycloakv1alpha1.Realm) error {
	logger := log.FromContext(ctx)
	logger.Info("Deleting client from Keycloak", "uuid", clientObj.Status.ClientUUID)

	if err := r.KeycloakClient.DeleteOpenidClient(ctx, realm.Name, clientObj.Status.ClientUUID); err != nil {
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

	if result.Error != nil {
		return ctrl.Result{}, result.Error
	}

	return ctrl.Result{RequeueAfter: requeueInterval}, nil
}

// performStatusUpdate performs a single status update attempt
func (r *ClientReconciler) performStatusUpdate(ctx context.Context, clientObj *keycloakv1alpha1.Client, result ReconcileResult) error {
	logger := log.FromContext(ctx)
	var latestClient keycloakv1alpha1.Client
	if err := r.Get(ctx, client.ObjectKeyFromObject(clientObj), &latestClient); err != nil {
		return err
	}

	r.applyStatusUpdate(&latestClient, clientObj, result)

	if err := r.Status().Update(ctx, &latestClient); err != nil {
		return err
	}

	clientObj.Status = latestClient.Status
	logger.V(1).Info("Status updated successfully",
		"ready", latestClient.Status.Ready,
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

	if originalClient.Status.ClientUUID != "" {
		latestClient.Status.ClientUUID = originalClient.Status.ClientUUID
	}

	if originalClient.Status.RoleUUIDs != nil {
		latestClient.Status.RoleUUIDs = originalClient.Status.RoleUUIDs
	} else {
		latestClient.Status.RoleUUIDs = make(map[string]string)
	}
}

// SetupWithManager sets up the controller with the Manager
func (r *ClientReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&keycloakv1alpha1.Client{}).
		Complete(r)
}
