// Copyright 2025 toastyice
//
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"context"
	"time"

	keycloakv1alpha1 "github.com/toastyice/keycloak-config-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

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

	// Handle different requeue scenarios based on the result
	if result.Error != nil {
		return ctrl.Result{}, result.Error
	}

	// Different requeue intervals based on the result state
	switch {
	case !result.RealmReady:
		// Shorter interval when waiting for realm to be ready
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	case result.Ready:
		// Longer interval for ready clients (periodic sync)
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil //change to 10 min later!
	default:
		// Default interval for other cases
		return ctrl.Result{RequeueAfter: requeueInterval}, nil
	}
}

// performStatusUpdate performs a single status update attempt
func (r *ClientReconciler) performStatusUpdate(ctx context.Context, clientObj *keycloakv1alpha1.Client, result ReconcileResult) error {
	logger := log.FromContext(ctx).WithValues("client", clientObj.Name)

	var latestClient keycloakv1alpha1.Client
	if err := r.Get(ctx, client.ObjectKeyFromObject(clientObj), &latestClient); err != nil {
		return err
	}

	// Optional: Check if update is needed to avoid unnecessary API calls
	if r.statusUnchanged(&latestClient, result) {
		logger.V(2).Info("Status unchanged, skipping update")
		clientObj.Status = latestClient.Status
		return nil
	}

	r.applyStatusUpdate(&latestClient, clientObj, result)

	if err := r.Status().Update(ctx, &latestClient); err != nil {
		return err
	}

	clientObj.Status = latestClient.Status
	logger.V(1).Info("Status updated successfully",
		"ready", latestClient.Status.Ready,
		"realmReady", latestClient.Status.RealmReady,
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

	// Preserve operational data from the original client
	if originalClient.Status.ClientUUID != "" {
		latestClient.Status.ClientUUID = originalClient.Status.ClientUUID
	}

	if len(originalClient.Status.RoleUUIDs) > 0 {
		latestClient.Status.RoleUUIDs = originalClient.Status.RoleUUIDs
	} else if latestClient.Status.RoleUUIDs == nil {
		// Only initialize if it doesn't already exist
		latestClient.Status.RoleUUIDs = make(map[string]string)
	}
}
