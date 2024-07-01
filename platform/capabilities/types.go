package capabilities

import (
	"context"

	"github.com/hashicorp/go-multierror"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dsciv1 "github.com/opendatahub-io/opendatahub-operator/v2/apis/dscinitialization/v1"
	featurev1 "github.com/opendatahub-io/opendatahub-operator/v2/apis/features/v1"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/feature"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/feature/kustomize"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/metadata/labels"
)

// Producer | Consumer
// PlatformCapabilities | ComponentCapability

// Handler is an interface that defines the capability management steps for given capability.
// TODO(mvp) rename - what are we going to use it for? adding stuff for capability?
type Handler interface {
	IsRequired() bool
	Reconcile(ctx context.Context, cli client.Client, metaOptions ...cluster.MetaOptions) error
}

type Availability interface {
	IsAvailable() bool
}

type PlatformRegistry interface {
	Save(ctx context.Context, cli client.Client, metaOptions ...cluster.MetaOptions) error
	ConfigureCapabilities(ctx context.Context, cli client.Client, object metav1.Object, dsciSpec *dsciv1.DSCInitializationSpec, metaOptions ...cluster.MetaOptions) error
	// RemoveCapabilities(context.Context, client.Client, *dsciv1.DSCInitializationSpec) error
	// authz: apply authorino setup
	// TODO(after-mvp): when KServe onboarded move authz setup from DSCI controller here
}

// Consumer.
type PlatformCapabilities interface {
	Authorization() Authorization
}

// Registry used by Components to register their Capabilities configuration.
type Registry struct {
	// owner           metav1.ObjectMeta
	// targetNamespace string
	authorization AuthorizationCapability
}

// TODO: include OwnedBy for DSC clean up, both Registry and Handler.
func (r *Registry) ConfigureCapabilities(ctx context.Context,
	cli client.Client,
	owner metav1.Object,
	dsciSpec *dsciv1.DSCInitializationSpec,
	metaOptions ...cluster.MetaOptions) error { // TODO(mvp): make it a dynamic slice
	handlers := []Handler{&r.authorization}

	isRequired := func(handlers ...Handler) bool {
		for _, handler := range handlers {
			if handler.IsRequired() {
				return true
			}
		}

		return false
	}

	var errReconcile *multierror.Error
	for _, handler := range handlers {
		errReconcile = multierror.Append(errReconcile, handler.Reconcile(ctx, cli, metaOptions...))
	}

	platformFeatures := feature.NewFeaturesHandler(
		dsciSpec.ApplicationsNamespace,
		featurev1.Source{Type: featurev1.PlatformCapabilityType, Name: "authorization"},
		owner,
		r.definePlatformDeployment(isRequired(handlers...)),
	)

	return platformFeatures.Apply(ctx)
}

func (r *Registry) definePlatformDeployment(required bool) feature.FeaturesProvider {
	return func(registry feature.FeaturesRegistry) error {
		return registry.Add(
			feature.Define("odh-platform-deployment").
				EnabledWhen(func(_ context.Context, _ *feature.Feature) (bool, error) {
					return required, nil
				}).
				Managed().
				Manifests(kustomize.Location("/opt/manifests/platform/default")),
		)
	}
}
func NewRegistry(authorization AuthorizationCapability) *Registry {
	return &Registry{authorization: authorization}
}

var _ PlatformCapabilities = (*Registry)(nil)

func (r *Registry) Authorization() Authorization { //nolint:ireturn //reason TODO figure out return type
	return &r.authorization
}

var _ PlatformRegistry = (*Registry)(nil)

func (r *Registry) Save(ctx context.Context, cli client.Client, metaOptions ...cluster.MetaOptions) error {
	// if requested at all
	platformSettings := make(map[string]string)

	authzJSON, authzErr := r.authorization.AsJSON()
	if authzErr != nil {
		return authzErr
	}

	platformSettings["authorization"] = string(authzJSON)

	metaOptions = append(metaOptions, cluster.WithLabels(
		labels.K8SCommon.PartOf, "opendatahub", // TODO revise
		labels.K8SCommon.ManagedBy, "opendatahub-operator", // TODO change to platform-type aware
	)) // TODO make label metaopt a var outside of here

	return cluster.CreateOrUpdateConfigMap(
		ctx,
		cli,
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: "platform-capabilities",
			},
			Data: platformSettings,
		},
		metaOptions...,
	)
}
