package feature

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ghodss/yaml"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/kustomize/api/krusty"
	"sigs.k8s.io/kustomize/api/resmap"
	"sigs.k8s.io/kustomize/kyaml/filesys"

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
	GetObjs() []*unstructured.Unstructured
	IsPatch() bool
}

type BaseManifest struct {
	name,
	path string
	objs  []*unstructured.Unstructured
	patch bool
	fsys  fs.FS
}

func (b *BaseManifest) IsPatch() bool {
	return b.patch
}

// Ensure BaseManifest implements the Manifest interface.
var _ Manifest = (*BaseManifest)(nil)

func (b *BaseManifest) GetObjs() []*unstructured.Unstructured {
	return b.objs
}

func (b *BaseManifest) Process(_ interface{}) error {
	manifestFile, err := b.fsys.Open(b.path)
	if err != nil {
		return err
	}
	defer manifestFile.Close()

	content, err := io.ReadAll(manifestFile)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}
	resources := string(content)

	var objs []*unstructured.Unstructured

	objs, err = convertToUnstructureds(resources, objs)
	if err != nil {
		return err
	}
	b.objs = objs
	return nil
}

type templateManifest struct {
	name,
	path string
	objs  []*unstructured.Unstructured
	patch bool
	fsys  fs.FS
}

func (t *templateManifest) IsPatch() bool {
	return t.patch
}

func (t *templateManifest) Process(data interface{}) error {
	manifestFile, err := t.fsys.Open(t.path)
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

	resources := buffer.String()
	var objs []*unstructured.Unstructured

	objs, err = convertToUnstructureds(resources, objs)
	if err != nil {
		return err
	}
	t.objs = objs
	return nil
}

func (t *templateManifest) GetObjs() []*unstructured.Unstructured {
	return t.objs
}

// Ensure templateManifest implements the Manifest interface.
var _ Manifest = (*templateManifest)(nil)

type kustomizeManifest struct {
	name,
	path string // path is to the directory containing a kustomization.yaml file within it.
	objs []*unstructured.Unstructured
	fsys filesys.FileSystem // todo: decide if we want to keep - helpful for mocking fs in testing.
}

func (k *kustomizeManifest) IsPatch() bool {
	return false
}

func (k *kustomizeManifest) Process(_ interface{}) error {
	kustomizer := krusty.MakeKustomizer(krusty.MakeDefaultOptions())
	// Create resmap
	// Use kustomization file under manifest path

	var resMap resmap.ResMap
	resMap, resErr := kustomizer.Run(k.fsys, k.path)

	if resErr != nil {
		return fmt.Errorf("error during resmap resources: %w", resErr)
	}

	// todo: here - decide if we need the add labels / replace ns functions that were used in deploy's version.
	// todo: this can be applied for "prometheus" component in monitoring namespace.
	// todo: features+builder API need to open up to accommodate different ns than DSCI.appnamespace + "component" name.
	// note: component name is not always really the component - prometheus is example.

	objs, resErr := deploy.GetResources(resMap)
	if resErr != nil {
		return resErr
	}
	k.objs = objs
	return nil
}

func (k *kustomizeManifest) GetObjs() []*unstructured.Unstructured {
	return k.objs
}

// Ensure kustomizeManifest implements the Manifest interface.
var _ Manifest = (*kustomizeManifest)(nil)

func loadManifestsFrom(fsys fs.FS, path string) ([]Manifest, error) {
	var manifests []Manifest
	if isKustomizeManifest(path) {
		m := CreateKustomizeManifestFrom(path, nil)
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
			m := CreateTemplateManifestFrom(fsys, path)
			manifests = append(manifests, m)
		}
		m := CreateBaseManifestFrom(fsys, path)
		manifests = append(manifests, m)

		return nil
	})

	if err != nil {
		return nil, err
	}

	return manifests, nil
}

func CreateBaseManifestFrom(fsys fs.FS, path string) *BaseManifest {
	basePath := filepath.Base(path)
	m := &BaseManifest{
		name:  basePath,
		path:  path,
		patch: strings.Contains(basePath, ".patch"),
		fsys:  fsys,
	}

	return m
}

func CreateTemplateManifestFrom(fsys fs.FS, path string) *templateManifest {
	basePath := filepath.Base(path)
	m := &templateManifest{
		name:  basePath,
		path:  path,
		patch: strings.Contains(basePath, ".patch"),
		fsys:  fsys,
	}

	return m
}

func CreateKustomizeManifestFrom(path string, fsys filesys.FileSystem) *kustomizeManifest {
	basePath := filepath.Base(path)
	if fsys == nil {
		fsys = filesys.MakeFsOnDisk()
	}
	m := &kustomizeManifest{
		name: basePath,
		path: path,
		fsys: fsys,
	}

	return m
}

// parsing helpers
// todo: support passing the path directly to kustomization.yaml
func isKustomizeManifest(path string) bool {
	_, err := os.Stat(filepath.Join(path, "kustomization.yaml"))
	return err != nil
}

func isTemplateManifest(path string) bool {
	return strings.Contains(path, ".tmpl")
}

func convertToUnstructureds(resources string, objs []*unstructured.Unstructured) ([]*unstructured.Unstructured, error) {
	splitter := regexp.MustCompile(YamlSeparator)
	objectStrings := splitter.Split(resources, -1)
	for _, str := range objectStrings {
		if strings.TrimSpace(str) == "" {
			continue
		}
		u := &unstructured.Unstructured{}
		if err := yaml.Unmarshal([]byte(str), u); err != nil {
			return nil, err
		}

		if !isNamespaceSet(u) {
			return nil, fmt.Errorf("no NS is set on %s", u.GetName())
		}
		objs = append(objs, u)
	}
	return objs, nil
}
