package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/util/jsonpath"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	deprecationv1alpha1 "github.com/openshift/api/deprecation/v1alpha1"
)

var clusterVersionGVK = schema.GroupVersionKind{
	Group:   "config.openshift.io",
	Version: "v1",
	Kind:    "ClusterVersion",
}

// FeatureDeprecationReconciler reconciles FeatureDeprecation objects.
//
// +kubebuilder:rbac:groups=deprecation.openshift.io,resources=featuredeprecations,verbs=get;list;watch
// +kubebuilder:rbac:groups=deprecation.openshift.io,resources=featuredeprecations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=deprecation.openshift.io,resources=featuredeprecations/finalizers,verbs=update
// +kubebuilder:rbac:groups=config.openshift.io,resources=clusterversions,verbs=get;list;watch
type FeatureDeprecationReconciler struct {
	client.Client

	// DynamicClient is used to fetch arbitrary resources for ResourceJSONPath rules.
	// Additional RBAC must be granted to the controller's service account for each
	// resource type referenced by a ResourceJSONPath rule.
	DynamicClient dynamic.Interface

	// PrometheusURL is the base URL of the Prometheus-compatible API used for
	// PromQL rules (e.g. "https://thanos-querier.openshift-monitoring.svc:9091").
	PrometheusURL string

	// HTTPClient is used for PromQL rule evaluation. It must be configured with
	// the appropriate TLS settings and bearer-token transport for the cluster.
	HTTPClient *http.Client
}

func (r *FeatureDeprecationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	fd := &deprecationv1alpha1.FeatureDeprecation{}
	if err := r.Get(ctx, req.NamespacedName, fd); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Snapshot for status patch.
	base := fd.DeepCopy()
	fd.Status.ObservedGeneration = fd.Generation

	// Fetch ClusterVersion via unstructured client (no typed openshift/api import).
	cv := &unstructured.Unstructured{}
	cv.SetGroupVersionKind(clusterVersionGVK)
	if err := r.Get(ctx, types.NamespacedName{Name: "version"}, cv); err != nil {
		logger.Error(err, "Unable to get ClusterVersion")
		apimeta.SetStatusCondition(&fd.Status.Conditions, metav1.Condition{
			Type:               deprecationv1alpha1.ConditionRemoved,
			Status:             metav1.ConditionUnknown,
			Reason:             "ClusterVersionUnavailable",
			Message:            fmt.Sprintf("Unable to retrieve ClusterVersion: %v", err),
			ObservedGeneration: fd.Generation,
		})
		return ctrl.Result{}, r.Status().Patch(ctx, fd, client.MergeFrom(base))
	}

	desiredVersion, found, err := unstructured.NestedString(cv.Object, "status", "desired", "version")
	if err != nil || !found || desiredVersion == "" {
		apimeta.SetStatusCondition(&fd.Status.Conditions, metav1.Condition{
			Type:               deprecationv1alpha1.ConditionRemoved,
			Status:             metav1.ConditionUnknown,
			Reason:             "ClusterVersionParseFailed",
			Message:            "Unable to extract desired version from ClusterVersion status",
			ObservedGeneration: fd.Generation,
		})
		return ctrl.Result{}, r.Status().Patch(ctx, fd, client.MergeFrom(base))
	}

	clusterMajor, clusterMinor, err := parseMajorMinor(desiredVersion)
	if err != nil {
		apimeta.SetStatusCondition(&fd.Status.Conditions, metav1.Condition{
			Type:               deprecationv1alpha1.ConditionRemoved,
			Status:             metav1.ConditionUnknown,
			Reason:             "ClusterVersionParseFailed",
			Message:            fmt.Sprintf("Unable to parse cluster version %q: %v", desiredVersion, err),
			ObservedGeneration: fd.Generation,
		})
		return ctrl.Result{}, r.Status().Patch(ctx, fd, client.MergeFrom(base))
	}

	removedMajor, removedMinor, err := parseMajorMinor(fd.Spec.RemovedInVersion)
	if err != nil {
		// CEL validation prevents malformed removedInVersion in practice.
		return ctrl.Result{}, fmt.Errorf("parsing removedInVersion %q: %w", fd.Spec.RemovedInVersion, err)
	}

	if versionGTE(clusterMajor, clusterMinor, removedMajor, removedMinor) {
		apimeta.SetStatusCondition(&fd.Status.Conditions, metav1.Condition{
			Type:               deprecationv1alpha1.ConditionRemoved,
			Status:             metav1.ConditionTrue,
			Reason:             "ClusterVersionReachedRemoval",
			Message:            fmt.Sprintf("Cluster version %s has reached or passed the removal version %s", desiredVersion, fd.Spec.RemovedInVersion),
			ObservedGeneration: fd.Generation,
		})
	} else {
		apimeta.SetStatusCondition(&fd.Status.Conditions, metav1.Condition{
			Type:               deprecationv1alpha1.ConditionRemoved,
			Status:             metav1.ConditionFalse,
			Reason:             "ClusterVersionBelowRemoval",
			Message:            fmt.Sprintf("Cluster version %s is below the removal version %s", desiredVersion, fd.Spec.RemovedInVersion),
			ObservedGeneration: fd.Generation,
		})
	}

	if fd.Spec.ReplacedBy != "" || fd.Spec.MigrationGuide != "" {
		apimeta.SetStatusCondition(&fd.Status.Conditions, metav1.Condition{
			Type:               deprecationv1alpha1.ConditionMigrationDocumented,
			Status:             metav1.ConditionTrue,
			Reason:             "MigrationPathDocumented",
			Message:            "Migration path is documented via replacedBy or migrationGuide",
			ObservedGeneration: fd.Generation,
		})
	} else {
		apimeta.SetStatusCondition(&fd.Status.Conditions, metav1.Condition{
			Type:               deprecationv1alpha1.ConditionMigrationDocumented,
			Status:             metav1.ConditionFalse,
			Reason:             "NoMigrationPath",
			Message:            "Neither replacedBy nor migrationGuide is set",
			ObservedGeneration: fd.Generation,
		})
	}

	if fd.Spec.FeatureInUseMatchingRule != nil {
		cond := r.evaluateFeatureInUse(ctx, fd)
		apimeta.SetStatusCondition(&fd.Status.Conditions, cond)
	}

	return ctrl.Result{}, r.Status().Patch(ctx, fd, client.MergeFrom(base))
}

