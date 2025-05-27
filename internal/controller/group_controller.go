//TODO
//Doesn't fix parent if manually moved in Keycloak ui
//recreates the group if renamed externally -> fix by try lookup with UUID first

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
)

const (
	groupFinalizer        = "group.keycloak.schella.network/finalizer"
	groupRequeueInterval  = 10 * time.Second
	groupMaxStatusRetries = 3
)

// GroupReconciler reconciles a Group object
type GroupReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	KeycloakClient *keycloak.KeycloakClient
}

// GroupReconcileResult represents the outcome of a group reconciliation operation
type GroupReconcileResult struct {
	Ready      bool
	RealmReady bool
	Message    string
	Error      error
}

// GroupFieldDiff represents a field difference for change detection
type GroupFieldDiff struct {
	Name string
	Old  any
	New  any
}

//+kubebuilder:rbac:groups=keycloak.schella.network,resources=groups,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=keycloak.schella.network,resources=groups/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=keycloak.schella.network,resources=groups/finalizers,verbs=update
//+kubebuilder:rbac:groups=keycloak.schella.network,resources=realms,verbs=get;list;watch

func (r *GroupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("group", req.NamespacedName)

	if err := r.validateReconciler(); err != nil {
		logger.Error(err, "Controller not properly initialized")
		return ctrl.Result{}, err
	}

	groupObj, err := r.fetchGroup(ctx, req.NamespacedName)
	if err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Unable to fetch Group")
		return ctrl.Result{}, err
	}

	if r.isMarkedForDeletion(groupObj) {
		return r.reconcileDelete(ctx, groupObj)
	}

	if err := r.ensureFinalizer(ctx, groupObj); err != nil {
		return ctrl.Result{}, err
	}

	realm, result := r.validateRealm(ctx, groupObj)
	if result != nil {
		return r.updateStatus(ctx, groupObj, *result)
	}

	if err := r.setOwnerReference(ctx, groupObj, realm); err != nil {
		logger.Error(err, "Failed to set owner reference")
		result := &GroupReconcileResult{
			Ready:      false,
			RealmReady: true,
			Message:    fmt.Sprintf("Failed to set owner reference: %v", err),
		}
		return r.updateStatus(ctx, groupObj, *result)
	}

	return r.reconcileGroup(ctx, groupObj, realm)
}

// validateReconciler ensures the controller is properly initialized
func (r *GroupReconciler) validateReconciler() error {
	if r.KeycloakClient == nil {
		return fmt.Errorf("KeycloakClient is nil - controller not properly initialized")
	}
	return nil
}

// fetchGroup retrieves the Group object from the cluster
func (r *GroupReconciler) fetchGroup(ctx context.Context, namespacedName types.NamespacedName) (*keycloakv1alpha1.Group, error) {
	var groupObj keycloakv1alpha1.Group
	if err := r.Get(ctx, namespacedName, &groupObj); err != nil {
		return nil, err
	}
	return &groupObj, nil
}

// isMarkedForDeletion checks if the group is being deleted
func (r *GroupReconciler) isMarkedForDeletion(groupObj *keycloakv1alpha1.Group) bool {
	return !groupObj.ObjectMeta.DeletionTimestamp.IsZero()
}

// ensureFinalizer adds the finalizer if it doesn't exist
func (r *GroupReconciler) ensureFinalizer(ctx context.Context, groupObj *keycloakv1alpha1.Group) error {
	if !controllerutil.ContainsFinalizer(groupObj, groupFinalizer) {
		controllerutil.AddFinalizer(groupObj, groupFinalizer)
		return r.Update(ctx, groupObj)
	}
	return nil
}

// validateRealm validates and retrieves the referenced realm
func (r *GroupReconciler) validateRealm(ctx context.Context, groupObj *keycloakv1alpha1.Group) (*keycloakv1alpha1.Realm, *GroupReconcileResult) {
	realm, err := r.getRealm(ctx, groupObj)
	if err != nil {
		return nil, &GroupReconcileResult{
			Ready:      false,
			RealmReady: false,
			Message:    fmt.Sprintf("Failed to get realm: %v", err),
		}
	}

	if realm == nil {
		return nil, &GroupReconcileResult{
			Ready:      false,
			RealmReady: false,
			Message:    "Referenced realm not found",
		}
	}

	if !realm.Status.Ready {
		return nil, &GroupReconcileResult{
			Ready:      false,
			RealmReady: false,
			Message:    "Referenced realm is not ready",
		}
	}

	return realm, nil
}

