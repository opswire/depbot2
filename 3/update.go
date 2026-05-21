package dockerimageparser

import (
	"bytes"
	"regexp"

	"github.com/Masterminds/semver/v3"
)

// shouldUpdate возвращает true, если old нужно заменить на new.
// Условия: одинаковые domain и name; major совпадает;
// minor выше, либо minor совпадает и patch выше.
func shouldUpdate(old, new *ImageRef) bool {
	if old.Domain != new.Domain || old.Name != new.Name {
		return false
	}
	o, n := old.Version, new.Version
	if o.Major() != n.Major() {
		return false
	}
	return n.Minor() > o.Minor() ||
		(n.Minor() == o.Minor() && n.Patch() > o.Patch())
}

// regexpUpdate — общий механизм обновления для regexp-based стратегий.
// buildReplacement формирует новый байтовый фрагмент взамен найденного совпадения.
// Обновляет все подходящие образы; возвращает ErrContentIdentical если ничего не изменилось.
func regexpUpdate(
	content []byte,
	re *regexp.Regexp,
	newImageRef *ImageRef,
	buildReplacement func(old, new *ImageRef, match []byte) []byte,
) ([]byte, error) {
	updated := false
	result := re.ReplaceAllFunc(content, func(match []byte) []byte {
		old, ok := extractNamedGroups(re, re.FindSubmatch(match))
		if !ok || !shouldUpdate(old, newImageRef) {
			return match
		}
		replacement := buildReplacement(old, newImageRef, match)
		if !bytes.Equal(replacement, match) {
			updated = true
		}
		return replacement
	})
	if !updated || bytes.Equal(result, content) {
		return nil, ErrContentIdentical
	}
	return result, nil
}

// parseVersion парсит semver-тег, отбрасывая нон-semver и нежелательные теги.
func parseVersion(tag string) (*semver.Version, bool) {
	switch tag {
	case "latest", "alpine", "stable", "edge", "slim", "bullseye", "bookworm", "buster":
		return nil, false
	}
	v, err := semver.NewVersion(tag)
	if err != nil {
		return nil, false
	}
	return v, true
}
