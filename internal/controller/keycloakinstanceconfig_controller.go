package controller

import (
	"context"
	"crypto/tls"
	"crypto/x509"
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
	// Use AdminUrl if provided, otherwise fall back to Url
	keycloakUrl := config.Spec.Url
	if config.Spec.AdminUrl != "" {
		keycloakUrl = config.Spec.AdminUrl
	}

	if keycloakUrl == "" {
		return false, "URL is not specified in the configuration"
	}

	// Create TLS config
	tlsConfig := &tls.Config{
		InsecureSkipVerify: config.Spec.TlsInsecureSkipVerify,
	}

	// If CA certificate is provided, add it to RootCAs for server verification
	if config.Spec.CaCert != "" {
		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM([]byte(config.Spec.CaCert)) {
			return false, "failed to parse provided CA certificate"
		}
		tlsConfig.RootCAs = caCertPool
	}

	// Set default timeout if not specified
	timeout := time.Duration(config.Spec.Timeout) * time.Second
	if config.Spec.Timeout <= 0 {
		timeout = 30 * time.Second // Default 30 seconds
	}

	// Create HTTP client with timeout and TLS configuration
	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
	}

	// Attempt to reach the URL
	resp, err := client.Get(keycloakUrl)
	if err != nil {
		return false, fmt.Sprintf("failed to reach URL %s: %v", keycloakUrl, err)
	}
	defer resp.Body.Close()

	// Check if the response status indicates success
	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		return true, fmt.Sprintf("URL %s is reachable (status: %d)", keycloakUrl, resp.StatusCode)
	}

	return false, fmt.Sprintf("URL %s returned non-success status: %d", keycloakUrl, resp.StatusCode)
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
			config.Status.Themes = convertKeycloakThemeToV1alpha1Theme(serverInfo.Themes)
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

func convertKeycloakThemeToV1alpha1Theme(themes map[string][]keycloak.Theme) map[string][]keycloakv1alpha1.Theme {
	if themes == nil {
		return nil
	}

	result := make(map[string][]keycloakv1alpha1.Theme, len(themes))
	for themeType, themeList := range themes {
		if len(themeList) == 0 {
			result[themeType] = nil
			continue
		}

		converted := make([]keycloakv1alpha1.Theme, 0, len(themeList))
		for _, theme := range themeList {
			converted = append(converted, keycloakv1alpha1.Theme{
				Name:    theme.Name,
				Locales: theme.Locales,
			})
		}
		result[themeType] = converted
	}
	return result
}

// SetupWithManager sets up the controller with the Manager.
func (r *KeycloakInstanceConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&keycloakv1alpha1.KeycloakInstanceConfig{}).
		Complete(r)
}
