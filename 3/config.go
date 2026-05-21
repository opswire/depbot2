package dockerimageparser

import (
	"fmt"
	"regexp"
)

// Config определяет настройки парсера.
// Все regexp-паттерны хранятся здесь — стратегии получают скомпилированные *regexp.Regexp.
// Поля соответствуют ключам в config.yaml.
type Config struct {
	// Расширения файлов для Generic-парсера (без точки).
	GenericExtensions []string `yaml:"generic_extensions"`

	// GenericPattern — regexp для поиска образов в generic-файлах.
	// Должен содержать именованные группы: domain, name, version, digest.
	GenericPattern string `yaml:"generic_pattern"`
}

// DefaultGenericPattern покрывает yaml (image: …), .env (KEY=…), plain-text, xml и другие форматы.
const DefaultGenericPattern = `(?P<domain>[a-zA-Z0-9](?:[a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?(?:\.[a-zA-Z]{2,})+(?::\d+)?)/(?P<name>[a-zA-Z0-9][a-zA-Z0-9._\-/]*):(?P<version>[a-zA-Z0-9][a-zA-Z0-9.\-_+]*)(?:@(?P<digest>sha256:[a-fA-F0-9]{64}))?`

// DefaultGenericExtensions — расширения, обрабатываемые Generic-парсером.
// yaml/yml и xml включены — отдельных стратегий для них нет.
var DefaultGenericExtensions = []string{
	"yaml", "yml", "xml",
	"txt", "env", "json", "properties", "cfg", "conf", "ini",
}

// DefaultConfig возвращает конфиг с настройками по умолчанию.
func DefaultConfig() Config {
	return Config{
		GenericExtensions: DefaultGenericExtensions,
		GenericPattern:    DefaultGenericPattern,
	}
}

// compiledConfig хранит скомпилированные regexp — создаётся один раз в New().
type compiledConfig struct {
	genericRe *regexp.Regexp
	cfg       Config
}

func compileConfig(cfg Config) (*compiledConfig, error) {
	if cfg.GenericPattern == "" {
		cfg.GenericPattern = DefaultGenericPattern
	}
	if len(cfg.GenericExtensions) == 0 {
		cfg.GenericExtensions = DefaultGenericExtensions
	}
	re, err := regexp.Compile(cfg.GenericPattern)
	if err != nil {
		return nil, fmt.Errorf("generic pattern compile: %w", err)
	}
	return &compiledConfig{genericRe: re, cfg: cfg}, nil
}
