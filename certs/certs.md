# Определение клиента MCP-сервера по `X-Forwarded-Client-Cert`

Как MCP-серверу на Go понять, кто пришёл (**MCP Gateway** или **агент**), когда mTLS принимает не сам сервер, а прокси перед ним: Envoy, Istio, sidecar. В этом документе: что поменять в коде, что проверить в настройках прокси и как всё протестировать локально на Windows.

---

## 1. Как это работает

```
клиент ──mTLS──▶ прокси (Envoy / Istio) ──HTTP + XFCC──▶ MCP-сервер (Go)
```

1. Прокси принимает TLS, проверяет клиентский сертификат по CA и отклоняет чужие.
2. В запрос к приложению прокси добавляет заголовок `X-Forwarded-Client-Cert` (XFCC) с данными сертификата.
3. Go-сервер работает на обычном HTTP, читает CN из XFCC и выбирает ветку логики.

Пример заголовка, который реально приходит от Envoy:

```
X-Forwarded-Client-Cert: Hash=fad64460...;Subject="CN=mcp-gateway";URI=
```

### Формат XFCC

- Заголовок может содержать **несколько элементов через запятую**: по одному от каждого прокси на пути запроса. **Последний элемент добавил ближайший к приложению прокси.**
- Внутри элемента пары `ключ=значение` идут через `;`. Значения могут быть в кавычках, а внутри кавычек могут встречаться `,` и `;`: `Subject="CN=mcp-gateway,O=Acme"`.
- Основные ключи: `Subject` (DN сертификата, в нём CN), `URI` (SAN URI, в Istio это SPIFFE ID), `Hash`, `By`, `DNS`.

Поэтому разбирать заголовок через `strings.Split(h, ",")` нельзя, нужен парсер, который учитывает кавычки (он ниже).

---

## 2. Что поменять в коде

### 2.1. Убрать TLS из Go-сервера

Теперь TLS принимает прокси, поэтому `TLSConfig`, `ClientCAs`, серверный сертификат и `ListenAndServeTLS` из сервера убираются:

```go
// было
srv.ListenAndServeTLS("server.crt", "server.key")

// стало
srv := &http.Server{
	Addr:    cfg.ListenAddr, // например "127.0.0.1:8080"
	Handler: classifier.Middleware(mcpHandler),
}
log.Fatal(srv.ListenAndServe())
```

`r.TLS` теперь всегда `nil`, и проверку через `r.TLS.VerifiedChains` нужно удалить.

### 2.2. Конфиг

```yaml
listen_addr: 127.0.0.1:8080

client_auth:
  gateway_cns: [mcp-gateway]
  agent_cns:   [ai-agent]
  # Пусто: клиент берётся из последнего элемента XFCC (один прокси перед приложением).
  # Заполнено: см. раздел 3.3 (цепочка ingress → sidecar).
  trusted_proxy_uris: []
```

### 2.3. Код: `xfcc.go`

