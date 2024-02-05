/*
Copyright (c) 2016-2017 Bitnami
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package feature

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/ghodss/yaml"
	"github.com/pkg/errors"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster"
)

const (
	YamlSeparator = "(?m)^---[ \t]*$"
)

func createResources(cli client.Client, resources string, metaOptions ...cluster.MetaOptions) error {
	splitter := regexp.MustCompile(YamlSeparator)
	objectStrings := splitter.Split(resources, -1)
	for _, str := range objectStrings {
		if strings.TrimSpace(str) == "" {
			continue
		}
		u := &unstructured.Unstructured{}
		if err := yaml.Unmarshal([]byte(str), u); err != nil {
			return errors.WithStack(err)
		}

		if !isNamespaceSet(u) {
			return fmt.Errorf("no NS is set on %s", u.GetName())
		}

		for _, opt := range metaOptions {
			if err := opt(u); err != nil {
				return err // return immediately if any of the MetaOptions functions fail
			}
		}

		name := u.GetName()
		namespace := u.GetNamespace()

		err := cli.Get(context.TODO(), k8stypes.NamespacedName{Name: name, Namespace: namespace}, u.DeepCopy())
		if err == nil {
			// object already exists
			continue
		}
		if !k8serrors.IsNotFound(err) {
			return errors.WithStack(err)
		}

		err = cli.Create(context.TODO(), u)
		if err != nil {
			return errors.WithStack(err)
		}
	}

	return nil
}

func patchResources(dyCli dynamic.Interface, resources string) error {
	splitter := regexp.MustCompile(YamlSeparator)
	objectStrings := splitter.Split(resources, -1)
	for _, str := range objectStrings {
		if strings.TrimSpace(str) == "" {
			continue
		}
		u := &unstructured.Unstructured{}
		if err := yaml.Unmarshal([]byte(str), u); err != nil {
			return errors.WithStack(err)
		}

		if !isNamespaceSet(u) {
			return fmt.Errorf("no NS is set on %s", u.GetName())
		}

		gvr := schema.GroupVersionResource{
			Group:    strings.ToLower(u.GroupVersionKind().Group),
			Version:  u.GroupVersionKind().Version,
			Resource: strings.ToLower(u.GroupVersionKind().Kind) + "s",
		}

		// Convert the individual resource patch from YAML to JSON
		patchAsJSON, err := yaml.YAMLToJSON([]byte(str))
		if err != nil {
			return errors.WithStack(err)
		}

		_, err = dyCli.Resource(gvr).
			Namespace(u.GetNamespace()).
			Patch(context.TODO(), u.GetName(), k8stypes.MergePatchType, patchAsJSON, metav1.PatchOptions{})
		if err != nil {
			return errors.WithStack(err)
		}

		if err != nil {
			return errors.WithStack(err)
		}
	}

	return nil
}

func isNamespaceSet(u *unstructured.Unstructured) bool {
	namespace := u.GetNamespace()
	if u.GetKind() != "Namespace" && namespace == "" {
		return false
	}
	return true
}
