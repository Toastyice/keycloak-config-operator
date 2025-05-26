package controller

import (
	"context"
	"fmt"
	"reflect"
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

	keycloakTypes "github.com/keycloak/terraform-provider-keycloak/keycloak/types"
	keycloakv1alpha1 "github.com/toastyice/keycloak-config-operator/api/v1alpha1"
	"github.com/toastyice/keycloak-config-operator/internal/keycloak"
)

const clientFinalizer = "client.keycloak.schella.network/finalizer"

// ClientReconciler reconciles a Client object
type ClientReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	KeycloakClient *keycloak.KeycloakClient
}

//+kubebuilder:rbac:groups=keycloak.schella.network,resources=clients,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=keycloak.schella.network,resources=clients/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=keycloak.schella.network,resources=clients/finalizers,verbs=update
//+kubebuilder:rbac:groups=keycloak.schella.network,resources=realms,verbs=get;list;watch

func (r *ClientReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Safety check - this should help identify initialization issues
	if r.KeycloakClient == nil {
		log.Error(nil, "KeycloakClient is nil - controller not properly initialized")
		return ctrl.Result{}, fmt.Errorf("KeycloakClient is nil - controller not properly initialized")
	}

	// Fetch the Client instance
	var clientObj keycloakv1alpha1.Client
	if err := r.Get(ctx, req.NamespacedName, &clientObj); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		log.Error(err, "unable to fetch Client")
		return ctrl.Result{}, err
	}

	// Check if the client is being deleted
	if !clientObj.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &clientObj)
	}

	// Add finalizer if it doesn't exist
	if !controllerutil.ContainsFinalizer(&clientObj, clientFinalizer) {
		controllerutil.AddFinalizer(&clientObj, clientFinalizer)
		if err := r.Update(ctx, &clientObj); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Validate and get the referenced realm
	realm, err := r.getRealm(ctx, &clientObj)
	if err != nil {
		return r.updateStatus(ctx, &clientObj, false, false, fmt.Sprintf("Failed to get realm: %v", err))
	}

	if realm == nil {
		return r.updateStatus(ctx, &clientObj, false, false, "Referenced realm not found")
	}

	// Check if the realm is ready
	if !realm.Status.Ready {
		return r.updateStatus(ctx, &clientObj, false, false, "Referenced realm is not ready")
	}

	// Set owner reference to the realm
	if err := r.setOwnerReference(ctx, &clientObj, realm); err != nil {
		log.Error(err, "Failed to set owner reference", "client", clientObj.Spec.ClientID, "realm", realm.Name)
		return r.updateStatus(ctx, &clientObj, false, true, fmt.Sprintf("Failed to set owner reference: %v", err))
	}

	// Reconcile the client
	return r.reconcileClient(ctx, &clientObj, realm)
}

func (r *ClientReconciler) getRealm(ctx context.Context, clientObj *keycloakv1alpha1.Client) (*keycloakv1alpha1.Realm, error) {
	// Determine the namespace for the realm
	realmNamespace := clientObj.Spec.RealmRef.Namespace
	if realmNamespace == "" {
		realmNamespace = clientObj.Namespace
	}

	// Fetch the realm
	var realm keycloakv1alpha1.Realm
	namespacedName := types.NamespacedName{
		Name:      clientObj.Spec.RealmRef.Name,
		Namespace: realmNamespace,
	}

	if err := r.Get(ctx, namespacedName, &realm); err != nil {
		if errors.IsNotFound(err) {
			return nil, nil // Realm not found, but not an error per se
		}
		return nil, err
	}

	return &realm, nil
}

