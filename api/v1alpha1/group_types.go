package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type GroupSpec struct {
	// RealmRef specifies which realm this group belongs to
	// +kubebuilder:validation:Required
	RealmRef RealmReference `json:"realmRef"`

	// Name is the name of the group
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	Name string `json:"name"`

	// ParentGroupRef specifies the parent group if this is a subgroup
	// +optional
	ParentGroupRef *GroupReference `json:"parentGroupRef,omitempty"`

	// RealmRoles is a list of realm-level roles assigned to this group
	// +optional
	// +kubebuilder:default={}
	RealmRoles []string `json:"realmRoles"`

	// ClientRoles maps client names to lists of roles assigned to this group for those clients
	// +optional
	// +kubebuilder:default={}
	ClientRoles map[string][]string `json:"clientRoles"`

	// Attributes are custom attributes for the group
	// +optional
	// +kubebuilder:default={}
	Attributes map[string][]string `json:"attributes"`
}

// GroupReference defines a reference to a Group resource
type GroupReference struct {
	// Name is the name of the Group resource
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=255
	Name string `json:"name"`

	// Namespace is the namespace of the Group resource
	// If empty, the same namespace as the referencing Group will be used
	// +optional
	// +kubebuilder:validation:MaxLength=253
	Namespace string `json:"namespace,omitempty"`
}

// GroupStatus defines the observed state of Group
type GroupStatus struct {
	// Ready indicates if the group is ready
	Ready bool `json:"ready,omitempty"`

	// Message provides additional information about the current state
	Message string `json:"message,omitempty"`

	// LastSyncTime is the last time the group was synced
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// RealmReady indicates if the referenced realm is ready
	RealmReady bool `json:"realmReady,omitempty"`

	// Only relevant if ParentGroupRef is set
	ParentGroupUUID string `json:"parentGroupUUID,omitempty"`

	// ParentGroupReady indicates if the referenced parent group is ready
	// Only relevant if ParentGroupRef is set
	ParentGroupReady *bool `json:"parentGroupReady,omitempty"`

	// GroupUUID is the internal Keycloak UUID for this group
	// +optional
	GroupUUID string `json:"groupUUID,omitempty"`

	// RealmRoleStatuses tracks the status of realm role assignments
	// +optional
	RealmRoleStatuses map[string]RoleAssignmentStatus `json:"realmRoleStatuses,omitempty"`

	// ClientRoleStatuses tracks the status of client role assignments
	// +optional
	ClientRoleStatuses map[string]map[string]RoleAssignmentStatus `json:"clientRoleStatuses,omitempty"`
}

// RoleAssignmentStatus represents the status of a role assignment
type RoleAssignmentStatus struct {
	// Assigned indicates if the role is successfully assigned
	Assigned bool `json:"assigned"`

	// Message provides additional information about the assignment status
	// +optional
	Message string `json:"message,omitempty"`

	// RoleUUID is the Keycloak UUID of the role
	// +optional
	RoleUUID string `json:"roleUUID,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Name",type=string,JSONPath=`.spec.name`
// +kubebuilder:printcolumn:name="Realm",type=string,JSONPath=`.spec.realmRef.name`
// +kubebuilder:printcolumn:name="Path",type=string,JSONPath=`.status.computedPath`
// +kubebuilder:printcolumn:name="Enabled",type=boolean,JSONPath=`.spec.enabled`
// +kubebuilder:printcolumn:name="Ready",type=boolean,JSONPath=`.status.ready`
// +kubebuilder:printcolumn:name="Members",type=integer,JSONPath=`.status.memberCount`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Group is the Schema for the groups API
type Group struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   GroupSpec   `json:"spec,omitempty"`
	Status GroupStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// GroupList contains a list of Group
type GroupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Group `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Group{}, &GroupList{})
}
