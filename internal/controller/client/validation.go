// Copyright 2025 toastyice
//
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"context"
	"fmt"

	"github.com/keycloak/terraform-provider-keycloak/keycloak"
	keycloakv1alpha1 "github.com/toastyice/keycloak-config-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// validateReconciler ensures the controller is properly initialized
func (r *ClientReconciler) validateReconciler(keycloakClient *keycloak.KeycloakClient) error {
	if keycloakClient == nil {
		return fmt.Errorf("KeycloakClient is nil - controller not properly initialized")
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

// isMarkedForDeletion checks if the client is being deleted
func (r *ClientReconciler) isMarkedForDeletion(clientObj *keycloakv1alpha1.Client) bool {
	return !clientObj.ObjectMeta.DeletionTimestamp.IsZero()
}

// isCrossNamespaceReference checks if this would be a cross-namespace reference
func (r *ClientReconciler) isCrossNamespaceReference(clientObj *keycloakv1alpha1.Client, realm *keycloakv1alpha1.Realm) bool {
	return clientObj.Namespace != realm.Namespace
}

// ensureFinalizer adds the finalizer if it doesn't exist
func (r *ClientReconciler) ensureFinalizer(ctx context.Context, clientObj *keycloakv1alpha1.Client) error {
	if !controllerutil.ContainsFinalizer(clientObj, clientFinalizer) {
		controllerutil.AddFinalizer(clientObj, clientFinalizer)
		return r.Update(ctx, clientObj)
	}
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

// isKeycloakInstanceConfigReady checks if the KeycloakInstanceConfig is ready based on its conditions
func (r *ClientReconciler) isKeycloakInstanceConfigReady(instanceConfig *keycloakv1alpha1.KeycloakInstanceConfig) bool {
	for _, condition := range instanceConfig.Status.Conditions {
		if condition.Type == "Ready" && condition.Status == metav1.ConditionTrue {
			return true
		}
	}
	return false
}

// statusUnchanged checks if the status would actually change
func (r *ClientReconciler) statusUnchanged(latest *keycloakv1alpha1.Client, result ReconcileResult) bool {
	return latest.Status.Ready == result.Ready &&
		latest.Status.RealmReady == result.RealmReady &&
		latest.Status.Message == result.Message
}