```go
package main

import (
	"context"
	"log"
	"net/http"
	"strings"
)

type ClientKind int

const (
	ClientUnknown ClientKind = iota
	ClientAgent
	ClientGateway
)

func (k ClientKind) String() string {
	switch k {
	case ClientAgent:
		return "agent"
	case ClientGateway:
		return "gateway"
	}
	return "unknown"
}

type ctxKey struct{}

func ClientKindFrom(ctx context.Context) ClientKind {
	k, _ := ctx.Value(ctxKey{}).(ClientKind)
	return k
}

const xfccHeader = "X-Forwarded-Client-Cert"

// splitUnquoted делит строку по sep, игнорируя разделители внутри "..." (с учётом \").
func splitUnquoted(s string, sep byte) []string {
	var parts []string
	inQuotes, escaped, start := false, false, 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case escaped:
			escaped = false
		case c == '\\':
			escaped = true
		case c == '"':
			inQuotes = !inQuotes
		case c == sep && !inQuotes:
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	return append(parts, s[start:])
}

func unquote(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		v = strings.ReplaceAll(v[1:len(v)-1], `\"`, `"`)
	}
	return v
}

// parseXFCCElement разбирает один элемент: Key=Value;Key="Value"
func parseXFCCElement(elem string) map[string]string {
	m := map[string]string{}
	for _, kv := range splitUnquoted(elem, ';') {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		m[strings.ToLower(strings.TrimSpace(k))] = unquote(v)
	}
	return m
}

// cnFromSubject достаёт CN из DN вида "CN=mcp-gateway,O=Acme".
func cnFromSubject(dn string) string {
	for _, rdn := range splitDN(dn) {
		k, v, ok := strings.Cut(rdn, "=")
		if ok && strings.EqualFold(strings.TrimSpace(k), "CN") {
			return strings.TrimSpace(unescapeDN(v))
		}
	}
	return ""
}

// splitDN делит DN по запятым, не экранированным через "\".
func splitDN(dn string) []string {
	var parts []string
	escaped, start := false, 0
	for i := 0; i < len(dn); i++ {
		switch {
		case escaped:
			escaped = false
		case dn[i] == '\\':
			escaped = true
		case dn[i] == ',' || dn[i] == '+':
			parts = append(parts, dn[start:i])
			start = i + 1
		}
	}
	return append(parts, dn[start:])
}

func unescapeDN(v string) string {
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		if v[i] == '\\' && i+1 < len(v) {
			i++
		}
		b.WriteByte(v[i])
	}
	return b.String()
}

// ClientCNFromXFCC возвращает CN клиента из XFCC.
// trustedProxyURIs пустой: клиент — последний элемент (его добавил ближайший прокси).
// trustedProxyURIs задан: последний элемент должен быть от доверенного прокси
// (например, Istio ingress gateway), а клиент — предпоследний элемент.
func ClientCNFromXFCC(r *http.Request, trustedProxyURIs map[string]bool) string {
	values := r.Header.Values(xfccHeader)
	if len(values) == 0 {
		return ""
	}
	elems := splitUnquoted(strings.Join(values, ","), ',')
	last := parseXFCCElement(elems[len(elems)-1])
	if len(trustedProxyURIs) == 0 {
		return cnFromSubject(last["subject"])
	}
	if len(elems) < 2 || !trustedProxyURIs[last["uri"]] {
		return ""
	}
	return cnFromSubject(parseXFCCElement(elems[len(elems)-2])["subject"])
}

type CNClassifier struct {
	roles            map[string]ClientKind
	trustedProxyURIs map[string]bool
}

func NewCNClassifier(gatewayCNs, agentCNs, trustedProxyURIs []string) *CNClassifier {
	m := map[string]ClientKind{}
	for _, cn := range gatewayCNs {
		m[cn] = ClientGateway
	}
	for _, cn := range agentCNs {
		m[cn] = ClientAgent
	}
	t := map[string]bool{}
	for _, u := range trustedProxyURIs {
		t[u] = true
	}
	return &CNClassifier{roles: m, trustedProxyURIs: t}
}

func (c *CNClassifier) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cn := ClientCNFromXFCC(r, c.trustedProxyURIs)
		kind := c.roles[cn]
		if kind == ClientUnknown {
			log.Printf("rejected client: CN=%q", cn)
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, kind)))
	})
}
```

### 2.4. Подключение и использование

```go
classifier := NewCNClassifier(
	cfg.ClientAuth.GatewayCNs,
	cfg.ClientAuth.AgentCNs,
	cfg.ClientAuth.TrustedProxyURIs,
)

srv := &http.Server{
	Addr:    cfg.ListenAddr,
	Handler: classifier.Middleware(mcpHandler),
}
```

В обработчике MCP-инструмента:

```go
switch ClientKindFrom(ctx) {
case ClientGateway:
	// логика для MCP Gateway
case ClientAgent:
	// логика для агента
}
```

> Проверьте, что `ctx` в tool handler вашего MCP SDK — это контекст HTTP-запроса. В `mark3labs/mcp-go` его пробрасывают через `server.WithHTTPContextFunc`.

---

## 3. Безопасность: когда XFCC можно доверять

Заголовок — это просто текст. Кто может отправить запрос в приложение напрямую, тот может написать в нём что угодно. Поэтому код безопасен **только** при выполнении трёх условий.

