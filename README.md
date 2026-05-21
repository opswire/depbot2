# dockerimageparser

Go-пакет для поиска и обновления Docker-образов в различных форматах файлов.

## Стратегии (3 штуки, в порядке приоритета)

| Стратегия  | Файлы                                              | Метод поиска          |
|------------|----------------------------------------------------|-----------------------|
| `dockerfile` | `Dockerfile`, `Dockerfile.*`, `Containerfile`, `Containerfile.*` | `moby/buildkit` parser |
| `pom`      | `pom.xml`                                          | regexp, поддержка multi-line |
| `generic`  | `yaml`, `yml`, `env`, `txt`, `json`, `properties`, `cfg`, `conf`, `ini` + из конфига | regexp |

YAML/docker-compose/k8s-манифесты обрабатываются стратегией `generic` — паттерн `domain/name:version` работает для любого текстового формата.

Образы **без явного домена** (`nginx:1.2.3`, `library/nginx:1.2.3`) и с нон-semver тегами (`latest`, `alpine`, `stable`) пропускаются.

## Структура

```
dockerimageparser/
├── config.go              # Config, DefaultGenericPattern, DefaultGenericExtensions
├── strategy.go            # Strategy interface, ImageRef, ErrContentIdentical
├── update.go              # shouldUpdate, regexpUpdate, parseVersion
├── imageref.go            # parseImageString — разбор domain/name:version@digest
├── strategy_dockerfile.go # Стратегия Dockerfile/Containerfile
├── strategies.go          # Стратегии pom и generic + extractNamedGroups
├── parser.go              # Parser — точка входа
└── parser_test.go         # Тесты всех стратегий
```

## Использование

```go
p, err := dockerimageparser.New(dockerimageparser.DefaultConfig())

// Парсинг
refs, err := p.Parse("docker-compose.yml", content)
// refs[0].Domain  → "ghcr.io"
// refs[0].Name    → "myorg/myapp"
// refs[0].Version → semver 2.1.0
// refs[0].Digest  → "sha256:..." (если есть)

// Обновление — все подходящие образы в файле
newRef := &dockerimageparser.ImageRef{
    Domain:  "ghcr.io",
    Name:    "myorg/myapp",
    Version: semver.MustParse("2.1.5"),
}
updated, err := p.UpdateVersion("docker-compose.yml", content, newRef)
// err == ErrContentIdentical если ни один образ не обновлён
```

## Правила обновления

Образ обновляется если выполнены **все** условия:

- `domain` и `name` совпадают с `newImageRef`
- `major` одинаковый
- `minor` новее **или** `minor` совпадает и `patch` новее

Обновляются **все** подходящие образы в файле. Если ни один не изменился — возвращается `ErrContentIdentical`.

## Конфигурация

```go
cfg := dockerimageparser.Config{
    // Расширения для generic-стратегии (без точки)
    GenericExtensions: []string{"yaml", "yml", "env", "txt", "toml"},

    // Regexp с 4 именованными группами: domain, name, version, digest
    GenericPattern: dockerimageparser.DefaultGenericPattern,
}
p, _ := dockerimageparser.New(cfg)
```

## Формат образа

```
ghcr.io/myorg/myapp:2.1.0@sha256:<64 hex>
│       │              │   └── digest (опционально)
│       │              └────── version (semver обязателен)
│       └───────────────────── name (обязателен)
└───────────────────────────── domain (обязателен, содержит точку)
```

## Зависимости

- [`github.com/moby/buildkit`](https://github.com/moby/buildkit) — парсер Dockerfile (11k+ ⭐)
- [`github.com/Masterminds/semver`](https://github.com/Masterminds/semver) — semver (4k+ ⭐)
