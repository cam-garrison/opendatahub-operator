package serverless

import (
	"fmt"
	"strings"

	infrav1 "github.com/opendatahub-io/opendatahub-operator/v2/infrastructure/v1"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/feature"
)

const DefaultCertificateSecretName = "knative-serving-cert"

func ServingDefaultValues(f *feature.Feature) error {
	servingSpec, ok := f.Spec["Serving"].(*infrav1.ServingSpec)
	if !ok {
		return fmt.Errorf("serving spec does not exist or is of incorrect type")
	}

	certificateSecretName := strings.TrimSpace(servingSpec.IngressGateway.Certificate.SecretName)
	if len(certificateSecretName) == 0 {
		certificateSecretName = DefaultCertificateSecretName
	}

	f.Spec["KnativeCertificateSecret"] = certificateSecretName
	return nil
}

func ServingIngressDomain(f *feature.Feature) error {
	servingSpec, ok := f.Spec["Serving"].(*infrav1.ServingSpec)
	if !ok {
		return fmt.Errorf("serving spec does not exist or is of incorrect type")
	}

	domain := strings.TrimSpace(servingSpec.IngressGateway.Domain)
	if len(domain) == 0 {
		var errDomain error
		domain, errDomain = GetDomain(f.DynamicClient)
		if errDomain != nil {
			return fmt.Errorf("failed to fetch OpenShift domain to generate certificate for Serverless: %w", errDomain)
		}

		domain = "*." + domain
	}

	f.Spec["KnativeIngressDomain"] = domain
	return nil
}
