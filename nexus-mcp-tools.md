# MCP-сервер для Sonatype Nexus Repository — каталог тулов

Документ описывает набор инструментов (tools), которые имеет смысл реализовать в MCP-сервере
для Sonatype Nexus Repository Manager 3 (OSS и Pro). Каталог собран на основе анализа REST API
Nexus (`/service/rest/v1`) и двух существующих открытых реализаций MCP на GitHub.

> **Область каталога:** приоритеты P0 (ядро) и P1 (важные). Административные и энтерпрайз-тулы
> уровня P2 (управление безопасностью, cleanup/routing, blob-store CRUD, support-zip, Firewall,
> создание/удаление/изменение репозиториев и компонентов) из этого списка исключены.

---

## 1. Что уже есть на GitHub (краткий разбор)

### `brianveltman/sonatype-mcp` (TypeScript, ~7★)
Наиболее полный из публичных серверов. Покрывает и чтение, и запись, имеет режим read-only
и опциональную интеграцию с Sonatype Firewall.

Реализованные тулы (чтение): `list_repositories`, `get_repository`, `search_components`,
`get_component`, `get_component_versions`, `upload_component`, `upload_asset`, `get_system_status`,
`list_blob_stores`, `list_tasks`, `get_usage_metrics`.

Сильные стороны: разделение read/write через режим, support-zip.
Что можно улучшить: нет отдельного поиска ассетов и поиска по контрольной сумме, нет скачивания
ассетов.

### `addozhang/nexus-mcp-server` (Python / FastMCP, ~1★)
Узкоспециализированный, только чтение. 6 тулов, заточен под форматы:
- Maven: `search_maven_artifact`, `get_maven_versions`
- Python/PyPI: `search_python_package`, `get_python_versions`
- Docker: `list_docker_images`, `get_docker_tags`

Сильные стороны: продуманная пагинация (`continuation_token`), аутентификация через HTTP-заголовки
(без хардкода секретов), формат-специфичные удобные обёртки.
Что можно улучшить: нет generic-поиска, нет скачивания.

**Вывод:** ни одно решение не покрывает нужные сценарии целиком. Оптимально взять разделение
read/write и модель аутентификации/пагинации из этих проектов и добавить недостающее: отдельный
поиск ассетов, поиск по SHA и скачивание дистрибутива.

---

## 2. Принципы отбора тулов

1. **Разделять чтение и запись.** Все мутирующие тулы должны быть доступны только во «write mode»
   (флаг запуска `--read-only=false`). По умолчанию сервер работает в read-only — это безопасно
   для подключения к проду.
2. **Одна операция REST ≈ один тул**, но там где формат-специфика важна (Maven/npm/Docker/PyPI),
   давать дополнительно удобные обёртки поверх generic-поиска.
3. **Пагинация обязательна** для всех списочных операций — Nexus использует `continuationToken`,
   и без него агент упрётся в первые 50 записей.
4. **Не дублировать то, что агент сделает сам.**

Приоритеты в каталоге ниже:
- 🟢 **P0** — must-have, ядро ценности (поиск, репозитории, компоненты, статус).
- 🟡 **P1** — сильно повышают полезность (скачивание/загрузка, форматные обёртки, задачи, blob stores, метрики).

---

## 3. Каталог тулов

### 3.1 Поиск (Search) — 🟢 P0

Ядро сервера. Базируется на `GET /service/rest/v1/search` и `.../search/assets`.
Покрывает **поиск дистрибутива** и **поиск дистрибутива по SHA**.

#### `nexus_search_components`
Поиск компонентов (дистрибутивов) по всем или конкретному репозиторию — generic, работает для
всех форматов.

| Поле | Тип | Обяз. | Описание |
|------|-----|:---:|----------|
| `query` | string | нет | Ключевое слово (`q`), ищет по имени/группе/версии |
| `repository` | string | нет | Ограничить поиск одним репозиторием |
| `format` | string | нет | maven2, npm, docker, pypi, raw, nuget, … |
| `group` | string | нет | groupId (Maven) / scope (npm) |
| `name` | string | нет | Имя артефакта/пакета |
| `version` | string | нет | Версия |
| `sort` | enum | нет | `group`, `name`, `version`, `repository` |
| `continuation_token` | string | нет | Токен следующей страницы |