// evaluateFeatureInUse dispatches to the appropriate rule evaluator and returns
// the resulting FeatureInUse condition.
func (r *FeatureDeprecationReconciler) evaluateFeatureInUse(ctx context.Context, fd *deprecationv1alpha1.FeatureDeprecation) metav1.Condition {
	rule := fd.Spec.FeatureInUseMatchingRule
	base := metav1.Condition{
		Type:               deprecationv1alpha1.ConditionFeatureInUse,
		ObservedGeneration: fd.Generation,
	}
	switch rule.Type {
	case deprecationv1alpha1.FeatureInUseRuleTypePromQL:
		return r.evaluatePromQL(ctx, rule.PromQL, base)
	case deprecationv1alpha1.FeatureInUseRuleTypeResourceJSONPath:
		return r.evaluateResourceJSONPath(ctx, rule.ResourceJSONPath, base)
	default:
		base.Status = metav1.ConditionUnknown
		base.Reason = "UnknownRuleType"
		base.Message = fmt.Sprintf("Unrecognized featureInUseMatchingRule type %q", rule.Type)
		return base
	}
}

// evaluatePromQL queries Prometheus with the configured PromQL expression and
// returns True when at least one result has a non-zero value.
func (r *FeatureDeprecationReconciler) evaluatePromQL(ctx context.Context, rule *deprecationv1alpha1.PromQLRule, base metav1.Condition) metav1.Condition {
	queryURL := r.PrometheusURL + "/api/v1/query"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, queryURL, nil)
	if err != nil {
		base.Status = metav1.ConditionUnknown
		base.Reason = "PromQLRequestFailed"
		base.Message = fmt.Sprintf("Failed to build Prometheus request: %v", err)
		return base
	}
	q := req.URL.Query()
	q.Set("query", rule.Query)
	req.URL.RawQuery = q.Encode()

	resp, err := r.HTTPClient.Do(req)
	if err != nil {
		base.Status = metav1.ConditionUnknown
		base.Reason = "PromQLRequestFailed"
		base.Message = fmt.Sprintf("Failed to query Prometheus: %v", err)
		return base
	}
	defer resp.Body.Close()

	// Prometheus HTTP API response envelope.
	var envelope struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				// Value is [unixTimestamp, "stringValue"].
				Value []json.RawMessage `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		base.Status = metav1.ConditionUnknown
		base.Reason = "PromQLParseFailed"
		base.Message = fmt.Sprintf("Failed to decode Prometheus response: %v", err)
		return base
	}
	if envelope.Status != "success" {
		base.Status = metav1.ConditionUnknown
		base.Reason = "PromQLQueryFailed"
		base.Message = fmt.Sprintf("Prometheus returned non-success status %q for query %q", envelope.Status, rule.Query)
		return base
	}

	for _, result := range envelope.Data.Result {
		if len(result.Value) < 2 {
			continue
		}
		var valueStr string
		if err := json.Unmarshal(result.Value[1], &valueStr); err != nil {
			continue
		}
		if valueStr != "0" && valueStr != "0.0" {
			base.Status = metav1.ConditionTrue
			base.Reason = "PromQLMatchFound"
			base.Message = "Prometheus query returned non-zero results indicating the feature is in use"
			return base
		}
	}

	base.Status = metav1.ConditionFalse
	base.Reason = "PromQLNoMatch"
	base.Message = "Prometheus query returned no non-zero results; feature does not appear to be in use"
	return base
}

// evaluateResourceJSONPath fetches the specified resource and checks whether
// the JSONPath expression resolves to the expected value.
func (r *FeatureDeprecationReconciler) evaluateResourceJSONPath(ctx context.Context, rule *deprecationv1alpha1.ResourceJSONPathRule, base metav1.Condition) metav1.Condition {
	gvr := schema.GroupVersionResource{
		Group:    rule.Group,
		Version:  rule.Version,
		Resource: rule.Resource,
	}

	var obj *unstructured.Unstructured
	var err error
	if rule.Namespace != "" {
		obj, err = r.DynamicClient.Resource(gvr).Namespace(rule.Namespace).Get(ctx, rule.Name, metav1.GetOptions{})
	} else {
		obj, err = r.DynamicClient.Resource(gvr).Get(ctx, rule.Name, metav1.GetOptions{})
	}
	if err != nil {
		if apierrors.IsNotFound(err) {
			base.Status = metav1.ConditionFalse
			base.Reason = "ResourceNotFound"
			base.Message = fmt.Sprintf("Resource %s/%s not found; feature does not appear to be in use", rule.Resource, rule.Name)
			return base
		}
		base.Status = metav1.ConditionUnknown
		base.Reason = "ResourceFetchFailed"
		base.Message = fmt.Sprintf("Failed to fetch %s/%s: %v", rule.Resource, rule.Name, err)
		return base
	}

	// Wrap the expression in curly braces if the caller omitted them.
	expr := rule.JSONPath
	if !strings.HasPrefix(expr, "{") {
		expr = "{" + expr + "}"
	}

	j := jsonpath.New("feature-in-use")
	if err := j.Parse(expr); err != nil {
		base.Status = metav1.ConditionUnknown
		base.Reason = "JSONPathParseFailed"
		base.Message = fmt.Sprintf("Failed to parse JSONPath %q: %v", rule.JSONPath, err)
		return base
	}

	var buf bytes.Buffer
	if err := j.Execute(&buf, obj.Object); err != nil {
		base.Status = metav1.ConditionUnknown
		base.Reason = "JSONPathEvalFailed"
		base.Message = fmt.Sprintf("Failed to evaluate JSONPath %q on %s/%s: %v", rule.JSONPath, rule.Resource, rule.Name, err)
		return base
	}

	actual := buf.String()
	if actual == rule.ExpectedValue {
		base.Status = metav1.ConditionTrue
		base.Reason = "JSONPathMatched"
		base.Message = fmt.Sprintf("JSONPath %q on %s/%s matches expected value", rule.JSONPath, rule.Resource, rule.Name)
		return base
	}

	base.Status = metav1.ConditionFalse
	base.Reason = "JSONPathNoMatch"
	base.Message = fmt.Sprintf("JSONPath %q on %s/%s is %q; expected %q", rule.JSONPath, rule.Resource, rule.Name, actual, rule.ExpectedValue)
	return base
}

// SetupWithManager sets up the controller with the Manager, including a watch
// on ClusterVersion that fans out to all FeatureDeprecation objects.
func (r *FeatureDeprecationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	cvProto := &unstructured.Unstructured{}
	cvProto.SetGroupVersionKind(clusterVersionGVK)

	return ctrl.NewControllerManagedBy(mgr).
		For(&deprecationv1alpha1.FeatureDeprecation{}).
		Watches(cvProto,
			handler.EnqueueRequestsFromMapFunc(r.clusterVersionToAllFDs),
			builder.WithPredicates(predicate.NewPredicateFuncs(
				func(obj client.Object) bool { return obj.GetName() == "version" },
			)),
		).
		Complete(r)
}

// clusterVersionToAllFDs maps a ClusterVersion event to reconcile requests for
// every FeatureDeprecation in the cluster.
func (r *FeatureDeprecationReconciler) clusterVersionToAllFDs(ctx context.Context, _ client.Object) []reconcile.Request {
	var fdList deprecationv1alpha1.FeatureDeprecationList
	if err := r.List(ctx, &fdList); err != nil {
		return nil
	}
	reqs := make([]reconcile.Request, len(fdList.Items))
	for i, fd := range fdList.Items {
		reqs[i] = reconcile.Request{
			NamespacedName: types.NamespacedName{Name: fd.Name},
		}
	}
	return reqs
}

// parseMajorMinor parses a version string into (major, minor) integers.
// It handles "6.0", "6.0.1", and pre-release forms like "6.0.1-rc.2" or "6.0.1+build".
func parseMajorMinor(version string) (int, int, error) {
	// Strip pre-release (-) and build metadata (+) suffixes.
	if i := strings.IndexAny(version, "-+"); i != -1 {
		version = version[:i]
	}
	parts := strings.SplitN(version, ".", 3)
	if len(parts) < 2 {
		return 0, 0, fmt.Errorf("version %q: expected at least major.minor", version)
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("version %q: invalid major: %w", version, err)
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("version %q: invalid minor: %w", version, err)
	}
	return major, minor, nil
}

// versionGTE returns true when (clusterMajor, clusterMinor) >= (removedMajor, removedMinor).
func versionGTE(clusterMajor, clusterMinor, removedMajor, removedMinor int) bool {
	if clusterMajor != removedMajor {
		return clusterMajor > removedMajor
	}
	return clusterMinor >= removedMinor
}