// setOwnerReference establishes an owner-dependent relationship between the realm and client
func (r *ClientReconciler) setOwnerReference(ctx context.Context, clientObj *keycloakv1alpha1.Client, realm *keycloakv1alpha1.Realm) error {
	log := log.FromContext(ctx)

	// Check if owner reference already exists and is correct
	for _, ownerRef := range clientObj.GetOwnerReferences() {
		if ownerRef.Kind == "Realm" &&
			ownerRef.APIVersion == realm.APIVersion &&
			ownerRef.Name == realm.Name &&
			ownerRef.UID == realm.UID {
			// Owner reference already correctly set
			return nil
		}
	}

	// Cross-namespace owner references are not allowed in Kubernetes
	if clientObj.Namespace != realm.Namespace {
		log.V(1).Info("Skipping owner reference - cross-namespace references not allowed",
			"client", clientObj.Name, "clientNamespace", clientObj.Namespace,
			"realm", realm.Name, "realmNamespace", realm.Namespace)
		return nil
	}

	// Set the owner reference
	if err := controllerutil.SetOwnerReference(realm, clientObj, r.Scheme); err != nil {
		return fmt.Errorf("failed to set owner reference: %w", err)
	}

	// Update the client with the new owner reference
	if err := r.Update(ctx, clientObj); err != nil {
		return fmt.Errorf("failed to update client with owner reference: %w", err)
	}

	log.Info("Owner reference set successfully", "client", clientObj.Name, "realm", realm.Name)
	return nil
}

// getClientDiffs compares client specifications and returns a list of differences
func getClientDiffs(clientObj *keycloakv1alpha1.Client, keycloakClient *keycloak.OpenidClient) []string {
	var diffs []string

	// Regular fields that can use reflect.DeepEqual
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
		// Settings / Login settings
		{"loginTheme", keycloakClient.Attributes.LoginTheme, clientObj.Spec.LoginTheme},
		{"consentRequired", keycloakClient.ConsentRequired, clientObj.Spec.ConsentRequired},
		{"displayOnConsentScreen", keycloakClient.Attributes.DisplayOnConsentScreen, keycloakTypes.KeycloakBoolQuoted(clientObj.Spec.DisplayOnConsentScreen)},
		{"consentScreenText", keycloakClient.Attributes.ConsentScreenText, clientObj.Spec.ConsentScreenText},
	}

	for _, field := range fields {
		if !reflect.DeepEqual(field.Old, field.New) {
			// Format strings with quotes, others without
			if reflect.TypeOf(field.Old).Kind() == reflect.String {
				diffs = append(diffs, fmt.Sprintf("%s: '%v' -> '%v'", field.Name, field.Old, field.New))
			} else {
				diffs = append(diffs, fmt.Sprintf("%s: %v -> %v", field.Name, field.Old, field.New))
			}
		}
	}

	// Handle slice fields separately to properly compare nil vs empty slices
	if !slicesEqual(keycloakClient.ValidRedirectUris, clientObj.Spec.RedirectUris) {
		diffs = append(diffs, fmt.Sprintf("validRedirectUris: %v -> %v", keycloakClient.ValidRedirectUris, clientObj.Spec.RedirectUris))
	}

	if !slicesEqual(keycloakClient.WebOrigins, clientObj.Spec.WebOrigins) {
		diffs = append(diffs, fmt.Sprintf("webOrigins: %v -> %v", keycloakClient.WebOrigins, clientObj.Spec.WebOrigins))
	}

	if !slicesEqual(keycloakClient.Attributes.PostLogoutRedirectUris, clientObj.Spec.PostLogoutRedirectUris) {
		diffs = append(diffs, fmt.Sprintf("webOrigins: %v -> %v", keycloakClient.Attributes.PostLogoutRedirectUris, clientObj.Spec.PostLogoutRedirectUris))
	}

	return diffs
}

// TODO replace this!
func (r *ClientReconciler) getClientRolesForSingleClient(ctx context.Context, realmName, clientUUID string) ([]*keycloak.Role, error) {
	// Create a mock OpenidClient to use with the existing GetClientRoles method
	mockClients := []*keycloak.OpenidClient{
		{
			Id: clientUUID,
		},
	}

	// Use the existing GetClientRoles method
	allRoles, err := r.KeycloakClient.GetClientRoles(ctx, realmName, mockClients)
	if err != nil {
		return nil, err
	}

	return allRoles, nil
}

