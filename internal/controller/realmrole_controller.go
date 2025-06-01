// Copyright 2025 toastyice
//
// SPDX-License-Identifier: Apache-2.0

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

	"github.com/keycloak/terraform-provider-keycloak/keycloak"
	keycloakv1alpha1 "github.com/toastyice/keycloak-config-operator/api/v1alpha1"
	keycloakclientmanager "github.com/toastyice/keycloak-config-operator/internal/keycloak"
)

const (
	realmRoleFinalizer        = "realmrole.keycloak.schella.network/finalizer"
	realmRoleRequeueInterval  = 10 * time.Second
	realmRoleMaxStatusRetries = 3
)

// RealmRoleReconciler reconciles a RealmRole object
type RealmRoleReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	ClientManager *keycloakclientmanager.ClientManager
}

// RealmRoleReconciler represents the outcome of a realmrole reconciliation operation
type RealmRoleReconcileResult struct {
	Ready      bool
	RealmReady bool
	Message    string
	Error      error
}

//+kubebuilder:rbac:groups=keycloak.schella.network,resources=realmroles,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=keycloak.schella.network,resources=realmroles/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=keycloak.schella.network,resources=realmroles/finalizers,verbs=update

func (r *RealmRoleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	var realmrole keycloakv1alpha1.RealmRole
	if err := r.Get(ctx, req.NamespacedName, &realmrole); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		log.Error(err, "unable to fetch RealmRole")
		return ctrl.Result{}, nil
	}

	if !realmrole.ObjectMeta.DeletionTimestamp.IsZero() {
		keycloakClient, err := r.getKeycloakClient(ctx, &realmrole)
		if err != nil {
			if strings.Contains(err.Error(), "is not ready") {
				log.Info("KeycloakInstanceConfig not ready during deletion, waiting...", "realmroles", realmrole.Name)

				result := RealmRoleReconcileResult{
					Ready:      false,
					RealmReady: false,
					Message:    "Deletion pending: waiting for KeycloakInstanceConfig to become ready",
				}
				r.updateStatus(ctx, &realmrole, result) // Ignore error during deletion

				// Keep the finalizer and requeue until KeycloakInstanceConfig is ready
				return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
			}
			return ctrl.Result{}, fmt.Errorf("failed to get Keycloak client during deletion: %w", err)
		}
		return r.reconcileDelete(ctx, keycloakClient, &realmrole)
	}

	// Check if KeycloakInstanceConfig is ready
	keycloakClient, err := r.getKeycloakClient(ctx, &realmrole)
	if err != nil {
		if strings.Contains(err.Error(), "is not ready") {
			// Don't update status, just log and requeue
			log.Info("KeycloakInstanceConfig is not ready, requeuing", "realmrole", realmrole.Name)
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
		log.Error(err, "failed to get Keycloak client")
		// For actual errors (not dependency issues), update status
		result := RealmRoleReconcileResult{
			Ready:      false,
			RealmReady: false,
			Message:    fmt.Sprintf("Failed to get Keycloak client: %v", err),
		}
		return r.updateStatus(ctx, &realmrole, result)
	}

	// Add finalizer if it doesn't exist
	if !controllerutil.ContainsFinalizer(&realmrole, realmRoleFinalizer) {
		controllerutil.AddFinalizer(&realmrole, realmRoleFinalizer)
		if err := r.Update(ctx, &realmrole); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Validate realm is ready
	realm, result := r.validateRealm(ctx, &realmrole)
	if result != nil {
		return r.updateStatus(ctx, &realmrole, *result)
	}

	// Set owner reference
	if err := r.setOwnerReference(ctx, &realmrole, realm); err != nil {
		log.Error(err, "Failed to set owner reference")
		result := &RealmRoleReconcileResult{
			Ready:      false,
			RealmReady: true,
			Message:    fmt.Sprintf("Failed to set owner reference: %v", err),
		}
		return r.updateStatus(ctx, &realmrole, *result)
	}

	// Reconcile the Realm Role
	return r.reconcileRealmRole(ctx, keycloakClient, &realmrole, realm)
}

// reconcilerealm Role handles the main realm Role reconciliation logic
func (r *RealmRoleReconciler) reconcileRealmRole(ctx context.Context, keycloakClient *keycloak.KeycloakClient, realmRoleObj *keycloakv1alpha1.RealmRole, realm *keycloakv1alpha1.Realm) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	keycloakRealmRole, err := r.getOrCreateRealmRole(ctx, keycloakClient, realmRoleObj, realm)
	if err != nil {
		result := RealmRoleReconcileResult{
			Ready:      false,
			RealmReady: true,
			Message:    fmt.Sprintf("Failed to reconcile realm Role: %v", err),
		}
		return r.updateStatus(ctx, realmRoleObj, result)
	}

	relamRoleChanged := keycloakRealmRole == nil // If nil, it means we created
	if keycloakRealmRole != nil {
		var updateErr error
		relamRoleChanged, updateErr = r.updateRealmRoleIfNeeded(ctx, keycloakClient, realmRoleObj, keycloakRealmRole, realm)
		if updateErr != nil {
			logger.Error(updateErr, "Failed to update Realm Role")
			result := RealmRoleReconcileResult{
				Ready:      false,
				RealmReady: true,
				Message:    fmt.Sprintf("Failed to update realm Role: %v", updateErr),
			}
			return r.updateStatus(ctx, realmRoleObj, result)
		}
	}

	message := r.getSuccessMessage(relamRoleChanged)
	result := RealmRoleReconcileResult{
		Ready:      true,
		RealmReady: true,
		Message:    message,
	}
	return r.updateStatus(ctx, realmRoleObj, result)
}

