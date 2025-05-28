package v1alpha1

// RealmReference defines a reference to a Realm resource
type RealmReference struct {
	// Name is the name of the Realm resource
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Namespace is the namespace of the Realm resource
	// If empty, the same namespace as the Client will be used
	// +optional
	Namespace string `json:"namespace,omitempty"`
}