### 3.1. Приложение недоступно в обход прокси

- Если Envoy работает отдельным sidecar, слушайте только `127.0.0.1:8080`.
- В Istio входящий трафик пода перехватывается sidecar, но mTLS должен быть строгим: `PeerAuthentication` с `mode: STRICT`. В режиме `PERMISSIVE` sidecar пропускает plaintext-запросы, и клиент без сертификата может прислать свой XFCC.
- Не публикуйте порт приложения через Service или NodePort мимо прокси.

### 3.2. Прокси очищает заголовок от клиента

В Envoy за это отвечает `forward_client_cert_details`:

| Режим | Что делает | Подходит |
|---|---|---|
| `SANITIZE_SET` | удаляет XFCC клиента и ставит свой | ✅ лучший вариант для внешнего входа |
| `APPEND_FORWARD` | оставляет XFCC клиента и **дописывает свой элемент в конец** | ✅ только если код читает последний элемент (так и сделано) |
| `FORWARD_ONLY` | пробрасывает заголовок клиента как есть | ❌ клиент может подделать CN |
| `SANITIZE` | удаляет XFCC, ничего не ставит | ❌ CN не дойдёт |

Плюс `set_current_client_cert_details.subject: true`, иначе в XFCC не будет `Subject` (а значит, и CN).

Проверено на Envoy 1.31: в режиме `APPEND_FORWARD` агент отправил `X-Forwarded-Client-Cert: Subject="CN=mcp-gateway"`. До приложения дошло `Subject="CN=mcp-gateway",Hash=...;Subject="CN=ai-agent"`, код взял последний элемент и определил клиента как **агента**. Подделка не сработала.

### 3.3. Если прокси несколько (Istio: ingress gateway → sidecar)

Типичная схема в Istio:

```
клиент ──mTLS──▶ istio-ingressgateway ──mesh mTLS──▶ sidecar ──▶ приложение
```

Здесь последний элемент XFCC добавляет sidecar, и описывает он **ingress gateway**: в нём есть `URI=spiffe://.../istio-ingressgateway-service-account` и нет `Subject`. Сертификат клиента с `CN=mcp-gateway` будет в **предпоследнем** элементе.

Для такой схемы заполните `trusted_proxy_uris`:

```yaml
trusted_proxy_uris:
  - spiffe://cluster.local/ns/istio-system/sa/istio-ingressgateway-service-account
```

Код тогда:

1. Проверяет, что последний элемент добавлен для доверенного ingress (по `URI`).
2. Берёт CN из предпоследнего элемента.
3. Если запрос пришёл от любого другого workload в mesh, отклоняет его. Без этой проверки любой под в кластере мог бы прислать поддельный XFCC.

**Сначала посмотрите реальный заголовок на своём окружении.** Временно залогируйте `r.Header.Values("X-Forwarded-Client-Cert")`, чтобы понять, сколько там элементов и какой URI у прокси. После этого логирование уберите.

> Ещё один нюанс Istio: у сертификатов workload внутри mesh **нет CN**, только SPIFFE URI. CN есть только у внешних клиентов со своими сертификатами, которые проходят через ingress. Если gateway и агент живут внутри mesh, различать их нужно по `URI` (ServiceAccount), а не по CN.

---

## 4. Тестирование

Три уровня, от быстрого к полному.

### 4.1. Unit-тесты (без прокси и сертификатов)

`xfcc_test.go`:

