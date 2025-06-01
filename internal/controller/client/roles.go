// Copyright 2025 toastyice
//
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"context"
	"fmt"

	"github.com/keycloak/terraform-provider-keycloak/keycloak"
	keycloakv1alpha1 "github.com/toastyice/keycloak-config-operator/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

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
