// Package dockerimageparser предоставляет набор стратегий для поиска и обновления
// Docker-образов в различных форматах файлов (Dockerfile, pom.xml, YAML, generic).
package dockerimageparser

import (
	"errors"
	"fmt"

	"github.com/Masterminds/semver/v3"
)

// ErrContentIdentical возвращается когда UpdateVersion не внёс никаких изменений.
var ErrContentIdentical = errors.New("content identical")

// ImageRef описывает Docker-образ, найденный в файле.
type ImageRef struct {
	Domain  string          // Реестр, например docker.io, ghcr.io
	Name    string          // Имя образа, например library/nginx
	Version *semver.Version // Версия в формате semver
	Digest  string          // sha256:..., может быть пустым
}

// String возвращает полное имя образа.
func (r *ImageRef) String() string {
	s := fmt.Sprintf("%s/%s:%s", r.Domain, r.Name, r.Version.Original())
	if r.Digest != "" {
		s += "@" + r.Digest
	}
	return s
}

// Strategy — интерфейс стратегии парсинга файла.
type Strategy interface {
	// Name возвращает имя стратегии.
	Name() string

	// Match определяет, применима ли стратегия к данному файлу по его пути/имени.
	Match(path string) bool

	// Parse извлекает все образы с валидным semver из содержимого файла.
	Parse(content []byte) ([]*ImageRef, error)

	// UpdateVersion обновляет версию образа в содержимом файла.
	// Обновление применяется ко всем образам, у которых:
	//   - совпадают domain и name с newImageRef,
	//   - major совпадает, а minor выше или minor совпадает и patch выше.
	// Возвращает ErrContentIdentical если ни один образ не был обновлён.
	UpdateVersion(content []byte, newImageRef *ImageRef) ([]byte, error)
}
