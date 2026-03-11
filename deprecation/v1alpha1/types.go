package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DeprecationPhase is the lifecycle phase of a deprecated feature.
// +kubebuilder:validation:Enum=Deprecated;Removed
type DeprecationPhase string

const (
	// PhaseDeprecated indicates the feature still works but operators should migrate.
	PhaseDeprecated DeprecationPhase = "Deprecated"

	// PhaseRemoved indicates the feature is no longer available.
	PhaseRemoved DeprecationPhase = "Removed"
)

const (
	// ConditionRemoved is True when the current cluster version >= removedInVersion.
	ConditionRemoved = "Removed"

	// ConditionMigrationDocumented is True when replacedBy or migrationGuide is set.
	ConditionMigrationDocumented = "MigrationDocumented"
)

// FeatureRef identifies the feature being deprecated.
type FeatureRef struct {
	// Name is the human-readable name of the deprecated feature.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	Name string `json:"name"`

	// Description provides longer context about the feature.
	// +optional
	// +kubebuilder:validation:MaxLength=2048
	Description string `json:"description,omitempty"`

	// Component is the platform component that owns this feature.
	// +optional
	// +kubebuilder:validation:MaxLength=128
	Component string `json:"component,omitempty"`
}

// FeatureDeprecationSpec defines the desired state of FeatureDeprecation.
type FeatureDeprecationSpec struct {
	// Feature identifies the feature being deprecated.
	Feature FeatureRef `json:"feature"`

	// Phase is the current lifecycle phase of the feature.
	// +kubebuilder:validation:Enum=Deprecated;Removed
	Phase DeprecationPhase `json:"phase"`

	// DeprecatedInVersion is the platform version when deprecation was announced.
	// +kubebuilder:validation:Pattern=`^\d+\.\d+$`
	DeprecatedInVersion string `json:"deprecatedInVersion"`

	// RemovedInVersion is the platform version when the feature is/was removed.
	// +kubebuilder:validation:Pattern=`^\d+\.\d+$`
	RemovedInVersion string `json:"removedInVersion"`

	// PlannedEarliestRemoval is the expected GA date of the release identified by
	// removedInVersion, formatted as YYYY-MM-DD. This is the earliest date on which
	// the feature will no longer be available on newly installed clusters.
	// +optional
	// +kubebuilder:validation:Pattern=`^\d{4}-\d{2}-\d{2}$`
	PlannedEarliestRemoval string `json:"plannedEarliestRemoval,omitempty"`

	// PlannedEndOfLife is the end-of-life date of the last platform version that
	// still includes this feature, formatted as YYYY-MM-DD. Existing clusters
	// running that version will continue to have access to the feature until this
	// date. This may be significantly later than plannedEarliestRemoval — for
	// example, if 6.0 GA is Q4 2029 but 5.8 is supported until Q2 2034.
	// +optional
	// +kubebuilder:validation:Pattern=`^\d{4}-\d{2}-\d{2}$`
	PlannedEndOfLife string `json:"plannedEndOfLife,omitempty"`

	// Reason explains why this feature is being deprecated.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=2048
	Reason string `json:"reason"`

	// ReplacedBy names the feature or API that supersedes this one.
	// +optional
	// +kubebuilder:validation:MaxLength=512
	ReplacedBy string `json:"replacedBy,omitempty"`

	// MigrationGuide is a URL pointing to migration documentation.
	// +optional
	// +kubebuilder:validation:MaxLength=2048
	MigrationGuide string `json:"migrationGuide,omitempty"`

	// AdditionalInfo holds supplemental information not covered by other fields.
	// +optional
	// +kubebuilder:validation:MaxLength=4096
	AdditionalInfo string `json:"additionalInfo,omitempty"`
}

// FeatureDeprecationStatus defines the observed state of FeatureDeprecation.
type FeatureDeprecationStatus struct {
	// ObservedGeneration is the last processed spec generation.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions describe the current state of this deprecation record.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// FeatureDeprecation records a platform feature that has been deprecated.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=fd
// +kubebuilder:printcolumn:name="Feature",type=string,JSONPath=`.spec.feature.name`
// +kubebuilder:printcolumn:name="Component",type=string,JSONPath=`.spec.feature.component`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.spec.phase`
// +kubebuilder:printcolumn:name="Deprecated-In",type=string,JSONPath=`.spec.deprecatedInVersion`
// +kubebuilder:printcolumn:name="Removed-In",type=string,JSONPath=`.spec.removedInVersion`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type FeatureDeprecation struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   FeatureDeprecationSpec   `json:"spec,omitempty"`
	Status FeatureDeprecationStatus `json:"status,omitempty"`
}

// FeatureDeprecationList contains a list of FeatureDeprecation.
//
// +kubebuilder:object:root=true
type FeatureDeprecationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []FeatureDeprecation `json:"items"`
}

func init() {
	SchemeBuilder.Register(&FeatureDeprecation{}, &FeatureDeprecationList{})
}
