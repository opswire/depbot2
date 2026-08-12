# Artifacts (вызов тулинга) vs Text Patch — по экосистемам

## Критерий

Есть ли у формата **лок-файл/checksum-файл**, который транзитивно зависит от всего дерева зависимостей?

- **Да** → Renovate не пытается реверс-инжинирить формат, а вызывает реальный CLI пакетного менеджера, чтобы он сам пересчитал лок-файл ("artifacts" стадия).
- **Нет** → зависимость объявлена плоско, без транзитивного резолвинга → прямая текстовая/YAML-замена значения на месте, без вызова внешних процессов.

## Таблица

| Экосистема | Manager | Основной файл | Лок/checksum файл | Тип обновления | Что вызывается |
|---|---|---|---|---|---|
| Go | `gomod` | `go.mod` | `go.sum` | **Tooling** | `go get`, опц. `go mod tidy`, `go mod vendor` |
| npm | `npm` | `package.json` | `package-lock.json` | **Tooling** | `npm install` |
| Yarn | `npm` | `package.json` | `yarn.lock` | **Tooling** | `yarn` (через Corepack) |
| pnpm | `npm` | `package.json` | `pnpm-lock.yaml` | **Tooling** | `pnpm install` |
| Python (Poetry) | `poetry` | `pyproject.toml` | `poetry.lock` | **Tooling** | `poetry lock` / `poetry update` |
| Python (pip-compile) | `pip-compile` | `requirements.in` | `requirements.txt` | **Tooling** | `pip-compile` / `uv pip compile` |
| Python (Pipenv) | `pipenv` | `Pipfile` | `Pipfile.lock` | **Tooling** | `pipenv lock` |
| Python (plain pip) | `pip_requirements` | `requirements.txt` | — нет — | **Текст** | нет, прямая замена версии |
| Helm (чарт) | `helmv3` | `Chart.yaml` | `Chart.lock` | **Tooling** | `helm dependency update` |
| Helm (values) | `helm-values` | `values.yaml` | — нет — | **Текст** | нет, YAML-замена `tag`/`repository` |
| Docker | `dockerfile` | `Dockerfile` | — нет — | **Текст** | нет, замена тега/digest в строке `FROM` |
| Docker Compose | `docker-compose` | `docker-compose.yml` | — нет — | **Текст** | нет |
| Maven | `maven` | `pom.xml` | — нет (обычно) — | **Текст** | нет, замена `<version>` |
| Rust | `cargo` | `Cargo.toml` | `Cargo.lock` | **Tooling** | `cargo update` |
| Ruby | `bundler` | `Gemfile` | `Gemfile.lock` | **Tooling** | `bundle lock` |
| PHP | `composer` | `composer.json` | `composer.lock` | **Tooling** | `composer update` |

## Почему это важно для самописного решения

- **Text-группа** — можно писать один generic-патчер (regex/YAML-path → найти → заменить offset в файле), без git-клонирования: многие API git-вендоров позволяют читать/писать файл напрямую, без полного `clone`.
- **Tooling-группа** — без вариантов нужен shallow/partial `git clone`, установленный бинарник нужной версии в окружении, и правильно проброшенные креды приватных registry в переменные окружения/конфиги, которые понимает сам инструмент (а не универсальный формат Renovate).
