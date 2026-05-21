package dockerimageparser

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/moby/buildkit/frontend/dockerfile/parser"
)

// dockerfileStrategy парсит Dockerfile/Containerfile через официальный парсер moby/buildkit.
type dockerfileStrategy struct{}

func (dockerfileStrategy) Name() string { return "dockerfile" }

// Match — матчим Dockerfile, Containerfile и их варианты (Dockerfile.dev, .dockerignore не нужен).
func (dockerfileStrategy) Match(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	return strings.HasPrefix(base, "dockerfile") || strings.HasPrefix(base, "containerfile")
}

func (s dockerfileStrategy) Parse(content []byte) ([]*ImageRef, error) {
	result, err := parser.Parse(bytes.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("dockerfile parse: %w", err)
	}

	var refs []*ImageRef
	for _, node := range result.AST.Children {
		if !strings.EqualFold(node.Value, "from") {
			continue
		}
		if node.Next == nil {
			continue
		}
		ref, ok := parseImageString(node.Next.Value)
		if ok {
			refs = append(refs, ref)
		}
	}
	return refs, nil
}

func (s dockerfileStrategy) UpdateVersion(content []byte, newImageRef *ImageRef) ([]byte, error) {
	result, err := parser.Parse(bytes.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("dockerfile parse: %w", err)
	}

	updated := false
	lines := strings.Split(string(content), "\n")

	for _, node := range result.AST.Children {
		if !strings.EqualFold(node.Value, "from") || node.Next == nil {
			continue
		}
		old, ok := parseImageString(node.Next.Value)
		if !ok || !shouldUpdate(old, newImageRef) {
			continue
		}
		// Строки 1-based в AST
		lineIdx := node.StartLine - 1
		if lineIdx < 0 || lineIdx >= len(lines) {
			continue
		}
		lines[lineIdx] = replaceFROMImage(lines[lineIdx], node.Next.Value, newImageRef.String())
		updated = true
	}

	if !updated {
		return nil, ErrContentIdentical
	}
	newContent := []byte(strings.Join(lines, "\n"))
	if bytes.Equal(newContent, content) {
		return nil, ErrContentIdentical
	}
	return newContent, nil
}

// replaceFROMImage заменяет старый образ на новый в строке FROM.
func replaceFROMImage(line, oldImage, newImage string) string {
	// Заменяем только первое вхождение, сохраняя AS alias если есть
	return strings.Replace(line, oldImage, newImage, 1)
}
