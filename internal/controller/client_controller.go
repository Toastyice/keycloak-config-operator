package controller

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

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

func (r *ClientReconciler) reconcileClient(ctx context.Context, clientObj *keycloakv1alpha1.Client, realm *keycloakv1alpha1.Realm) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	var keycloakClient *keycloak.OpenidClient
	var err error

	// If we have a stored UUID, use it to fetch the client
	if clientObj.Status.ClientUUID != "" {
		log.V(1).Info("Fetching client by UUID", "uuid", clientObj.Status.ClientUUID, "realm", realm.Name)
		keycloakClient, err = r.KeycloakClient.GetOpenidClient(ctx, realm.Name, clientObj.Status.ClientUUID)
		if err != nil {
			if keycloak.ErrorIs404(err) {
				// Client was deleted in Keycloak but we still have the UUID
				log.Info("Client UUID not found in Keycloak, will recreate", "uuid", clientObj.Status.ClientUUID)
				clientObj.Status.ClientUUID = "" // Clear the invalid UUID
				keycloakClient = nil             // Will trigger recreation below
			} else {
				return r.updateStatus(ctx, clientObj, false, true, fmt.Sprintf("Failed to get client by UUID: %v", err))
			}
		}
	}

	// If we don't have a UUID or the client was not found, create a new one
	if keycloakClient == nil {
		log.Info("Creating client in Keycloak", "client", clientObj.Spec.ClientID, "realm", realm.Name)

		newClient := &keycloak.OpenidClient{
			RealmId:                 realm.Name,
			ClientId:                clientObj.Spec.ClientID,
			Name:                    clientObj.Spec.Name,
			Description:             clientObj.Spec.Description,
			Enabled:                 clientObj.Spec.Enabled,
			ClientAuthenticatorType: clientObj.Spec.ClientAuthenticatorType,
			PublicClient:            clientObj.Spec.PublicClient,
			ValidRedirectUris:       clientObj.Spec.RedirectUris,
			WebOrigins:              clientObj.Spec.WebOrigins,
		}

		// Create the client (returns only error)
		err := r.KeycloakClient.NewOpenidClient(ctx, newClient)
		if err != nil {
			if keycloak.ErrorIs409(err) {
				// Client already exists but we don't have the UUID
				// This is the edge case we're ignoring for now
				log.Info("Client already exists in Keycloak but no UUID stored - ignoring until manual intervention", "client", clientObj.Spec.ClientID)
				return r.updateStatus(ctx, clientObj, false, true, "Client exists in Keycloak but UUID not tracked - manual intervention required")
			}
			return r.updateStatus(ctx, clientObj, false, true, fmt.Sprintf("Failed to create client: %v", err))
		}

		// Fetch the created client to get its UUID
		createdClient, err := r.KeycloakClient.GetOpenidClientByClientId(ctx, realm.Name, clientObj.Spec.ClientID)
		if err != nil {
			return r.updateStatus(ctx, clientObj, false, true, fmt.Sprintf("Client created but failed to retrieve UUID: %v", err))
		}

		// Store the UUID of the created client
		clientObj.Status.ClientUUID = createdClient.Id
		log.Info("Client created successfully", "client", clientObj.Spec.ClientID, "realm", realm.Name, "uuid", createdClient.Id)
		return r.updateStatus(ctx, clientObj, true, true, "Client created successfully")
	}

	// Check for differences and update if necessary
	if r.clientNeedsUpdate(clientObj, keycloakClient) {
		log.Info("Client configuration changes detected", "client", clientObj.Spec.ClientID, "realm", realm.Name)

		updatedClient := *keycloakClient
		r.applyClientChanges(clientObj, &updatedClient)

		if err := r.KeycloakClient.UpdateOpenidClient(ctx, &updatedClient); err != nil {
			log.Error(err, "Failed to update client", "client", clientObj.Spec.ClientID)
			return r.updateStatus(ctx, clientObj, false, true, fmt.Sprintf("Failed to update client: %v", err))
		}

		log.Info("Client updated successfully", "client", clientObj.Spec.ClientID)
		return r.updateStatus(ctx, clientObj, true, true, "Client updated successfully")
	}

	return r.updateStatus(ctx, clientObj, true, true, "Client synchronized")
}

