package dockerimageparser

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/goccy/go-yaml"
	goyamlast "github.com/goccy/go-yaml/ast"
	goyamlparser "github.com/goccy/go-yaml/parser"
)

// kustomizeImage — один элемент списка images: в kustomization.yaml.
type kustomizeImage struct {
	Name    string `yaml:"name"`
	NewName string `yaml:"newName"`
	NewTag  string `yaml:"newTag"`
	Digest  string `yaml:"digest"`
}

// kustomizeFile — минимальная структура kustomization.yaml.
type kustomizeFile struct {
	Images []kustomizeImage `yaml:"images"`
}

// kustomizeStrategy обрабатывает kustomization.yaml / kustomization.yml.
// Образ разбит на поля: name (domain/imageName без тега), newTag, digest.
type kustomizeStrategy struct{}

func (kustomizeStrategy) Name() string { return "kustomize" }

func (kustomizeStrategy) Match(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	return base == "kustomization.yaml" || base == "kustomization.yml"
}

func (s kustomizeStrategy) Parse(content []byte) ([]*ImageRef, error) {
	var kf kustomizeFile
	if err := yaml.Unmarshal(content, &kf); err != nil {
		return nil, fmt.Errorf("kustomize parse: %w", err)
	}
	var refs []*ImageRef
	for _, img := range kf.Images {
		if ref, ok := s.toRef(img); ok {
			refs = append(refs, ref)
		}
	}
	return refs, nil
}

// toRef собирает ImageRef из полей одной kustomize-записи.
// name содержит domain/imageName без тега; тег берётся из newTag.
func (kustomizeStrategy) toRef(img kustomizeImage) (*ImageRef, bool) {
	imageName := img.Name
	if img.NewName != "" {
		imageName = img.NewName
	}
	if img.NewTag == "" {
		return nil, false
	}
	suffix := ":" + img.NewTag
	if img.Digest != "" {
		suffix += "@" + img.Digest
	}
	return parseImageString(imageName + suffix)
}

func (s kustomizeStrategy) UpdateVersion(content []byte, newRef *ImageRef) ([]byte, error) {
	var kf kustomizeFile
	if err := yaml.Unmarshal(content, &kf); err != nil {
		return nil, fmt.Errorf("kustomize parse: %w", err)
	}

	// Парсим AST один раз — будем точечно менять newTag через YAMLPath.
	file, err := goyamlparser.ParseBytes(content, goyamlparser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("kustomize ast parse: %w", err)
	}

	updated := false
	for i, img := range kf.Images {
		old, ok := s.toRef(img)
		if !ok || !shouldUpdate(old, newRef) {
			continue
		}
		// YAMLPath: $.images[i].newTag
		path, err := yaml.PathString(fmt.Sprintf("$.images[%d].newTag", i))
		if err != nil {
			return nil, fmt.Errorf("kustomize path: %w", err)
		}
		newNode, err := goyamlparser.ParseBytes([]byte(newRef.Version.Original()), 0)
		if err != nil {
			return nil, fmt.Errorf("kustomize value parse: %w", err)
		}
		if err := path.ReplaceWithNode(file, newNode.Docs[0].Body); err != nil {
			return nil, fmt.Errorf("kustomize replace: %w", err)
		}
		updated = true
	}

	if !updated {
		return nil, ErrContentIdentical
	}
	return []byte(file.String()), nil
}

// nodeString извлекает строковое значение из AST-узла без кавычек.
func nodeString(n goyamlast.Node) string {
	s := n.String()
	return strings.Trim(s, `"'`)
}
