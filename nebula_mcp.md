# MCP-сервер для NebulaGraph на Go — проектный документ

Источники: [nebula-graph.io](https://nebula-graph.io/) (продукт), [github.com/vesoft-inc/nebula-go](https://github.com/vesoft-inc/nebula-go) (официальный Go-клиент, Apache-2.0, пакет `nebula_go` / `nebula-go/v3`).

## 1. Контекст продукта и реальный API клиента

NebulaGraph — распределённая графовая СУБД, рассчитанная на графы с триллионами рёбер/вершин и миллисекундными задержками. Клиент общается со слоем **Graphd** по бинарному протоколу **fbthrift**; отдельного HTTP/REST API у самой БД нет — есть только графовая **сессия** и язык **nGQL**.

Я прочитал исходники `nebula-go` (`session_pool.go`, `session.go`, `connection_pool.go`, `result_set.go`, `value_wrapper.go`) и ниже — реальная, а не гипотетическая карта методов, на которую опирается каталог тулов в разделе 3.

### 1.1. Открытие соединения — два пути

- **`SessionPool`** (`session_pool.go`) — пул, привязанный к одному пользователю/паролю/space, создаётся через `nebula_go.NewSessionPool(conf SessionPoolConf, log Logger) (*SessionPool, error)`. Ограничения, задокументированные прямо в коде: запрос не должен быть «голым» `USE <space>`; после выполнения space откатывается на дефолтный из конфига; через пул **нельзя** менять пароль пользователя или дропать юзера.
- **`ConnectionPool` + `Session`** (`connection_pool.go`, `session.go`) — низкоуровневый вариант. `nebula_go.NewConnectionPool(addresses, conf, log)` / `NewSslConnectionPool(...)` создают пул TCP-соединений; `pool.GetSession(username, password string) (*Session, error)` выполняет thrift-вызов `Authenticate` и возвращает сессию. Сессию обязательно нужно вернуть через `session.Release()`.

### 1.2. Выполнение запросов (общее и для Pool, и для Session)

Оба типа реализуют один и тот же набор методов выполнения — **это единственный низкоуровневый интерфейс, всё остальное строится поверх него**:

```go
Execute(stmt string) (*ResultSet, error)
ExecuteWithParameter(stmt string, params map[string]any) (*ResultSet, error)
ExecuteWithTimeout(stmt string, timeoutMs int64) (*ResultSet, error)
ExecuteWithParameterTimeout(stmt string, params map[string]any, timeoutMs int64) (*ResultSet, error)
ExecuteAndCheck(stmt string) (*ResultSet, error) // как Execute, но сразу возвращает error, если ResultSet.IsSucceed() == false
```

`Execute` — просто обёртка над `ExecuteWithParameter(stmt, map[string]any{})`. Важно: `SessionPool.ExecuteJson`/`ExecuteJsonWithParameter` **не реализованы** (в коде буквально `return nil, fmt.Errorf("not implemented")`) — для JSON-вывода нужно использовать `Session.ExecuteJson(stmt) ([]byte, error)` (у `Session` эти методы рабочие), либо самостоятельно сериализовать `ResultSet`.

Готовые обёртки поверх `Execute*`, которые есть только у `Session` (не у `SessionPool`):

```go
func (session *Session) CreateSpace(conf SpaceConf) (*ResultSet, error)
func (session *Session) ShowSpaces() ([]SpaceName, error)
func (session *Session) Ping() error
func (session *Session) GetSessionID() int64
func (session *Session) Release()
```
где `SpaceConf{ Name, Partition uint, Replica uint, VidType string, IgnoreIfExists bool, Comment string }` — под капотом сама собирает `CREATE SPACE [IF NOT EXISTS] <name> (partition_num=.., replica_factor=.., vid_type=..) [COMMENT="..."]` и вызывает `ExecuteAndCheck`.

### 1.3. Разбор результата

`ResultSet` (`result_set.go`) даёт как «сырой», так и типизированный доступ:

```go
GetColNames() []string
GetRows() []*nebula.Row
GetValuesByColName(colName string) ([]*ValueWrapper, error)
GetRowValuesByIndex(index int) (*Record, error)
Scan(v any) error            // маппинг строк в []struct с тегами `nebula:"..."`
AsStringTable() [][]string   // готовая текстовая таблица
IsSucceed() / IsEmpty() / GetErrorMsg() / GetLatencyInMs() / GetSpaceName()
```

`ValueWrapper` (`value_wrapper.go`) типизирует конкретное значение ячейки: `AsBool/AsInt/AsFloat/AsString/AsDate/AsDateTime/AsList/AsMap/AsDuration/AsGeography`, а также графовые типы — `AsNode() (*Node, error)`, `AsRelationship() (*Relationship, error)`, `AsPath() (*PathWrapper, error)`. У `Node` есть `GetID()/GetTags()/Properties(tagName)`, у `Relationship` — `GetSrcVertexID()/GetDstVertexID()/GetEdgeName()/Properties()`, у `PathWrapper` — `GetNodes()/GetRelationships()/GetPathLength()`. Именно через эти методы обработчик MCP-тула превращает `ResultSet` в JSON, понятный LLM.

Вывод для раздела 3: подавляющее большинство тулов на уровне nebula-go — это **`ExecuteWithParameter(stmt, params)`** на `SessionPool` или `Session` с разными nGQL-шаблонами; отдельные "именованные" Go-методы есть только для `CreateSpace`, `ShowSpaces`, `Ping`, `Release`, `GetSessionID`.

## 2. Таблица тулов

| Тул | Сценарий использования | Приоритет |
|---|---|---|
| `nebula_executeQuery` | Универсальное выполнение произвольного nGQL-запроса (в т.ч. составленного самой LLM) — «аварийный клапан» для всего, что не покрыто специализированными тулами | **P0** |
| `nebula_listSpaces` | Узнать, какие графовые пространства есть на кластере, прежде чем что-то запрашивать | **P0** |
| `nebula_getSchema` | Получить полную схему space (теги, рёбра, индексы) — нужно LLM для «заземления» перед генерацией запросов | **P0** |
| `nebula_getNeighbors` | Обойти соседей вершины по ребру заданного типа (базовая графовая навигация, аналог `GO FROM ... OVER ...`) | **P0** |
| `nebula_fetchVertex` | Получить все свойства конкретной вершины (или списка вершин) по VID | **P0** |
| `nebula_insertVertex` | Создать/перезаписать вершину с заданными тегами и свойствами | **P0** |
| `nebula_insertEdge` | Создать/перезаписать ребро между двумя вершинами | **P0** |
| `nebula_findPath` | Найти кратчайший/все пути между двумя вершинами | **P0** |
| `nebula_describeTag` | Получить детальное описание структуры конкретного тега (типы свойств, default, nullable) | P1 |
| `nebula_describeEdge` | То же самое для типа ребра | P1 |
| `nebula_getSubgraph` | Извлечь N-hop подграф вокруг вершины (для визуализации/анализа окружения) | P1 |
| `nebula_upsertVertex` | Идемпотентно обновить свойства вершины без риска затереть отсутствующие поля | P1 |
| `nebula_deleteVertex` | Удалить вершину (и опционально инцидентные рёбра) | P1 |
| `nebula_deleteEdge` | Удалить конкретное ребро | P1 |
| `nebula_lookupByIndex` | Найти вершины/рёбра по значению индексированного свойства (без обхода графа) | P1 |
| `nebula_batchInsert` | Пакетная вставка большого числа вершин/рёбер за один вызов (bulk import) | P1 |
| `nebula_createSpace` | Создать новое графовое пространство (partition_num, replica_factor, vid_type) | P1 |
| `nebula_createTag` | Создать новый тип вершины (DDL) | P1 |
| `nebula_createEdge` | Создать новый тип ребра (DDL) | P1 |
| `nebula_createIndex` | Создать индекс по свойствам тега/ребра | P1 |
| `nebula_listIndexes` | Посмотреть, какие индексы уже существуют | P1 |
| `nebula_dropSpace` | Удалить графовое пространство целиком (деструктивная операция) | P2 |
| `nebula_dropTag` / `nebula_dropEdge` | Удалить тип вершины/ребра из схемы | P2 |
| `nebula_alterTag` / `nebula_alterEdge` | Изменить структуру существующего тега/ребра (добавить/удалить/переименовать свойство) | P2 |
| `nebula_rebuildIndex` | Перестроить индекс после массовых изменений данных | P2 |
| `nebula_submitJob` | Запустить служебные job'ы (STATS, COMPACT, FLUSH, BALANCE) | P2 |
| `nebula_showHosts` | Показать состояние узлов кластера (storaged/graphd), их статус и загрузку space'ов | P2 |
| `nebula_showStats` | Получить агрегированную статистику по space (число вершин/рёбер по типам — требует предварительного `SUBMIT JOB STATS`) | P2 |
| `nebula_explainQuery` | `EXPLAIN`/`PROFILE` запроса — анализ плана выполнения для оптимизации медленных запросов | P2 |
| `nebula_showSessions` / `nebula_killSession` | Мониторинг и принудительное завершение зависших сессий на кластере | P2 |
| `nebula_healthCheck` | Пинг кластера/пула сессий для health-check интеграции самого MCP-сервера | P2 |

## 3. Каталог тулов

Для каждого тула: входные данные (JSON-schema тула), точный метод `nebula-go` и nGQL-шаблон, который в него передаётся. Везде, где в запрос попадают значения от пользователя/LLM (не идентификаторы схемы), используется `ExecuteWithParameter(stmt, params)` с именованными `$параметрами`, а не конкатенация строк — это единственный встроенный в клиент механизм защиты от nGQL-инъекций.

### P0 — ядро

**`nebula_executeQuery`**
- Вход: `query: string`, `params?: map[string]any`, `space?: string`, `readOnly?: bool`.
- nebula-go: `sessionPool.ExecuteWithParameter(query, params) (*ResultSet, error)` — для read-only контекста; для DDL/admin-запросов — `session.ExecuteWithParameter(query, params)` на сессии, полученной через `connectionPool.GetSession(adminUser, adminPassword)`. Разбор ответа — `ResultSet.GetColNames()` + `GetValuesByColName`, либо `ResultSet.AsStringTable()` для компактного текстового вывода.

**`nebula_listSpaces`**
- Вход: без параметров.
- nebula-go: `session.ShowSpaces() ([]SpaceName, error)` — готовая обёртка над `ExecuteAndCheck("SHOW SPACES;")` + `ResultSet.Scan(&names)`. При работе через `SessionPool` (без выделенной admin-сессии) — `sessionPool.Execute("SHOW SPACES")`.

**`nebula_getSchema`**
- Вход: `space: string`.
- nebula-go: последовательность `sessionPool.Execute("SHOW TAGS")`, `Execute("SHOW EDGES")`, `Execute("SHOW INDEXES")`, затем для каждого найденного имени — `ExecuteWithParameter("DESCRIBE TAG <tag>", nil)` / `DESCRIBE EDGE`; результаты (`ResultSet.GetRows()`/`GetColNames()` на каждом вызове) агрегируются обработчиком тула в один JSON-объект схемы.

**`nebula_getNeighbors`**
- Вход: `vid: string`, `edgeTypes: string[]`, `direction?: "out"|"in"|"bidirect"`, `yieldProps?: string[]`, `limit?: int`.
- nebula-go: `sessionPool.ExecuteWithParameter("GO FROM $vid OVER edge1,edge2 [REVERSELY|BIDIRECT] YIELD ...", map[string]any{"vid": vid})`. Разбор: колонки со свойствами через `ResultSet.GetValuesByColName`, при YIELD вершин/рёбер — `ValueWrapper.AsNode()`/`AsRelationship()`.

**`nebula_fetchVertex`**
- Вход: `tag: string`, `vids: string[]`.
- nebula-go: `sessionPool.ExecuteWithParameter("FETCH PROP ON <tag> $vids YIELD properties(vertex)", map[string]any{"vids": vids})`. Для типизированного результата — `ResultSet.Scan(&structSlice)` с Go-структурой, размеченной тегами `nebula:"prop_name"` (см. пример `Person` в README), либо через `ValueWrapper.AsNode()` → `Node.Properties(tag)`.

**`nebula_insertVertex`**
- Вход: `vid: string`, `tags: [{ tag: string, properties: map[string]any }]`.
- nebula-go: `sessionPool.ExecuteWithParameter("INSERT VERTEX <tag>(<propNames>) VALUES $vid:(<propPlaceholders>)", params)`.

**`nebula_insertEdge`**
- Вход: `edgeType: string`, `srcVid: string`, `dstVid: string`, `rank?: int`, `properties: map[string]any`.
- nebula-go: `sessionPool.ExecuteWithParameter("INSERT EDGE <edgeType>(<propNames>) VALUES $src -> $dst[@rank]:(<propPlaceholders>)", params)`.

**`nebula_findPath`**
- Вход: `srcVid: string`, `dstVid: string`, `edgeTypes?: string[]`, `mode?: "shortest"|"all"|"noloop"`, `maxSteps?: int`.
- nebula-go: `sessionPool.ExecuteWithParameter("FIND [SHORTEST|ALL|NOLOOP] PATH FROM $src TO $dst OVER <edges> UPTO <maxSteps> STEPS YIELD path AS p", params)`. Разбор: `ResultSet.GetValuesByColName("p")` → `ValueWrapper.AsPath()` → `PathWrapper.GetNodes()/GetRelationships()/GetPathLength()`.

### P1 — расширение

**`nebula_describeTag`** / **`nebula_describeEdge`**
- Вход: `name: string`.
- nebula-go: `sessionPool.Execute("DESCRIBE TAG <name>")` / `Execute("DESCRIBE EDGE <name>")`.

**`nebula_getSubgraph`**
- Вход: `vid: string`, `steps?: int`, `edgeTypes?: string[]`, `direction?: "out"|"in"|"bidirect"`.
- nebula-go: `sessionPool.ExecuteWithParameter("GET SUBGRAPH [WITH PROP] <steps> STEPS FROM $vid [IN|OUT|BOTH <edges>] YIELD VERTICES AS nodes, EDGES AS relationships", params)`. Разбор: колонки `nodes`/`relationships` → `AsList()` → каждый элемент `AsNode()`/`AsRelationship()`.

**`nebula_upsertVertex`**
- Вход: `tag: string`, `vid: string`, `set: map[string]any`, `whenCondition?: string`.
- nebula-go: `sessionPool.ExecuteWithParameter("UPSERT VERTEX ON <tag> $vid SET prop1 = $val1, ... [WHEN <cond>]", params)`.

**`nebula_deleteVertex`**
- Вход: `vids: string[]`, `withEdges?: bool`.
- nebula-go: `sessionPool.ExecuteWithParameter("DELETE VERTEX $vids [WITH EDGE]", map[string]any{"vids": vids})`.

**`nebula_deleteEdge`**
- Вход: `edgeType: string`, `srcVid: string`, `dstVid: string`, `rank?: int`.
- nebula-go: `sessionPool.ExecuteWithParameter("DELETE EDGE <edgeType> $src -> $dst[@rank]", params)`.

**`nebula_lookupByIndex`**
- Вход: `tagOrEdge: string`, `condition: string | {property, op, value}[]`, `yieldProps?: string[]`, `limit?: int`.
- nebula-go: `sessionPool.ExecuteWithParameter("LOOKUP ON <tagOrEdge> WHERE <condition> YIELD ...", params)` — требует существующего индекса на используемом свойстве (см. `nebula_createIndex`).

**`nebula_batchInsert`**
- Вход: `vertices?: [{tag, vid, properties}]`, `edges?: [{edgeType, srcVid, dstVid, rank?, properties}]`, `batchSize?: int`.
- nebula-go: серия вызовов `sessionPool.ExecuteWithParameter("INSERT VERTEX ... VALUES v1:(...), v2:(...), ...", params)` / аналогично для рёбер, пачками по `batchSize`, с ретраями на уровне обработчика тула (сам клиент батчинг не делает — это multi-VALUES nGQL-запрос).

**`nebula_createSpace`**
- Вход: `name: string`, `partitionNum?: int`, `replicaFactor?: int`, `vidType: string`, `ignoreIfExists?: bool`, `comment?: string`.
- nebula-go: **`session.CreateSpace(nebula_go.SpaceConf{Name: name, Partition: uint(partitionNum), Replica: uint(replicaFactor), VidType: vidType, IgnoreIfExists: ignoreIfExists, Comment: comment}) (*ResultSet, error)`** — готовая обёртка библиотеки; вызывается на `Session` из `ConnectionPool.GetSession(adminUser, adminPassword)`, т.к. `SessionPool` привязан к одному существующему space и не годится для создания новых.

**`nebula_createTag`** / **`nebula_createEdge`**
- Вход: `name: string`, `properties: [{name, type, nullable?, default?}]`, `ttl?: {col, duration}`.
- nebula-go: `session.ExecuteWithParameter("CREATE TAG <name> (<propDefs>) [TTL_DURATION=.., TTL_COL=..]", nil)` / `CREATE EDGE` — через admin-сессию из `ConnectionPool`.

**`nebula_createIndex`**
- Вход: `on: "tag"|"edge"`, `name: string`, `targetName: string`, `properties: string[]`.
- nebula-go: `sessionPool.Execute("CREATE TAG INDEX <name> ON <tag>(<props>)")` / `CREATE EDGE INDEX ...`.

**`nebula_listIndexes`**
- Вход: `on?: "tag"|"edge"`.
- nebula-go: `sessionPool.Execute("SHOW TAG INDEXES")` / `Execute("SHOW EDGE INDEXES")`.

### P2 — администрирование и наблюдаемость

**`nebula_dropSpace`**
- Вход: `name: string`, `confirm: bool`.
- nebula-go: `session.Execute("DROP SPACE <name>")` через admin-сессию; обработчик тула обязан требовать `confirm=true` до вызова.

**`nebula_dropTag`** / **`nebula_dropEdge`**
- Вход: `name: string`, `confirm: bool`.
- nebula-go: `session.Execute("DROP TAG <name>")` / `DROP EDGE`.

**`nebula_alterTag`** / **`nebula_alterEdge`**
- Вход: `name: string`, `add?: [{name, type}]`, `drop?: string[]`, `change?: [{name, type}]`.
- nebula-go: `session.Execute("ALTER TAG <name> ADD (...) | DROP (...) | CHANGE (...)")`.

**`nebula_rebuildIndex`**
- Вход: `indexName: string`, `on: "tag"|"edge"`.
- nebula-go: `sessionPool.Execute("REBUILD TAG INDEX <name>")` / `REBUILD EDGE INDEX`.

**`nebula_submitJob`**
- Вход: `jobType: "STATS"|"COMPACT"|"FLUSH"|"BALANCE"`.
- nebula-go: `session.Execute("SUBMIT JOB <TYPE>")`; статус — `sessionPool.Execute("SHOW JOB <job_id>")` / `Execute("SHOW JOBS")`.

**`nebula_showHosts`**
- Вход: без параметров.
- nebula-go: `sessionPool.Execute("SHOW HOSTS")`.

**`nebula_showStats`**
- Вход: `space: string`.
- nebula-go: `sessionPool.Execute("SHOW STATS")` (данные актуальны после `nebula_submitJob({jobType:"STATS"})`).

**`nebula_explainQuery`**
- Вход: `query: string`, `mode?: "explain"|"profile"`.
- nebula-go: `sessionPool.Execute("EXPLAIN <query>")` / `Execute("PROFILE <query>")`. Дополнительно доступны `ResultSet.IsSetPlanDesc()` / `GetPlanDesc()` и утилиты `ResultSet.MakeDotGraph()` / `MakePlanByRow()` для визуализации плана выполнения.

**`nebula_showSessions`** / **`nebula_killSession`**
- Вход (для kill): `sessionId: int`.
- nebula-go: `sessionPool.Execute("SHOW SESSIONS")` / `session.Execute("KILL SESSION <id>")` через admin-сессию.

**`nebula_healthCheck`**
- Вход: без параметров.
- nebula-go: **`session.Ping() error`** (проверка живости конкретной сессии) и/или `connectionPool.Ping(host HostAddress, timeout time.Duration) error` (проверка доступности хоста Graphd на уровне пула, без открытия сессии) — используется в readiness/liveness пробе самого MCP-сервера, не требует выполнения nGQL.

## 4. Авторизация и технические особенности

**Авторизация на уровне NebulaGraph.** Протокол — бинарный fbthrift поверх TCP, а не HTTP; аутентификация в `nebula-go` происходит только через thrift-вызов `Authenticate`, скрытый внутри `ConnectionPool.GetSession(username, password string) (*Session, error)` (см. `connection_pool.go`) или через конфиг `SessionPoolConf` (`session_pool.go`) — везде это пара **логин/пароль**, никакого bearer-токена в самом протоколе не предусмотрено.

Рекомендации:
- **Сервисный аккаунт БД**: отдельный технический пользователь NebulaGraph с минимально необходимыми правами; в идеале — раздельные read-only и read-write учётки, привязанные к разным группам тулов (P0/P1 read-тулы → `SessionPool` на read-only юзере; insert/delete/DDL-тулы → отдельный `ConnectionPool`+`Session` на write/admin-юзере). Пароли — в secret manager/переменных окружения MCP-сервера, никогда не как параметр тула и не в промпте LLM.
- **TLS/mTLS до Graphd**: `nebula-go` поддерживает SSL через `NewSslConnectionPool(addresses, conf, sslConfig *tls.Config, log)` (см. `certs/`, `ssl_connection_test.go`, `ssl_sessionpool_test.go`) — в проде обязательно включать, для облака — предпочтительно mTLS.
- **Токен — на уровне самого MCP-сервера, не БД.** Раз в протоколе NebulaGraph токенов нет, авторизацию по токену стоит реализовать на границе MCP-сервера: клиенты (агенты/LLM-оркестраторы) обращаются к MCP по Bearer-токену/API-key/OAuth2 (актуально для HTTP/SSE-транспорта; для локального stdio-транспорта не требуется). Это разделяет два периметра: «кто может дёргать MCP-тулы» (токен MCP) и «какими правами MCP-сервер сам ходит в NebulaGraph» (сервисный логин/пароль БД).
- **Разграничение по приоритету тулов**: P2-тулы (`DROP SPACE`, `DROP TAG/EDGE`, `ALTER`, `SUBMIT JOB`) стоит закрыть отдельным более узким токеном/ролью MCP и требовать explicit `confirm: true` в payload — дешёвая защита от случайного деструктивного вызова со стороны LLM.

**Прочие технические особенности реализации:**
- **`SessionPool` vs `ConnectionPool`+`Session`**: `SessionPool` привязан к одному пользователю и одному space, не даёт менять пароли/дропать юзеров и не годится для `CREATE/DROP SPACE` (нет ещё существующего space на момент создания). Для DDL и кросс-space операций нужен `ConnectionPool.GetSession(...)`, ручной `USE <space>` и обязательный `session.Release()` после использования — иначе соединение не вернётся в пул.
- **Параметризация**: практически все входные значения пользователя должны идти через `ExecuteWithParameter(stmt, params)` с `map[string]any`, а не строковую конкатенацию — единственный встроенный в клиент механизм защиты от nGQL-инъекций, критичный именно в MCP-сценарии, где аргументы тула генерирует LLM.
- **`ExecuteJson` не реализован на `SessionPool`**: если нужен JSON-ответ напрямую от сервера (а не собранный руками из `ResultSet`), это доступно только через `Session.ExecuteJson`/`ExecuteJsonWithParameter`, то есть через `ConnectionPool`.
- **Конкурентность и конфиг пула**: `SessionPool`/`ConnectionPool` потокобезопасны (внутри — `sync.RWMutex`), размер и таймауты настраиваются в `SessionPoolConf`/`PoolConfig` (`configs.go`) на уровне запуска MCP-сервера, а не как аргументы тулов.
- **Множественность space**: один `SessionPool` = один space, поэтому MCP-серверу нужно либо держать по пулу на каждый используемый space (выбор через параметр `space` в тулах), либо создавать пул лениво при первом обращении к новому space.
- **Разбор графовых типов**: `ValueWrapper.AsNode()/AsRelationship()/AsPath()` — единственный корректный способ достать VERTEX/EDGE/PATH из `ResultSet`; их нужно единообразно сериализовать в JSON (id/tags/properties, а не сырые thrift-структуры) перед возвратом LLM.
- **Не трогать `/nebula`**: директория в репозитории — сгенерированный fbthrift-код (`nebula/graph` и т.д.), её нельзя редактировать вручную; апдейт клиента = `go get -u github.com/vesoft-inc/nebula-go/v3@<tag>`.
- **Совместимость версий**: жёстко пиновать версию `nebula-go` под версию сервера (таблица совместимости в README, например v3.4.x клиент ↔ сервер 3.1.x–3.4.x) — завязать на это CI/интеграционные тесты MCP-сервера.
- **Долгие/асинхронные операции**: `CREATE SPACE`, `SUBMIT JOB COMPACT/STATS`, `REBUILD INDEX` асинхронны на стороне кластера; соответствующие тулы должны возвращать статус job'а (`SHOW JOB <id>`), а не блокироваться до завершения.
