// Copyright 2025 toastyice
//
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"context"
	"fmt"
	"strings"

	"github.com/keycloak/terraform-provider-keycloak/keycloak"
	keycloakv1alpha1 "github.com/toastyice/keycloak-config-operator/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// fetchExistingClient retrieves an existing client by UUID
func (r *ClientReconciler) fetchExistingClient(ctx context.Context, keycloakClient *keycloak.KeycloakClient,
	clientObj *keycloakv1alpha1.Client, realm *keycloakv1alpha1.Realm) (*keycloak.OpenidClient, error) {
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

// getOrCreateClient retrieves an existing client or creates a new one
func (r *ClientReconciler) getOrCreateClient(ctx context.Context, keycloakClient *keycloak.KeycloakClient,
	clientObj *keycloakv1alpha1.Client, realm *keycloakv1alpha1.Realm) (*keycloak.OpenidClient, error) {
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

// getClientRolesForSingleClient retrieves roles for a specific client
func (r *ClientReconciler) getClientRolesForSingleClient(ctx context.Context, keycloakClient *keycloak.KeycloakClient, realmName, clientUUID string) ([]*keycloak.Role, error) {
	mockClients := []*keycloak.OpenidClient{{Id: clientUUID}}
	return keycloakClient.GetClientRoles(ctx, realmName, mockClients)
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
