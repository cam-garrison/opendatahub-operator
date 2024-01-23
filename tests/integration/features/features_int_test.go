package features_test

import (
	"context"
	"embed"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"time"

	conditionsv1 "github.com/openshift/custom-resource-status/conditions/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dscv1 "github.com/opendatahub-io/opendatahub-operator/v2/apis/dscinitialization/v1"
	featurev1 "github.com/opendatahub-io/opendatahub-operator/v2/apis/features/v1"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/feature"
	"github.com/opendatahub-io/opendatahub-operator/v2/tests/envtestutil"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/opendatahub-io/opendatahub-operator/v2/tests/assertions"
)

//go:embed templates
var testEmbeddedFiles embed.FS

const (
	timeout      = 5 * time.Second
	interval     = 250 * time.Millisecond
	templatesDir = "templates"
)

var _ = Describe("preconditions", func() {

	Context("namespace existence", func() {

		var (
			objectCleaner *envtestutil.Cleaner
			testFeature   *feature.Feature
			namespace     string
		)

		BeforeEach(func() {
			objectCleaner = envtestutil.CreateCleaner(envTestClient, envTest.Config, timeout, interval)

			testFeatureName := "test-ns-creation"
			namespace = envtestutil.AppendRandomNameTo(testFeatureName)

			dsciSpec := newDSCInitializationSpec(namespace)
			var err error
			testFeature, err = feature.CreateFeature(testFeatureName).
				For(dsciSpec).
				UsingConfig(envTest.Config).
				Load()
			Expect(err).ToNot(HaveOccurred())
		})

		It("should create namespace if it does not exist", func() {
			// given
			_, err := getNamespace(namespace)
			Expect(errors.IsNotFound(err)).To(BeTrue())
			defer objectCleaner.DeleteAll(&v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}})

			// when
			err = feature.CreateNamespaceIfNotExists(namespace)(testFeature)

			// then
			Expect(err).ToNot(HaveOccurred())
		})

		It("should not try to create namespace if it does already exist", func() {
			// given
			ns := createNamespace(namespace)
			Expect(envTestClient.Create(context.Background(), ns)).To(Succeed())
			defer objectCleaner.DeleteAll(ns)

			// when
			err := feature.CreateNamespaceIfNotExists(namespace)(testFeature)

			// then
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("ensuring custom resource definitions are installed", func() {

		var (
			dsciSpec            *dscv1.DSCInitializationSpec
			verificationFeature *feature.Feature
		)

		BeforeEach(func() {
			dsciSpec = newDSCInitializationSpec("default")
		})

		It("should successfully check for existing CRD", func() {
			// given example CRD installed into env
			name := "test-resources.openshift.io"

			var err error
			verificationFeature, err = feature.CreateFeature("CRD verification").
				For(dsciSpec).
				UsingConfig(envTest.Config).
				PreConditions(feature.EnsureCRDIsInstalled(name)).
				Load()
			Expect(err).ToNot(HaveOccurred())

			// when
			err = verificationFeature.Apply()

			// then
			Expect(err).ToNot(HaveOccurred())
		})

		It("should fail to check non-existing CRD", func() {
			// given
			name := "non-existing-resource.non-existing-group.io"

			var err error
			verificationFeature, err = feature.CreateFeature("CRD verification").
				For(dsciSpec).
				UsingConfig(envTest.Config).
				PreConditions(feature.EnsureCRDIsInstalled(name)).
				Load()
			Expect(err).ToNot(HaveOccurred())

			// when
			err = verificationFeature.Apply()

			// then
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("\"non-existing-resource.non-existing-group.io\" not found"))
		})
	})

})

var _ = Describe("feature trackers", func() {
	Context("ensuring feature trackers indicate status and phase", func() {

		var (
			dsciSpec *dscv1.DSCInitializationSpec
		)

		BeforeEach(func() {
			dsciSpec = newDSCInitializationSpec("default")
		})

		It("should indicate successful installation in FeatureTracker", func() {
			// given example CRD installed into env
			name := "test-resources.openshift.io"
			verificationFeature, err := feature.CreateFeature("crd-verification").
				For(dsciSpec).
				UsingConfig(envTest.Config).
				PreConditions(feature.EnsureCRDIsInstalled(name)).
				Load()
			Expect(err).ToNot(HaveOccurred())

			// when
			Expect(verificationFeature.Apply()).To(Succeed())

			// then
			featureTracker := getFeatureTracker("default-crd-verification")
			Expect(featureTracker.Status.Conditions).To(HaveCondition(conditionsv1.ConditionAvailable, v1.ConditionTrue, featurev1.FeatureCreated))
		})

		It("should indicate failure in preconditions", func() {
			// given
			name := "non-existing-resource.non-existing-group.io"
			verificationFeature, err := feature.CreateFeature("crd-verification").
				For(dsciSpec).
				UsingConfig(envTest.Config).
				PreConditions(feature.EnsureCRDIsInstalled(name)).
				Load()
			Expect(err).ToNot(HaveOccurred())

			// when
			Expect(verificationFeature.Apply()).ToNot(Succeed())

			// then
			featureTracker := getFeatureTracker("default-crd-verification")
			Expect(featureTracker.Status.Conditions).To(HaveCondition(conditionsv1.ConditionDegraded, v1.ConditionTrue, featurev1.PreConditions))
		})

		It("should indicate failure in post-conditions", func() {
			// given
			verificationFeature, err := feature.CreateFeature("post-condition-failure").
				For(dsciSpec).
				UsingConfig(envTest.Config).
				PostConditions(func(f *feature.Feature) error {
					return fmt.Errorf("always fail")
				}).
				Load()
			Expect(err).ToNot(HaveOccurred())

			// when
			Expect(verificationFeature.Apply()).ToNot(Succeed())

			// then
			featureTracker := getFeatureTracker("default-post-condition-failure")
			Expect(featureTracker.Status.Conditions).To(HaveCondition(conditionsv1.ConditionDegraded, v1.ConditionTrue, featurev1.PostConditions))
		})
	})
})

var _ = Describe("Manifest sources", func() {
	Context("using various manifest sources", func() {

		var (
			objectCleaner *envtestutil.Cleaner
			dsciSpec      *dscv1.DSCInitializationSpec
			namespace     = "default"
		)

		BeforeEach(func() {
			objectCleaner = envtestutil.CreateCleaner(envTestClient, envTest.Config, timeout, interval)
			dsciSpec = newDSCInitializationSpec(namespace)
		})

		It("should be able to process an embedded template from the default location", func() {
			// given
			ns := createNamespace("service-ns")
			Expect(envTestClient.Create(context.Background(), ns)).To(Succeed())
			defer objectCleaner.DeleteAll(ns)

			serviceMeshSpec := &dsciSpec.ServiceMesh
			serviceMeshSpec.ControlPlane.Namespace = "service-ns"

			createService, err := feature.CreateFeature("create-control-plane").
				For(dsciSpec).
				Manifests(path.Join(templatesDir, "serverless", "serving-istio-gateways", "local-gateway-svc.tmpl")).
				UsingConfig(envTest.Config).
				Load()

			Expect(err).ToNot(HaveOccurred())

			// when
			Expect(createService.Apply()).To(Succeed())

			// then
			service, err := getService("knative-local-gateway", "service-ns")
			Expect(err).ToNot(HaveOccurred())
			Expect(service.Name).To(Equal("knative-local-gateway"))
		})

		It("should be able to process an embedded YAML file from the default location", func() {
			// given
			ns := createNamespace("knative-serving")
			Expect(envTestClient.Create(context.Background(), ns)).To(Succeed())
			defer objectCleaner.DeleteAll(ns)

			createGateway, err := feature.CreateFeature("create-gateway").
				For(dsciSpec).
				Manifests(path.Join(templatesDir, "serverless", "serving-istio-gateways", "istio-local-gateway.yaml")).
				UsingConfig(envTest.Config).
				Load()

			Expect(err).ToNot(HaveOccurred())

			// when
			Expect(createGateway.Apply()).To(Succeed())

			// then
			gateway, err := getGateway(envTest.Config, "knative-serving", "knative-local-gateway")
			Expect(err).ToNot(HaveOccurred())
			Expect(gateway).ToNot(BeNil())
		})

		It("should be able to process an embedded file from a non default location", func() {
			createNs, err := feature.CreateFeature("create-ns").
				For(dsciSpec).
				ManifestSource(testEmbeddedFiles).
				Manifests(path.Join(templatesDir, "namespace.yaml")).
				UsingConfig(envTest.Config).
				Load()

			Expect(err).ToNot(HaveOccurred())

			// when
			Expect(createNs.Apply()).To(Succeed())

			// then
			namespace, err := getNamespace("embedded-test-ns")
			Expect(err).ToNot(HaveOccurred())
			Expect(namespace.Name).To(Equal("embedded-test-ns"))
		})

		It("should source manifests from a specified temporary directory within the file system", func() {
			// given
			tempDir := GinkgoT().TempDir()
			yamlData := `apiVersion: v1
kind: Namespace
metadata:
  name: real-file-test-ns`

			err := createFile(tempDir, "namespace.yaml", yamlData)
			Expect(err).ToNot(HaveOccurred())

			createNs, err := feature.CreateFeature("create-ns").
				For(dsciSpec).
				ManifestSource(os.DirFS(tempDir)).
				Manifests(path.Join("namespace.yaml")). // must be relative to root DirFS defined above
				UsingConfig(envTest.Config).
				Load()

			Expect(err).ToNot(HaveOccurred())

			// when
			Expect(createNs.Apply()).To(Succeed())

			// then
			namespace, err := getNamespace("real-file-test-ns")
			Expect(err).ToNot(HaveOccurred())
			Expect(namespace.Name).To(Equal("real-file-test-ns"))
		})
	})
})

func createNamespace(name string) *v1.Namespace {
	return &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
}

func newDSCInitializationSpec(ns string) *dscv1.DSCInitializationSpec {
	spec := dscv1.DSCInitializationSpec{}
	spec.ApplicationsNamespace = ns

	return &spec
}

func getNamespace(namespace string) (*v1.Namespace, error) {
	ns := createNamespace(namespace)
	err := envTestClient.Get(context.Background(), k8stypes.NamespacedName{Name: namespace}, ns)

	return ns, err
}

func getFeatureTracker(name string) *featurev1.FeatureTracker {
	tracker := &featurev1.FeatureTracker{}
	err := envTestClient.Get(context.Background(), client.ObjectKey{
		Name: name,
	}, tracker)

	Expect(err).ToNot(HaveOccurred())

	return tracker
}

func getService(name, namespace string) (*v1.Service, error) {
	svc := &v1.Service{}
	err := envTestClient.Get(context.Background(), k8stypes.NamespacedName{
		Name: name, Namespace: namespace,
	}, svc)

	return svc, err
}

func createFile(dir, filename, data string) error {
	filePath := filepath.Join(dir, filename)
	file, err := os.Create(filePath)
	if err != nil {
		return err
	}

	_, err = file.WriteString(data)
	if err != nil {
		return err
	}
	return file.Sync()
}