// getRealm retrieves the realm object referenced by the group
func (r *GroupReconciler) getRealm(ctx context.Context, groupObj *keycloakv1alpha1.Group) (*keycloakv1alpha1.Realm, error) {
	realmNamespace := groupObj.Spec.RealmRef.Namespace
	if realmNamespace == "" {
		realmNamespace = groupObj.Namespace
	}

	var realm keycloakv1alpha1.Realm
	namespacedName := types.NamespacedName{
		Name:      groupObj.Spec.RealmRef.Name,
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

// setOwnerReference establishes an owner-dependent relationship between realm and group
func (r *GroupReconciler) setOwnerReference(ctx context.Context, groupObj *keycloakv1alpha1.Group, realm *keycloakv1alpha1.Realm) error {
	logger := log.FromContext(ctx)
	if r.hasCorrectOwnerReference(groupObj, realm) {
		return nil
	}

	if r.isCrossNamespaceReference(groupObj, realm) {
		logger.V(1).Info("Skipping owner reference - cross-namespace references not allowed")
		return nil
	}

	if err := controllerutil.SetOwnerReference(realm, groupObj, r.Scheme); err != nil {
		return fmt.Errorf("failed to set owner reference: %w", err)
	}

	if err := r.Update(ctx, groupObj); err != nil {
		return fmt.Errorf("failed to update group with owner reference: %w", err)
	}

	logger.Info("Owner reference set successfully")
	return nil
}

// hasCorrectOwnerReference checks if the correct owner reference already exists
func (r *GroupReconciler) hasCorrectOwnerReference(groupObj *keycloakv1alpha1.Group, realm *keycloakv1alpha1.Realm) bool {
	for _, ownerRef := range groupObj.GetOwnerReferences() {
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
func (r *GroupReconciler) isCrossNamespaceReference(groupObj *keycloakv1alpha1.Group, realm *keycloakv1alpha1.Realm) bool {
	return groupObj.Namespace != realm.Namespace
}

// reconcileGroup handles the main group reconciliation logic
func (r *GroupReconciler) reconcileGroup(ctx context.Context, groupObj *keycloakv1alpha1.Group, realm *keycloakv1alpha1.Realm) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Handle parent group resolution first if needed
	if err := r.resolveParentGroup(ctx, groupObj, realm); err != nil {
		logger.Error(err, "Failed to resolve parent group")
		result := GroupReconcileResult{
			Ready:      false,
			RealmReady: true,
			Message:    fmt.Sprintf("Failed to resolve parent group: %v", err),
		}
		return r.updateStatus(ctx, groupObj, result)
	}

	keycloakGroup, err := r.getOrCreateGroup(ctx, groupObj, realm)
	if err != nil {
		result := GroupReconcileResult{
			Ready:      false,
			RealmReady: true,
			Message:    fmt.Sprintf("Failed to reconcile group: %v", err),
		}
		return r.updateStatus(ctx, groupObj, result)
	}

	groupChanged := keycloakGroup == nil // If nil, it means we created
	if keycloakGroup != nil {
		var updateErr error
		groupChanged, updateErr = r.updateGroupIfNeeded(ctx, groupObj, keycloakGroup, realm)
		if updateErr != nil {
			logger.Error(updateErr, "Failed to update group")
			result := GroupReconcileResult{
				Ready:      false,
				RealmReady: true,
				Message:    fmt.Sprintf("Failed to update group: %v", updateErr),
			}
			return r.updateStatus(ctx, groupObj, result)
		}
	}

	message := r.getSuccessMessage(groupChanged)
	result := GroupReconcileResult{
		Ready:      true,
		RealmReady: true,
		Message:    message,
	}
	return r.updateStatus(ctx, groupObj, result)
}

// resolveParentGroup resolves the parent group ID if a parent is specified
func (r *GroupReconciler) resolveParentGroup(ctx context.Context, groupObj *keycloakv1alpha1.Group, realm *keycloakv1alpha1.Realm) error {
	logger := log.FromContext(ctx)

	if groupObj.Spec.ParentGroupRef == nil || groupObj.Spec.ParentGroupRef.Name == "" {
		// No parent group specified, clear any existing parent UUID
		if groupObj.Status.ParentGroupUUID != "" {
			logger.V(1).Info("Clearing parent group UUID - no parent specified")
			groupObj.Status.ParentGroupUUID = ""
		}
		return nil
	}

	// Check if we already have the parent UUID and it's still valid
	if groupObj.Status.ParentGroupUUID != "" {
		if _, err := r.KeycloakClient.GetGroup(ctx, realm.Name, groupObj.Status.ParentGroupUUID); err == nil {
			logger.V(1).Info("Parent group UUID still valid", "parentUUID", groupObj.Status.ParentGroupUUID)
			return nil // Parent still exists
		}
		// Parent not found, need to resolve again
		logger.Info("Parent group UUID no longer valid, resolving again", "oldParentUUID", groupObj.Status.ParentGroupUUID)
		groupObj.Status.ParentGroupUUID = ""
	}

	// Get the parent group namespace (default to same namespace if not specified)
	parentNamespace := groupObj.Spec.ParentGroupRef.Namespace
	if parentNamespace == "" {
		parentNamespace = groupObj.Namespace
	}

	// Fetch the parent Group resource
	var parentGroupObj keycloakv1alpha1.Group
	parentKey := types.NamespacedName{
		Name:      groupObj.Spec.ParentGroupRef.Name,
		Namespace: parentNamespace,
	}

	if err := r.Get(ctx, parentKey, &parentGroupObj); err != nil {
		if errors.IsNotFound(err) {
			return fmt.Errorf("parent group '%s' not found in namespace '%s'", groupObj.Spec.ParentGroupRef.Name, parentNamespace)
		}
		return fmt.Errorf("failed to fetch parent group '%s': %w", groupObj.Spec.ParentGroupRef.Name, err)
	}

	// Validate parent group hierarchy to prevent circular references
	if err := r.validateParentGroupHierarchy(ctx, groupObj, &parentGroupObj); err != nil {
		return fmt.Errorf("invalid parent group hierarchy: %w", err)
	}

	// Check if parent group is ready
	if !parentGroupObj.Status.Ready {
		return fmt.Errorf("parent group '%s' is not ready", groupObj.Spec.ParentGroupRef.Name)
	}

	// Check if parent group belongs to the same realm
	if parentGroupObj.Spec.RealmRef.Name != groupObj.Spec.RealmRef.Name {
		return fmt.Errorf("parent group '%s' belongs to different realm '%s', expected '%s'",
			groupObj.Spec.ParentGroupRef.Name, parentGroupObj.Spec.RealmRef.Name, groupObj.Spec.RealmRef.Name)
	}

	// Check if parent group has a valid UUID
	if parentGroupObj.Status.GroupUUID == "" {
		return fmt.Errorf("parent group '%s' does not have a valid GroupUUID", groupObj.Spec.ParentGroupRef.Name)
	}

	// Verify the parent group exists in Keycloak
	_, err := r.KeycloakClient.GetGroup(ctx, realm.Name, parentGroupObj.Status.GroupUUID)
	if err != nil {
		if keycloak.ErrorIs404(err) {
			return fmt.Errorf("parent group '%s' not found in Keycloak (UUID: %s)",
				groupObj.Spec.ParentGroupRef.Name, parentGroupObj.Status.GroupUUID)
		}
		return fmt.Errorf("failed to verify parent group in Keycloak: %w", err)
	}

	// Set the parent group UUID
	groupObj.Status.ParentGroupUUID = parentGroupObj.Status.GroupUUID
	logger.Info("Parent group resolved successfully",
		"parentName", groupObj.Spec.ParentGroupRef.Name,
		"parentUUID", groupObj.Status.ParentGroupUUID)

	return nil
}

// validateParentGroupHierarchy validates parent group hierarchy to prevent circular dependencies
func (r *GroupReconciler) validateParentGroupHierarchy(ctx context.Context, groupObj *keycloakv1alpha1.Group, parentGroupObj *keycloakv1alpha1.Group) error {
	// Prevent self-reference
	if groupObj.Name == parentGroupObj.Name && groupObj.Namespace == parentGroupObj.Namespace {
		return fmt.Errorf("group cannot be its own parent")
	}

	// Check for circular reference by walking up the parent chain
	visited := make(map[string]bool)
	current := parentGroupObj

	for current.Spec.ParentGroupRef != nil && current.Spec.ParentGroupRef.Name != "" {
		key := fmt.Sprintf("%s/%s", current.Namespace, current.Spec.ParentGroupRef.Name)
		if visited[key] {
			return fmt.Errorf("circular parent group reference detected")
		}
		visited[key] = true

		// Check if this would create a circular reference with our group
		if current.Spec.ParentGroupRef.Name == groupObj.Name {
			parentNS := current.Spec.ParentGroupRef.Namespace
			if parentNS == "" {
				parentNS = current.Namespace
			}
			if parentNS == groupObj.Namespace {
				return fmt.Errorf("circular parent group reference: %s would reference %s", groupObj.Name, current.Name)
			}
		}

		// Get the next parent
		var nextParent keycloakv1alpha1.Group
		nextParentNS := current.Spec.ParentGroupRef.Namespace
		if nextParentNS == "" {
			nextParentNS = current.Namespace
		}

		nextKey := types.NamespacedName{
			Name:      current.Spec.ParentGroupRef.Name,
			Namespace: nextParentNS,
		}

		if err := r.Get(ctx, nextKey, &nextParent); err != nil {
			if errors.IsNotFound(err) {
				break // Parent chain ends here
			}
			return fmt.Errorf("failed to validate parent hierarchy: %w", err)
		}

		current = &nextParent
	}

	return nil
}

// getOrCreateGroup retrieves an existing group or creates a new one
func (r *GroupReconciler) getOrCreateGroup(ctx context.Context, groupObj *keycloakv1alpha1.Group, realm *keycloakv1alpha1.Realm) (*keycloak.Group, error) {
	if groupObj.Status.GroupUUID != "" {
		group, err := r.fetchExistingGroup(ctx, groupObj, realm)
		if err != nil {
			return nil, err
		}
		if group != nil {
			return group, nil
		}
	}

	return nil, r.createNewGroup(ctx, groupObj, realm)
}

// fetchExistingGroup retrieves an existing group by UUID
func (r *GroupReconciler) fetchExistingGroup(ctx context.Context, groupObj *keycloakv1alpha1.Group, realm *keycloakv1alpha1.Realm) (*keycloak.Group, error) {
	logger := log.FromContext(ctx)
	logger.V(1).Info("Fetching group by UUID", "uuid", groupObj.Status.GroupUUID)

	group, err := r.KeycloakClient.GetGroup(ctx, realm.Name, groupObj.Status.GroupUUID)
	if err != nil {
		if keycloak.ErrorIs404(err) {
			logger.Info("Group UUID not found in Keycloak, will recreate")
			r.clearGroupState(groupObj)
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get group by UUID: %w", err)
	}

	return group, nil
}

// clearGroupState clears stored group state when group is not found
func (r *GroupReconciler) clearGroupState(groupObj *keycloakv1alpha1.Group) {
	groupObj.Status.GroupUUID = ""
	groupObj.Status.ParentGroupUUID = ""
}

// createNewGroup creates a new group in Keycloak
func (r *GroupReconciler) createNewGroup(ctx context.Context, groupObj *keycloakv1alpha1.Group, realm *keycloakv1alpha1.Realm) error {
	logger := log.FromContext(ctx)
	logger.Info("Creating group in Keycloak")

	newGroup := r.buildGroupFromSpec(groupObj, realm)

	if err := r.KeycloakClient.NewGroup(ctx, newGroup); err != nil {
		if keycloak.ErrorIs409(err) {
			return fmt.Errorf("group exists in Keycloak but UUID not tracked - manual intervention required")
		}
		return fmt.Errorf("failed to create group: %w", err)
	}

	return r.storeGroupUUID(ctx, groupObj, realm)
}

// buildGroupFromSpec creates a new Group from the spec
func (r *GroupReconciler) buildGroupFromSpec(groupObj *keycloakv1alpha1.Group, realm *keycloakv1alpha1.Realm) *keycloak.Group {
	return &keycloak.Group{
		RealmId:     realm.Name,
		ParentId:    groupObj.Status.ParentGroupUUID,
		Name:        groupObj.Spec.Name,
		Attributes:  groupObj.Spec.Attributes,
		RealmRoles:  groupObj.Spec.RealmRoles,
		ClientRoles: groupObj.Spec.ClientRoles,
	}
}

// storeGroupUUID fetches and stores the UUID of the created group
func (r *GroupReconciler) storeGroupUUID(ctx context.Context, groupObj *keycloakv1alpha1.Group, realm *keycloakv1alpha1.Realm) error {
	logger := log.FromContext(ctx)
	createdGroup, err := r.KeycloakClient.GetGroupByName(ctx, realm.Name, groupObj.Spec.Name)
	if err != nil {
		return fmt.Errorf("group created but failed to retrieve UUID: %w", err)
	}

	groupObj.Status.GroupUUID = createdGroup.Id
	logger.Info("Group created successfully", "uuid", createdGroup.Id)
	return nil
}

// updateGroupIfNeeded checks for differences and updates the group if needed
func (r *GroupReconciler) updateGroupIfNeeded(ctx context.Context, groupObj *keycloakv1alpha1.Group, keycloakGroup *keycloak.Group, realm *keycloakv1alpha1.Realm) (bool, error) {
	logger := log.FromContext(ctx)
	diffs := r.getGroupDiffs(groupObj, keycloakGroup)
	if len(diffs) == 0 {
		return false, nil
	}

	logger.Info("Group configuration changes detected", "changes", strings.Join(diffs, ", "))

	updatedGroup := r.applyChangesToGroup(groupObj, keycloakGroup, realm)
	if err := r.KeycloakClient.UpdateGroup(ctx, updatedGroup); err != nil {
		return false, fmt.Errorf("failed to update group: %w", err)
	}

	logger.Info("Group updated successfully")
	return true, nil
}

// applyChangesToGroup applies spec changes to the Keycloak group
func (r *GroupReconciler) applyChangesToGroup(groupObj *keycloakv1alpha1.Group, keycloakGroup *keycloak.Group, realm *keycloakv1alpha1.Realm) *keycloak.Group {
	updatedGroup := *keycloakGroup

	// Apply basic fields
	updatedGroup.Name = groupObj.Spec.Name
	updatedGroup.RealmId = realm.Name
	updatedGroup.ParentId = groupObj.Status.ParentGroupUUID
	updatedGroup.Attributes = groupObj.Spec.Attributes
	updatedGroup.RealmRoles = groupObj.Spec.RealmRoles
	updatedGroup.ClientRoles = groupObj.Spec.ClientRoles

	return &updatedGroup
}

// getGroupDiffs compares group specifications and returns a list of differences
func (r *GroupReconciler) getGroupDiffs(groupObj *keycloakv1alpha1.Group, keycloakGroup *keycloak.Group) []string {
	var diffs []string

	fields := []GroupFieldDiff{
		{"name", keycloakGroup.Name, groupObj.Spec.Name},
		{"parentId", keycloakGroup.ParentId, groupObj.Status.ParentGroupUUID},
	}

	for _, field := range fields {
		if !reflect.DeepEqual(field.Old, field.New) {
			diffs = append(diffs, r.formatGroupFieldDiff(field))
		}
	}

	// Handle map and slice fields
	if !r.stringSlicesEqual(keycloakGroup.RealmRoles, groupObj.Spec.RealmRoles) {
		diffs = append(diffs, fmt.Sprintf("realmRoles: %v -> %v", keycloakGroup.RealmRoles, groupObj.Spec.RealmRoles))
	}

	if !r.stringMapSlicesEqual(keycloakGroup.ClientRoles, groupObj.Spec.ClientRoles) {
		diffs = append(diffs, fmt.Sprintf("clientRoles: %v -> %v", keycloakGroup.ClientRoles, groupObj.Spec.ClientRoles))
	}

	if !r.stringMapSlicesEqual(keycloakGroup.Attributes, groupObj.Spec.Attributes) {
		diffs = append(diffs, fmt.Sprintf("attributes: %v -> %v", keycloakGroup.Attributes, groupObj.Spec.Attributes))
	}

	return diffs
}

// formatGroupFieldDiff formats a field difference for logging
func (r *GroupReconciler) formatGroupFieldDiff(field GroupFieldDiff) string {
	if reflect.TypeOf(field.Old).Kind() == reflect.String {
		return fmt.Sprintf("%s: '%v' -> '%v'", field.Name, field.Old, field.New)
	}
	return fmt.Sprintf("%s: %v -> %v", field.Name, field.Old, field.New)
}

// stringSlicesEqual compares two string slices, treating nil and empty as equal
func (r *GroupReconciler) stringSlicesEqual(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

// stringMapSlicesEqual compares two map[string][]string, treating nil and empty as equal
func (r *GroupReconciler) stringMapSlicesEqual(a, b map[string][]string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

// getSuccessMessage returns an appropriate success message
func (r *GroupReconciler) getSuccessMessage(groupChanged bool) string {
	if groupChanged {
		return "Group and members reconciled successfully"
	}
	return "Group and members reconciled"
}

// reconcileDelete handles group deletion
func (r *GroupReconciler) reconcileDelete(ctx context.Context, groupObj *keycloakv1alpha1.Group) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(groupObj, groupFinalizer) {
		return ctrl.Result{}, nil
	}

	if err := r.deleteGroupFromKeycloak(ctx, groupObj); err != nil {
		return ctrl.Result{}, err
	}

	controllerutil.RemoveFinalizer(groupObj, groupFinalizer)
	return ctrl.Result{}, r.Update(ctx, groupObj)
}

// deleteGroupFromKeycloak handles the actual deletion from Keycloak
func (r *GroupReconciler) deleteGroupFromKeycloak(ctx context.Context, groupObj *keycloakv1alpha1.Group) error {
	logger := log.FromContext(ctx)
	if groupObj.Status.GroupUUID == "" {
		logger.Info("No UUID stored - skipping Keycloak deletion")
		return nil
	}

	realm, err := r.getRealm(ctx, groupObj)
	if err != nil {
		logger.Error(err, "Failed to get realm for group deletion")
		return err
	}

	if realm == nil {
		logger.Info("Realm not found - skipping Keycloak deletion")
		return nil
	}

	logger.Info("Deleting group from Keycloak", "uuid", groupObj.Status.GroupUUID)

	if err := r.KeycloakClient.DeleteGroup(ctx, realm.Name, groupObj.Status.GroupUUID); err != nil {
		if keycloak.ErrorIs404(err) {
			logger.Info("Group already deleted from Keycloak")
			return nil
		}
		logger.Error(err, "Failed to delete group from Keycloak")
		return err
	}

	logger.Info("Group deleted from Keycloak")
	return nil
}

// updateStatus updates the group status with retry logic
func (r *GroupReconciler) updateStatus(ctx context.Context, groupObj *keycloakv1alpha1.Group, result GroupReconcileResult) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("group", groupObj.Name)

	for i := range groupMaxStatusRetries {
		if err := r.performStatusUpdate(ctx, groupObj, result); err != nil {
			if errors.IsConflict(err) && i < groupMaxStatusRetries-1 {
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

	return ctrl.Result{RequeueAfter: groupRequeueInterval}, nil
}

// performStatusUpdate performs a single status update attempt
func (r *GroupReconciler) performStatusUpdate(ctx context.Context, groupObj *keycloakv1alpha1.Group, result GroupReconcileResult) error {
	logger := log.FromContext(ctx)
	var latestGroup keycloakv1alpha1.Group
	if err := r.Get(ctx, client.ObjectKeyFromObject(groupObj), &latestGroup); err != nil {
		return err
	}

	r.applyStatusUpdate(&latestGroup, groupObj, result)

	if err := r.Status().Update(ctx, &latestGroup); err != nil {
		return err
	}

	groupObj.Status = latestGroup.Status
	logger.V(1).Info("Status updated successfully",
		"ready", latestGroup.Status.Ready,
		"message", latestGroup.Status.Message,
		"groupUUID", latestGroup.Status.GroupUUID,
		"parentGroupUUID", latestGroup.Status.ParentGroupUUID)

	return nil
}

// applyStatusUpdate applies the status changes to the latest group object
func (r *GroupReconciler) applyStatusUpdate(latestGroup, originalGroup *keycloakv1alpha1.Group, result GroupReconcileResult) {
	latestGroup.Status.Ready = result.Ready
	latestGroup.Status.RealmReady = result.RealmReady
	latestGroup.Status.Message = result.Message
	now := metav1.NewTime(time.Now())
	latestGroup.Status.LastSyncTime = &now

	if originalGroup.Status.GroupUUID != "" {
		latestGroup.Status.GroupUUID = originalGroup.Status.GroupUUID
	}

	// Preserve parent group UUID
	latestGroup.Status.ParentGroupUUID = originalGroup.Status.ParentGroupUUID
}

// SetupWithManager sets up the controller with the Manager
func (r *GroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&keycloakv1alpha1.Group{}).
		Complete(r)
}
