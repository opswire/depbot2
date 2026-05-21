package dockerimageparser

import (
	"fmt"
	"regexp"
)

// Config определяет настройки парсера.
// Все regexp-паттерны хранятся здесь — стратегии получают скомпилированные *regexp.Regexp.
type Config struct {
	// Расширения файлов для Generic-парсера (без точки).
	// По умолчанию: yaml, yml, txt, env, json, properties, cfg, conf, ini.
	GenericExtensions []string

	// GenericPattern — regexp для поиска образов в generic-файлах.
	// Должен содержать именованные группы: domain, name, version, digest.
	GenericPattern string

	// PomPattern — regexp для поиска образов в pom.xml.
	// Должен содержать именованные группы: domain, name, version, digest.
	PomPattern string
}

// Паттерны по умолчанию — содержат 4 именованные группы: domain, name, version, digest.

// DefaultGenericPattern покрывает yaml (image: …), .env (KEY=…), plain-text и другие форматы.
const DefaultGenericPattern = `(?P<domain>[a-zA-Z0-9](?:[a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?(?:\.[a-zA-Z]{2,})+(?::\d+)?)/(?P<name>[a-zA-Z0-9][a-zA-Z0-9._\-/]*):(?P<version>[a-zA-Z0-9][a-zA-Z0-9.\-_+]*)(?:@(?P<digest>sha256:[a-fA-F0-9]{64}))?`

// DefaultPomPattern находит образы в тегах jib (<from><image>, <to><image>)
// и docker-maven-plugin (<imageName>, <baseImage>).
// Флаг (?s) позволяет . матчить переносы строк — поддержка multi-line значений.
const DefaultPomPattern = `(?s)<(?:image|imageName|baseImage|from)>\s*` +
	`(?P<domain>[a-zA-Z0-9](?:[a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?(?:\.[a-zA-Z]{2,})+(?::\d+)?)/` +
	`(?P<name>[a-zA-Z0-9][a-zA-Z0-9._\-/]*):` +
	`(?P<version>[a-zA-Z0-9][a-zA-Z0-9.\-_+]*)` +
	`(?:@(?P<digest>sha256:[a-fA-F0-9]{64}))?` +
	`\s*</`

// DefaultGenericExtensions — расширения, обрабатываемые Generic-парсером.
// yaml/yml включены сюда — отдельной стратегии для них нет.
var DefaultGenericExtensions = []string{
	"yaml", "yml",
	"txt", "env", "json", "properties", "cfg", "conf", "ini",
}

// DefaultConfig возвращает конфиг с настройками по умолчанию.
func DefaultConfig() Config {
	return Config{
		GenericExtensions: DefaultGenericExtensions,
		GenericPattern:    DefaultGenericPattern,
		PomPattern:        DefaultPomPattern,
	}
}

// compiledConfig хранит скомпилированные regexp — создаётся один раз в New().
type compiledConfig struct {
	genericRe *regexp.Regexp
	pomRe     *regexp.Regexp
	cfg       Config
}

func compileConfig(cfg Config) (*compiledConfig, error) {
	if cfg.GenericPattern == "" {
		cfg.GenericPattern = DefaultGenericPattern
	}
	if cfg.PomPattern == "" {
		cfg.PomPattern = DefaultPomPattern
	}
	if len(cfg.GenericExtensions) == 0 {
		cfg.GenericExtensions = DefaultGenericExtensions
	}

	genericRe, err := regexp.Compile(cfg.GenericPattern)
	if err != nil {
		return nil, fmt.Errorf("generic pattern compile: %w", err)
	}
	pomRe, err := regexp.Compile(cfg.PomPattern)
	if err != nil {
		return nil, fmt.Errorf("pom pattern compile: %w", err)
	}
	return &compiledConfig{genericRe: genericRe, pomRe: pomRe, cfg: cfg}, nil
}
