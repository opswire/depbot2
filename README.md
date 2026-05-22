# dockerimageparser

Go-пакет для поиска и обновления Docker-образов в различных форматах файлов.

## Стратегии

Стратегия выбирается автоматически по имени файла в порядке приоритета:

| Приоритет | Стратегия    | Файлы                                                        | Формат образа                            |
|-----------|--------------|--------------------------------------------------------------|------------------------------------------|
| 1         | `dockerfile` | `Dockerfile`, `Dockerfile.*`, `Containerfile`, `Containerfile.*` | `FROM domain/name:version`           |
| 2         | `kustomize`  | `kustomization.yaml`, `kustomization.yml`                    | `name` + `newTag` (разбитые поля)        |
| 3         | `helm`       | `values.yaml`, `values.yml`, `values-*.yaml`, `values-*.yml` | `repository` + `tag` (разбитые поля)    |
| 4         | `generic`    | `yaml`, `yml`, `xml`, `env`, `txt`, `json`, `properties`, … | `domain/name:version@digest` (regexp)   |

**Образы без явного домена** (`nginx:1.2.3`) и с нон-semver тегами (`latest`, `alpine`, `stable`) пропускаются.

## Форматы образов по стратегиям

### Dockerfile / Containerfile
```dockerfile
FROM docker.io/library/nginx:1.25.3
FROM ghcr.io/myorg/myapp:2.1.0 AS builder
FROM docker.io/library/nginx:1.25.3@sha256:<64 hex>
```

### Kustomize
```yaml
images:
  - name: ghcr.io/myorg/myapp      # domain/name без тега
    newTag: "2.1.0"
  - name: old.registry.io/org/svc
    newName: ghcr.io/org/svc        # опционально — переопределяет имя
    newTag: "1.4.0"
    digest: sha256:<64 hex>         # опционально
```

### Helm values
```yaml
image:
  repository: ghcr.io/myorg/myapp  # domain/name
  tag: "2.1.0"

sidecar:
  image:
    repository: ghcr.io/myorg/sidecar
    tag: "1.0.3"
```

### Generic (yaml, xml, env, txt, …)
```yaml
# docker-compose / k8s manifest
image: ghcr.io/myorg/myapp:2.1.0

# pom.xml (jib / docker-maven-plugin)
<image>gcr.io/distroless/java17:1.0.0</image>

# .env
BASE_IMAGE=docker.io/library/debian:12.1.0@sha256:<64 hex>
```

## Использование

```go
// С настройками по умолчанию
p, err := dockerimageparser.New(dockerimageparser.DefaultConfig())

// Из YAML-файла конфигурации
p, err := dockerimageparser.NewFromFile("config.yaml")

// Парсинг
refs, err := p.Parse("kustomization.yaml", content)
// refs[0].Domain  → "ghcr.io"
// refs[0].Name    → "myorg/myapp"
// refs[0].Version → semver 2.1.0
// refs[0].Digest  → "sha256:..." (если есть)

// Обновление — все подходящие образы в файле
newRef := &dockerimageparser.ImageRef{
    Domain:  "ghcr.io",
    Name:    "myorg/myapp",
    Version: semver.MustParse("2.2.0"),
}
updated, err := p.UpdateVersion("values.yaml", content, newRef)
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
    GenericExtensions: []string{"yaml", "yml", "xml", "env", "txt"},

    // Regexp с 4 именованными группами: domain, name, version, digest
    GenericPattern: dockerimageparser.DefaultGenericPattern,
}
p, _ := dockerimageparser.New(cfg)
```

Или через YAML-файл (`config.yaml`):

```yaml
generic_extensions: [yaml, yml, xml, env, txt, json]
generic_pattern: '(?P<domain>...)...'
```

```go
p, err := dockerimageparser.NewFromFile("config.yaml")
```

## Структура пакета

```
dockerimageparser/
├── config.go                  # Config, паттерны по умолчанию, compiledConfig
├── config.yaml                # Дефолтный конфиг — можно редактировать
├── strategy.go                # Strategy interface, ImageRef, ErrContentIdentical
├── imageref.go                # parseImageString
├── update.go                  # shouldUpdate, regexpUpdate, parseVersion
├── parser.go                  # Parser — точка входа, New / NewFromFile
├── strategy_dockerfile.go     # Dockerfile / Containerfile
├── strategy_kustomize.go      # kustomization.yaml (name + newTag)
├── strategy_helm.go           # values.yaml (repository + tag)
├── strategies.go              # genericStrategy + extractNamedGroups
└── testdata/fixtures/         # Тестовые данные
    ├── Dockerfile
    ├── kustomization.yaml
    ├── values.yaml
    ├── values-prod.yaml
    ├── pom-inline.xml
    ├── pom-multiline.xml
    ├── pom-with-digest.xml
    ├── pom-no-valid-images.xml
    ├── docker-compose.yml
    └── images.env
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
- [`github.com/goccy/go-yaml`](https://github.com/goccy/go-yaml) — YAML-парсер с поддержкой YAMLPath (900+ ⭐)
- [`github.com/stretchr/testify`](https://github.com/stretchr/testify) — тесты (23k+ ⭐)
