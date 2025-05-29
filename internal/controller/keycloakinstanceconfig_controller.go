package controller

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/keycloak/terraform-provider-keycloak/keycloak"
	keycloakv1alpha1 "github.com/toastyice/keycloak-config-operator/api/v1alpha1"
	keycloakclientmanager "github.com/toastyice/keycloak-config-operator/internal/keycloak"
)

const (
	// Condition types
	TypeConnected = "Connected"
	TypeReady     = "Ready"

	// Condition reasons
	ReasonConnectionSuccessful = "ConnectionSuccessful"
	ReasonConnectionFailed     = "ConnectionFailed"
	ReasonConfigurationInvalid = "ConfigurationInvalid"
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

	// Fetch the KeycloakInstanceConfig instance
	var config keycloakv1alpha1.KeycloakInstanceConfig
	if err := r.Get(ctx, req.NamespacedName, &config); err != nil {
		if client.IgnoreNotFound(err) == nil {
			log.Info("KeycloakInstanceConfig resource not found. Ignoring since object must be deleted")
			r.ClientManager.RemoveClient(&config)
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get KeycloakInstanceConfig")
		return ctrl.Result{}, err
	}

	// Handle deletion
	if !config.DeletionTimestamp.IsZero() {
		log.Info("KeycloakInstanceConfig is being deleted")
		r.ClientManager.RemoveClient(&config)
		return ctrl.Result{}, nil
	}

	if r.ClientManager == nil {
		return r.updateConnectionStatus(ctx, &config, false, "ClientManager not initialized", nil)
	}

	// Test the connection and get server info
	serverInfo, err := r.testConnection(ctx, &config)
	if err != nil {
		log.Error(err, "Failed to connect to Keycloak")
		return r.updateConnectionStatus(ctx, &config, false, err.Error(), nil)
	}

	log.Info("Successfully connected to Keycloak instance",
		"url", config.Spec.Url,
		"version", serverInfo.SystemInfo.ServerVersion)
	return r.updateConnectionStatus(ctx, &config, true, "Connected successfully", serverInfo)
}

func (r *KeycloakInstanceConfigReconciler) updateConnectionStatus(ctx context.Context, config *keycloakv1alpha1.KeycloakInstanceConfig, connected bool, message string, serverInfo *keycloak.ServerInfo) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Update the status
	now := metav1.Now()

	var condition metav1.Condition
	if connected {
		condition = metav1.Condition{
			Type:               TypeConnected,
			Status:             metav1.ConditionTrue,
			Reason:             ReasonConnectionSuccessful,
			Message:            message,
			LastTransitionTime: now,
		}
		config.Status.LastConnected = &now

		// Update server version if we have server info
		if serverInfo != nil {
			config.Status.ServerVersion = serverInfo.SystemInfo.ServerVersion
			log.V(1).Info("Updated server version", "version", serverInfo.SystemInfo.ServerVersion)
		}
	} else {
		condition = metav1.Condition{
			Type:               TypeConnected,
			Status:             metav1.ConditionFalse,
			Reason:             ReasonConnectionFailed,
			Message:            message,
			LastTransitionTime: now,
		}
		// Optionally clear server version on connection failure
		// config.Status.ServerVersion = ""
	}

	// Update or add the condition
	apimeta.SetStatusCondition(&config.Status.Conditions, condition)

	// Update ready condition based on connection status
	readyCondition := metav1.Condition{
		Type:               TypeReady,
		Status:             metav1.ConditionTrue,
		Reason:             ReasonConnectionSuccessful,
		Message:            "Keycloak instance is ready",
		LastTransitionTime: now,
	}
	if !connected {
		readyCondition.Status = metav1.ConditionFalse
		readyCondition.Reason = ReasonConnectionFailed
		readyCondition.Message = "Keycloak instance is not ready: " + message
	}
	apimeta.SetStatusCondition(&config.Status.Conditions, readyCondition)

	// Update the status subresource
	if err := r.Status().Update(ctx, config); err != nil {
		log.Error(err, "Failed to update KeycloakInstanceConfig status")
		return ctrl.Result{}, err
	}

	log.V(1).Info("Updated KeycloakInstanceConfig status", "connected", connected, "message", message)

	if !connected {
		// Requeue with backoff if connection failed
		return ctrl.Result{RequeueAfter: time.Minute * 2}, nil
	}

	// Requeue periodically to check connection health
	return ctrl.Result{RequeueAfter: time.Minute * 10}, nil
}

func (r *KeycloakInstanceConfigReconciler) testConnection(ctx context.Context, config *keycloakv1alpha1.KeycloakInstanceConfig) (*keycloak.ServerInfo, error) {
	// Get the client from the manager
	client, err := r.ClientManager.GetOrCreateClient(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to get Keycloak client: %w", err)
	}

	// Try to get server info to test connection
	serverInfo, err := client.GetServerInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Keycloak server: %w", err)
	}
	return serverInfo, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *KeycloakInstanceConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&keycloakv1alpha1.KeycloakInstanceConfig{}).
		Complete(r)
}
