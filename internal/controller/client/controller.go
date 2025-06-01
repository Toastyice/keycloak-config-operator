// Copyright 2025 toastyice
//
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"context"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/keycloak/terraform-provider-keycloak/keycloak"
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

// FieldDiff represents a difference between expected and actual field values
type FieldDiff struct {
	Name string
	Old  any
	New  any
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

// reconcileClientSecret handles the creation of a secret in Kubernetes
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
	if err := r.createOrUpdateClientSecret(ctx, clientObj, clientSecret, realm /*instanceConfig*/); err != nil {
		return fmt.Errorf("failed to create/update client secret: %w", err)
	}

	return nil
}

// Reconcile secret end

// SetupWithManager sets up the controller with the Manager
func (r *ClientReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&keycloakv1alpha1.Client{}).
		Complete(r)
}
