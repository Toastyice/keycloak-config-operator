// Copyright 2025 toastyice
//
// SPDX-License-Identifier: Apache-2.0

package keycloak

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/keycloak/terraform-provider-keycloak/keycloak"
	keycloakv1alpha1 "github.com/toastyice/keycloak-config-operator/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/log"
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

// getClientKey generates a unique key that includes URL and generation
// to ensure new clients are created when configuration changes
func (cm *ClientManager) getClientKey(config *keycloakv1alpha1.KeycloakInstanceConfig) string {
	return fmt.Sprintf("%s/%s-%s-%d",
		config.Namespace,
		config.Name,
		config.Spec.Url,
		config.Generation)
}

// getClientPrefix returns the prefix used to identify all clients for a specific config
func (cm *ClientManager) getClientPrefix(config *keycloakv1alpha1.KeycloakInstanceConfig) string {
	return fmt.Sprintf("%s/%s-", config.Namespace, config.Name)
}

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

	key := cm.getClientKey(config)
	logger := log.FromContext(ctx)

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

	// Clean up any old clients for this config (different generation/URL)
	prefix := cm.getClientPrefix(config)
	for existingKey := range cm.clients {
		if strings.HasPrefix(existingKey, prefix) && existingKey != key {
			delete(cm.clients, existingKey)
			logger.Info("Removed old client due to configuration change",
				"oldKey", existingKey,
				"newKey", key,
				"namespace", config.Namespace,
				"name", config.Name)
		}
	}

	// Create new client
	client, err := cm.createKeycloakClient(ctx, &config.Spec)
	if err != nil {
		return nil, fmt.Errorf("failed to create Keycloak client for %s: %w", key, err)
	}

	cm.clients[key] = client
	logger.Info("Created new Keycloak client",
		"key", key,
		"url", config.Spec.Url,
		"generation", config.Generation)

	return client, nil
}

func (cm *ClientManager) RemoveClient(config *keycloakv1alpha1.KeycloakInstanceConfig) {
	if config == nil {
		return
	}

	cm.mutex.Lock()
	defer cm.mutex.Unlock()

	// Remove all clients for this config (any generation/URL)
	prefix := cm.getClientPrefix(config)
	for existingKey := range cm.clients {
		if strings.HasPrefix(existingKey, prefix) {
			delete(cm.clients, existingKey)
		}
	}
}

func (cm *ClientManager) createKeycloakClient(ctx context.Context, spec *keycloakv1alpha1.KeycloakInstanceConfigSpec) (*keycloak.KeycloakClient, error) {
	// Use AdminUrl if provided, otherwise fall back to Url
	keycloakUrl := spec.Url
	if spec.AdminUrl != "" {
		keycloakUrl = spec.AdminUrl
	}
	return keycloak.NewKeycloakClient(
		ctx,
		keycloakUrl,
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
		spec.RedHatSso,
		spec.AdditionalHeaders,
	)
}
