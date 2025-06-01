// Copyright 2025 toastyice
//
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/keycloak/terraform-provider-keycloak/keycloak"
	keycloakv1alpha1 "github.com/toastyice/keycloak-config-operator/api/v1alpha1"
)

func (r *ClientReconciler) getClientSecret(clientObj *keycloakv1alpha1.Client, keycloakOpenIdClient *keycloak.OpenidClient) (string, error) {
	if clientObj.Status.ClientUUID == "" {
		return "", fmt.Errorf("client UUID not available")
	}

	if keycloakOpenIdClient == nil {
		return "", fmt.Errorf("keycloak client is nil")
	}

	// For public clients, there shouldn't be a secret
	if keycloakOpenIdClient.PublicClient {
		return "", fmt.Errorf("cannot get secret for public client")
	}

	secret := keycloakOpenIdClient.ClientSecret
	if secret == "" {
		return "", fmt.Errorf("client secret is empty - ensure this is a confidential client")
	}

	return secret, nil
}

func (r *ClientReconciler) createOrUpdateClientSecret(ctx context.Context, clientObj *keycloakv1alpha1.Client, clientSecret string, realm *keycloakv1alpha1.Realm) error {
	logger := log.FromContext(ctx).WithValues("client", clientObj.Name)

	secretName := r.getSecretName(clientObj)
	secretNamespace := r.getSecretNamespace(clientObj)

	// Get the KeycloakInstanceConfig to build URLs
	instanceConfig, err := r.getKeycloakInstanceConfig(ctx, clientObj)
	if err != nil {
		return fmt.Errorf("failed to get KeycloakInstanceConfig for secret: %w", err)
	}

	// Build URLs from the instance config
	keycloakURL := instanceConfig.Spec.Url
	if instanceConfig.Spec.BasePath != "" && instanceConfig.Spec.BasePath != "/" {
		keycloakURL += instanceConfig.Spec.BasePath
	}
	tokenURL := fmt.Sprintf("%s/realms/%s/protocol/openid-connect/token", keycloakURL, realm.Name)
	wellKnownURL := fmt.Sprintf("%s/realms/%s/.well-known/openid-configuration", keycloakURL, realm.Name)

	// Create the desired secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        secretName,
			Namespace:   secretNamespace,
			Labels:      r.getSecretLabels(clientObj),
			Annotations: r.getSecretAnnotations(clientObj),
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"keycloak-url":        []byte(keycloakURL),
			"keycloak-realm":      []byte(realm.Name),
			"keycloak-token-url":  []byte(tokenURL),
			"keycloak-well-known": []byte(wellKnownURL),
			"client-id":           []byte(clientObj.Spec.ClientID),
			"client-secret":       []byte(clientSecret),
		},
	}

	// Set owner reference
	if err := ctrl.SetControllerReference(clientObj, secret, r.Scheme); err != nil {
		return fmt.Errorf("failed to set controller reference: %w", err)
	}

	// Check if secret already exists
	existingSecret := &corev1.Secret{}
	err = r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: secretNamespace}, existingSecret)

	if err != nil {
		if errors.IsNotFound(err) {
			// Secret doesn't exist, create it
			if err := r.Create(ctx, secret); err != nil {
				return fmt.Errorf("failed to create kubernetes secret: %w", err)
			}
			logger.Info("Created kubernetes client secret",
				"secretName", secretName,
				"secretNamespace", secretNamespace,
				"keycloakURL", keycloakURL)
			return nil
		}
		return fmt.Errorf("failed to get existing secret: %w", err)
	}

	// Secret exists, check if it needs updating
	needsUpdate := false
	updateReasons := []string{}

	// Check all data fields for changes
	dataFields := map[string][]byte{
		"keycloak-url":        []byte(keycloakURL),
		"keycloak-realm":      []byte(realm.Name),
		"keycloak-token-url":  []byte(tokenURL),
		"keycloak-well-known": []byte(wellKnownURL),
		"client-id":           []byte(clientObj.Spec.ClientID),
		"client-secret":       []byte(clientSecret),
	}

	for key, expectedValue := range dataFields {
		if !bytes.Equal(existingSecret.Data[key], expectedValue) {
			needsUpdate = true
			updateReasons = append(updateReasons, fmt.Sprintf("%s changed", key))
			logger.V(1).Info("Secret field changed",
				"field", key,
				"old", string(existingSecret.Data[key]),
				"new", string(expectedValue))
		}
	}

	// Check if labels changed
	if !maps.Equal(existingSecret.Labels, secret.Labels) {
		needsUpdate = true
		updateReasons = append(updateReasons, "labels changed")
	}

	// Check if annotations changed
	if !maps.Equal(existingSecret.Annotations, secret.Annotations) {
		needsUpdate = true
		updateReasons = append(updateReasons, "annotations changed")
	}

	if needsUpdate {
		// Update the existing secret
		existingSecret.Data = secret.Data
		existingSecret.Labels = secret.Labels
		existingSecret.Annotations = secret.Annotations

		if err := r.Update(ctx, existingSecret); err != nil {
			return fmt.Errorf("failed to update kubernetes secret: %w", err)
		}
		logger.Info("Updated kubernetes client secret",
			"secretName", secretName,
			"secretNamespace", secretNamespace,
			"reasons", strings.Join(updateReasons, ", "))
	} else {
		logger.V(1).Info("Kubernetes Secret already up to date",
			"secretName", secretName,
			"secretNamespace", secretNamespace)
	}

	return nil
}

