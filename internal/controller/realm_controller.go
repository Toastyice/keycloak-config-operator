/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	keycloakv1alpha1 "github.com/toastyice/keycloak-config-operator/api/v1alpha1"
	"github.com/toastyice/keycloak-config-operator/internal/keycloak"
)

const realmFinalizer = "realm.keycloak.schella.network/finalizer"

// RealmReconciler reconciles a Realm object
type RealmReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	KeycloakClient *keycloak.KeycloakClient
}

// FieldDiff represents a difference between expected and actual field values
type FieldDiff struct {
	Name string
	Old  any
	New  any
}

//+kubebuilder:rbac:groups=keycloak.schella.network,resources=realms,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=keycloak.schella.network,resources=realms/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=keycloak.schella.network,resources=realms/finalizers,verbs=update

func (r *RealmReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Fetch the Realm instance
	var realm keycloakv1alpha1.Realm
	if err := r.Get(ctx, req.NamespacedName, &realm); err != nil {
		if errors.IsNotFound(err) {
			// Object not found, could have been deleted after reconcile request
			return ctrl.Result{}, nil
		}
		log.Error(err, "unable to fetch Realm")
		return ctrl.Result{}, err
	}

	// Check if the realm is being deleted
	if !realm.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &realm)
	}

	// Add finalizer if it doesn't exist
	if !controllerutil.ContainsFinalizer(&realm, realmFinalizer) {
		controllerutil.AddFinalizer(&realm, realmFinalizer)
		if err := r.Update(ctx, &realm); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Reconcile the realm
	return r.reconcileRealm(ctx, &realm)
}

// getDiffs compares realm specifications and returns a list of differences
func getDiffs(realm *keycloakv1alpha1.Realm, keycloakRealm *keycloak.Realm) []string {
	var diffs []string

	fields := []FieldDiff{
		// General
		{"enabled", keycloakRealm.Enabled, realm.Spec.Enabled},
		{"displayName", keycloakRealm.DisplayName, realm.Spec.DisplayName},
		{"displayNameHtml", keycloakRealm.DisplayNameHtml, realm.Spec.DisplayNameHtml},
		{"sslRequired", keycloakRealm.SslRequired, realm.Spec.SslRequired},
		{"userManagedAccess", keycloakRealm.UserManagedAccess, realm.Spec.UserManagedAccess},
		{"organizationsEnabled", keycloakRealm.OrganizationsEnabled, realm.Spec.OrganizationsEnabled},
		// Login / Login screen customization
		{"registrationAllowed", keycloakRealm.RegistrationAllowed, realm.Spec.RegistrationAllowed},
		{"resetPasswordAllowed", keycloakRealm.ResetPasswordAllowed, realm.Spec.ResetPasswordAllowed},
		{"rememberMe", keycloakRealm.RememberMe, realm.Spec.RememberMe},
		// Login / Email settings
		{"registrationEmailAsUsername", keycloakRealm.RegistrationEmailAsUsername, realm.Spec.RegistrationEmailAsUsername}, //if true duplicateEmailsAllowed must be false
		{"loginWithEmailAllowed", keycloakRealm.LoginWithEmailAllowed, realm.Spec.LoginWithEmailAllowed},                   //if true duplicateEmailsAllowed must be false
		{"duplicateEmailsAllowed", keycloakRealm.DuplicateEmailsAllowed, realm.Spec.DuplicateEmailsAllowed},
		{"verifyEmail", keycloakRealm.VerifyEmail, realm.Spec.VerifyEmail},
		// Login / User info settings
		{"editUsernameAllowed", keycloakRealm.EditUsernameAllowed, realm.Spec.EditUsernameAllowed},
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

	return diffs
}

func (r *RealmReconciler) reconcileRealm(ctx context.Context, realm *keycloakv1alpha1.Realm) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Check if realm exists in Keycloak
	keycloakRealm, err := r.KeycloakClient.GetRealm(ctx, realm.Name)
	if err != nil {
		// Check if it's a 404 error (realm doesn't exist)
		if keycloak.ErrorIs404(err) {
			// Create the realm
			log.Info("Creating realm in Keycloak", "realm", realm.Name)

			newRealm := &keycloak.Realm{
				Realm:                       realm.Name,
				Enabled:                     realm.Spec.Enabled,
				DisplayName:                 realm.Spec.DisplayName,
				DisplayNameHtml:             realm.Spec.DisplayNameHtml,
				SslRequired:                 realm.Spec.SslRequired,
				UserManagedAccess:           realm.Spec.UserManagedAccess,
				OrganizationsEnabled:        realm.Spec.OrganizationsEnabled,
				RegistrationAllowed:         realm.Spec.RegistrationAllowed,
				ResetPasswordAllowed:        realm.Spec.ResetPasswordAllowed,
				RememberMe:                  realm.Spec.RememberMe,
				LoginWithEmailAllowed:       realm.Spec.LoginWithEmailAllowed,
				RegistrationEmailAsUsername: realm.Spec.RegistrationEmailAsUsername,
				DuplicateEmailsAllowed:      realm.Spec.DuplicateEmailsAllowed,
				VerifyEmail:                 realm.Spec.VerifyEmail,
				EditUsernameAllowed:         realm.Spec.EditUsernameAllowed,
			}

			if err := r.KeycloakClient.NewRealm(ctx, newRealm); err != nil {
				// Check if it's a 409 conflict (realm already exists - race condition)
				if keycloak.ErrorIs409(err) {
					log.Info("Realm already exists (conflict), will check again", "realm", realm.Name)
					return ctrl.Result{Requeue: true}, nil
				}
				return r.updateStatus(ctx, realm, false, fmt.Sprintf("Failed to create realm: %v", err))
			}

			log.Info("Realm created successfully", "realm", realm.Name)
			return r.updateStatus(ctx, realm, true, "Realm created successfully")
		}

		// Other error
		return r.updateStatus(ctx, realm, false, fmt.Sprintf("Failed to check realm: %v", err))
	}

	// Check for differences using the structured approach
	diffs := getDiffs(realm, keycloakRealm)

	if len(diffs) > 0 {
		log.Info("Realm configuration changes detected", "realm", realm.Name, "changes", strings.Join(diffs, ", "))

		// Create a copy to modify
		updatedRealm := *keycloakRealm

		// Apply changes
		updatedRealm.Enabled = realm.Spec.Enabled
		updatedRealm.DisplayName = realm.Spec.DisplayName
		updatedRealm.DisplayNameHtml = realm.Spec.DisplayNameHtml
		updatedRealm.SslRequired = realm.Spec.SslRequired
		updatedRealm.UserManagedAccess = realm.Spec.UserManagedAccess
		updatedRealm.OrganizationsEnabled = realm.Spec.OrganizationsEnabled
		updatedRealm.RegistrationAllowed = realm.Spec.RegistrationAllowed
		updatedRealm.ResetPasswordAllowed = realm.Spec.ResetPasswordAllowed
		updatedRealm.RememberMe = realm.Spec.RememberMe
		updatedRealm.LoginWithEmailAllowed = realm.Spec.LoginWithEmailAllowed
		updatedRealm.RegistrationEmailAsUsername = realm.Spec.RegistrationEmailAsUsername
		updatedRealm.DuplicateEmailsAllowed = realm.Spec.DuplicateEmailsAllowed
		updatedRealm.VerifyEmail = realm.Spec.VerifyEmail
		updatedRealm.EditUsernameAllowed = realm.Spec.EditUsernameAllowed

		// Perform the update
		if err := r.KeycloakClient.UpdateRealm(ctx, &updatedRealm); err != nil {
			log.Error(err, "Failed to update realm", "realm", realm.Name)
			return r.updateStatus(ctx, realm, false, fmt.Sprintf("Failed to update realm: %v", err))
		}

		log.Info("Realm updated successfully", "realm", realm.Name)
		return r.updateStatus(ctx, realm, true, "Realm updated successfully")
	}

	// No changes detected - no logging needed for sync success
	return r.updateStatus(ctx, realm, true, "Realm synchronized")
}

