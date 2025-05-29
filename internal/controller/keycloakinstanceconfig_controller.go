package controller

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	keycloakv1alpha1 "github.com/toastyice/keycloak-config-operator/api/v1alpha1"
	keycloakclientmanager "github.com/toastyice/keycloak-config-operator/internal/keycloak"
)

// KeycloakInstanceConfigReconciler reconciles a KeycloakInstanceConfig object
type KeycloakInstanceConfigReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	ClientManager *keycloakclientmanager.ClientManager
}

// +kubebuilder:rbac:groups=keycloak.schella.network,resources=keycloakinstanceconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=keycloak.schella.network,resources=keycloakinstanceconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=keycloak.schella.network,resources=keycloakinstanceconfigs/finalizers,verbs=update

// internal/controller/keycloakinstanceconfig_controller.go
func (r *KeycloakInstanceConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	var config keycloakv1alpha1.KeycloakInstanceConfig
	if err := r.Get(ctx, req.NamespacedName, &config); err != nil {
		if client.IgnoreNotFound(err) == nil {
			// Resource was deleted, clean up the client
			r.ClientManager.RemoveClient(&config)
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Test the connection
	keycloakClient, err := r.ClientManager.GetOrCreateClient(ctx, &config)
	if err != nil {
		log.Error(err, "Failed to create Keycloak client")
		// Update status to reflect the error
		return ctrl.Result{RequeueAfter: time.Minute * 5}, nil
	}

	// Test connection by trying to get realm info
	_, err = keycloakClient.GetRealm(ctx, config.Spec.Realm)
	if err != nil {
		log.Error(err, "Failed to connect to Keycloak instance")
		// Update status to reflect connection failure
		return ctrl.Result{RequeueAfter: time.Minute * 5}, nil
	}

	// Update status to reflect successful connection
	log.Info("Successfully connected to Keycloak instance")

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *KeycloakInstanceConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&keycloakv1alpha1.KeycloakInstanceConfig{}).
		Complete(r)
}
