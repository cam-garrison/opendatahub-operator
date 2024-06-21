//nolint:structcheck // Reason: false positive, complains about unused fields in HandlerWithReporter
package feature

import (
	"fmt"

	"github.com/hashicorp/go-multierror"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/kustomize/api/resmap"

	v1 "github.com/opendatahub-io/opendatahub-operator/v2/apis/dscinitialization/v1"
	featurev1 "github.com/opendatahub-io/opendatahub-operator/v2/apis/features/v1"
	"github.com/opendatahub-io/opendatahub-operator/v2/controllers/status"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/feature/kustomize"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/plugins"
)

type featuresHandler interface {
	Apply() error
	Delete() error
}

type FeaturesRegistry interface {
	Add(builders ...*featureBuilder) error
}

var _ featuresHandler = (*FeaturesHandler)(nil)

// FeaturesHandler provides a structured way to manage and coordinate the creation, application,
// and deletion of features needed in particular Data Science Cluster configuration.
type FeaturesHandler struct {
	targetNamespace   string
	source            featurev1.Source
	owner             metav1.OwnerReference
	features          []*Feature
	featuresProviders []FeaturesProvider
}

var _ FeaturesRegistry = (*FeaturesHandler)(nil)

// Add loads features defined by passed builders and adds to internal list which is then used to Apply on the cluster.
// It also makes sure that both TargetNamespace and Source are added to the feature before it's `Create()`ed.
func (fh *FeaturesHandler) Add(builders ...*featureBuilder) error {
	var featureAddErrors *multierror.Error

	globalPlugins := []resmap.Transformer{plugins.CreateNamespaceApplierPlugin(fh.targetNamespace)}
	if fh.source.Type == featurev1.ComponentType {
		globalPlugins = append(globalPlugins, plugins.CreateAddLabelsPlugin(fh.source.Name))
	}

	for i := range builders {
		fb := builders[i]
		feature, err := fb.TargetNamespace(fh.targetNamespace).
			Source(fh.source).
			OwnedBy(fh.owner).
			EnrichManifests(&kustomize.PluginsEnricher{Plugins: globalPlugins}).
			Create()
		featureAddErrors = multierror.Append(featureAddErrors, err)
		fh.features = append(fh.features, feature)
	}

	return featureAddErrors.ErrorOrNil()
}

func (fh *FeaturesHandler) Apply() error {
	fh.features = make([]*Feature, 0)

	for _, featuresProvider := range fh.featuresProviders {
		if err := featuresProvider(fh); err != nil {
			return fmt.Errorf("failed adding features to the handler. cause: %w", err)
		}
	}

	var applyErrors *multierror.Error
	for _, f := range fh.features {
		if applyErr := f.Apply(); applyErr != nil {
			applyErrors = multierror.Append(applyErrors, fmt.Errorf("failed applying FeatureHandler features. cause: %w", applyErr))
		}
	}

	return applyErrors.ErrorOrNil()
}

// Delete executes registered clean-up tasks for handled Features in the opposite order they were initiated.
// This approach assumes that Features are either instantiated in the correct sequence or are self-contained.
func (fh *FeaturesHandler) Delete() error {
	fh.features = make([]*Feature, 0)

	for _, featuresProvider := range fh.featuresProviders {
		if err := featuresProvider(fh); err != nil {
			return fmt.Errorf("delete phase failed when wiring Feature instances in FeatureHandler.Delete. cause: %w", err)
		}
	}

	var cleanupErrors *multierror.Error
	for i := len(fh.features) - 1; i >= 0; i-- {
		if cleanupErr := fh.features[i].Cleanup(); cleanupErr != nil {
			cleanupErrors = multierror.Append(cleanupErrors, fmt.Errorf("failed executing cleanup in FeatureHandler. cause: %w", cleanupErr))
		}
	}

	return cleanupErrors.ErrorOrNil()
}

// FeaturesProvider is a function which allow to define list of features
// and add them to the handler's registry.
type FeaturesProvider func(registry FeaturesRegistry) error

func ClusterFeaturesHandler(dsci *v1.DSCInitialization, def ...FeaturesProvider) *FeaturesHandler {
	controller := true
	owner := metav1.OwnerReference{
		APIVersion: dsci.APIVersion,
		Kind:       dsci.Kind,
		Name:       dsci.Name,
		UID:        dsci.UID,
		Controller: &controller,
	}
	return &FeaturesHandler{
		targetNamespace:   dsci.Spec.ApplicationsNamespace,
		source:            featurev1.Source{Type: featurev1.DSCIType, Name: dsci.Name},
		featuresProviders: def,
		owner:             owner,
	}
}

func ComponentFeaturesHandler(owner metav1.OwnerReference, componentName, targetNamespace string, def ...FeaturesProvider) *FeaturesHandler {
	return &FeaturesHandler{
		targetNamespace:   targetNamespace,
		source:            featurev1.Source{Type: featurev1.ComponentType, Name: componentName},
		owner:             owner,
		featuresProviders: def,
	}
}

// EmptyFeaturesHandler is noop handler so that we can avoid nil checks in the code and safely call Apply/Delete methods.
var EmptyFeaturesHandler = &FeaturesHandler{
	features:          []*Feature{},
	featuresProviders: []FeaturesProvider{},
}

// HandlerWithReporter is a wrapper around FeaturesHandler and status.Reporter
// It is intended apply features related to a given resource capabilities and report its status using custom reporter.
type HandlerWithReporter[T client.Object] struct {
	handler  *FeaturesHandler
	reporter *status.Reporter[T]
}

var _ featuresHandler = (*HandlerWithReporter[client.Object])(nil)

func NewHandlerWithReporter[T client.Object](handler *FeaturesHandler, reporter *status.Reporter[T]) *HandlerWithReporter[T] {
	return &HandlerWithReporter[T]{
		handler:  handler,
		reporter: reporter,
	}
}

func (h HandlerWithReporter[T]) Apply() error {
	applyErr := h.handler.Apply()
	_, reportErr := h.reporter.ReportCondition(applyErr)
	// We could have failed during Apply phase as well as during reporting.
	// We should return both errors to the caller.
	return multierror.Append(applyErr, reportErr).ErrorOrNil()
}

func (h HandlerWithReporter[T]) Delete() error {
	deleteErr := h.handler.Delete()
	_, reportErr := h.reporter.ReportCondition(deleteErr)
	// We could have failed during Delete phase as well as during reporting.
	// We should return both errors to the caller.
	return multierror.Append(deleteErr, reportErr).ErrorOrNil()
}
