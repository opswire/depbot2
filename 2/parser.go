package dockerimageparser

import (
	"fmt"
	"os"

	"github.com/goccy/go-yaml"
)

// Parser выбирает подходящую стратегию по пути файла.
// Стратегии проверяются в порядке: Dockerfile → Kustomize → Helm → generic.
type Parser struct {
	strategies []Strategy
}

// New создаёт Parser из Config.
func New(cfg Config) (*Parser, error) {
	cc, err := compileConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &Parser{
		strategies: []Strategy{
			dockerfileStrategy{},
			kustomizeStrategy{},
			helmStrategy{},
			genericStrategy{extensions: cc.cfg.GenericExtensions, re: cc.genericRe},
		},
	}, nil
}

// NewFromFile создаёт Parser, загружая Config из YAML-файла.
// Незаполненные поля подставляются из DefaultConfig.
func NewFromFile(path string) (*Parser, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg := DefaultConfig()
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return New(cfg)
}

// Parse извлекает все Docker-образы из содержимого файла.
func (p *Parser) Parse(path string, content []byte) ([]*ImageRef, error) {
	s, err := p.strategyFor(path)
	if err != nil {
		return nil, err
	}
	return s.Parse(content)
}

// UpdateVersion обновляет версию образа в содержимом файла.
func (p *Parser) UpdateVersion(path string, content []byte, newRef *ImageRef) ([]byte, error) {
	s, err := p.strategyFor(path)
	if err != nil {
		return nil, err
	}
	return s.UpdateVersion(content, newRef)
}

// strategyFor возвращает первую подходящую стратегию или ошибку.
func (p *Parser) strategyFor(path string) (Strategy, error) {
	for _, s := range p.strategies {
		if s.Match(path) {
			return s, nil
		}
	}
	return nil, fmt.Errorf("no strategy matched for file: %s", path)
}