func (r *ClientReconciler) reconcileClient(ctx context.Context, clientObj *keycloakv1alpha1.Client, realm *keycloakv1alpha1.Realm) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	var keycloakClient *keycloak.OpenidClient
	var err error
	var clientCreatedOrUpdated bool

	// If we have a stored UUID, use it to fetch the client
	if clientObj.Status.ClientUUID != "" {
		log.V(1).Info("Fetching client by UUID", "uuid", clientObj.Status.ClientUUID, "realm", realm.Name)
		keycloakClient, err = r.KeycloakClient.GetOpenidClient(ctx, realm.Name, clientObj.Status.ClientUUID)
		if err != nil {
			if keycloak.ErrorIs404(err) {
				// Client was deleted in Keycloak but we still have the UUID
				log.Info("Client UUID not found in Keycloak, will recreate", "uuid", clientObj.Status.ClientUUID)
				clientObj.Status.ClientUUID = ""                     // Clear the invalid UUID
				clientObj.Status.RoleUUIDs = make(map[string]string) // Clear role UUIDs too
				keycloakClient = nil                                 // Will trigger recreation below
			} else {
				return r.updateStatus(ctx, clientObj, false, true, fmt.Sprintf("Failed to get client by UUID: %v", err))
			}
		}
	}

	// If we don't have a UUID or the client was not found, create a new one
	if keycloakClient == nil {
		log.Info("Creating client in Keycloak", "client", clientObj.Spec.ClientID, "realm", realm.Name)

		newClientAttributes := &keycloak.OpenidClientAttributes{
			PostLogoutRedirectUris:                clientObj.Spec.PostLogoutRedirectUris,
			Oauth2DeviceAuthorizationGrantEnabled: keycloakTypes.KeycloakBoolQuoted(clientObj.Spec.Oauth2DeviceAuthorizationGrantEnabled),
			LoginTheme:                            clientObj.Spec.LoginTheme,
			DisplayOnConsentScreen:                keycloakTypes.KeycloakBoolQuoted(clientObj.Spec.DisplayOnConsentScreen),
			ConsentScreenText:                     clientObj.Spec.ConsentScreenText,
		}

		newClient := &keycloak.OpenidClient{
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
			Attributes:                *newClientAttributes,
			StandardFlowEnabled:       clientObj.Spec.StandardFlowEnabled,
			DirectAccessGrantsEnabled: clientObj.Spec.DirectAccessGrantsEnabled,
			ImplicitFlowEnabled:       clientObj.Spec.ImplicitFlowEnabled,
			ServiceAccountsEnabled:    clientObj.Spec.ServiceAccountsEnabled,
			ConsentRequired:           clientObj.Spec.ConsentRequired,
		}

		// Create the client
		err := r.KeycloakClient.NewOpenidClient(ctx, newClient)
		if err != nil {
			if keycloak.ErrorIs409(err) {
				log.Info("Client already exists in Keycloak but no UUID stored", "client", clientObj.Spec.ClientID)
				return r.updateStatus(ctx, clientObj, false, true, "Client exists in Keycloak but UUID not tracked - manual intervention required")
			}
			return r.updateStatus(ctx, clientObj, false, true, fmt.Sprintf("Failed to create client: %v", err))
		}

		// Fetch the created client to get its UUID
		createdClient, err := r.KeycloakClient.GetOpenidClientByClientId(ctx, realm.Name, clientObj.Spec.ClientID)
		if err != nil {
			return r.updateStatus(ctx, clientObj, false, true, fmt.Sprintf("Client created but failed to retrieve UUID: %v", err))
		}

		// Store the UUID and clear any stale role UUIDs
		clientObj.Status.ClientUUID = createdClient.Id
		clientObj.Status.RoleUUIDs = make(map[string]string) // Reset role UUIDs for new client
		clientCreatedOrUpdated = true
		log.Info("Client created successfully", "client", clientObj.Spec.ClientID, "realm", realm.Name, "uuid", createdClient.Id)
	} else {
		// Check for differences and update if needed
		diffs := getClientDiffs(clientObj, keycloakClient)

		if len(diffs) > 0 {
			log.Info("Client configuration changes detected", "client", clientObj.Spec.ClientID, "realm", realm.Name, "changes", strings.Join(diffs, ", "))

			// Create a copy to modify
			updatedClient := *keycloakClient

			// Apply changes
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

			updatedClient.Attributes.PostLogoutRedirectUris = clientObj.Spec.PostLogoutRedirectUris
			updatedClient.Attributes.Oauth2DeviceAuthorizationGrantEnabled = keycloakTypes.KeycloakBoolQuoted(clientObj.Spec.Oauth2DeviceAuthorizationGrantEnabled)
			updatedClient.Attributes.LoginTheme = clientObj.Spec.LoginTheme
			updatedClient.Attributes.DisplayOnConsentScreen = keycloakTypes.KeycloakBoolQuoted(clientObj.Spec.DisplayOnConsentScreen)
			updatedClient.Attributes.ConsentScreenText = clientObj.Spec.ConsentScreenText

			if err := r.KeycloakClient.UpdateOpenidClient(ctx, &updatedClient); err != nil {
				log.Error(err, "Failed to update client", "client", clientObj.Spec.ClientID)
				return r.updateStatus(ctx, clientObj, false, true, fmt.Sprintf("Failed to update client: %v", err))
			}

			clientCreatedOrUpdated = true
			log.Info("Client updated successfully", "client", clientObj.Spec.ClientID)
		}
	}

	// Reconcile roles after client is ready
	if clientObj.Status.ClientUUID != "" {
		if err := r.reconcileClientRoles(ctx, clientObj, realm); err != nil {
			return r.updateStatus(ctx, clientObj, false, true, fmt.Sprintf("Client ready but failed to reconcile roles: %v", err))
		}
	}

	// Determine the appropriate success message
	var message string
	if clientCreatedOrUpdated {
		message = "Client and roles synchronized successfully"
	} else {
		message = "Client and roles synchronized"
	}

	return r.updateStatus(ctx, clientObj, true, true, message)
}

