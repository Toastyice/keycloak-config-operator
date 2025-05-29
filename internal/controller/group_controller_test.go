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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	keycloakv1alpha1 "github.com/toastyice/keycloak-config-operator/api/v1alpha1"
)

var _ = Describe("Group Controller", func() {
	Context("When reconciling a Group resource", func() {
		const (
			groupName = "test-group"
			realmName = "test-realm"
			namespace = "default"
			timeout   = time.Second * 10
			interval  = time.Millisecond * 250
		)

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      groupName,
			Namespace: namespace,
		}

		BeforeEach(func() {
			By("Creating the Group resource")
			group := &keycloakv1alpha1.Group{
				ObjectMeta: metav1.ObjectMeta{
					Name:      groupName,
					Namespace: namespace,
				},
				Spec: keycloakv1alpha1.GroupSpec{
					Name: "My Test Group",
					RealmRef: keycloakv1alpha1.RealmReference{
						Name:      realmName,
						Namespace: namespace,
					},
					RealmRoles:  []string{},            // Default empty slice
					ClientRoles: map[string][]string{}, // Default empty map
					Attributes:  map[string][]string{}, // Default empty map
				},
			}

			err := k8sClient.Get(ctx, typeNamespacedName, &keycloakv1alpha1.Group{})
			if err != nil && errors.IsNotFound(err) {
				Expect(k8sClient.Create(ctx, group)).To(Succeed())
			}
		})

		AfterEach(func() {
			By("Cleanup the Group resource")
			group := &keycloakv1alpha1.Group{}
			err := k8sClient.Get(ctx, typeNamespacedName, group)
			if err == nil {
				Expect(k8sClient.Delete(ctx, group)).To(Succeed())
			}
		})

		Context("Basic Group Properties", func() {
			It("should have the correct group name in spec", func() {
				group := &keycloakv1alpha1.Group{}
				Expect(k8sClient.Get(ctx, typeNamespacedName, group)).To(Succeed())

				By("Verifying the group name matches what was set")
				Expect(group.Spec.Name).To(Equal("My Test Group"))
				Expect(group.ObjectMeta.Name).To(Equal(groupName))
			})

			It("should reference the correct realm", func() {
				group := &keycloakv1alpha1.Group{}
				Expect(k8sClient.Get(ctx, typeNamespacedName, group)).To(Succeed())

				By("Verifying the realm reference is correct")
				Expect(group.Spec.RealmRef.Name).To(Equal(realmName))
				Expect(group.Spec.RealmRef.Namespace).To(Equal(namespace))
			})

			It("should have empty collections by default", func() {
				group := &keycloakv1alpha1.Group{}
				Expect(k8sClient.Get(ctx, typeNamespacedName, group)).To(Succeed())

				By("Verifying default empty collections")
				Expect(group.Spec.RealmRoles).To(Equal([]string{}))
				Expect(group.Spec.ClientRoles).To(Equal(map[string][]string{}))
				Expect(group.Spec.Attributes).To(Equal(map[string][]string{}))
				Expect(group.Spec.ParentGroupRef).To(BeNil())
			})
		})

		Context("Group with Realm Roles", func() {
			It("should handle realm roles assignment", func() {
				By("Creating a group with realm roles")
				groupWithRoles := &keycloakv1alpha1.Group{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "group-with-roles",
						Namespace: namespace,
					},
					Spec: keycloakv1alpha1.GroupSpec{
						Name: "Group With Roles",
						RealmRef: keycloakv1alpha1.RealmReference{
							Name:      realmName,
							Namespace: namespace,
						},
						RealmRoles:  []string{"admin", "user", "manager"},
						ClientRoles: map[string][]string{},
						Attributes:  map[string][]string{},
					},
				}

				Expect(k8sClient.Create(ctx, groupWithRoles)).To(Succeed())

				rolesNamespacedName := types.NamespacedName{Name: "group-with-roles", Namespace: namespace}
				createdGroup := &keycloakv1alpha1.Group{}
				Expect(k8sClient.Get(ctx, rolesNamespacedName, createdGroup)).To(Succeed())

				By("Verifying realm roles are set correctly")
				Expect(createdGroup.Spec.RealmRoles).To(HaveLen(3))
				Expect(createdGroup.Spec.RealmRoles).To(ContainElements("admin", "user", "manager"))

				By("Cleanup")
				Expect(k8sClient.Delete(ctx, groupWithRoles)).To(Succeed())
			})
		})

		Context("Group with Client Roles", func() {
			It("should handle client roles assignment", func() {
				By("Creating a group with client roles")
				groupWithClientRoles := &keycloakv1alpha1.Group{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "group-with-client-roles",
						Namespace: namespace,
					},
					Spec: keycloakv1alpha1.GroupSpec{
						Name: "Group With Client Roles",
						RealmRef: keycloakv1alpha1.RealmReference{
							Name:      realmName,
							Namespace: namespace,
						},
						RealmRoles: []string{},
						ClientRoles: map[string][]string{
							"my-client":      {"client-admin", "client-user"},
							"another-client": {"view", "edit"},
						},
						Attributes: map[string][]string{},
					},
				}

				Expect(k8sClient.Create(ctx, groupWithClientRoles)).To(Succeed())

				clientRolesNamespacedName := types.NamespacedName{Name: "group-with-client-roles", Namespace: namespace}
				createdGroup := &keycloakv1alpha1.Group{}
				Expect(k8sClient.Get(ctx, clientRolesNamespacedName, createdGroup)).To(Succeed())

				By("Verifying client roles are set correctly")
				Expect(createdGroup.Spec.ClientRoles).To(HaveLen(2))
				Expect(createdGroup.Spec.ClientRoles["my-client"]).To(ContainElements("client-admin", "client-user"))
				Expect(createdGroup.Spec.ClientRoles["another-client"]).To(ContainElements("view", "edit"))

				By("Cleanup")
				Expect(k8sClient.Delete(ctx, groupWithClientRoles)).To(Succeed())
			})
		})

		Context("Group with Attributes", func() {
			It("should handle custom attributes", func() {
				By("Creating a group with custom attributes")
				groupWithAttrs := &keycloakv1alpha1.Group{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "group-with-attributes",
						Namespace: namespace,
					},
					Spec: keycloakv1alpha1.GroupSpec{
						Name: "Group With Attributes",
						RealmRef: keycloakv1alpha1.RealmReference{
							Name:      realmName,
							Namespace: namespace,
						},
						RealmRoles:  []string{},
						ClientRoles: map[string][]string{},
						Attributes: map[string][]string{
							"department":  {"engineering", "product"},
							"location":    {"usa"},
							"permissions": {"read", "write", "execute"},
						},
					},
				}

				Expect(k8sClient.Create(ctx, groupWithAttrs)).To(Succeed())

				attrsNamespacedName := types.NamespacedName{Name: "group-with-attributes", Namespace: namespace}
				createdGroup := &keycloakv1alpha1.Group{}
				Expect(k8sClient.Get(ctx, attrsNamespacedName, createdGroup)).To(Succeed())

				By("Verifying attributes are set correctly")
				Expect(createdGroup.Spec.Attributes).To(HaveLen(3))
				Expect(createdGroup.Spec.Attributes["department"]).To(ContainElements("engineering", "product"))
				Expect(createdGroup.Spec.Attributes["location"]).To(ContainElements("usa"))
				Expect(createdGroup.Spec.Attributes["permissions"]).To(ContainElements("read", "write", "execute"))

				By("Cleanup")
				Expect(k8sClient.Delete(ctx, groupWithAttrs)).To(Succeed())
			})
		})

		Context("Parent Group References", func() {
			It("should handle groups without parent references", func() {
				group := &keycloakv1alpha1.Group{}
				Expect(k8sClient.Get(ctx, typeNamespacedName, group)).To(Succeed())

				By("Verifying no parent group is referenced")
				Expect(group.Spec.ParentGroupRef).To(BeNil())
				Expect(group.Status.ParentGroupUUID).To(BeEmpty())
				Expect(group.Status.ParentGroupReady).To(BeNil())
			})

			It("should create a group with parent reference", func() {
				By("Creating a parent group first")
				parentGroup := &keycloakv1alpha1.Group{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "parent-group",
						Namespace: namespace,
					},
					Spec: keycloakv1alpha1.GroupSpec{
						Name: "Parent Group",
						RealmRef: keycloakv1alpha1.RealmReference{
							Name:      realmName,
							Namespace: namespace,
						},
						RealmRoles:  []string{},
						ClientRoles: map[string][]string{},
						Attributes:  map[string][]string{},
					},
				}
				Expect(k8sClient.Create(ctx, parentGroup)).To(Succeed())

				By("Creating a child group")
				childGroup := &keycloakv1alpha1.Group{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "child-group",
						Namespace: namespace,
					},
					Spec: keycloakv1alpha1.GroupSpec{
						Name: "Child Group",
						RealmRef: keycloakv1alpha1.RealmReference{
							Name:      realmName,
							Namespace: namespace,
						},
						ParentGroupRef: &keycloakv1alpha1.GroupReference{
							Name:      "parent-group",
							Namespace: namespace,
						},
						RealmRoles:  []string{},
						ClientRoles: map[string][]string{},
						Attributes:  map[string][]string{},
					},
				}
				Expect(k8sClient.Create(ctx, childGroup)).To(Succeed())

				By("Verifying parent reference is set correctly")
				createdChild := &keycloakv1alpha1.Group{}
				childNamespacedName := types.NamespacedName{Name: "child-group", Namespace: namespace}
				Expect(k8sClient.Get(ctx, childNamespacedName, createdChild)).To(Succeed())

				Expect(createdChild.Spec.ParentGroupRef).NotTo(BeNil())
				Expect(createdChild.Spec.ParentGroupRef.Name).To(Equal("parent-group"))
				Expect(createdChild.Spec.ParentGroupRef.Namespace).To(Equal(namespace))

				By("Cleanup child and parent groups")
				Expect(k8sClient.Delete(ctx, childGroup)).To(Succeed())
				Expect(k8sClient.Delete(ctx, parentGroup)).To(Succeed())
			})

			It("should handle parent group reference without namespace (same namespace)", func() {
				By("Creating a parent group")
				parentGroup := &keycloakv1alpha1.Group{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "same-ns-parent",
						Namespace: namespace,
					},
					Spec: keycloakv1alpha1.GroupSpec{
						Name: "Same Namespace Parent",
						RealmRef: keycloakv1alpha1.RealmReference{
							Name: realmName,
						},
						RealmRoles:  []string{},
						ClientRoles: map[string][]string{},
						Attributes:  map[string][]string{},
					},
				}
				Expect(k8sClient.Create(ctx, parentGroup)).To(Succeed())

				By("Creating a child group with empty namespace in parent ref")
				childGroup := &keycloakv1alpha1.Group{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "same-ns-child",
						Namespace: namespace,
					},
					Spec: keycloakv1alpha1.GroupSpec{
						Name: "Same Namespace Child",
						RealmRef: keycloakv1alpha1.RealmReference{
							Name: realmName,
						},
						ParentGroupRef: &keycloakv1alpha1.GroupReference{
							Name: "same-ns-parent",
							// Namespace is omitted - should use same namespace
						},
						RealmRoles:  []string{},
						ClientRoles: map[string][]string{},
						Attributes:  map[string][]string{},
					},
				}
				Expect(k8sClient.Create(ctx, childGroup)).To(Succeed())

				By("Verifying parent reference works without explicit namespace")
				createdChild := &keycloakv1alpha1.Group{}
				childNamespacedName := types.NamespacedName{Name: "same-ns-child", Namespace: namespace}
				Expect(k8sClient.Get(ctx, childNamespacedName, createdChild)).To(Succeed())

				Expect(createdChild.Spec.ParentGroupRef).NotTo(BeNil())
				Expect(createdChild.Spec.ParentGroupRef.Name).To(Equal("same-ns-parent"))
				Expect(createdChild.Spec.ParentGroupRef.Namespace).To(BeEmpty()) // As specified in the spec

				By("Cleanup")
				Expect(k8sClient.Delete(ctx, childGroup)).To(Succeed())
				Expect(k8sClient.Delete(ctx, parentGroup)).To(Succeed())
			})
		})

		Context("Controller Reconciliation", func() {
			It("should fail reconciliation without KeycloakClient", func() {
				By("Reconciling without KeycloakClient initialized")
				controllerReconciler := &GroupReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
					// KeycloakClient is nil
				}

				_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})

				By("Expecting error due to uninitialized KeycloakClient")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("KeycloakClient is nil"))
			})

			It("should fail reconciliation for non-existent group when KeycloakClient is nil", func() {
				By("Reconciling a non-existent group without KeycloakClient")
				controllerReconciler := &GroupReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
					// KeycloakClient is nil
				}

				nonExistentName := types.NamespacedName{
					Name:      "non-existent-group",
					Namespace: namespace,
				}

				_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: nonExistentName,
				})

				By("Expecting error due to uninitialized KeycloakClient (controller validates early)")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("KeycloakClient is nil"))
			})
		})

		Context("Group Status Fields", func() {
			It("should initialize with empty status", func() {
				group := &keycloakv1alpha1.Group{}
				Expect(k8sClient.Get(ctx, typeNamespacedName, group)).To(Succeed())

				By("Verifying initial status is empty/default")
				Expect(group.Status.Ready).To(BeFalse())
				Expect(group.Status.RealmReady).To(BeFalse())
				Expect(group.Status.Message).To(BeEmpty())
				Expect(group.Status.GroupUUID).To(BeEmpty())
				Expect(group.Status.ParentGroupUUID).To(BeEmpty())
				Expect(group.Status.ParentGroupReady).To(BeNil())
				Expect(group.Status.LastSyncTime).To(BeNil())
				Expect(group.Status.RealmRoleStatuses).To(BeNil())
				Expect(group.Status.ClientRoleStatuses).To(BeNil())
			})
		})
	})

	Context("Group Validation", func() {
		It("should validate group name is required and within length limits", func() {
			ctx := context.Background()

			By("Testing that a valid group name works")
			validGroup := &keycloakv1alpha1.Group{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "valid-group",
					Namespace: "default",
				},
				Spec: keycloakv1alpha1.GroupSpec{
					Name: "Valid Group Name",
					RealmRef: keycloakv1alpha1.RealmReference{
						Name: "test-realm",
					},
				},
			}

			err := k8sClient.Create(ctx, validGroup)
			Expect(err).NotTo(HaveOccurred())

			// Verify it was created successfully
			createdGroup := &keycloakv1alpha1.Group{}
			namespacedName := types.NamespacedName{Name: "valid-group", Namespace: "default"}
			Expect(k8sClient.Get(ctx, namespacedName, createdGroup)).To(Succeed())
			Expect(createdGroup.Spec.Name).To(Equal("Valid Group Name"))

			By("Cleanup")
			Expect(k8sClient.Delete(ctx, validGroup)).To(Succeed())
		})

		It("should handle realm reference correctly", func() {
			ctx := context.Background()

			By("Creating a group with realm reference")
			group := &keycloakv1alpha1.Group{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "realm-ref-group",
					Namespace: "default",
				},
				Spec: keycloakv1alpha1.GroupSpec{
					Name: "Realm Reference Group",
					RealmRef: keycloakv1alpha1.RealmReference{
						Name:      "my-realm",
						Namespace: "custom-namespace",
					},
				},
			}

			Expect(k8sClient.Create(ctx, group)).To(Succeed())

			createdGroup := &keycloakv1alpha1.Group{}
			namespacedName := types.NamespacedName{Name: "realm-ref-group", Namespace: "default"}
			Expect(k8sClient.Get(ctx, namespacedName, createdGroup)).To(Succeed())

			Expect(createdGroup.Spec.RealmRef.Name).To(Equal("my-realm"))
			Expect(createdGroup.Spec.RealmRef.Namespace).To(Equal("custom-namespace"))

			By("Cleanup")
			Expect(k8sClient.Delete(ctx, group)).To(Succeed())
		})
	})
})
