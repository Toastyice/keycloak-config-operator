package controller

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/keycloak/terraform-provider-keycloak/keycloak"
	keycloakv1alpha1 "github.com/toastyice/keycloak-config-operator/api/v1alpha1"
	keycloakclientmanager "github.com/toastyice/keycloak-config-operator/internal/keycloak"
)

const (
	TypeConnected = "Connected" // Can we reach the Keycloak URL?
	TypeReady     = "Ready"     // Can we authenticate and use the API?

	// Connected condition reasons
	ReasonURLReachable   = "URLReachable"
	ReasonURLUnreachable = "URLUnreachable"

	// Ready condition reasons
	ReasonAuthenticationSuccessful = "AuthenticationSuccessful"
	ReasonAuthenticationFailed     = "AuthenticationFailed"
	ReasonAPIError                 = "APIError"
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
		return r.updateStatus(ctx, &config, false, false, "ClientManager not initialized", "ClientManager not initialized", nil)
	}

	// Phase 1: Check if URL is reachable
	urlReachable, connectMessage := r.checkURLReachability(&config)

	if !urlReachable {
		// If not connected, Ready is implicitly false
		return r.updateStatus(ctx, &config, false, false, connectMessage, "Cannot reach Keycloak URL", nil)
	}

	// Phase 2: Check authentication and API access
	client, err := r.ClientManager.GetOrCreateClient(ctx, &config)
	if err != nil {
		authMessage := fmt.Sprintf("Authentication failed: %v", err)
		// Connected=true, but Ready=false due to auth failure
		return r.updateStatus(ctx, &config, true, false, "Keycloak URL is reachable", authMessage, nil)
	}

	// Phase 3: Test API functionality
	serverInfo, err := r.testConnectionWithClient(ctx, client)
	if err != nil {
		apiMessage := fmt.Sprintf("API test failed: %v", err)
		// Connected=true, but Ready=false due to API failure
		return r.updateStatus(ctx, &config, true, false, "Keycloak URL is reachable", apiMessage, nil)
	}

	// All checks passed - both Connected and Ready are true
	successMessage := "Successfully authenticated and retrieved server info"
	return r.updateStatus(ctx, &config, true, true, "Keycloak URL is reachable", successMessage, serverInfo)
}

func (r *KeycloakInstanceConfigReconciler) checkURLReachability(config *keycloakv1alpha1.KeycloakInstanceConfig) (bool, string) {
	// Create a simple HTTP client with timeout
	client := &http.Client{
		Timeout: time.Duration(config.Spec.Timeout) * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: config.Spec.TlsInsecureSkipVerify,
			},
		},
	}

	// Try to reach the base URL
	testURL := fmt.Sprintf("%s%s", config.Spec.Url, config.Spec.BasePath)

	resp, err := client.Get(testURL)
	if err != nil {
		return false, fmt.Sprintf("Cannot reach Keycloak URL: %v", err)
	}
	defer resp.Body.Close()

	// Any HTTP response (even 404, 401, etc.) means the URL is reachable
	return true, "Keycloak URL is reachable"
}

func (r *KeycloakInstanceConfigReconciler) updateStatus(ctx context.Context, config *keycloakv1alpha1.KeycloakInstanceConfig, connected, ready bool, connectMessage, readyMessage string, serverInfo *keycloak.ServerInfo) (ctrl.Result, error) {
	now := metav1.NewTime(time.Now())

	// Update Connected condition
	var connectedCondition metav1.Condition
	if connected {
		connectedCondition = metav1.Condition{
			Type:               TypeConnected,
			Status:             metav1.ConditionTrue,
			Reason:             ReasonURLReachable,
			Message:            connectMessage,
			LastTransitionTime: now,
		}
	} else {
		connectedCondition = metav1.Condition{
			Type:               TypeConnected,
			Status:             metav1.ConditionFalse,
			Reason:             ReasonURLUnreachable,
			Message:            connectMessage,
			LastTransitionTime: now,
		}
	}

	// Update Ready condition
	var readyCondition metav1.Condition
	if ready {
		readyCondition = metav1.Condition{
			Type:               TypeReady,
			Status:             metav1.ConditionTrue,
			Reason:             ReasonAuthenticationSuccessful,
			Message:            readyMessage,
			LastTransitionTime: now,
		}
		config.Status.LastConnected = &now

		if serverInfo != nil {
			config.Status.ServerVersion = serverInfo.SystemInfo.ServerVersion
		}
	} else {
		var reason string
		if !connected {
			reason = ReasonURLUnreachable
		} else {
			// Connected but not ready means auth or API issues
			if strings.Contains(strings.ToLower(readyMessage), "auth") {
				reason = ReasonAuthenticationFailed
			} else {
				reason = ReasonAPIError
			}
		}

		readyCondition = metav1.Condition{
			Type:               TypeReady,
			Status:             metav1.ConditionFalse,
			Reason:             reason,
			Message:            readyMessage,
			LastTransitionTime: now,
		}
	}

	meta.SetStatusCondition(&config.Status.Conditions, connectedCondition)
	meta.SetStatusCondition(&config.Status.Conditions, readyCondition)

	if err := r.Status().Update(ctx, config); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
	}

	if ready {
		return ctrl.Result{RequeueAfter: time.Minute * 5}, nil
	}
	return ctrl.Result{RequeueAfter: time.Second * 30}, nil
}

func (r *KeycloakInstanceConfigReconciler) testConnectionWithClient(ctx context.Context, client *keycloak.KeycloakClient) (*keycloak.ServerInfo, error) {
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