```go
package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMiddleware(t *testing.T) {
	cl := NewCNClassifier([]string{"mcp-gateway"}, []string{"ai-agent"}, nil)
	var got ClientKind
	h := cl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = ClientKindFrom(r.Context())
	}))

	tests := []struct {
		name     string
		xfcc     []string
		wantCode int
		wantKind ClientKind
	}{
		{"gateway", []string{`Hash=abc;Subject="CN=mcp-gateway"`}, 200, ClientGateway},
		{"agent", []string{`Hash=abc;Subject="CN=ai-agent,O=Acme";URI=spiffe://x`}, 200, ClientAgent},
		{"cn not first", []string{`Subject="O=Acme,CN=ai-agent"`}, 200, ClientAgent},
		{"no header", nil, 403, ClientUnknown},
		{"unknown cn", []string{`Subject="CN=someone"`}, 403, ClientUnknown},
		{"spoof in earlier element", []string{`Subject="CN=mcp-gateway",Subject="CN=ai-agent"`}, 200, ClientAgent},
		{"spoof as separate header", []string{`Subject="CN=mcp-gateway"`, `Subject="CN=someone"`}, 403, ClientUnknown},
		{"comma inside quotes", []string{`By=spiffe://a;Subject="CN=mcp-gateway,OU=x,O=y"`}, 200, ClientGateway},
		{"prefix is not enough", []string{`Subject="CN=mcp-gateway-evil"`}, 403, ClientUnknown},
		{"no subject", []string{`Hash=abc;URI=spiffe://x`}, 403, ClientUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got = ClientUnknown
			req := httptest.NewRequest("GET", "/", nil)
			for _, v := range tt.xfcc {
				req.Header.Add(xfccHeader, v)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tt.wantCode || got != tt.wantKind {
				t.Fatalf("code=%d kind=%v, want %d %v", rec.Code, got, tt.wantCode, tt.wantKind)
			}
		})
	}
}

func TestTrustedProxy(t *testing.T) {
	gw := "spiffe://cluster.local/ns/istio-system/sa/istio-ingressgateway-service-account"
	cl := NewCNClassifier([]string{"mcp-gateway"}, []string{"ai-agent"}, []string{gw})
	h := cl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	tests := []struct {
		name string
		xfcc string
		want int
	}{
		{"via ingress", `Hash=a;Subject="CN=mcp-gateway";URI=,By=spiffe://x;Hash=b;Subject="";URI=` + gw, 200},
		{"from other workload", `Subject="CN=mcp-gateway",By=spiffe://x;Hash=b;Subject="";URI=spiffe://cluster.local/ns/default/sa/evil`, 403},
		{"single element", `Hash=b;Subject="CN=mcp-gateway";URI=` + gw, 403},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set(xfccHeader, tt.xfcc)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("code=%d want %d", rec.Code, tt.want)
			}
		})
	}
}
```

```powershell
go test ./...
```

### 4.2. Быстрая ручная проверка: curl с заголовком, без прокси

Локально приложение доверяет любому XFCC, поэтому прокси можно сымитировать, просто передав заголовок. Это проверяет логику ветвления, но не mTLS.

```powershell
$curl = "C:\Program Files\Git\mingw64\bin\curl.exe"

# gateway → ветка gateway
& $curl -H 'X-Forwarded-Client-Cert: Hash=x;Subject="CN=mcp-gateway"' http://127.0.0.1:8080/

# агент → ветка агента
& $curl -H 'X-Forwarded-Client-Cert: Hash=x;Subject="CN=ai-agent"' http://127.0.0.1:8080/

# без заголовка → 403
& $curl http://127.0.0.1:8080/
```

> Этот же тест показывает, почему приложение **нельзя** открывать в обход прокси: заголовок подделывается одной строкой.

### 4.3. Полная проверка: настоящий mTLS через Envoy в Docker

Понадобятся Docker Desktop и сертификаты из инструкции `mtls-local-certs-windows.md`: `ca.crt`, `server.crt/.key`, `mcp-gateway.crt/.key`, `ai-agent.crt/.key` в папке `certs`.

**1. Запустите приложение на `:8080`**, а не на `127.0.0.1:8080`, иначе Envoy из контейнера до него не достучится. Windows может спросить про брандмауэр: разрешите доступ к частным сетям. Это настройка только для локального теста.

**2. Создайте `envoy.yaml`** рядом с папкой `certs`:

```yaml
static_resources:
  listeners:
  - name: mtls
    address:
      socket_address: { address: 0.0.0.0, port_value: 8443 }
    filter_chains:
    - transport_socket:
        name: envoy.transport_sockets.tls
        typed_config:
          "@type": type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.DownstreamTlsContext
          require_client_certificate: true          # mTLS: без клиентского сертификата не пускать
          common_tls_context:
            tls_certificates:                       # серверный сертификат прокси
            - certificate_chain: { filename: /certs/server.crt }
              private_key: { filename: /certs/server.key }
            validation_context:                     # CA для проверки клиентов
              trusted_ca: { filename: /certs/ca.crt }
      filters:
      - name: envoy.filters.network.http_connection_manager
        typed_config:
          "@type": type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager
          stat_prefix: ingress
          forward_client_cert_details: SANITIZE_SET # выкинуть XFCC клиента, поставить свой
          set_current_client_cert_details:          # что положить в XFCC
            subject: true
            uri: true
            dns: true
          route_config:
            virtual_hosts:
            - name: app
              domains: ["*"]
              routes:
              - match: { prefix: "/" }
                route: { cluster: app, timeout: 0s } # 0s: не обрывать стриминг MCP (SSE)
          http_filters:
          - name: envoy.filters.http.router
            typed_config:
              "@type": type.googleapis.com/envoy.extensions.filters.http.router.v3.Router
  clusters:
  - name: app
    type: LOGICAL_DNS
    connect_timeout: 1s
    load_assignment:
      cluster_name: app
      endpoints:
      - lb_endpoints:
        - endpoint:
            address:
              socket_address: { address: host.docker.internal, port_value: 8080 }
