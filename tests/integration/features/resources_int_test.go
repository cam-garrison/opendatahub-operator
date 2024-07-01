package features_test

import (
	"context"
	"path"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dsciv1 "github.com/opendatahub-io/opendatahub-operator/v2/apis/dscinitialization/v1"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/feature"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/feature/manifest"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/feature/provider"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/metadata/annotations"
	"github.com/opendatahub-io/opendatahub-operator/v2/tests/envtestutil"
	"github.com/opendatahub-io/opendatahub-operator/v2/tests/integration/features/fixtures"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Applying and updating resources", func() {
	var (
		testNamespace   string
		namespace       *corev1.Namespace
		istioNamespace  *corev1.Namespace
		objectCleaner   *envtestutil.Cleaner
		dsci            *dsciv1.DSCInitialization
		dummyAnnotation string
	)

	BeforeEach(func(ctx context.Context) {
		objectCleaner = envtestutil.CreateCleaner(envTestClient, envTest.Config, fixtures.Timeout, fixtures.Interval)

		testNamespace = "test-namespace"
		dummyAnnotation = "fake-anno"

		var err error
		namespace, err = cluster.CreateNamespace(ctx, envTestClient, testNamespace)
		Expect(err).ToNot(HaveOccurred())
		istioNamespace, err = cluster.CreateNamespace(ctx, envTestClient, "istio-system")
		Expect(err).ToNot(HaveOccurred())

		dsci = fixtures.NewDSCInitialization(testNamespace)
		dsci.Spec.ServiceMesh.ControlPlane.Namespace = namespace.Name
		err = fixtures.CreateOrUpdateDsci(envTestClient, dsci)
		dsci.APIVersion = fixtures.DsciAPIVersion
		dsci.Kind = fixtures.DsciKind
		Expect(err).NotTo(HaveOccurred())
	})

	When("a feature is managed", func() {
		It("should reconcile the object to its managed state", func(ctx context.Context) {
			// given managed feature
			featuresHandler := feature.ClusterFeaturesHandler(dsci, func(registry feature.FeaturesRegistry) error {
				return registry.Add(
					feature.Define("create-local-gw-svc").
						UsingConfig(envTest.Config).
						Managed().
						Manifests(
							manifest.Location(fixtures.TestEmbeddedFiles).
								Include(path.Join(fixtures.BaseDir, "local-gateway-svc.tmpl.yaml")),
						).
						WithData(feature.Entry("ControlPlane", provider.ValueOf(dsci.Spec.ServiceMesh.ControlPlane).Get)),
				)
			})
			Expect(featuresHandler.Apply(ctx)).To(Succeed())

			// expect created svc to have managed annotation
			service := getServiceAndExpectAnnotations(ctx, envTestClient, testNamespace, "knative-local-gateway", map[string]string{
				"example-annotation":             "",
				annotations.ManagedByODHOperator: "true",
			})

			// modify managed service
			modifyAndExpectUpdate(ctx, envTestClient, service, "example-annotation", dummyAnnotation)

			// expect that modification is reconciled away
			Expect(featuresHandler.Apply(ctx)).To(Succeed())
			verifyAnnotation(ctx, envTestClient, testNamespace, service.Name, "example-annotation", "")
		})
	})

	When("a feature is unmanaged", func() {
		It("should not reconcile the object", func(ctx context.Context) {
			// given unmanaged feature
			featuresHandler := feature.ClusterFeaturesHandler(dsci, func(registry feature.FeaturesRegistry) error {
				return registry.Add(
					feature.Define("create-local-gw-svc").
						UsingConfig(envTest.Config).
						Manifests(
							manifest.Location(fixtures.TestEmbeddedFiles).
								Include(path.Join(fixtures.BaseDir, "local-gateway-svc.tmpl.yaml")),
						).
						WithData(feature.Entry("ControlPlane", provider.ValueOf(dsci.Spec.ServiceMesh.ControlPlane).Get)),
				)
			})
			Expect(featuresHandler.Apply(ctx)).To(Succeed())

			// modify unmanaged service object
			service, err := fixtures.GetService(ctx, envTestClient, testNamespace, "knative-local-gateway")
			Expect(err).ToNot(HaveOccurred())
			modifyAndExpectUpdate(ctx, envTestClient, service, "example-annotation", dummyAnnotation)

			// expect modification to remain after "reconcile"
			Expect(featuresHandler.Apply(ctx)).To(Succeed())
			verifyAnnotation(ctx, envTestClient, testNamespace, service.Name, "example-annotation", dummyAnnotation)
		})
	})

	When("a feature is unmanaged but the object is marked as managed", func() {
		It("should reconcile this object", func(ctx context.Context) {
			// given unmanaged feature but object marked with managed annotation
			featuresHandler := feature.ClusterFeaturesHandler(dsci, func(registry feature.FeaturesRegistry) error {
				return registry.Add(
					feature.Define("create-managed-svc").
						UsingConfig(envTest.Config).
						Manifests(
							manifest.Location(fixtures.TestEmbeddedFiles).
								Include(path.Join(fixtures.BaseDir, "managed-svc.yaml")),
						).
						WithData(feature.Entry("ControlPlane", provider.ValueOf(dsci.Spec.ServiceMesh.ControlPlane).Get)),
				)
			})
			Expect(featuresHandler.Apply(ctx)).To(Succeed())

			// expect service to have managed annotation
			service := getServiceAndExpectAnnotations(ctx, envTestClient, testNamespace, "managed-svc", map[string]string{
				"example-annotation":             "",
				annotations.ManagedByODHOperator: "true",
			})

			// modify managed service
			modifyAndExpectUpdate(ctx, envTestClient, service, "example-annotation", dummyAnnotation)

			// expect that modification is reconciled away
			Expect(featuresHandler.Apply(ctx)).To(Succeed())
			verifyAnnotation(ctx, envTestClient, testNamespace, service.Name, "example-annotation", "")
		})
	})

	AfterEach(func(ctx context.Context) {
		objectCleaner.DeleteAll(ctx, namespace, istioNamespace, dsci)
	})
})

func getServiceAndExpectAnnotations(ctx context.Context, testClient client.Client, namespace, serviceName string, annotations map[string]string) *corev1.Service {
	service, err := fixtures.GetService(ctx, testClient, namespace, serviceName)
	Expect(err).ToNot(HaveOccurred())
	for key, val := range annotations {
		Expect(service.Annotations[key]).To(Equal(val))
	}
	return service
}

func modifyAndExpectUpdate(ctx context.Context, client client.Client, service *corev1.Service, annotationKey, newValue string) {
	if service.Annotations == nil {
		service.Annotations = make(map[string]string)
	}
	service.Annotations[annotationKey] = newValue
	Expect(client.Update(ctx, service)).To(Succeed())
}

func verifyAnnotation(ctx context.Context, client client.Client, namespace, serviceName, annotationKey, expectedValue string) {
	updatedService, err := fixtures.GetService(ctx, client, namespace, serviceName)
	Expect(err).ToNot(HaveOccurred())
	Expect(updatedService.Annotations[annotationKey]).To(Equal(expectedValue))
}
