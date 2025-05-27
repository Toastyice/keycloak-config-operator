package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/keycloak/terraform-provider-keycloak/keycloak"
	keycloakv1alpha1 "github.com/toastyice/keycloak-config-operator/api/v1alpha1"
)

// GroupReconciler reconciles a Group object
type GroupReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	KeycloakClient *keycloak.KeycloakClient
}

// +kubebuilder:rbac:groups=keycloak.schella.network,resources=groups,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=keycloak.schella.network,resources=groups/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=keycloak.schella.network,resources=groups/finalizers,verbs=update

func (r *GroupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	// TODO(user): your logic here

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *GroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&keycloakv1alpha1.Group{}).
		Complete(r)
}
