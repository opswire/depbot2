package dockerimageparser

import (
	"strings"
)

// parseImageString разбирает строку вида "domain/name:version@digest".
// Domain обязателен — строки без домена (например, "nginx:1.2.3") пропускаются.
func parseImageString(s string) (*ImageRef, bool) {
	// Отделяем digest (@sha256:...)
	var digest string
	if idx := strings.Index(s, "@"); idx != -1 {
		digest = s[idx+1:]
		s = s[:idx]
	}

	// Разделяем на image и тег
	var tag string
	if idx := strings.LastIndex(s, ":"); idx != -1 {
		tag = s[idx+1:]
		s = s[:idx]
	}

	// Должен быть домен — минимум одна точка в первом сегменте пути или "localhost"
	slashIdx := strings.Index(s, "/")
	if slashIdx == -1 {
		// Нет слэша — нет домена, пропускаем (например просто "nginx")
		return nil, false
	}

	possibleDomain := s[:slashIdx]
	// Домен содержит точку или это localhost/localhost:port
	if !strings.Contains(possibleDomain, ".") && !strings.HasPrefix(possibleDomain, "localhost") {
		return nil, false
	}

	domain := possibleDomain
	name := s[slashIdx+1:]

	if domain == "" || name == "" || tag == "" {
		return nil, false
	}

	v, ok := parseVersion(tag)
	if !ok {
		return nil, false
	}

	return &ImageRef{
		Domain:  domain,
		Name:    name,
		Version: v,
		Digest:  digest,
	}, true
}