```

**3. Запустите Envoy:**

```powershell
docker run --rm -p 8443:8443 `
  -v "${PWD}\certs:/certs:ro" `
  -v "${PWD}\envoy.yaml:/etc/envoy/envoy.yaml:ro" `
  envoyproxy/envoy:v1.31.2
```

**4. Проверьте curl'ом** из папки `certs`:

```powershell
cd certs
$curl = "C:\Program Files\Git\mingw64\bin\curl.exe"

# gateway → ветка gateway
& $curl --cacert ca.crt --cert mcp-gateway.crt --key mcp-gateway.key https://localhost:8443/

# агент → ветка агента
& $curl --cacert ca.crt --cert ai-agent.crt --key ai-agent.key https://localhost:8443/

# агент пытается выдать себя за gateway через заголовок → всё равно ветка агента
& $curl --cacert ca.crt --cert ai-agent.crt --key ai-agent.key -H 'X-Forwarded-Client-Cert: Subject="CN=mcp-gateway"' https://localhost:8443/

# без клиентского сертификата → Envoy обрывает handshake
& $curl --cacert ca.crt https://localhost:8443/
```

Ожидаемый результат (проверено на Envoy 1.31.2):

| Запрос | Результат |
|---|---|
| сертификат `mcp-gateway` | ветка gateway, XFCC: `Hash=...;Subject="CN=mcp-gateway";URI=` |
| сертификат `ai-agent` | ветка агента |
| агент + поддельный заголовок | ветка агента: `SANITIZE_SET` выкинул подделку |
| без сертификата | `alert certificate required` |
| сертификат не от вашего CA | `alert unknown ca` |
| валидный сертификат, но чужой CN | `403 forbidden` от приложения |

Для отладки удобно, чтобы тестовый хендлер возвращал `r.Header.Get("X-Forwarded-Client-Cert")`: так видно, что именно передал прокси.

---

## 5. Чек-лист перед продом

- [ ] Из Go-сервера убран TLS, сервер слушает HTTP.
- [ ] Приложение недоступно в обход прокси (`127.0.0.1` для sidecar; Istio `PeerAuthentication: STRICT`; нет прямых Service/NodePort на порт приложения).
- [ ] В прокси `forward_client_cert_details` = `SANITIZE_SET` или `APPEND_FORWARD`, **не** `FORWARD_ONLY`.
- [ ] В прокси включено `subject: true` в `set_current_client_cert_details`.
- [ ] Реальный XFCC на окружении посмотрен в логах, понятно, сколько в нём элементов.
- [ ] Если прокси больше одного (ingress → sidecar), заполнен `trusted_proxy_uris`.
- [ ] Если клиенты внутри mesh без CN, различение переделано на `URI` (SPIFFE).
- [ ] Для MCP-маршрута отключён таймаут ответа (`timeout: 0s` или аналог), иначе прокси оборвёт стриминг.
- [ ] Отказы логируются с CN, а временное логирование всего XFCC убрано.