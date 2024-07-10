package features_test

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dsciv1 "github.com/opendatahub-io/opendatahub-operator/v2/apis/dscinitialization/v1"
	featurev1 "github.com/opendatahub-io/opendatahub-operator/v2/apis/features/v1"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/feature"
	"github.com/opendatahub-io/opendatahub-operator/v2/tests/envtestutil"
	"github.com/opendatahub-io/opendatahub-operator/v2/tests/integration/features/fixtures"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
)

var _ = Describe("feature cleanup", func() {

	Context("using FeatureTracker and ownership as cleanup strategy", func() {

		const (
			featureName = "create-secret"
			secretName  = "test-secret"
		)

		var (
			dsci        *dsciv1.DSCInitialization
			namespace   string
			testFeature *feature.Feature
		)

		BeforeEach(func() {
			namespace = envtestutil.AppendRandomNameTo("test-secret-ownership")
			dsci = fixtures.NewDSCInitialization(namespace)
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

		It("should successfully create resource and associated feature tracker", func(ctx context.Context) {
			// when
			Expect(testFeature.Apply(ctx)).Should(Succeed())

			// then
			Eventually(createdSecretHasOwnerReferenceToOwningFeature(namespace, secretName, featureName)).
				WithContext(ctx).
				WithTimeout(fixtures.Timeout).
				WithPolling(fixtures.Interval).
				Should(Succeed())
		})

		It("should remove feature tracker on clean-up", func(ctx context.Context) {
			// when
			Expect(testFeature.Cleanup(ctx)).To(Succeed())

			// then
			Consistently(createdSecretHasOwnerReferenceToOwningFeature(namespace, secretName, featureName)).
				WithContext(ctx).
				WithTimeout(fixtures.Timeout).
				WithPolling(fixtures.Interval).
				Should(WithTransform(k8serr.IsNotFound, BeTrue()))
		})

	})

	Context("conditionally enabled features", Ordered, func() {

		const (
			featureName  = "enabled-conditionally"
			secretName   = "test-secret"
			additionalNs = "conditional-ns"
		)

		var (
			nsName        string
			namespace     *corev1.Namespace
			objectCleaner *envtestutil.Cleaner
		)

		BeforeEach(func(ctx context.Context) {
			objectCleaner = envtestutil.CreateCleaner(envTestClient, envTest.Config, fixtures.Timeout, fixtures.Interval)
			nsName = envtestutil.AppendRandomNameTo("test-conditional-cleanup")
			var err error
			namespace, err = cluster.CreateNamespace(ctx, envTestClient, nsName)
			Expect(err).ToNot(HaveOccurred())
		})

		AfterEach(func(ctx context.Context) {
			// Ignore err if 404
			_ = envTestClientset.CoreV1().Secrets(nsName).Delete(ctx, secretName, metav1.DeleteOptions{})
			objectCleaner.DeleteAll(ctx, namespace)
		})

		It("should create feature, apply resource and create feature tracker", func(ctx context.Context) {
			// given
			err := fixtures.CreateOrUpdateNamespace(ctx, envTestClient, fixtures.NewNamespace(additionalNs))
			Expect(err).To(Not(HaveOccurred()))

			feature, conditionalCreationErr := feature.Define(featureName).
				UsingConfig(envTest.Config).
				TargetNamespace(nsName).
				EnabledWhen(namespaceExists(additionalNs)).
				WithResources(fixtures.CreateSecret(secretName, nsName)).
				Create()

			Expect(conditionalCreationErr).ToNot(HaveOccurred())

			// when
			Expect(feature.Apply(ctx)).Should(Succeed())

			// then
			Eventually(createdSecretHasOwnerReferenceToOwningFeature(nsName, secretName, featureName)).
				WithContext(ctx).
				WithTimeout(fixtures.Timeout).
				WithPolling(fixtures.Interval).
				Should(Succeed())
		})

		It("should clean up resources when the condition is no longer met", func(ctx context.Context) {
			// given
			err := envTestClient.Delete(context.Background(), fixtures.NewNamespace(additionalNs))
			Expect(err).To(Not(HaveOccurred()))

			feature, conditionalCreationErr := feature.Define(featureName).
				UsingConfig(envTest.Config).
				TargetNamespace(nsName).
				EnabledWhen(namespaceExists(additionalNs)).
				WithResources(fixtures.CreateSecret(secretName, nsName)).
				Create()

			// Mimic reconcile by re-applying the feature
			Expect(conditionalCreationErr).ToNot(HaveOccurred())
			Expect(feature.Apply(ctx)).Should(Succeed())

			// then
			Consistently(createdSecretHasOwnerReferenceToOwningFeature(nsName, secretName, featureName)).
				WithContext(ctx).
				WithTimeout(fixtures.Timeout).
				WithPolling(fixtures.Interval).
				Should(WithTransform(k8serr.IsNotFound, BeTrue()))

			Consistently(func(ctx context.Context) error {
				_, getErr := fixtures.GetFeatureTracker(ctx, envTestClient, nsName, featureName)
				return client.IgnoreNotFound(getErr)
			}).
				WithContext(ctx).
				WithTimeout(fixtures.Timeout).
				WithPolling(fixtures.Interval).
				Should(Succeed())
		})

	})

})

func createdSecretHasOwnerReferenceToOwningFeature(namespace, secretName, featureName string) func(context.Context) error { //nolint:unparam //reason: secretName
	return func(ctx context.Context) error {
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

func namespaceExists(ns string) func(ctx context.Context, f *feature.Feature) (bool, error) {
	return func(ctx context.Context, f *feature.Feature) (bool, error) {
		namespace, err := fixtures.GetNamespace(ctx, f.Client, ns)
		if k8serr.IsNotFound(err) {
			return false, nil
		}
		// ensuring it fails if namespace is still deleting
		if namespace.Status.Phase == corev1.NamespaceTerminating {
			return false, nil
		}
		return true, nil
	}
}
