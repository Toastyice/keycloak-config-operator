package keycloak

import (
	"context"
	"fmt"
	"sync"

	"github.com/keycloak/terraform-provider-keycloak/keycloak"
	keycloakv1alpha1 "github.com/toastyice/keycloak-config-operator/api/v1alpha1"
)

type ClientManager struct {
	clients map[string]*keycloak.KeycloakClient
	mutex   sync.RWMutex
}

func NewClientManager() *ClientManager {
	return &ClientManager{
		clients: make(map[string]*keycloak.KeycloakClient),
	}
}

// internal/keycloak/manager.go
func (cm *ClientManager) GetOrCreateClient(ctx context.Context, config *keycloakv1alpha1.KeycloakInstanceConfig) (*keycloak.KeycloakClient, error) {
	// Add nil checks
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	if cm == nil {
		return nil, fmt.Errorf("client manager is nil")
	}

	if cm.clients == nil {
		cm.clients = make(map[string]*keycloak.KeycloakClient)
	}

	key := fmt.Sprintf("%s/%s", config.Namespace, config.Name)

	cm.mutex.RLock()
	if client, exists := cm.clients[key]; exists {
		cm.mutex.RUnlock()
		return client, nil
	}
	cm.mutex.RUnlock()

	// Create new client
	cm.mutex.Lock()
	defer cm.mutex.Unlock()

	// Double-check after acquiring write lock
	if client, exists := cm.clients[key]; exists {
		return client, nil
	}

	client, err := cm.createKeycloakClient(ctx, &config.Spec)
	if err != nil {
		return nil, fmt.Errorf("failed to create Keycloak client for %s: %w", key, err)
	}

	cm.clients[key] = client
	return client, nil
}

func (cm *ClientManager) RemoveClient(config *keycloakv1alpha1.KeycloakInstanceConfig) {
	key := fmt.Sprintf("%s/%s", config.Namespace, config.Name)

	cm.mutex.Lock()
	defer cm.mutex.Unlock()

	delete(cm.clients, key)
}

func (cm *ClientManager) createKeycloakClient(ctx context.Context, spec *keycloakv1alpha1.KeycloakInstanceConfigSpec) (*keycloak.KeycloakClient, error) {
	return keycloak.NewKeycloakClient(
		ctx,
		spec.Url,
		spec.BasePath,
		spec.ClientId,
		spec.ClientSecret,
		spec.Realm,
		spec.Username,
		spec.Password,
		true, // perform initial login
		spec.Timeout,
		spec.CaCert,
		spec.TlsInsecureSkipVerify,
		"keycloak-config-operator/v1alpha1",
		false, // Red Hat SSO
		spec.AdditionalHeaders,
	)
}
