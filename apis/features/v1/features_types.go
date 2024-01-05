package v1

import (
	conditionsv1 "github.com/openshift/custom-resource-status/conditions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// FeatureTracker represents a cluster-scoped resource in the Data Science Cluster,
// specifically designed for monitoring and managing objects created via the internal Features API.
// This resource serves a crucial role in cross-namespace resource management, acting as
// an owner reference for various resources. The primary purpose of the FeatureTracker
// is to enable efficient garbage collection by Kubernetes. This is essential for
// ensuring that resources are automatically cleaned up and reclaimed when they are
// no longer required.
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
type FeatureTracker struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              FeatureTrackerSpec   `json:"spec,omitempty"`
	Status            FeatureTrackerStatus `json:"status,omitempty"`
}

const (
	ConditionPhaseFeatureCreated   = "FeatureCreated"
	ConditionPhasePreConditions    = "FeaturePreConditions"
	ConditionPhaseResourceCreation = "ResourceCreation"
	ConditionPhaseLoadTemplateData = "LoadTemplateData"
	ConditionPhaseProcessTemplates = "ProcessTemplates"
	ConditionPhaseApplyManifests   = "ApplyManifests"
	ConditionPhasePostConditions   = "FeaturePostConditions"
	ComponentType                  = "Component"
	DSCIType                       = "DSCI"
)

func (s *FeatureTracker) ToOwnerReference() metav1.OwnerReference {
	return metav1.OwnerReference{
		APIVersion: s.APIVersion,
		Kind:       s.Kind,
		Name:       s.Name,
		UID:        s.UID,
	}
}

// Origin describes the type of object that created the related Feature to this FeatureTracker
type Origin struct {
	Type string
	Name string
}

// FeatureTrackerSpec defines the desired state of FeatureTracker.
type FeatureTrackerSpec struct {
	Origin       Origin
	AppNamespace string
}

// FeatureTrackerStatus defines the observed state of FeatureTracker.
type FeatureTrackerStatus struct {
	// +optional
	Conditions *[]conditionsv1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true

// FeatureTrackerList contains a list of FeatureTracker.
type FeatureTrackerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []FeatureTracker `json:"items"`
}

func init() {
	SchemeBuilder.Register(
		&FeatureTracker{},
		&FeatureTrackerList{},
	)
}