func (r *ClientReconciler) reconcileClientRoles(ctx context.Context, clientObj *keycloakv1alpha1.Client, realm *keycloakv1alpha1.Realm) error {
	log := log.FromContext(ctx)

	// Initialize RoleUUIDs map if nil
	if clientObj.Status.RoleUUIDs == nil {
		clientObj.Status.RoleUUIDs = make(map[string]string)
	}

	// Get existing roles from Keycloak for this specific client
	existingRoles, err := r.getClientRolesForSingleClient(ctx, realm.Name, clientObj.Status.ClientUUID)
	if err != nil {
		return fmt.Errorf("failed to get existing client roles: %w", err)
	}

	// Convert existing roles to map for easier lookup
	existingRoleMap := make(map[string]*keycloak.Role)
	for _, role := range existingRoles {
		existingRoleMap[role.Name] = role
	}

	// Create a set of desired role names from the spec
	desiredRoles := make(map[string]keycloakv1alpha1.RoleSpec)
	for _, roleSpec := range clientObj.Spec.Roles {
		desiredRoles[roleSpec.Name] = roleSpec
	}

	log.V(1).Info("Role reconciliation starting",
		"client", clientObj.Spec.ClientID,
		"existingRoleCount", len(existingRoles),
		"desiredRoleCount", len(desiredRoles),
		"trackedRoleCount", len(clientObj.Status.RoleUUIDs))

	// Step 1: Clean up tracking map FIRST - remove any roles that are not desired
	// This handles the case where a role was deleted from both Keycloak and spec
	for trackedRoleName := range clientObj.Status.RoleUUIDs {
		if _, stillDesired := desiredRoles[trackedRoleName]; !stillDesired {
			log.Info("Removing role UUID from tracking (no longer desired)",
				"role", trackedRoleName,
				"uuid", clientObj.Status.RoleUUIDs[trackedRoleName])
			delete(clientObj.Status.RoleUUIDs, trackedRoleName)
		}
	}

	// Step 2: Delete roles that exist in Keycloak but are not desired
	for _, existingRole := range existingRoles {
		if _, stillDesired := desiredRoles[existingRole.Name]; !stillDesired {
			log.Info("Deleting client role not in spec",
				"role", existingRole.Name,
				"client", clientObj.Spec.ClientID,
				"uuid", existingRole.Id)

			if err := r.KeycloakClient.DeleteRole(ctx, realm.Name, existingRole.Id); err != nil {
				if !keycloak.ErrorIs404(err) {
					log.Error(err, "Failed to delete role from Keycloak", "role", existingRole.Name, "uuid", existingRole.Id)
					return fmt.Errorf("failed to delete role %s: %w", existingRole.Name, err)
				}
				log.Info("Role was already deleted from Keycloak", "role", existingRole.Name)
			} else {
				log.Info("Successfully deleted client role from Keycloak", "role", existingRole.Name)
			}
			// Note: We already removed this from tracking in Step 1
		}
	}

	// Step 3: Create or update desired roles
	for roleName, roleSpec := range desiredRoles {
		if existingRole, exists := existingRoleMap[roleName]; exists {
			// Role exists, check if it needs updating
			if existingRole.Description != roleSpec.Description {
				log.Info("Updating client role", "role", roleName, "client", clientObj.Spec.ClientID)

				updatedRole := &keycloak.Role{
					Id:          existingRole.Id,
					RealmId:     realm.Name,
					ClientId:    clientObj.Status.ClientUUID,
					Name:        roleName,
					Description: roleSpec.Description,
					ClientRole:  true,
					ContainerId: clientObj.Status.ClientUUID,
					Composite:   existingRole.Composite,
					Attributes:  existingRole.Attributes,
				}

				if err := r.KeycloakClient.UpdateRole(ctx, updatedRole); err != nil {
					return fmt.Errorf("failed to update role %s: %w", roleName, err)
				}
				log.Info("Successfully updated client role", "role", roleName)
			}
			// Store/update the UUID in our tracking map
			clientObj.Status.RoleUUIDs[roleName] = existingRole.Id
		} else {
			// Role doesn't exist in Keycloak, create it
			log.Info("Creating client role", "role", roleName, "client", clientObj.Spec.ClientID)

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

			// Store the UUID
			clientObj.Status.RoleUUIDs[roleName] = newRole.Id
			log.Info("Successfully created client role", "role", roleName, "uuid", newRole.Id)
		}
	}

	log.V(1).Info("Role reconciliation completed",
		"client", clientObj.Spec.ClientID,
		"finalTrackedRoleCount", len(clientObj.Status.RoleUUIDs))

	return nil
}