func (r *ClientReconciler) getSecretName(clientObj *keycloakv1alpha1.Client) string {
	if clientObj.Spec.Secret != nil && clientObj.Spec.Secret.SecretName != "" {
		return clientObj.Spec.Secret.SecretName
	}
	return fmt.Sprintf("%s-client-secret", clientObj.Name)
}

func (r *ClientReconciler) getSecretNamespace(clientObj *keycloakv1alpha1.Client) string {
	if clientObj.Spec.Secret != nil && clientObj.Spec.Secret.SecretNamespace != "" {
		return clientObj.Spec.Secret.SecretNamespace
	}
	return clientObj.Namespace
}

func (r *ClientReconciler) getSecretLabels(clientObj *keycloakv1alpha1.Client) map[string]string {
	labels := map[string]string{
		"app.kubernetes.io/name":       "keycloak-client",
		"app.kubernetes.io/instance":   clientObj.Name,
		"app.kubernetes.io/managed-by": "keycloak-operator",
		"app.kubernetes.io/version":    "v1alpha1",
		"app.kubernetes.io/component":  "client-secret",
		"app.kubernetes.io/part-of":    "keycloak",
	}

	// Add additional labels if specified
	if clientObj.Spec.Secret != nil && clientObj.Spec.Secret.AdditionalLabels != nil {
		maps.Copy(labels, clientObj.Spec.Secret.AdditionalLabels)
	}

	return labels
}

func (r *ClientReconciler) getSecretAnnotations(clientObj *keycloakv1alpha1.Client) map[string]string {
	annotations := map[string]string{
		"keycloak.org/client-name": clientObj.Name,
		"keycloak.org/realm":       clientObj.Spec.RealmRef.Name,
	}

	// Add additional annotations if specified
	if clientObj.Spec.Secret != nil && clientObj.Spec.Secret.AdditionalAnnotations != nil {
		maps.Copy(annotations, clientObj.Spec.Secret.AdditionalAnnotations)
	}

	return annotations
}

func (r *ClientReconciler) deleteClientSecret(ctx context.Context, clientObj *keycloakv1alpha1.Client) error {
	if clientObj.Status.Secret == nil || !clientObj.Status.Secret.SecretCreated {
		return nil
	}

	secret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      clientObj.Status.Secret.SecretName,
		Namespace: clientObj.Status.Secret.SecretNamespace,
	}, secret)

	if err != nil {
		if errors.IsNotFound(err) {
			return nil // Already deleted
		}
		return err
	}

	return r.Delete(ctx, secret)
}

func (r *ClientReconciler) updateSecretStatus(clientObj *keycloakv1alpha1.Client, created bool, secretName, secretNamespace string) {
	if clientObj.Status.Secret == nil {
		clientObj.Status.Secret = &keycloakv1alpha1.ClientSecretStatus{}
	}

	now := metav1.Now()
	clientObj.Status.Secret.SecretCreated = created
	clientObj.Status.Secret.SecretName = secretName
	clientObj.Status.Secret.SecretNamespace = secretNamespace
	clientObj.Status.Secret.LastSecretUpdate = &now
}