func (r *RealmReconciler) reconcileDelete(ctx context.Context, realm *keycloakv1alpha1.Realm) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Check if we have the finalizer
	if controllerutil.ContainsFinalizer(realm, realmFinalizer) {
		// Delete the realm from Keycloak
		log.Info("Deleting realm from Keycloak", "realm", realm.Name)

		if err := r.KeycloakClient.DeleteRealm(ctx, realm.Name); err != nil {
			// Check if it's a 404 error (realm already doesn't exist)
			if keycloak.ErrorIs404(err) {
				log.Info("Realm already deleted from Keycloak", "realm", realm.Name)
			} else {
				log.Error(err, "Failed to delete realm from Keycloak", "realm", realm.Name)
				return ctrl.Result{}, err
			}
		} else {
			log.Info("Realm deleted from Keycloak", "realm", realm.Name)
		}

		// Remove finalizer
		controllerutil.RemoveFinalizer(realm, realmFinalizer)
		if err := r.Update(ctx, realm); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *RealmReconciler) updateStatus(ctx context.Context, realm *keycloakv1alpha1.Realm, ready bool, message string) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Retry status update with exponential backoff to handle conflicts
	maxRetries := 3
	for i := 0; i < maxRetries; i++ {
		// Fetch the latest version of the realm to avoid conflicts
		var latestRealm keycloakv1alpha1.Realm
		if err := r.Get(ctx, client.ObjectKeyFromObject(realm), &latestRealm); err != nil {
			log.Error(err, "Failed to fetch latest Realm for status update")
			return ctrl.Result{}, err
		}

		// Update status on the latest version
		latestRealm.Status.Ready = ready
		latestRealm.Status.Message = message
		now := metav1.NewTime(time.Now())
		latestRealm.Status.LastSyncTime = &now

		if err := r.Status().Update(ctx, &latestRealm); err != nil {
			if errors.IsConflict(err) && i < maxRetries-1 {
				log.V(1).Info("Status update conflict, retrying", "attempt", i+1, "realm", realm.Name)
				time.Sleep(time.Duration(i+1) * 100 * time.Millisecond) // Simple exponential backoff
				continue
			}
			log.Error(err, "Failed to update Realm status after retries")
			return ctrl.Result{}, err
		}

		// Success
		break
	}

	// Requeue after 10 sec for periodic sync
	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *RealmReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&keycloakv1alpha1.Realm{}).
		Complete(r)
}
