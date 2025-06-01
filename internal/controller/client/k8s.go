// Copyright 2025 toastyice
//
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"context"
	"fmt"

	keycloakv1alpha1 "github.com/toastyice/keycloak-config-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// fetchClient retrieves the Client object from the cluster
func (r *ClientReconciler) fetchClient(ctx context.Context, namespacedName types.NamespacedName) (*keycloakv1alpha1.Client, error) {
	var clientObj keycloakv1alpha1.Client
	if err := r.Get(ctx, namespacedName, &clientObj); err != nil {
		return nil, err
	}
	return &clientObj, nil
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

// getKeycloakInstanceConfig retrieves and validates the KeycloakInstanceConfig object referenced by the client
func (r *ClientReconciler) getKeycloakInstanceConfig(ctx context.Context, clientObj *keycloakv1alpha1.Client) (*keycloakv1alpha1.KeycloakInstanceConfig, error) {
	instanceConfigNamespace := clientObj.Spec.InstanceConfigRef.Namespace
	if instanceConfigNamespace == "" {
		instanceConfigNamespace = clientObj.Namespace
	}

	var instanceConfig keycloakv1alpha1.KeycloakInstanceConfig
	namespacedName := types.NamespacedName{
		Name:      clientObj.Spec.InstanceConfigRef.Name,
		Namespace: instanceConfigNamespace,
	}

	if err := r.Get(ctx, namespacedName, &instanceConfig); err != nil {
		if errors.IsNotFound(err) {
			return nil, fmt.Errorf("KeycloakInstanceConfig '%s' not found in namespace '%s'",
				clientObj.Spec.InstanceConfigRef.Name, instanceConfigNamespace)
		}
		return nil, fmt.Errorf("failed to get KeycloakInstanceConfig '%s': %w",
			clientObj.Spec.InstanceConfigRef.Name, err)
	}

	// Check if the instance config is ready by looking at conditions
	if !r.isKeycloakInstanceConfigReady(&instanceConfig) {
		return nil, fmt.Errorf("KeycloakInstanceConfig '%s' is not ready", instanceConfig.Name)
	}

	return &instanceConfig, nil
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
