package feature

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"path"
	"path/filepath"
	"strings"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/kustomize/api/krusty"
	"sigs.k8s.io/kustomize/api/resmap"
	"sigs.k8s.io/kustomize/kyaml/filesys"

	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/deploy"
)

//go:embed templates
var embeddedFiles embed.FS

var (
	BaseDir        = "templates"
	ServiceMeshDir = path.Join(BaseDir, "servicemesh")
	ServerlessDir  = path.Join(BaseDir, "serverless")
)

// Manifest defines the interface that all manifest types should implement.
type Manifest interface {
	Process(data interface{}) error
	Open() (fs.File, error)
	Apply(cli client.Client, dyCli dynamic.Interface, metaOptions ...cluster.MetaOptions) error
}

type baseManifest struct {
	name,
	path,
	content string
	patch bool
	fsys  fs.FS
}

// Ensure baseManifest implements the Manifest interface.
var _ Manifest = (*baseManifest)(nil)

func (b baseManifest) Process(_ interface{}) error {
	manifestFile, err := b.Open()
	if err != nil {
		return err
	}
	defer manifestFile.Close()

	content, err := io.ReadAll(manifestFile)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}

	b.content = string(content)
	return nil
}

func (b baseManifest) Open() (fs.File, error) {
	return b.fsys.Open(b.path)
}

func (b baseManifest) Apply(cli client.Client, dyCli dynamic.Interface, metaOptions ...cluster.MetaOptions) error {
	if b.patch {
		return patchResources(dyCli, b.content)
	}
	return createResources(cli, b.content, metaOptions...)
}

type templateManifest struct {
	name,
	path,
	processedContent string
	patch bool
	fsys  fs.FS
}

func (t templateManifest) Apply(cli client.Client, dyCli dynamic.Interface, metaOptions ...cluster.MetaOptions) error {
	if t.patch {
		return patchResources(dyCli, t.processedContent)
	}
	return createResources(cli, t.processedContent, metaOptions...)
}

func (t templateManifest) Process(data interface{}) error {
	manifestFile, err := t.Open()
	if err != nil {
		return err
	}
	defer manifestFile.Close()

	content, err := io.ReadAll(manifestFile)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}

	tmpl, err := template.New(t.name).Funcs(template.FuncMap{"ReplaceChar": ReplaceChar}).Parse(string(content))
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}

	var buffer bytes.Buffer
	if err := tmpl.Execute(&buffer, data); err != nil {
		return err
	}

	t.processedContent = buffer.String()
	return nil
}

func (t templateManifest) Open() (fs.File, error) {
	return t.fsys.Open(t.path)
}

// Ensure templateManifest implements the Manifest interface.
var _ Manifest = (*templateManifest)(nil)

type kustomizeManifest struct {
	name,
	path string
}

func (k kustomizeManifest) Apply(cli client.Client, _ dynamic.Interface, metaOptions ...cluster.MetaOptions) error {
	// TODO: currently am recycling code from deploy.DeployManifestsFromPath
	// todo: determine if we should be calling it instead.
	// calling it directly is awkward since it takes in the namespace, component name etc.
	kustomizer := krusty.MakeKustomizer(krusty.MakeDefaultOptions())
	fsys := filesys.MakeFsOnDisk()
	// Create resmap
	// Use kustomization file under manifestPath or use `default` overlay

	// todo: make sure passing kustomization path not dir path works.
	var resMap resmap.ResMap
	resMap, resErr := kustomizer.Run(fsys, k.path)

	if resErr != nil {
		return fmt.Errorf("error during resmap resources: %w", resErr)
	}

	objs, resErr := deploy.GetResources(resMap)
	if resErr != nil {
		return resErr
	}

	// todo: this bit is awkward - pulling from createResources func since we have unstructured already not strings
	// todo: rework.
	for _, obj := range objs {
		for _, opt := range metaOptions {
			if err := opt(obj); err != nil {
				return err // return immediately if any of the MetaOptions functions fail
			}
		}
		name := obj.GetName()
		namespace := obj.GetNamespace()

		err := cli.Get(context.TODO(), k8stypes.NamespacedName{Name: name, Namespace: namespace}, obj.DeepCopy())
		if err == nil {
			// object already exists
			continue
		}
		if !k8serrors.IsNotFound(err) {
			return err
		}

		err = cli.Create(context.TODO(), obj)
		if err != nil {
			return err
		}
	}
	return nil
}

func (k kustomizeManifest) Process(_ interface{}) error {
	return nil
}

func (k kustomizeManifest) Open() (fs.File, error) {
	return nil, nil
}

// Ensure kustomizeManifest implements the Manifest interface.
var _ Manifest = (*kustomizeManifest)(nil)

func loadManifestsFrom(fsys fs.FS, path string) ([]Manifest, error) {
	var manifests []Manifest
	if isKustomizeManifest(path) {
		m := createKustomizeManifestFrom(path)
		manifests = append(manifests, m)
		return manifests, nil
	}

	err := fs.WalkDir(fsys, path, func(path string, dirEntry fs.DirEntry, walkErr error) error {
		_, err := dirEntry.Info()
		if err != nil {
			return err
		}

		if dirEntry.IsDir() {
			return nil
		}
		if isTemplateManifest(path) {
			m := createTemplateManifestFrom(fsys, path)
			manifests = append(manifests, m)
		}
		m := createBaseManifestFrom(fsys, path)
		manifests = append(manifests, m)

		return nil
	})

	if err != nil {
		return nil, err
	}

	return manifests, nil
}

func createBaseManifestFrom(fsys fs.FS, path string) baseManifest {
	basePath := filepath.Base(path)
	m := baseManifest{
		name:  basePath,
		path:  path,
		patch: strings.Contains(basePath, ".patch"),
		fsys:  fsys,
	}

	return m
}

func createTemplateManifestFrom(fsys fs.FS, path string) templateManifest {
	basePath := filepath.Base(path)
	m := templateManifest{
		name:  basePath,
		path:  path,
		patch: strings.Contains(basePath, ".patch"),
		fsys:  fsys,
	}

	return m
}

func createKustomizeManifestFrom(path string) kustomizeManifest {
	basePath := filepath.Base(path)
	m := kustomizeManifest{
		name: basePath,
		path: path,
	}

	return m
}

// parsing helpers
// TODO: parse if passing dir, check for kustomization file? not sure.
func isKustomizeManifest(path string) bool {
	return strings.Contains(path, "kustomization")
}

func isTemplateManifest(path string) bool {
	return strings.Contains(path, ".tmpl")
}
