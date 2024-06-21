package scheme

import (
	"github.com/hashicorp/go-multierror"
	ofapiv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	dsci "github.com/opendatahub-io/opendatahub-operator/v2/apis/dscinitialization/v1"
	featurev1 "github.com/opendatahub-io/opendatahub-operator/v2/apis/features/v1"
)

// AddToScheme adds all resources to the Scheme.
func AddToScheme(s *runtime.Scheme) (*runtime.Scheme, *multierror.Error) {
	var multiErr *multierror.Error

	utilruntime.Must(clientgoscheme.AddToScheme(s))
	multiErr = multierror.Append(multiErr, apiextv1.AddToScheme(s))
	multiErr = multierror.Append(multiErr, ofapiv1alpha1.AddToScheme(s))
	multiErr = multierror.Append(multiErr, featurev1.AddToScheme(s))
	multiErr = multierror.Append(multiErr, dsci.AddToScheme(s))

	return s, multiErr
}
