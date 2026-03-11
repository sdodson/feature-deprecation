package controller

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
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

	return ctrl.Result{}, r.Status().Patch(ctx, fd, client.MergeFrom(base))
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
