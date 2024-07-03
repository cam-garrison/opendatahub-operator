package feature

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/hashicorp/go-multierror"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	featurev1 "github.com/opendatahub-io/opendatahub-operator/v2/apis/features/v1"
	"github.com/opendatahub-io/opendatahub-operator/v2/controllers/status"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/feature/builder"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/metadata/annotations"
)

// Feature is a high-level abstraction that represents a collection of resources and actions
// that are applied to the cluster to enable a specific feature.
//
// Features can be either managed or unmanaged. Managed features are reconciled to their
// desired state based on defined manifests.
//
// In addition to creating resources using manifest files or through Golang functions, a Feature
// allows defining preconditions and postconditions. These conditions are checked to ensure
// the cluster is in the desired state for the feature to be applied successfully.
//
// When a Feature is applied, an associated resource called FeatureTracker is created. This
// resource establishes ownership for related resources, allowing for easy cleanup of all resources
// associated with the feature when it is about to be removed during reconciliation.
//
// Each Feature can have a list of cleanup functions. These functions can be particularly useful
// when the cleanup involves actions other than the removal of resources, such as reverting a patch operation.
//
// To create a Feature, use the provided FeatureBuilder. This builder guides through the process
// using a fluent API.
type Feature struct {
	Name            string
	TargetNamespace string
	Enabled         EnabledFunc
	Managed         bool

	Client client.Client
	Log    logr.Logger

	tracker *featurev1.FeatureTracker
	source  *featurev1.Source

	data map[string]any

	resources []builder.ResourceApplier

	cleanups          []Action
	clusterOperations []Action
	preconditions     []Action
	postconditions    []Action
	dataProviders     []Action
}

// Action is a func type which can be used for different purposes while having access to Feature struct.
type Action func(ctx context.Context, f *Feature) error

// EnabledFunc is a func type used to determine if a feature should be enabled.
type EnabledFunc func(ctx context.Context, feature *Feature) (bool, error)

func (f *Feature) Apply(ctx context.Context) error {
	// If the feature is disabled, but the FeatureTracker exists in the cluster, ensure clean-up is triggered.
	// This means that the feature was previously enabled, but now it is not anymore.
	if enabled, err := f.Enabled(ctx, f); !enabled || err != nil {
		if err != nil {
			return err
		}

		return f.Cleanup(ctx)
	}

	if trackerErr := createFeatureTracker(ctx, f); trackerErr != nil {
		return trackerErr
	}

	if _, updateErr := status.UpdateWithRetry(ctx, f.Client, f.tracker, func(saved *featurev1.FeatureTracker) {
		status.SetProgressingCondition(&saved.Status.Conditions, string(featurev1.ConditionReason.FeatureCreated), fmt.Sprintf("Applying feature [%s]", f.Name))
		saved.Status.Phase = status.PhaseProgressing
	}); updateErr != nil {
		return updateErr
	}

	applyErr := f.applyFeature(ctx)
	_, reportErr := createFeatureTrackerStatusReporter(f).ReportCondition(ctx, applyErr)

	return multierror.Append(applyErr, reportErr).ErrorOrNil()
}

// Cleanup removes all resources associated with the feature and invokes any cleanup functions defined as part of the Feature.
func (f *Feature) Cleanup(ctx context.Context) error {
	// Ensure associated FeatureTracker instance has been removed as last one
	// in the chain of cleanups.
	f.addCleanup(removeFeatureTracker)

	var cleanupErrors *multierror.Error
	for _, dataProvider := range f.dataProviders {
		cleanupErrors = multierror.Append(cleanupErrors, dataProvider(ctx, f))
	}

	if dataLoadErr := cleanupErrors.ErrorOrNil(); dataLoadErr != nil {
		return dataLoadErr
	}

	for _, cleanupFunc := range f.cleanups {
		cleanupErrors = multierror.Append(cleanupErrors, cleanupFunc(ctx, f))
	}

	return cleanupErrors.ErrorOrNil()
}

func (f *Feature) addCleanup(cleanupFuncs ...Action) {
	f.cleanups = append(f.cleanups, cleanupFuncs...)
}

func (f *Feature) applyFeature(ctx context.Context) error {
	var multiErr *multierror.Error

	for _, dataProvider := range f.dataProviders {
		multiErr = multierror.Append(multiErr, dataProvider(ctx, f))
	}
	if errDataLoader := multiErr.ErrorOrNil(); errDataLoader != nil {
		return &withConditionReasonError{reason: featurev1.ConditionReason.LoadTemplateData, err: errDataLoader}
	}

	for _, precondition := range f.preconditions {
		multiErr = multierror.Append(multiErr, precondition(ctx, f))
	}
	if preconditionsErr := multiErr.ErrorOrNil(); preconditionsErr != nil {
		return &withConditionReasonError{reason: featurev1.ConditionReason.PreConditions, err: preconditionsErr}
	}

	for _, clusterOperation := range f.clusterOperations {
		if errClusterOperation := clusterOperation(ctx, f); errClusterOperation != nil {
			return &withConditionReasonError{reason: featurev1.ConditionReason.ResourceCreation, err: errClusterOperation}
		}
	}

	for i := range f.resources {
		r := f.resources[i]
		if processErr := r.Apply(ctx, f.Client, f.data, DefaultMetaOptions(f)...); processErr != nil {
			return &withConditionReasonError{reason: featurev1.ConditionReason.ApplyManifests, err: processErr}
		}
	}

	for _, postcondition := range f.postconditions {
		multiErr = multierror.Append(multiErr, postcondition(ctx, f))
	}
	if postConditionErr := multiErr.ErrorOrNil(); postConditionErr != nil {
		return &withConditionReasonError{reason: featurev1.ConditionReason.PostConditions, err: postConditionErr}
	}

	return nil
}

// AsOwnerReference returns an OwnerReference for the FeatureTracker resource.
func (f *Feature) AsOwnerReference() metav1.OwnerReference {
	return f.tracker.ToOwnerReference()
}

// OwnedBy returns a cluster.MetaOptions that sets the owner reference to the FeatureTracker resource.
func OwnedBy(f *Feature) cluster.MetaOptions {
	return func(obj metav1.Object) error {
		obj.SetOwnerReferences([]metav1.OwnerReference{f.AsOwnerReference()})
		return nil
	}
}

func DefaultMetaOptions(f *Feature) []cluster.MetaOptions {
	resourceMeta := []cluster.MetaOptions{OwnedBy(f)}
	if f.Managed {
		resourceMeta = append(resourceMeta, cluster.WithAnnotations(annotations.ManagedByODHOperator, "true"))
	}
	return resourceMeta
}
