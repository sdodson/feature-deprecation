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

	// ConditionFeatureInUse is True when the featureInUseMatchingRule indicates the
	// feature is actively used by the cluster. Not set when featureInUseMatchingRule
	// is omitted.
	ConditionFeatureInUse = "FeatureInUse"
)

// FeatureInUseRuleType identifies which matching strategy is used to detect
// whether a cluster is actively using a deprecated feature.
// +kubebuilder:validation:Enum=PromQL;ResourceJSONPath
type FeatureInUseRuleType string

const (
	// FeatureInUseRuleTypePromQL evaluates a PromQL expression against the
	// cluster's Prometheus. Non-empty results with non-zero values indicate
	// the feature is in use.
	FeatureInUseRuleTypePromQL FeatureInUseRuleType = "PromQL"

	// FeatureInUseRuleTypeResourceJSONPath fetches a Kubernetes resource and
	// checks whether a JSONPath expression resolves to an expected value.
	FeatureInUseRuleTypeResourceJSONPath FeatureInUseRuleType = "ResourceJSONPath"
)

// PromQLRule evaluates a PromQL expression against the cluster's Prometheus
// (typically via the thanos-querier service in openshift-monitoring).
type PromQLRule struct {
	// Query is a PromQL expression to evaluate. The feature is considered in use
	// when the query returns one or more results with non-zero values.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=2048
	Query string `json:"query"`
}

// ResourceJSONPathRule fetches a Kubernetes resource and checks whether a
// JSONPath expression resolves to an expected value.
type ResourceJSONPathRule struct {
	// Group is the API group of the resource. Use an empty string for the core group.
	// +optional
	Group string `json:"group,omitempty"`

	// Version is the API version of the resource (e.g. "v1", "config.openshift.io/v1").
	// +kubebuilder:validation:MinLength=1
	Version string `json:"version"`

	// Resource is the plural resource name (e.g. "clusterversions", "nodes").
	// +kubebuilder:validation:MinLength=1
	Resource string `json:"resource"`

	// Name is the name of the specific resource instance to fetch
	// (e.g. "version" for the singleton ClusterVersion).
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Namespace is the namespace of the resource. Leave empty for cluster-scoped resources.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// JSONPath is a dot-notation path into the fetched resource
	// (e.g. ".spec.channel", ".status.desired.version").
	// Curly braces are optional; the controller adds them if absent.
	// +kubebuilder:validation:MinLength=1
	JSONPath string `json:"jsonPath"`

	// ExpectedValue is the string value the JSONPath result must equal for
	// the feature to be considered in use.
	// +kubebuilder:validation:MinLength=1
	ExpectedValue string `json:"expectedValue"`
}

// FeatureInUseMatchingRule is a discriminated union describing how to detect
// whether a cluster is actively using the deprecated feature. Exactly one of
// promQL or resourceJSONPath must be set, corresponding to the value of type.
//
// +kubebuilder:validation:XValidation:rule="self.type == 'PromQL' ? has(self.promQL) && !has(self.resourceJSONPath) : true",message="promQL must be set and resourceJSONPath must not be set when type is PromQL"
// +kubebuilder:validation:XValidation:rule="self.type == 'ResourceJSONPath' ? has(self.resourceJSONPath) && !has(self.promQL) : true",message="resourceJSONPath must be set and promQL must not be set when type is ResourceJSONPath"
type FeatureInUseMatchingRule struct {
	// Type identifies which matching strategy is used.
	// +kubebuilder:validation:Enum=PromQL;ResourceJSONPath
	Type FeatureInUseRuleType `json:"type"`

	// PromQL configures a Prometheus query to detect feature usage.
	// Required when type is PromQL; must be omitted otherwise.
	// +optional
	PromQL *PromQLRule `json:"promQL,omitempty"`

	// ResourceJSONPath configures a Kubernetes resource field lookup to detect
	// feature usage. Required when type is ResourceJSONPath; must be omitted otherwise.
	// +optional
	ResourceJSONPath *ResourceJSONPathRule `json:"resourceJSONPath,omitempty"`
}

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

	// FeatureInUseMatchingRule describes how to detect whether the cluster is
	// actively using this deprecated feature. When set, the controller evaluates
	// the rule on every reconcile and reflects the result in the FeatureInUse
	// status condition. When omitted, the FeatureInUse condition is not set.
	// +optional
	FeatureInUseMatchingRule *FeatureInUseMatchingRule `json:"featureInUseMatchingRule,omitempty"`
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
