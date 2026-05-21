package dockerimageparser

import (
	"bytes"
	"path/filepath"
	"regexp"
	"strings"
)

// ---- pom.xml ----

// pomStrategy обрабатывает Maven pom.xml (плагины jib и docker-maven-plugin).
// Паттерн поиска задаётся через Config.PomPattern.
type pomStrategy struct {
	re *regexp.Regexp
}

func (pomStrategy) Name() string { return "pom" }

func (pomStrategy) Match(path string) bool {
	return strings.ToLower(filepath.Base(path)) == "pom.xml"
}

func (s pomStrategy) Parse(content []byte) ([]*ImageRef, error) {
	var refs []*ImageRef
	for _, match := range s.re.FindAllSubmatch(content, -1) {
		if ref, ok := extractNamedGroups(s.re, match); ok {
			refs = append(refs, ref)
		}
	}
	return refs, nil
}

func (s pomStrategy) UpdateVersion(content []byte, newImageRef *ImageRef) ([]byte, error) {
	return regexpUpdate(content, s.re, newImageRef, func(old, new *ImageRef, match []byte) []byte {
		// Заменяем только строку образа; пробелы/переносы вокруг сохраняются
		return bytes.Replace(match, []byte(old.String()), []byte(new.String()), 1)
	})
}

// ---- Generic ----

// genericStrategy обрабатывает произвольные текстовые файлы через Config.GenericPattern.
// Покрывает: yaml/yml (image: …), .env, .txt, .json, .properties и другие.
type genericStrategy struct {
	extensions []string
	re         *regexp.Regexp
}

func (genericStrategy) Name() string { return "generic" }

func (s genericStrategy) Match(path string) bool {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	for _, e := range s.extensions {
		if ext == e {
			return true
		}
	}
	return false
}

func (s genericStrategy) Parse(content []byte) ([]*ImageRef, error) {
	var refs []*ImageRef
	for _, match := range s.re.FindAllSubmatch(content, -1) {
		if ref, ok := extractNamedGroups(s.re, match); ok {
			refs = append(refs, ref)
		}
	}
	return refs, nil
}

func (s genericStrategy) UpdateVersion(content []byte, newImageRef *ImageRef) ([]byte, error) {
	return regexpUpdate(content, s.re, newImageRef, func(old, new *ImageRef, match []byte) []byte {
		return bytes.Replace(match, []byte(old.String()), []byte(new.String()), 1)
	})
}

// extractNamedGroups извлекает ImageRef из именованных групп regexp-совпадения.
func extractNamedGroups(re *regexp.Regexp, match [][]byte) (*ImageRef, bool) {
	if match == nil {
		return nil, false
	}
	g := make(map[string]string, len(re.SubexpNames()))
	for i, name := range re.SubexpNames() {
		if name != "" && i < len(match) {
			g[name] = string(match[i])
		}
	}
	if g["domain"] == "" || g["name"] == "" || g["version"] == "" {
		return nil, false
	}
	v, ok := parseVersion(g["version"])
	if !ok {
		return nil, false
	}
	return &ImageRef{Domain: g["domain"], Name: g["name"], Version: v, Digest: g["digest"]}, true
}
