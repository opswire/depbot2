package dockerimageparser

import "fmt"

// Parser выбирает подходящую стратегию по пути файла и делегирует парсинг/обновление.
// Стратегии проверяются в порядке: Dockerfile → pom.xml → generic.
type Parser struct {
	strategies []Strategy
}

// New создаёт Parser: компилирует regexp из конфига и инициализирует стратегии.
func New(cfg Config) (*Parser, error) {
	cc, err := compileConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &Parser{
		strategies: []Strategy{
			dockerfileStrategy{},
			pomStrategy{re: cc.pomRe},
			genericStrategy{extensions: cc.cfg.GenericExtensions, re: cc.genericRe},
		},
	}, nil
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
