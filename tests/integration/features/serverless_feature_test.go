package features_test

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	dsciv1 "github.com/opendatahub-io/opendatahub-operator/v2/apis/dscinitialization/v1"
	infrav1 "github.com/opendatahub-io/opendatahub-operator/v2/apis/infrastructure/v1"
	"github.com/opendatahub-io/opendatahub-operator/v2/components/kserve"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/feature"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/feature/serverless"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/feature/servicemesh"
	"github.com/opendatahub-io/opendatahub-operator/v2/tests/envtestutil"
	"github.com/opendatahub-io/opendatahub-operator/v2/tests/integration/features/fixtures"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Serverless feature", func() {

	var (
		dsci            *dsciv1.DSCInitialization
		objectCleaner   *envtestutil.Cleaner
		kserveComponent *kserve.Kserve
	)

	BeforeEach(func() {
		// TODO rework
		c, err := client.New(envTest.Config, client.Options{})
		Expect(err).ToNot(HaveOccurred())
		objectCleaner = envtestutil.CreateCleaner(c, envTest.Config, fixtures.Timeout, fixtures.Interval)

		dsci = fixtures.NewDSCInitialization("default")
		kserveComponent = &kserve.Kserve{}
	})

	Context("verifying preconditions", func() {

		When("operator is not installed", func() {

			It("should fail on precondition check", func() {
				// given
				featuresHandler := feature.ComponentFeaturesHandler(dsci, kserveComponent.GetComponentName(), dsci.Spec.ApplicationsNamespace, func(registry feature.FeaturesRegistry) error {
					verificationFeatureErr := registry.Add(
						feature.Define("no-serverless-operator-check").
							UsingConfig(envTest.Config).
							PreConditions(serverless.EnsureServerlessOperatorInstalled),
					)

					Expect(verificationFeatureErr).ToNot(HaveOccurred())

					return nil
				})

				// when
				applyErr := featuresHandler.Apply()

				// then
				Expect(applyErr).To(MatchError(ContainSubstring("failed to find the pre-requisite operator subscription \"serverless-operator\"")))
			})
		})

		When("operator is installed", func() {

			var knativeServingCrdObj *apiextensionsv1.CustomResourceDefinition

			BeforeEach(func() {
				err := fixtures.CreateSubscription(envTestClient, "openshift-serverless", fixtures.KnativeServingSubscription)
				Expect(err).ToNot(HaveOccurred())

				// Create KNativeServing the CRD
				knativeServingCrdObj = &apiextensionsv1.CustomResourceDefinition{}
				Expect(yaml.Unmarshal([]byte(fixtures.KnativeServingCrd), knativeServingCrdObj)).To(Succeed())
				c, err := client.New(envTest.Config, client.Options{})
				Expect(err).ToNot(HaveOccurred())
				Expect(c.Create(context.TODO(), knativeServingCrdObj)).To(Succeed())

				crdOptions := envtest.CRDInstallOptions{PollInterval: fixtures.Interval, MaxTime: fixtures.Timeout}
				err = envtest.WaitForCRDs(envTest.Config, []*apiextensionsv1.CustomResourceDefinition{knativeServingCrdObj}, crdOptions)
				Expect(err).ToNot(HaveOccurred())
			})

			AfterEach(func() {
				// Delete KNativeServing CRD
				objectCleaner.DeleteAll(knativeServingCrdObj)
			})

			It("should succeed checking operator installation using precondition", func() {
				// when
				featuresHandler := feature.ComponentFeaturesHandler(dsci, kserveComponent.GetComponentName(), dsci.Spec.ApplicationsNamespace, func(registry feature.FeaturesRegistry) error {
					verificationFeatureErr := registry.Add(
						feature.Define("serverless-operator-check").
							UsingConfig(envTest.Config).
							PreConditions(serverless.EnsureServerlessOperatorInstalled),
					)

					Expect(verificationFeatureErr).ToNot(HaveOccurred())

					return nil
				})

				// then
				Expect(featuresHandler.Apply()).To(Succeed())
			})

			It("should succeed if serving is not installed for KNative serving precondition", func() {
				// when
				featuresHandler := feature.ComponentFeaturesHandler(dsci, kserveComponent.GetComponentName(), dsci.Spec.ApplicationsNamespace, func(registry feature.FeaturesRegistry) error {
					verificationFeatureErr := registry.Add(
						feature.Define("no-serving-installed-yet").
							UsingConfig(envTest.Config).
							PreConditions(serverless.EnsureServerlessAbsent),
					)

					Expect(verificationFeatureErr).ToNot(HaveOccurred())

					return nil
				})

				// then
				Expect(featuresHandler.Apply()).To(Succeed())
			})

			It("should fail if serving is already installed for KNative serving precondition", func() {
				// given
				ns := envtestutil.AppendRandomNameTo(fixtures.TestNamespacePrefix)
				nsResource := fixtures.NewNamespace(ns)
				Expect(envTestClient.Create(context.TODO(), nsResource)).To(Succeed())
				defer objectCleaner.DeleteAll(nsResource)

				knativeServing := &unstructured.Unstructured{}
				Expect(yaml.Unmarshal([]byte(fixtures.KnativeServingInstance), knativeServing)).To(Succeed())
				knativeServing.SetNamespace(nsResource.Name)
				Expect(envTestClient.Create(context.TODO(), knativeServing)).To(Succeed())

				// when
				featuresHandler := feature.ComponentFeaturesHandler(dsci, kserveComponent.GetComponentName(), dsci.Spec.ApplicationsNamespace, func(registry feature.FeaturesRegistry) error {
					verificationFeatureErr := registry.Add(
						feature.Define("serving-already-installed").
							UsingConfig(envTest.Config).
							PreConditions(serverless.EnsureServerlessAbsent),
					)

					Expect(verificationFeatureErr).ToNot(HaveOccurred())

					return nil
				})

				// then
				Expect(featuresHandler.Apply()).ToNot(Succeed())
			})
		})

	})

	Context("default values", func() {

		var testFeature *feature.Feature

		BeforeEach(func() {
			// Stubbing feature as we want to test particular functions in isolation
			testFeature = &feature.Feature{
				Name: "test-feature",
			}

			testFeature.Client = envTestClient
		})

		Context("ingress gateway TLS secret name", func() {

			It("should set default value when value is empty in the DSCI", func() {
				// given
				serving := infrav1.ServingSpec{
					IngressGateway: infrav1.IngressGatewaySpec{
						Certificate: infrav1.CertificateSpec{
							SecretName: "",
						},
					},
				}

				// when
				actualSecretName, err := serverless.FeatureData.Certificate.Create(&serving).Value(context.Background(), envTestClient)

				// then
				Expect(err).ToNot(HaveOccurred())
				Expect(actualSecretName).To(Equal(serverless.DefaultCertificateSecretName))
			})

			It("should use user value when set in the DSCI", func() {
				// given
				serving := infrav1.ServingSpec{
					IngressGateway: infrav1.IngressGatewaySpec{
						Certificate: infrav1.CertificateSpec{
							SecretName: "top-secret-service",
						},
					},
				}

				// when
				actualSecretName, err := serverless.FeatureData.Certificate.Create(&serving).Value(context.Background(), envTestClient)

				// then
				Expect(err).ToNot(HaveOccurred())
				Expect(actualSecretName).To(Equal("top-secret-service"))
			})
		})

		Context("ingress domain name suffix", func() {

			It("should use OpenShift ingress domain when value is empty in the DSCI", func() {
				// given
				osIngressResource := &unstructured.Unstructured{}
				Expect(yaml.Unmarshal([]byte(fixtures.OpenshiftClusterIngress), osIngressResource)).ToNot(HaveOccurred())
				Expect(envTestClient.Create(context.Background(), osIngressResource)).To(Succeed())

				serving := infrav1.ServingSpec{
					IngressGateway: infrav1.IngressGatewaySpec{
						Domain: "",
					},
				}

				// when
				domain, err := serverless.FeatureData.IngressDomain.Create(&serving).Value(context.Background(), envTestClient)

				// then
				Expect(err).ToNot(HaveOccurred())
				Expect(domain).To(Equal("*.foo.io"))
			})

			It("should use user value when set in the DSCI", func() {
				// given
				serving := infrav1.ServingSpec{
					IngressGateway: infrav1.IngressGatewaySpec{
						Domain: fixtures.TestDomainFooCom,
					},
				}

				// when
				domain, err := serverless.FeatureData.IngressDomain.Create(&serving).Value(context.Background(), envTestClient)

				// then
				Expect(err).ToNot(HaveOccurred())
				Expect(domain).To(Equal(fixtures.TestDomainFooCom))
			})
		})

	})

	Context("resources creation", func() {

		var (
			namespace *corev1.Namespace
		)

		BeforeEach(func() {
			ns := envtestutil.AppendRandomNameTo(fixtures.TestNamespacePrefix)
			namespace = fixtures.NewNamespace(ns)
			Expect(envTestClient.Create(context.TODO(), namespace)).To(Succeed())

			dsci.Spec.ServiceMesh.ControlPlane.Namespace = ns
		})

		AfterEach(func() {
			objectCleaner.DeleteAll(namespace)
		})

		It("should create a TLS secret if certificate is SelfSigned", func() {
			// given
			kserveComponent.Serving.IngressGateway.Certificate.Type = infrav1.SelfSigned
			kserveComponent.Serving.IngressGateway.Domain = fixtures.TestDomainFooCom

			featuresHandler := feature.ComponentFeaturesHandler(dsci, kserveComponent.GetComponentName(), dsci.Spec.ApplicationsNamespace, func(registry feature.FeaturesRegistry) error {
				verificationFeatureErr := registry.Add(
					feature.Define("tls-secret-creation").
						UsingConfig(envTest.Config).
						WithData(
							servicemesh.FeatureData.ControlPlane.Create(&dsci.Spec).AsAction(),
							serverless.FeatureData.Serving.Create(&kserveComponent.Serving).AsAction(),
							serverless.FeatureData.IngressDomain.Create(&kserveComponent.Serving).AsAction(),
							serverless.FeatureData.Certificate.Create(&kserveComponent.Serving).AsAction(),
						).
						WithResources(serverless.ServingCertificateResource),
				)

				Expect(verificationFeatureErr).ToNot(HaveOccurred())

				return nil
			})

			// when
			Expect(featuresHandler.Apply()).To(Succeed())

			// then
			Eventually(func() error {
				secret, err := envTestClientset.CoreV1().Secrets(namespace.Name).Get(context.TODO(), serverless.DefaultCertificateSecretName, metav1.GetOptions{})
				if err != nil {
					return err
				}

				if secret == nil {
					return fmt.Errorf("secret not found")
				}

				return nil
			}).WithTimeout(fixtures.Timeout).WithPolling(fixtures.Interval).Should(Succeed())
		})

		It("should not create any TLS secret if certificate is user provided", func() {
			// given
			kserveComponent.Serving.IngressGateway.Certificate.Type = infrav1.Provided
			kserveComponent.Serving.IngressGateway.Domain = fixtures.TestDomainFooCom
			featuresHandler := feature.ComponentFeaturesHandler(dsci, kserveComponent.GetComponentName(), dsci.Spec.ApplicationsNamespace, func(registry feature.FeaturesRegistry) error {
				verificationFeatureErr := registry.Add(
					feature.Define("tls-secret-creation").
						UsingConfig(envTest.Config).
						WithData(
							servicemesh.FeatureData.ControlPlane.Create(&dsci.Spec).AsAction(),
							serverless.FeatureData.Serving.Create(&kserveComponent.Serving).AsAction(),
							serverless.FeatureData.IngressDomain.Create(&kserveComponent.Serving).AsAction(),
							serverless.FeatureData.Certificate.Create(&kserveComponent.Serving).AsAction(),
						).
						WithResources(serverless.ServingCertificateResource),
				)

				Expect(verificationFeatureErr).ToNot(HaveOccurred())

				return nil
			})

			// when
			Expect(featuresHandler.Apply()).To(Succeed())

			// then
			Consistently(func() error {
				list, err := envTestClientset.CoreV1().Secrets(namespace.Name).List(context.TODO(), metav1.ListOptions{})
				if err != nil || len(list.Items) != 0 {
					return fmt.Errorf("list len: %d, error: %w", len(list.Items), err)
				}

				return nil
			}).WithTimeout(fixtures.Timeout).WithPolling(fixtures.Interval).Should(Succeed())
		})

	})

})