*REST:* `GET /v1/search`

#### `nexus_search_assets`
Поиск отдельных ассетов (файлов дистрибутива) с путями, контрольными суммами и размерами.
**Именно этот тул реализует поиск по SHA** — заполните одно из полей контрольной суммы.

| Поле | Тип | Обяз. | Описание |
|------|-----|:---:|----------|
| `query` | string | нет | Ключевое слово |
| `repository` | string | нет | Репозиторий |
| `format` | string | нет | Формат |
| `sha1` | string | нет | **Поиск по SHA-1** |
| `sha256` | string | нет | **Поиск по SHA-256** |
| `sha512` | string | нет | **Поиск по SHA-512** |
| `md5` | string | нет | Поиск по MD5 |
| `maven_group_id`, `maven_artifact_id`, `maven_base_version`, `maven_extension`, `maven_classifier` | string | нет | Maven-специфичные фильтры |
| `docker_image_name`, `docker_image_tag` | string | нет | Docker-специфичные фильтры |
| `continuation_token` | string | нет | Пагинация |

*REST:* `GET /v1/search/assets` (параметры `assets.attributes.checksum.sha1` и т.д.)

#### `nexus_search_asset_download_url` — 🟡 P1
Найти единственный ассет по критериям (включая SHA) и вернуть прямой URL для скачивания
(эндпоинт делает 302 redirect). Удобно для «дай ссылку на последний jar» и как способ **скачать
дистрибутив по контрольной сумме**.

| Поле | Тип | Обяз. | Описание |
|------|-----|:---:|----------|
| `repository` | string | нет | Репозиторий |
| `format` | string | нет | Формат |
| `sha1` / `sha256` | string | нет | Найти конкретный файл по SHA |
| `group`, `name`, `version` | string | нет | Координаты для сужения до одного ассета |

*REST:* `GET /v1/search/assets/download`

---

### 3.2 Компоненты и ассеты (Components / Assets) — 🟢 P0 / 🟡 P1

Получение параметров и содержимого дистрибутивов. `/v1/components`, `/v1/assets`.

#### `nexus_list_components` — 🟢 P0
Постранично перечислить все компоненты (дистрибутивы) в конкретном репозитории.

| Поле | Тип | Обяз. | Описание |
|------|-----|:---:|----------|
| `repository` | string | **да** | Имя репозитория |
| `continuation_token` | string | нет | Пагинация |

*REST:* `GET /v1/components?repository={name}`

#### `nexus_get_component` — 🟢 P0
**Получение параметров дистрибутива** по внутреннему ID: группа, имя, версия, формат, список
ассетов с их контрольными суммами.

| Поле | Тип | Обяз. | Описание |
|------|-----|:---:|----------|
| `id` | string | **да** | Внутренний ID компонента Nexus |

*REST:* `GET /v1/components/{id}`

#### `nexus_upload_component` — 🟡 P1 (write)
Загрузить (upload) компонент с ассетами в hosted-репозиторий. Поля зависят от формата.

| Поле | Тип | Обяз. | Описание |
|------|-----|:---:|----------|
| `repository` | string | **да** | Целевой hosted-репозиторий |
| `format` | string | **да** | Формат (определяет остальные поля) |
| `file_path` | string | **да** | Путь к загружаемому файлу |
| `group_id`, `artifact_id`, `version`, `packaging`, `classifier`, `extension` | string | зависит | Maven-координаты |
| `directory` | string | нет | Путь внутри raw-репозитория |

*REST:* `POST /v1/components?repository={name}` (multipart/form-data)

#### `nexus_get_asset` — 🟡 P1
Получить параметры одного ассета по ID: путь, checksum (SHA-1/256/512, MD5), размер, дата, blob.

| Поле | Тип | Обяз. | Описание |
|------|-----|:---:|----------|
| `id` | string | **да** | ID ассета |