// getKeycloakClient with improved error handling and requeue logic
func (r *RealmRoleReconciler) getKeycloakClient(ctx context.Context, realmrole *keycloakv1alpha1.RealmRole) (*keycloak.KeycloakClient, error) {
	configName := realmrole.Spec.InstanceConfigRef.Name
	configNamespace := realmrole.Namespace
	if realmrole.Spec.InstanceConfigRef.Namespace != "" {
		configNamespace = realmrole.Spec.InstanceConfigRef.Namespace
	}

	var config keycloakv1alpha1.KeycloakInstanceConfig
	if err := r.Get(ctx, types.NamespacedName{
		Name:      configName,
		Namespace: configNamespace,
	}, &config); err != nil {
		return nil, fmt.Errorf("failed to get KeycloakInstanceConfig %s/%s: %w", configNamespace, configName, err)
	}

	// Check if KeycloakInstanceConfig is ready
	if !r.isKeycloakInstanceConfigReady(&config) {
		return nil, fmt.Errorf("KeycloakInstanceConfig %s/%s is not ready", configNamespace, configName)
	}

	return r.ClientManager.GetOrCreateClient(ctx, &config)
}

func (r *RealmRoleReconciler) isKeycloakInstanceConfigReady(config *keycloakv1alpha1.KeycloakInstanceConfig) bool {
	for _, condition := range config.Status.Conditions {
		if condition.Type == "Ready" {
			return condition.Status == "True"
		}
	}
	return false
}

// validateRealm with requeue logic
func (r *RealmRoleReconciler) validateRealm(ctx context.Context, realmRoleObj *keycloakv1alpha1.RealmRole) (*keycloakv1alpha1.Realm, *RealmRoleReconcileResult) {
	realm, err := r.getRealm(ctx, realmRoleObj)
	if err != nil {
		return nil, &RealmRoleReconcileResult{
			Ready:      false,
			RealmReady: false,
			Message:    fmt.Sprintf("Failed to get realm: %v", err),
		}
	}

	if realm == nil {
		return nil, &RealmRoleReconcileResult{
			Ready:      false,
			RealmReady: false,
			Message:    "Referenced realm not found",
		}
	}

	if !realm.Status.Ready {
		return nil, &RealmRoleReconcileResult{
			Ready:      false,
			RealmReady: false,
			Message:    "Referenced realm is not ready",
		}
	}

	return realm, nil
}

