package dockerimageparser

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
	goyamlparser "github.com/goccy/go-yaml/parser"
)

// helmStrategy обрабатывает Helm values.yaml / values-*.yaml.
// Образ разбит на поля repository (domain/name) и tag в одном YAML-объекте:
//
//	image:
//	  repository: ghcr.io/myorg/myapp
//	  tag: "2.1.5"
//
// Стратегия рекурсивно обходит весь файл в поисках таких пар.
type helmStrategy struct{}

func (helmStrategy) Name() string { return "helm" }

func (helmStrategy) Match(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	return base == "values.yaml" || base == "values.yml" ||
		strings.HasPrefix(base, "values-") &&
			(strings.HasSuffix(base, ".yaml") || strings.HasSuffix(base, ".yml"))
}

func (s helmStrategy) Parse(content []byte) ([]*ImageRef, error) {
	var root any
	if err := yaml.Unmarshal(content, &root); err != nil {
		return nil, fmt.Errorf("helm parse: %w", err)
	}
	var refs []*ImageRef
	walkHelmMap(root, func(repo, tag string) {
		if ref, ok := parseImageString(repo + ":" + tag); ok {
			refs = append(refs, ref)
		}
	})
	return refs, nil
}

func (s helmStrategy) UpdateVersion(content []byte, newRef *ImageRef) ([]byte, error) {
	var root any
	if err := yaml.Unmarshal(content, &root); err != nil {
		return nil, fmt.Errorf("helm parse: %w", err)
	}

	paths := collectHelmTagPaths(root, "$", newRef)
	if len(paths) == 0 {
		return nil, ErrContentIdentical
	}

	file, err := goyamlparser.ParseBytes(content, goyamlparser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("helm ast parse: %w", err)
	}

	for _, p := range paths {
		ypath, err := yaml.PathString(p)
		if err != nil {
			return nil, fmt.Errorf("helm path %q: %w", p, err)
		}
		newNode, err := goyamlparser.ParseBytes([]byte(newRef.Version.Original()), 0)
		if err != nil {
			return nil, fmt.Errorf("helm value parse: %w", err)
		}
		if err := ypath.ReplaceWithNode(file, newNode.Docs[0].Body); err != nil {
			return nil, fmt.Errorf("helm replace %q: %w", p, err)
		}
	}

	result := []byte(file.String())
	if string(result) == string(content) {
		return nil, ErrContentIdentical
	}
	return result, nil
}

// walkHelmMap рекурсивно обходит map[string]any в поисках пар repository+tag.
// Ключи сортируются для детерминированного порядка результатов.
func walkHelmMap(v any, found func(repo, tag string)) {
	m, ok := v.(map[string]any)
	if !ok {
		return
	}
	repo, hasRepo := stringVal(m["repository"])
	tag, hasTag := stringVal(m["tag"])
	if hasRepo && hasTag && repo != "" && tag != "" {
		found(repo, tag)
		return // не спускаемся глубже внутрь блока с образом
	}
	// Сортируем ключи для стабильного порядка
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		walkHelmMap(m[k], found)
	}
}

// collectHelmTagPaths возвращает YAMLPath до поля tag для каждого блока,
// где repository+tag соответствуют newRef и подходят для обновления.
func collectHelmTagPaths(v any, prefix string, newRef *ImageRef) []string {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}

	repo, hasRepo := stringVal(m["repository"])
	tag, hasTag := stringVal(m["tag"])
	if hasRepo && hasTag {
		if ref, ok := parseImageString(repo + ":" + tag); ok && shouldUpdate(ref, newRef) {
			return []string{prefix + ".tag"}
		}
		return nil // блок найден, но не подходит — не спускаемся глубже
	}

	var paths []string
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		paths = append(paths, collectHelmTagPaths(m[k], prefix+"."+k, newRef)...)
	}
	return paths
}

// stringVal безопасно приводит any к строке.
func stringVal(v any) (string, bool) {
	if v == nil {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}