func (r *ClientReconciler) clientNeedsUpdate(clientObj *keycloakv1alpha1.Client, keycloakClient *keycloak.OpenidClient) bool {
	return clientObj.Spec.Name != keycloakClient.Name ||
		clientObj.Spec.Description != keycloakClient.Description ||
		clientObj.Spec.Enabled != keycloakClient.Enabled ||
		clientObj.Spec.ClientAuthenticatorType != keycloakClient.ClientAuthenticatorType ||
		clientObj.Spec.PublicClient != keycloakClient.PublicClient ||
		!slicesEqual(clientObj.Spec.RedirectUris, keycloakClient.ValidRedirectUris) ||
		!slicesEqual(clientObj.Spec.WebOrigins, keycloakClient.WebOrigins)
}

func (r *ClientReconciler) applyClientChanges(clientObj *keycloakv1alpha1.Client, keycloakClient *keycloak.OpenidClient) {
	keycloakClient.Name = clientObj.Spec.Name
	keycloakClient.Description = clientObj.Spec.Description
	keycloakClient.Enabled = clientObj.Spec.Enabled
	keycloakClient.ClientAuthenticatorType = clientObj.Spec.ClientAuthenticatorType
	keycloakClient.PublicClient = clientObj.Spec.PublicClient
	keycloakClient.ValidRedirectUris = clientObj.Spec.RedirectUris
	keycloakClient.WebOrigins = clientObj.Spec.WebOrigins
}

func (r *ClientReconciler) reconcileDelete(ctx context.Context, clientObj *keycloakv1alpha1.Client) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	if controllerutil.ContainsFinalizer(clientObj, clientFinalizer) {
		// Only attempt deletion if we have a UUID
		if clientObj.Status.ClientUUID != "" {
			// Get the realm to know which realm to delete the client from
			realm, err := r.getRealm(ctx, clientObj)
			if err != nil {
				log.Error(err, "Failed to get realm for client deletion", "client", clientObj.Spec.ClientID)
				return ctrl.Result{}, err
			}

			if realm != nil {
				log.Info("Deleting client from Keycloak", "client", clientObj.Spec.ClientID, "realm", realm.Name, "uuid", clientObj.Status.ClientUUID)

				if err := r.KeycloakClient.DeleteOpenidClient(ctx, realm.Name, clientObj.Status.ClientUUID); err != nil {
					if keycloak.ErrorIs404(err) {
						log.Info("Client already deleted from Keycloak", "client", clientObj.Spec.ClientID)
					} else {
						log.Error(err, "Failed to delete client from Keycloak", "client", clientObj.Spec.ClientID)
						return ctrl.Result{}, err
					}
				} else {
					log.Info("Client deleted from Keycloak", "client", clientObj.Spec.ClientID)
				}
			}
		} else {
			log.Info("No UUID stored for client - skipping Keycloak deletion", "client", clientObj.Spec.ClientID)
		}

		// Remove finalizer
		controllerutil.RemoveFinalizer(clientObj, clientFinalizer)
		if err := r.Update(ctx, clientObj); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *ClientReconciler) updateStatus(ctx context.Context, clientObj *keycloakv1alpha1.Client, ready bool, realmReady bool, message string) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	clientObj.Status.Ready = ready
	clientObj.Status.RealmReady = realmReady
	clientObj.Status.Message = message
	now := metav1.NewTime(time.Now())
	clientObj.Status.LastSyncTime = &now

	if err := r.Status().Update(ctx, clientObj); err != nil {
		log.Error(err, "Failed to update Client status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

// Helper function to compare slices
func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i, v := range a {
		if v != b[i] {
			return false
		}
	}
	return true
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClientReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&keycloakv1alpha1.Client{}).
		Complete(r)
}
