package features_test

import (
	"context"
	"os"
	"path"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/kustomize/api/resmap"

	dsciv1 "github.com/opendatahub-io/opendatahub-operator/v2/apis/dscinitialization/v1"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/feature"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/feature/kustomize"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/feature/manifest"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/feature/provider"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/plugins"
	"github.com/opendatahub-io/opendatahub-operator/v2/tests/envtestutil"
	"github.com/opendatahub-io/opendatahub-operator/v2/tests/integration/features/fixtures"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Applying resources", func() {

	var (
		objectCleaner *envtestutil.Cleaner
		dsci          *dsciv1.DSCInitialization
		namespace     *corev1.Namespace
	)

	BeforeEach(func() {
		objectCleaner = envtestutil.CreateCleaner(envTestClient, envTest.Config, fixtures.Timeout, fixtures.Interval)
		nsName := envtestutil.AppendRandomNameTo("smcp-ns")

		var err error
		namespace, err = cluster.CreateNamespace(context.Background(), envTestClient, nsName)
		Expect(err).ToNot(HaveOccurred())

		dsci = fixtures.NewDSCInitialization(nsName)
		dsci.Spec.ServiceMesh.ControlPlane.Namespace = namespace.Name
		err = fixtures.CreateOrUpdateDsci(envTestClient, dsci)
		Expect(err).ToNot(HaveOccurred())
		dsci.APIVersion = fixtures.DsciAPIVersion
		dsci.Kind = fixtures.DsciKind
	})

	AfterEach(func() {
		objectCleaner.DeleteAll(namespace)
		objectCleaner.DeleteAll(dsci)
	})

	It("should be able to process an embedded YAML file", func() {
		// given
		featuresHandler := feature.ClusterFeaturesHandler(dsci, func(registry feature.FeaturesRegistry) error {
			createNamespaceErr := registry.Add(feature.Define("create-namespace").
				UsingConfig(envTest.Config).
				Manifests(
					manifest.Location(fixtures.TestEmbeddedFiles).
						Include(path.Join(fixtures.BaseDir, "namespace.yaml")),
				),
			)

			Expect(createNamespaceErr).ToNot(HaveOccurred())

			return nil
		})

		// when
		Expect(featuresHandler.Apply()).To(Succeed())

		// then
		embeddedNs, err := fixtures.GetNamespace(envTestClient, "embedded-test-ns")
		defer objectCleaner.DeleteAll(embeddedNs)
		Expect(err).ToNot(HaveOccurred())
		Expect(embeddedNs.Name).To(Equal("embedded-test-ns"))
	})

	It("should be able to process an embedded template file", func() {
		// given
		featuresHandler := feature.ClusterFeaturesHandler(dsci, func(registry feature.FeaturesRegistry) error {
			createServiceErr := registry.Add(feature.Define("create-local-gw-svc").
				UsingConfig(envTest.Config).
				Manifests(
					manifest.Location(fixtures.TestEmbeddedFiles).
						Include(path.Join(fixtures.BaseDir, "local-gateway-svc.tmpl.yaml")),
				).
				WithData(feature.Entry("ControlPlane", provider.ValueOf(dsci.Spec.ServiceMesh.ControlPlane).Get)),
			)

			Expect(createServiceErr).ToNot(HaveOccurred())

			return nil
		})

		// when
		Expect(featuresHandler.Apply()).To(Succeed())

		// then
		service, err := fixtures.GetService(envTestClient, namespace.Name, "knative-local-gateway")
		Expect(err).ToNot(HaveOccurred())
		Expect(service.Name).To(Equal("knative-local-gateway"))
	})

	const nsYAML = `apiVersion: v1
kind: Namespace
metadata:
  name: real-file-test-ns`

	It("should source manifests from a specified temporary directory within the file system", func() {
		// given
		tempDir := GinkgoT().TempDir()

		Expect(fixtures.CreateFile(tempDir, "namespace.yaml", nsYAML)).To(Succeed())

		featuresHandler := feature.ClusterFeaturesHandler(dsci, func(registry feature.FeaturesRegistry) error {
			createServiceErr := registry.Add(feature.Define("create-namespace").
				UsingConfig(envTest.Config).
				Manifests(
					manifest.Location(os.DirFS(tempDir)).
						Include(path.Join("namespace.yaml")), // must be relative to root DirFS defined above
				),
			)

			Expect(createServiceErr).ToNot(HaveOccurred())

			return nil
		})

		// when
		Expect(featuresHandler.Apply()).To(Succeed())

		// then
		realNs, err := fixtures.GetNamespace(envTestClient, "real-file-test-ns")
		defer objectCleaner.DeleteAll(realNs)
		Expect(err).ToNot(HaveOccurred())
		Expect(realNs.Name).To(Equal("real-file-test-ns"))
	})

	It("should process kustomization manifests and apply namespace using plugin defined through builder", func() {
		// given
		targetNamespace := dsci.Spec.ApplicationsNamespace

		cfgMapFeature, errFeatureCreate := feature.Define("create-cfg-map").
			UsingConfig(envTest.Config).
			TargetNamespace(targetNamespace).
			OwnedBy(dsci).
			Manifests(
				kustomize.Location(kustomizeTestFixture()).
					WithPlugins(plugins.CreateNamespaceApplierPlugin(targetNamespace)),
			).
			Create()

		Expect(errFeatureCreate).ToNot(HaveOccurred())

		// when
		Expect(cfgMapFeature.Apply()).To(Succeed())

		// then
		cfgMap, err := fixtures.GetConfigMap(envTestClient, targetNamespace, "my-configmap")
		Expect(err).ToNot(HaveOccurred())
		Expect(cfgMap.Name).To(Equal("my-configmap"))
		Expect(cfgMap.Data["key"]).To(Equal("value"))
	})

	It("should process kustomization manifests with namespace plugin defined through enricher", func() {
		// given
		targetNamespace := dsci.Spec.ApplicationsNamespace

		createCfgMapFeature, errCreateFeature := feature.Define("create-cfg-map").
			UsingConfig(envTest.Config).
			TargetNamespace(targetNamespace).
			OwnedBy(dsci).
			EnrichManifests(&kustomize.PluginsEnricher{Plugins: []resmap.Transformer{plugins.CreateNamespaceApplierPlugin(targetNamespace)}}).
			Manifests(
				kustomize.Location(kustomizeTestFixture()),
			).
			Create()

		Expect(errCreateFeature).ToNot(HaveOccurred())

		// when
		Expect(createCfgMapFeature.Apply()).To(Succeed())

		// then
		cfgMap, err := fixtures.GetConfigMap(envTestClient, targetNamespace, "my-configmap")
		Expect(err).ToNot(HaveOccurred())
		Expect(cfgMap.Name).To(Equal("my-configmap"))
		Expect(cfgMap.Data["key"]).To(Equal("value"))
	})

	When("using feature handler", func() {

		It("should set target namespace and kustomize shared plugins automatically", func() {
			// given
			targetNamespace := dsci.Spec.ApplicationsNamespace

			featuresHandler := feature.ClusterFeaturesHandler(dsci, func(registry feature.FeaturesRegistry) error {
				return registry.Add(feature.Define("create-cfg-map").
					UsingConfig(envTest.Config).
					Manifests(
						kustomize.Location(kustomizeTestFixture()),
					),
				)
			})

			// when
			Expect(featuresHandler.Apply()).To(Succeed())

			// then
			cfgMap, err := fixtures.GetConfigMap(envTestClient, targetNamespace, "my-configmap")
			Expect(err).ToNot(HaveOccurred())
			Expect(cfgMap.Name).To(Equal("my-configmap"))
			Expect(cfgMap.Data["key"]).To(Equal("value"))
		})

	})
})

func kustomizeTestFixture() string {
	rootDir, errRootDir := envtestutil.FindProjectRoot()
	Expect(errRootDir).ToNot(HaveOccurred())
	return path.Join(rootDir, "tests", "integration", "features", "fixtures", "kustomize-manifests")
}
