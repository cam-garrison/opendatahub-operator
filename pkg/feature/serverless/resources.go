package serverless

import (
	"context"
	"fmt"

	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"

	infrav1 "github.com/opendatahub-io/opendatahub-operator/v2/infrastructure/v1"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/feature"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/gvr"
)

func ServingCertificateResource(f *feature.Feature) error {
	// WithData -> should be a func of how to provide values for templates and these being kept in the generic container

	knativeCertificateSecret, err := feature.GetValue[string](f, "KnativeCertificateSecret")

	if err != nil {
		return fmt.Errorf("failed fetching certificate secret: %w", err)
	}

	knativeIngressDomain, err := feature.GetValue[string](f, "KnativeIngressDomain")
	if err != nil {
		return fmt.Errorf("failed fetching ingress domain: %w", err)
	}

	// WithData(ServiceMeshSpecProvider) -> func Mapper[T](t *T) map[string]any
	// feature.GetData[ServiceMeshSpec]() -> ServiceMeshSpace

	servingSpec, err := feature.GetValue[*infrav1.ServingSpec](f, "Serving")
	if err != nil {
		return fmt.Errorf("failed fetching serving spec: %w", err)
	}
	certificateType := servingSpec.IngressGateway.Certificate.Type

	serviceMeshSpec, err := feature.GetValue[*infrav1.ServiceMeshSpec](f, "ServiceMesh")
	if err != nil {
		return fmt.Errorf("failed fetching service mesh spect: %w", err)
	}
	namespace := serviceMeshSpec.ControlPlane.Namespace

	return f.CreateSelfSignedCertificate(knativeCertificateSecret, certificateType, knativeIngressDomain, namespace)
}

func GetDomain(dynamicClient dynamic.Interface) (string, error) {
	cluster, err := dynamicClient.Resource(gvr.OpenshiftIngress).Get(context.TODO(), "cluster", metav1.GetOptions{})
	if err != nil {
		return "", err
	}

	domain, found, err := unstructured.NestedString(cluster.Object, "spec", "domain")
	if !found {
		return "", errors.New("spec.domain not found")
	}
	return domain, err
}