*REST:* `GET /v1/assets/{id}`

#### `nexus_list_assets` — 🟡 P1
Перечислить ассеты репозитория постранично.

| Поле | Тип | Обяз. | Описание |
|------|-----|:---:|----------|
| `repository` | string | **да** | Репозиторий |
| `continuation_token` | string | нет | Пагинация |

*REST:* `GET /v1/assets?repository={name}`

#### `nexus_download_asset` — 🟡 P1
**Скачать дистрибутив** — содержимое ассета по прямому content-URL
(`{nexus}/repository/{repo}/{path}`) в локальный файл. Не отдельный REST-эндпоинт v1, а обёртка
над content-URL с basic-auth.

| Поле | Тип | Обяз. | Описание |
|------|-----|:---:|----------|
| `repository` | string | **да** | Репозиторий |
| `path` | string | **да** | Путь к ассету внутри репозитория |
| `dest_path` | string | нет | Куда сохранить локально |

---

### 3.3 Форматные обёртки (удобство для агента) — 🟡 P1

Тонкие обёртки над `search`, которые сильно упрощают жизнь модели: понятные параметры,
готовая пагинация версий, сортировка по semver. Опционально, но заметно повышают UX.

#### `nexus_get_maven_versions`
Все версии Maven-артефакта, отсортированные, с пагинацией.
Поля: `group_id` (**да**), `artifact_id` (**да**), `repository`, `page_size`, `continuation_token`.

#### `nexus_get_latest_version`
Последняя (или последняя release/snapshot) версия артефакта/пакета.
Поля: `format`, `group`/`name` (**да**), `repository`, `include_prerelease` (bool).

#### `nexus_list_docker_images` / `nexus_get_docker_tags`
Список Docker-образов в репозитории / все теги образа.
Поля: `repository` (**да**), `image_name` (для тегов, **да**).

#### `nexus_get_npm_versions` / `nexus_get_pypi_versions`
Аналогично Maven, но для npm и PyPI.
Поля: `package_name` (**да**), `repository`, `continuation_token`.

---

### 3.4 Репозитории (Repositories) — 🟢 P0

Обзор репозиториев. `/v1/repositories`, `/v1/repositorySettings`.

#### `nexus_list_repositories` — 🟢 P0
Список всех репозиториев: имя, формат (maven2/npm/docker/…), тип (hosted/proxy/group), URL, online.

| Поле | Тип | Обяз. | Описание |
|------|-----|:---:|----------|
| `format` | string | нет | Фильтр по формату (клиентский) |
| `type` | enum | нет | hosted / proxy / group |

*REST:* `GET /v1/repositories` (и `GET /v1/repositorySettings` для полной конфигурации)

#### `nexus_get_repository` — 🟢 P0
Полная конфигурация одного репозитория (storage, blob store, cleanup, proxy-настройки и т.д.).

| Поле | Тип | Обяз. | Описание |
|------|-----|:---:|----------|
| `repository_name` | string | **да** | Имя репозитория |

*REST:* `GET /v1/repositories/{format}/{type}/{name}` или `GET /v1/repositorySettings`

---

### 3.5 Задачи (Scheduled Tasks) — 🟡 P1

`/v1/tasks`. Мониторинг обслуживающих задач (cleanup, compact blob store, repair).

#### `nexus_list_tasks` — 🟡 P1
Список запланированных задач: id, имя, тип, статус (WAITING/RUNNING), последний/следующий запуск.
Поле: `type` (нет) — фильтр по типу задачи.

*REST:* `GET /v1/tasks`

#### `nexus_get_task` — 🟡 P1
Детали одной задачи по ID.
Поле: `task_id` (**да**).

*REST:* `GET /v1/tasks/{id}`

---

### 3.6 Хранилище (Blob Stores) — 🟡 P1

`/v1/blobstores`. Мониторинг физического хранения.

#### `nexus_list_blob_stores` — 🟡 P1
Список blob stores: имя, тип (File/S3), занято/доступно, число блобов.

*REST:* `GET /v1/blobstores`

