package capabilities

import (
	"context"
	"os"
	"path"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dsciv1 "github.com/opendatahub-io/opendatahub-operator/v2/apis/dscinitialization/v1"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/feature"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/metadata/labels"
)

// Producer | Consumer
// PlatformCapabilities | ComponentCapability

// Handler is an interface that defines the capability management steps for given capability.
// TODO(mvp) rename - what are we going to use it for? adding stuff for capability?
type Handler interface {
	IsRequired() bool
	Configure(ctx context.Context, cli client.Client) error
	Remove(ctx context.Context, cli client.Client) error
}

type Availability interface {
	IsAvailable() bool
}

type PlatformRegistry interface {
	Save(c context.Context, cli client.Client, metaOptions ...cluster.MetaOptions) error
	ConfigureCapabilities(context.Context, client.Client, *dsciv1.DSCInitializationSpec) error
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
	authorization AuthorizationCapability
}

// TODO: include OwnedBy for DSC clean up, both Registry and Handler.
func (r *Registry) ConfigureCapabilities(ctx context.Context, cli client.Client, dsciSpec *dsciv1.DSCInitializationSpec) error {
	// iterate over all handlers and configure

	handlers := []Handler{&r.authorization}

	isRequired := func(handlers ...Handler) bool {
		for _, handler := range handlers {
			if handler.IsRequired() {
				return true
			}
		}

		return false
	}

	// TODO(mvp): multi error
	configure := func(handlers ...Handler) error {
		for _, handler := range handlers {
			err := handler.Configure(ctx, cli)
			if err != nil {
				return err
			}
		}

		return nil
	}

	remove := func(handlers ...Handler) error {
		for _, handler := range handlers {
			err := handler.Remove(ctx, cli)
			if err != nil {
				return err
			}
		}

		return nil
	}

	var err error

	authInitializer := feature.ComponentFeaturesHandler("Platform", dsciSpec, r.definePlatform())

	// TODO: we need to track state if we once were deployed, but now not needed?
	if isRequired(handlers...) {
		// return nil // nothing to do..
		err = authInitializer.Apply()
		if err != nil {
			return err
		}
	} else {
		if err := authInitializer.Delete(); err != nil {
			return err
		}

		// TODO(mvp): if none are required - we have to remove all
		// TODO(mvp): how to deal with DSC/DSCI removal
		if err := remove(handlers...); err != nil {
			return err
		}
	}

	// TODO: instead of a defined configure/remove phase.. should we simply call remove if !HasBeenConfigured incase it once was?
	err = configure(handlers...)
	if err != nil {
		return err
	}

	return nil
}

func (r *Registry) definePlatform() feature.FeaturesProvider {
	return func(handler *feature.FeaturesHandler) error {
		createControllerErr := feature.CreateFeature("deploy-odh-platform").
			For(handler).
			ManifestSource(os.DirFS(".")). // tmp, unused
			Manifests(
				path.Join("/opt/manifests/platform/default"),
			).
			Load()

		if createControllerErr != nil {
			return createControllerErr
		}

		return nil
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

	authzJSON, authzErr := r.authorization.asJSON()
	if authzErr != nil {
		return authzErr
	}

	platformSettings["authorization"] = string(authzJSON)

	metaOptions = append(metaOptions, cluster.WithLabels(
		labels.K8SCommon.PartOf, "opendatahub", // TODO revise
		labels.K8SCommon.ManagedBy, "opendatahub-operator", // TODO change to platform-type aware
	)) // TODO make label metaopt a var outside of here

	// TODO change signature to pass ctx as first param
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