package features_test

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dsciv1 "github.com/opendatahub-io/opendatahub-operator/v2/apis/dscinitialization/v1"
	featurev1 "github.com/opendatahub-io/opendatahub-operator/v2/apis/features/v1"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/feature"
	"github.com/opendatahub-io/opendatahub-operator/v2/tests/envtestutil"
	"github.com/opendatahub-io/opendatahub-operator/v2/tests/integration/features/fixtures"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
)

var _ = Describe("feature cleanup", func() {

	Context("using FeatureTracker and ownership as cleanup strategy", Ordered, func() {

		const (
			featureName = "create-secret"
			secretName  = "test-secret"
		)

		var (
			dsci          *dsciv1.DSCInitialization
			namespace     string
			ns            *corev1.Namespace
			testFeature   *feature.Feature
			objectCleaner *envtestutil.Cleaner
		)

		BeforeAll(func() {
			objectCleaner = envtestutil.CreateCleaner(envTestClient, envTest.Config, fixtures.Timeout, fixtures.Interval)
			namespace = envtestutil.AppendRandomNameTo("test-secret-ownership")
			ns = fixtures.NewNamespace(namespace)
			dsci = fixtures.NewDSCInitialization(namespace)
			err := fixtures.CreateOrUpdateDsci(envTestClient, dsci)
			Expect(err).ToNot(HaveOccurred())
			dsci.APIVersion = fixtures.DsciAPIVersion
			dsci.Kind = fixtures.DsciKind
			var errSecretCreation error
			testFeature, errSecretCreation = feature.Define(featureName).
				TargetNamespace(dsci.Spec.ApplicationsNamespace).
				Source(featurev1.Source{
					Type: featurev1.DSCIType,
					Name: dsci.Name,
				}).
				UsingConfig(envTest.Config).
				PreConditions(
					feature.CreateNamespaceIfNotExists(namespace),
				).
				WithResources(fixtures.CreateSecret(secretName, namespace)).
				Create()

			Expect(errSecretCreation).ToNot(HaveOccurred())

		})

		AfterAll(func(ctx context.Context) {
			objectCleaner.DeleteAll(ctx, dsci, ns)
		})

		It("should successfully create resource and associated feature tracker", func(ctx context.Context) {
			// when
			Expect(testFeature.Apply(ctx)).Should(Succeed())

			// then
			Eventually(createdSecretHasOwnerReferenceToOwningFeature(namespace, featureName)).
				WithContext(ctx).
				WithTimeout(fixtures.Timeout).
				WithPolling(fixtures.Interval).
				Should(Succeed())
		})

		It("should remove feature tracker on clean-up", func(ctx context.Context) {
			// when
			Expect(testFeature.Cleanup(ctx)).To(Succeed())

			// then
			Consistently(createdSecretHasOwnerReferenceToOwningFeature(namespace, featureName)).
				WithContext(ctx).
				WithTimeout(fixtures.Timeout).
				WithPolling(fixtures.Interval).
				Should(WithTransform(errors.IsNotFound, BeTrue()))
		})

	})

	Context("cleaning up conditionally enabled features", Ordered, func() {

		const (
			featureName = "enabled-conditionally"
			secretName  = "test-secret"
		)

		var (
			namespace string
		)

		BeforeAll(func() {
			namespace = envtestutil.AppendRandomNameTo("test-conditional-cleanup")
		})

		It("should create feature, apply resource and create feature tracker", func(ctx context.Context) {
			// given
			err := fixtures.CreateOrUpdateNamespace(ctx, envTestClient, fixtures.NewNamespace("conditional-ns"))
			Expect(err).To(Not(HaveOccurred()))

			feature, conditionalCreationErr := feature.Define(featureName).
				UsingConfig(envTest.Config).
				TargetNamespace(namespace).
				PreConditions(
					feature.CreateNamespaceIfNotExists(namespace),
				).
				EnabledWhen(namespaceExists).
				WithResources(fixtures.CreateSecret(secretName, namespace)).
				Create()

			Expect(conditionalCreationErr).ToNot(HaveOccurred())

			// when
			Expect(feature.Apply(ctx)).Should(Succeed())

			// then
			Eventually(createdSecretHasOwnerReferenceToOwningFeature(namespace, featureName)).
				WithContext(ctx).
				WithTimeout(fixtures.Timeout).
				WithPolling(fixtures.Interval).
				Should(Succeed())
		})

		It("should clean up resources when the condition is no longer met", func(ctx context.Context) {
			// given
			err := envTestClient.Delete(context.Background(), fixtures.NewNamespace("conditional-ns"))
			Expect(err).To(Not(HaveOccurred()))

			// Mimic reconcile by re-loading the feature handler
			feature, conditionalCreationErr := feature.Define(featureName).
				UsingConfig(envTest.Config).
				TargetNamespace(namespace).
				PreConditions(
					feature.CreateNamespaceIfNotExists(namespace),
				).
				EnabledWhen(namespaceExists).
				WithResources(fixtures.CreateSecret(secretName, namespace)).
				Create()

			Expect(conditionalCreationErr).ToNot(HaveOccurred())

			Expect(feature.Apply(ctx)).Should(Succeed())

			// then
			Consistently(createdSecretHasOwnerReferenceToOwningFeature(namespace, featureName)).
				WithContext(ctx).
				WithTimeout(fixtures.Timeout).
				WithPolling(fixtures.Interval).
				Should(WithTransform(errors.IsNotFound, BeTrue()))

			Consistently(func() error {
				_, err := fixtures.GetFeatureTracker(ctx, envTestClient, namespace, featureName)
				if errors.IsNotFound(err) {
					return nil
				}
				return err
			}).
				WithContext(ctx).
				WithTimeout(fixtures.Timeout).
				WithPolling(fixtures.Interval).
				Should(Succeed())
		})
	})
})

func createdSecretHasOwnerReferenceToOwningFeature(namespace, featureName string) func(context.Context) error {
	return func(ctx context.Context) error {
		secretName := "test-secret"
		secret, err := envTestClientset.CoreV1().
			Secrets(namespace).
			Get(ctx, secretName, metav1.GetOptions{})

		if err != nil {
			return err
		}

		Expect(secret.OwnerReferences).Should(
			ContainElement(
				MatchFields(IgnoreExtras, Fields{"Kind": Equal("FeatureTracker")}),
			),
		)

		trackerName := ""
		for _, ownerRef := range secret.OwnerReferences {
			if ownerRef.Kind == "FeatureTracker" {
				trackerName = ownerRef.Name
				break
			}
		}

		tracker := &featurev1.FeatureTracker{}
		err = envTestClient.Get(ctx, client.ObjectKey{
			Name: trackerName,
		}, tracker)
		if err != nil {
			return err
		}

		expectedName := namespace + "-" + featureName
		Expect(tracker.ObjectMeta.Name).To(Equal(expectedName))

		return nil
	}
}

func namespaceExists(ctx context.Context, f *feature.Feature) (bool, error) {
	namespace, err := fixtures.GetNamespace(ctx, f.Client, "conditional-ns")
	if errors.IsNotFound(err) {
		return false, nil
	}
	// ensuring it fails if namespace is still deleting
	if namespace.Status.Phase == corev1.NamespaceTerminating {
		return false, nil
	}
	return true, nil
}