#### `nexus_get_blob_store_quota_status` — 🟡 P1
Проверить статус квоты blob store (не превышен ли лимит) — важно для мониторинга.
Поле: `blob_store_name` (**да**).

*REST:* `GET /v1/blobstores/{name}/quota-status`

---

### 3.7 Система и мониторинг (System / Status) — 🟢 P0

#### `nexus_get_system_status` — 🟢 P0
Здоровье инстанса: доступность, writable, версия, edition (OSS/Pro).
Комбинирует `GET /v1/status` и `/v1/status/writable`.

*REST:* `GET /v1/status` · `GET /v1/status/writable`

#### `nexus_get_read_only_state` — 🟡 P1
Узнать, в read-only ли система (режим обслуживания). Полезно перед записью.

*REST:* `GET /v1/read-only`

#### `nexus_get_usage_metrics` — 🟡 P1
Метрики использования: суммарное число компонентов, суточные запросы, размер хранилища.
Требует привилегии `nexus:metrics:read`.

*REST:* `GET /service/metrics/data` (частично Pro)

---

## 4. Рекомендуемый минимальный набор (MVP)

Если начинать с малого, реализуйте в первую очередь эти 🟢 P0-тулы — они дают основную ценность
при read-only подключении, безопасном для прода:

1. `nexus_search_components` — поиск дистрибутива
2. `nexus_search_assets` — поиск ассетов/файлов и **поиск по SHA**
3. `nexus_list_repositories` — обзор репозиториев
4. `nexus_get_repository` — детали репозитория
5. `nexus_list_components` — содержимое репозитория
6. `nexus_get_component` — **получение параметров дистрибутива**
7. `nexus_get_system_status` — здоровье инстанса

Затем добавляйте 🟡 P1: `nexus_download_asset` и `nexus_search_asset_download_url`
(**скачивание дистрибутива**), форматные обёртки, задачи, blob stores, метрики.

---

## 5. Технические рекомендации

- **База API:** `{nexus_url}/service/rest/v1`. Swagger доступен на `{nexus_url}/service/rest/swagger.json`
  и UI — на `{nexus_url}/#admin/system/api` (`/swagger-ui/`). Сгенерируйте клиент из OpenAPI-спеки
  конкретной версии, чтобы не расходиться с реальным API.
- **Аутентификация:** HTTP Basic (`username:password`) или user token. Лучше передавать креденшелы
  через переменные окружения / заголовки, а не хардкодить (подход `addozhang`).
- **Пагинация:** почти все списки отдают `continuationToken`; тулы должны принимать и возвращать его,
  иначе агент увидит только первую страницу.
- **Read-only по умолчанию:** мутирующие тулы (`upload`) прятать за флагом `--read-only=false`.
- **Поиск по SHA:** в `GET /v1/search/assets` используйте параметр
  `assets.attributes.checksum.sha1` (или `sha256`/`sha512`/`md5`). Для скачивания найденного файла —
  `GET /v1/search/assets/download` с теми же параметрами.
- **Скачивание:** прямой content-URL `{nexus}/repository/{repo}/{path}` требует basic-auth;
  учитывайте 302-redirect на blob store.
- **OSS vs Pro:** часть метрик и теги компонентов доступны только в Pro. Проверяйте edition через
  `nexus_get_system_status` и деградируйте мягко.
- **Обработка ошибок:** маппить 401/403 (нет прав/привилегии), 404 (нет объекта), 422 (валидация)
  в понятные агенту сообщения.

---

## Источники

- [brianveltman/sonatype-mcp (GitHub)](https://github.com/brianveltman/sonatype-mcp)
- [addozhang/nexus-mcp-server (GitHub)](https://github.com/addozhang/nexus-mcp-server)
- [Sonatype — REST APIs](https://help.sonatype.com/en/rest-apis.html)
- [Sonatype — Search API](https://help.sonatype.com/en/search-api.html)
- [Sonatype — Repositories API](https://help.sonatype.com/en/repositories-api.html)
- [Model Context Protocol](https://modelcontextprotocol.io/)