// getRealm retrieves the realm object referenced by the realm Role
func (r *RealmRoleReconciler) getRealm(ctx context.Context, realmRoleObj *keycloakv1alpha1.RealmRole) (*keycloakv1alpha1.Realm, error) {
	realmNamespace := realmRoleObj.Spec.RealmRef.Namespace
	if realmNamespace == "" {
		realmNamespace = realmRoleObj.Namespace
	}

	var realm keycloakv1alpha1.Realm
	namespacedName := types.NamespacedName{
		Name:      realmRoleObj.Spec.RealmRef.Name,
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

// setOwnerReference establishes an owner-dependent relationship between realm and realm Role
func (r *RealmRoleReconciler) setOwnerReference(ctx context.Context, realmRoleObj *keycloakv1alpha1.RealmRole, realm *keycloakv1alpha1.Realm) error {
	logger := log.FromContext(ctx)
	if r.hasCorrectOwnerReference(realmRoleObj, realm) {
		return nil
	}

	if r.isCrossNamespaceReference(realmRoleObj, realm) {
		logger.V(1).Info("Skipping owner reference - cross-namespace references not allowed")
		return nil
	}

	if err := controllerutil.SetOwnerReference(realm, realmRoleObj, r.Scheme); err != nil {
		return fmt.Errorf("failed to set owner reference: %w", err)
	}

	if err := r.Update(ctx, realmRoleObj); err != nil {
		return fmt.Errorf("failed to update realm Role with owner reference: %w", err)
	}

	logger.Info("Owner reference set successfully")
	return nil
}

// hasCorrectOwnerReference checks if the correct owner reference already exists
func (r *RealmRoleReconciler) hasCorrectOwnerReference(realmRoleObj *keycloakv1alpha1.RealmRole, realm *keycloakv1alpha1.Realm) bool {
	for _, ownerRef := range realmRoleObj.GetOwnerReferences() {
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
func (r *RealmRoleReconciler) isCrossNamespaceReference(realmRoleObj *keycloakv1alpha1.RealmRole, realm *keycloakv1alpha1.Realm) bool {
	return realmRoleObj.Namespace != realm.Namespace
}

// getOrCreaterealm Role retrieves an existing realm Role or creates a new one
func (r *RealmRoleReconciler) getOrCreateRealmRole(ctx context.Context, keycloakClient *keycloak.KeycloakClient, realmRoleObj *keycloakv1alpha1.RealmRole, realm *keycloakv1alpha1.Realm) (*keycloak.Role, error) {
	if realmRoleObj.Status.RealmRoleUUID != "" {
		realmRole, err := r.fetchExistingRealmRole(ctx, keycloakClient, realmRoleObj, realm)
		if err != nil {
			return nil, err
		}
		if realmRole != nil {
			return realmRole, nil
		}
	}

	return nil, r.createNewRealmRole(ctx, keycloakClient, realmRoleObj, realm)
}

// fetchExistingrealm Role retrieves an existing realm Role by UUID
func (r *RealmRoleReconciler) fetchExistingRealmRole(ctx context.Context, keycloakClient *keycloak.KeycloakClient, realmRoleObj *keycloakv1alpha1.RealmRole, realm *keycloakv1alpha1.Realm) (*keycloak.Role, error) {
	logger := log.FromContext(ctx)
	logger.V(1).Info("Fetching realm Role by UUID", "uuid", realmRoleObj.Status.RealmRoleUUID)

	realmRole, err := keycloakClient.GetRole(ctx, realm.Name, realmRoleObj.Status.RealmRoleUUID)
	if err != nil {
		if keycloak.ErrorIs404(err) {
			logger.Info("Realm Role UUID not found in Keycloak, will recreate")
			r.clearRealmRoleState(realmRoleObj)
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get realm Role by UUID: %w", err)
	}

	return realmRole, nil
}

// clearrealm RoleState clears stored realm Role state when realm Role is not found
func (r *RealmRoleReconciler) clearRealmRoleState(realmRoleObj *keycloakv1alpha1.RealmRole) {
	realmRoleObj.Status.RealmRoleUUID = ""
}

// createNewrealm Role creates a new realm Role in Keycloak
func (r *RealmRoleReconciler) createNewRealmRole(ctx context.Context, keycloakClient *keycloak.KeycloakClient, realmRoleObj *keycloakv1alpha1.RealmRole, realm *keycloakv1alpha1.Realm) error {
	logger := log.FromContext(ctx)
	logger.Info("Creating Realm Role in Keycloak")

	newRealmRole := r.buildRealmRoleFromSpec(realmRoleObj, realm)

	if err := keycloakClient.CreateRole(ctx, newRealmRole); err != nil {
		if keycloak.ErrorIs409(err) {
			return fmt.Errorf("realm Role exists in Keycloak but UUID not tracked - manual intervention required")
		}
		return fmt.Errorf("failed to create Realm Role: %w", err)
	}

	return r.storeRealmRoleUUID(ctx, keycloakClient, realmRoleObj, realm)
}

// buildrealm RoleFromSpec creates a new realm Role from the spec
func (r *RealmRoleReconciler) buildRealmRoleFromSpec(realmRoleObj *keycloakv1alpha1.RealmRole, realm *keycloakv1alpha1.Realm) *keycloak.Role {
	return &keycloak.Role{
		RealmId:    realm.Name,
		Name:       realmRoleObj.Spec.Name,
		Attributes: realmRoleObj.Spec.Attributes,
	}
}

// storerealm RoleUUID fetches and stores the UUID of the created realm Role
func (r *RealmRoleReconciler) storeRealmRoleUUID(ctx context.Context, keycloakClient *keycloak.KeycloakClient, realmRoleObj *keycloakv1alpha1.RealmRole, realm *keycloakv1alpha1.Realm) error {
	logger := log.FromContext(ctx)

	var createdRealmRole []*keycloak.Role
	createdRealmRole, err := keycloakClient.GetRealmRoles(ctx, realm.Name)
	if err != nil {
		return fmt.Errorf("could Not retrieve realm roles: %w", err)
	}

	var targetRealmRole *keycloak.Role
	for _, role := range createdRealmRole {
		if role.Name == realmRoleObj.Spec.Name {
			targetRealmRole = role
			break
		}
	}

	realmRoleObj.Status.RealmRoleUUID = targetRealmRole.Id
	logger.Info("realm Role created successfully", "uuid", targetRealmRole.Id)
	return nil
}

// updaterealm RoleIfNeeded checks for differences and updates the realm Role if needed
func (r *RealmRoleReconciler) updateRealmRoleIfNeeded(ctx context.Context, keycloakClient *keycloak.KeycloakClient, realmRoleObj *keycloakv1alpha1.RealmRole, keycloakRole *keycloak.Role, realm *keycloakv1alpha1.Realm) (bool, error) {
	logger := log.FromContext(ctx)
	diffs := r.getRealmRoleDiffs(realmRoleObj, keycloakRole)
	if len(diffs) == 0 {
		return false, nil
	}

	logger.Info("realm Role configuration changes detected", "changes", strings.Join(diffs, ", "))

	updatedRealmRole := r.applyChangesToRealmRole(realmRoleObj, keycloakRole, realm)
	if err := keycloakClient.UpdateRole(ctx, updatedRealmRole); err != nil {
		return false, fmt.Errorf("failed to update Realm Role: %w", err)
	}

	logger.Info("Realm Role updated successfully")
	return true, nil
}

// applyChangesTorealm Role applies spec changes to the Keycloak realm Role
func (r *RealmRoleReconciler) applyChangesToRealmRole(realmRoleObj *keycloakv1alpha1.RealmRole, keycloakRole *keycloak.Role, realm *keycloakv1alpha1.Realm) *keycloak.Role {
	updatedRealmRole := *keycloakRole

	// Apply basic fields
	updatedRealmRole.Name = realmRoleObj.Spec.Name
	updatedRealmRole.RealmId = realm.Name
	updatedRealmRole.Attributes = realmRoleObj.Spec.Attributes
	updatedRealmRole.Description = realmRoleObj.Spec.Description

	return &updatedRealmRole
}

// getrealm RoleDiffs compares realm Role specifications and returns a list of differences
func (r *RealmRoleReconciler) getRealmRoleDiffs(realmRoleObj *keycloakv1alpha1.RealmRole, keycloakRole *keycloak.Role) []string {
	var diffs []string

	fields := []FieldDiff{
		{"name", keycloakRole.Name, realmRoleObj.Spec.Name},
	}

	for _, field := range fields {
		if !reflect.DeepEqual(field.Old, field.New) {
			diffs = append(diffs, r.formatRealmRoleFieldDiff(field))
		}
	}

	if !r.stringMapSlicesEqual(keycloakRole.Attributes, realmRoleObj.Spec.Attributes) {
		diffs = append(diffs, fmt.Sprintf("attributes: %v -> %v", keycloakRole.Attributes, realmRoleObj.Spec.Attributes))
	}

	return diffs
}

// formatrealm RoleFieldDiff formats a field difference for logging
func (r *RealmRoleReconciler) formatRealmRoleFieldDiff(field FieldDiff) string {
	if reflect.TypeOf(field.Old).Kind() == reflect.String {
		return fmt.Sprintf("%s: '%v' -> '%v'", field.Name, field.Old, field.New)
	}
	return fmt.Sprintf("%s: %v -> %v", field.Name, field.Old, field.New)
}

// stringSlicesEqual compares two string slices, treating nil and empty as equal
func (r *RealmRoleReconciler) stringSlicesEqual(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

// stringMapSlicesEqual compares two map[string][]string, treating nil and empty as equal
func (r *RealmRoleReconciler) stringMapSlicesEqual(a, b map[string][]string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

// getSuccessMessage returns an appropriate success message
func (r *RealmRoleReconciler) getSuccessMessage(realmRoleChanged bool) string {
	if realmRoleChanged {
		return "Realm Role reconciled successfully"
	}
	return "Realm Role reconciled"
}

// updateStatus updates the realm Role status with retry logic (simplified version)
func (r *RealmRoleReconciler) updateStatus(ctx context.Context, realmRoleObj *keycloakv1alpha1.RealmRole, result RealmRoleReconcileResult) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("realm Role", realmRoleObj.Name)

	for i := range realmRoleMaxStatusRetries {
		if err := r.performStatusUpdate(ctx, realmRoleObj, result); err != nil {
			if errors.IsConflict(err) && i < realmRoleMaxStatusRetries-1 {
				logger.V(1).Info("Status update conflict, retrying", "attempt", i+1)
				time.Sleep(time.Duration(i+1) * 100 * time.Millisecond)
				continue
			}
			logger.Error(err, "Failed to update status after retries")
			return ctrl.Result{}, err
		}
		break
	}

	// Simple requeue logic - only requeue on failure
	if !result.Ready {
		return ctrl.Result{RequeueAfter: realmRoleRequeueInterval}, nil
	}

	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

// performStatusUpdate performs a single status update attempt
func (r *RealmRoleReconciler) performStatusUpdate(ctx context.Context, realmRoleObj *keycloakv1alpha1.RealmRole, result RealmRoleReconcileResult) error {
	logger := log.FromContext(ctx).WithValues("realm Role", realmRoleObj.Name)

	var latestRealmRole keycloakv1alpha1.RealmRole
	if err := r.Get(ctx, client.ObjectKeyFromObject(realmRoleObj), &latestRealmRole); err != nil {
		return err
	}

	// Optional: Check if update is needed to avoid unnecessary API calls
	if r.statusUnchanged(&latestRealmRole, result) {
		logger.V(2).Info("Status unchanged, skipping update")
		realmRoleObj.Status = latestRealmRole.Status
		return nil
	}

	r.applyStatusUpdate(&latestRealmRole, realmRoleObj, result)

	if err := r.Status().Update(ctx, &latestRealmRole); err != nil {
		return err
	}

	realmRoleObj.Status = latestRealmRole.Status
	logger.V(1).Info("Status updated successfully",
		"ready", latestRealmRole.Status.Ready,
		"realmReady", latestRealmRole.Status.RealmReady,
		"message", latestRealmRole.Status.Message,
	)

	return nil
}

// applyStatusUpdate applies the status changes to the latest realm Role object
func (r *RealmRoleReconciler) applyStatusUpdate(latestRealmRole, originalRealmRole *keycloakv1alpha1.RealmRole, result RealmRoleReconcileResult) {
	latestRealmRole.Status.Ready = result.Ready
	latestRealmRole.Status.RealmReady = result.RealmReady
	latestRealmRole.Status.Message = result.Message
	now := metav1.NewTime(time.Now())
	latestRealmRole.Status.LastSyncTime = &now

	// Preserve operational data from the original realm Role
	if originalRealmRole.Status.RealmRoleUUID != "" {
		latestRealmRole.Status.RealmRoleUUID = originalRealmRole.Status.RealmRoleUUID
	}
}

// reconcileDelete handles realm Role deletion
func (r *RealmRoleReconciler) reconcileDelete(ctx context.Context, keycloakClient *keycloak.KeycloakClient, realmRole *keycloakv1alpha1.RealmRole) (ctrl.Result, error) {
	log := log.FromContext(ctx).WithValues("realm Role", realmRole.Name)

	// Delete realm Role from Keycloak if it exists
	if realmRole.Status.RealmRoleUUID != "" {
		if err := r.deleteRealmRoleFromKeycloak(ctx, keycloakClient, realmRole); err != nil {
			log.Error(err, "Failed to delete realm Role from Keycloak")
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
	}

	// Remove finalizer
	if controllerutil.ContainsFinalizer(realmRole, realmRoleFinalizer) {
		controllerutil.RemoveFinalizer(realmRole, realmRoleFinalizer)
		if err := r.Update(ctx, realmRole); err != nil {
			return ctrl.Result{}, err
		}
	}

	log.Info("realm Role deletion completed successfully")
	return ctrl.Result{}, nil
}

// deleteRealmRoleFromKeycloak handles the actual deletion from Keycloak
func (r *RealmRoleReconciler) deleteRealmRoleFromKeycloak(ctx context.Context, keycloakClient *keycloak.KeycloakClient, realmRoleObj *keycloakv1alpha1.RealmRole) error {
	logger := log.FromContext(ctx)
	if realmRoleObj.Status.RealmRoleUUID == "" {
		logger.Info("No UUID stored - skipping Keycloak deletion")
		return nil
	}

	realm, err := r.getRealm(ctx, realmRoleObj)
	if err != nil {
		logger.Error(err, "Failed to get realm for realm Role deletion")
		return err
	}

	if realm == nil {
		logger.Info("Realm not found - skipping Keycloak deletion")
		return nil
	}

	logger.Info("Deleting realm Role from Keycloak", "uuid", realmRoleObj.Status.RealmRoleUUID)

	if err := keycloakClient.DeleteRole(ctx, realm.Name, realmRoleObj.Status.RealmRoleUUID); err != nil {
		if keycloak.ErrorIs404(err) {
			logger.Info("realm Role already deleted from Keycloak")
			return nil
		}
		logger.Error(err, "Failed to delete realm Role from Keycloak")
		return err
	}

	logger.Info("realm Role deleted from Keycloak")
	return nil
}

// statusUnchanged checks if the status would actually change
func (r *RealmRoleReconciler) statusUnchanged(latest *keycloakv1alpha1.RealmRole, result RealmRoleReconcileResult) bool {
	return latest.Status.Ready == result.Ready &&
		latest.Status.RealmReady == result.RealmReady &&
		latest.Status.Message == result.Message
}

// SetupWithManager sets up the controller with the Manager.
func (r *RealmRoleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&keycloakv1alpha1.RealmRole{}).
		Complete(r)
}