func (r *ClientReconciler) reconcileDelete(ctx context.Context, clientObj *keycloakv1alpha1.Client) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	if controllerutil.ContainsFinalizer(clientObj, clientFinalizer) {
		if clientObj.Status.ClientUUID != "" {
			realm, err := r.getRealm(ctx, clientObj)
			if err != nil {
				log.Error(err, "Failed to get realm for client deletion", "client", clientObj.Spec.ClientID)
				return ctrl.Result{}, err
			}

			if realm != nil {
				// Delete individual roles first (optional, as they'll be deleted with the client)
				if clientObj.Status.RoleUUIDs != nil && len(clientObj.Status.RoleUUIDs) > 0 {
					log.Info("Deleting client roles from Keycloak", "client", clientObj.Spec.ClientID, "roleCount", len(clientObj.Status.RoleUUIDs))
					for roleName, roleUUID := range clientObj.Status.RoleUUIDs {
						if err := r.KeycloakClient.DeleteRole(ctx, realm.Name, roleUUID); err != nil {
							if !keycloak.ErrorIs404(err) {
								log.Error(err, "Failed to delete client role from Keycloak", "role", roleName)
							}
						}
					}
				}

				log.Info("Deleting client from Keycloak", "client", clientObj.Spec.ClientID, "realm", realm.Name, "uuid", clientObj.Status.ClientUUID)

				if err := r.KeycloakClient.DeleteOpenidClient(ctx, realm.Name, clientObj.Status.ClientUUID); err != nil {
					if keycloak.ErrorIs404(err) {
						log.Info("Client already deleted from Keycloak", "client", clientObj.Spec.ClientID)
					} else {
						log.Error(err, "Failed to delete client from Keycloak", "client", clientObj.Spec.ClientID)
						return ctrl.Result{}, err
					}
				} else {
					log.Info("Client and roles deleted from Keycloak", "client", clientObj.Spec.ClientID)
				}
			}
		} else {
			log.Info("No UUID stored for client - skipping Keycloak deletion", "client", clientObj.Spec.ClientID)
		}

		controllerutil.RemoveFinalizer(clientObj, clientFinalizer)
		if err := r.Update(ctx, clientObj); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *ClientReconciler) updateStatus(ctx context.Context, clientObj *keycloakv1alpha1.Client, ready bool, realmReady bool, message string) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Retry status update with exponential backoff to handle conflicts
	maxRetries := 3
	for i := range maxRetries {
		// Fetch the latest version of the client to avoid conflicts
		var latestClient keycloakv1alpha1.Client
		if err := r.Get(ctx, client.ObjectKeyFromObject(clientObj), &latestClient); err != nil {
			log.Error(err, "Failed to fetch latest Client for status update")
			return ctrl.Result{}, err
		}

		// Update status on the latest version
		latestClient.Status.Ready = ready
		latestClient.Status.RealmReady = realmReady
		latestClient.Status.Message = message
		now := metav1.NewTime(time.Now())
		latestClient.Status.LastSyncTime = &now

		// Preserve ClientUUID if it was set in the original object
		if clientObj.Status.ClientUUID != "" {
			latestClient.Status.ClientUUID = clientObj.Status.ClientUUID
		}

		// ALWAYS use the RoleUUIDs from the current reconciliation
		// This is the authoritative source after reconciliation
		if clientObj.Status.RoleUUIDs != nil {
			latestClient.Status.RoleUUIDs = clientObj.Status.RoleUUIDs
			log.V(1).Info("Using RoleUUIDs from current reconciliation",
				"client", clientObj.Name,
				"roleCount", len(clientObj.Status.RoleUUIDs))
		} else {
			// Initialize empty map if nil
			latestClient.Status.RoleUUIDs = make(map[string]string)
			log.V(1).Info("Initializing empty RoleUUIDs map", "client", clientObj.Name)
		}

		if err := r.Status().Update(ctx, &latestClient); err != nil {
			if errors.IsConflict(err) && i < maxRetries-1 {
				log.V(1).Info("Status update conflict, retrying", "attempt", i+1, "client", clientObj.Name)
				time.Sleep(time.Duration(i+1) * 100 * time.Millisecond)
				continue
			}
			log.Error(err, "Failed to update Client status after retries")
			return ctrl.Result{}, err
		}

		// Success - update the original object's status to reflect what was saved
		clientObj.Status = latestClient.Status

		log.V(1).Info("Status updated successfully",
			"client", clientObj.Name,
			"ready", latestClient.Status.Ready,
			"message", latestClient.Status.Message,
			"clientUUID", latestClient.Status.ClientUUID,
			"roleUUIDsCount", len(latestClient.Status.RoleUUIDs),
			"roleUUIDs", latestClient.Status.RoleUUIDs)
		break
	}

	// Requeue after 10 sec for periodic sync
	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClientReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&keycloakv1alpha1.Client{}).
		Complete(r)
}
