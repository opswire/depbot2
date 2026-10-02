# Определение клиента MCP-сервера по `X-Forwarded-Client-Cert`

Как MCP-серверу на Go понять, кто пришёл, **MCP Gateway** или **агент**, когда mTLS принимает прокси (Envoy / Istio), а не сам сервер.

**Правило:** если CN клиентского сертификата подходит под один из шаблонов `gateway_cns`, это gateway. Любой другой CN — агент.

---

## 1. Как это работает

```
клиент ──mTLS──▶ прокси (Envoy / Istio) ──HTTP + XFCC──▶ MCP-сервер (Go)
```

1. Прокси принимает TLS, проверяет клиентский сертификат по CA и отклоняет чужие.
2. В запрос к приложению прокси добавляет заголовок `X-Forwarded-Client-Cert` (XFCC):
   ```
   X-Forwarded-Client-Cert: Hash=fad64460...;Subject="CN=mcp-gateway";URI=
   ```
3. Go-сервер работает на обычном HTTP, берёт CN из `Subject` и выбирает ветку.

Особенности формата:

- В заголовке может быть **несколько элементов через запятую**, по одному от каждого прокси. **Последний элемент добавил ближайший к приложению прокси**, а предыдущие мог прислать сам клиент.
- Значения бывают в кавычках, и внутри них встречаются запятые: `Subject="CN=mcp-gateway,O=Acme"`. Поэтому обычный `strings.Split` не подходит.

### Почему без готовой библиотеки

Поддерживаемой Go-библиотеки для XFCC нет: отдельный `xfccparser` заброшен, а `envoyutil` входит в большой `blend/go-sdk`. Для разбора DN есть `ldap.ParseDN` из `go-ldap`, но тянуть LDAP-клиент ради одной функции не стоит. Нужной логики примерно 30 строк на стандартной библиотеке, и они покрыты тестами ниже.

---

## 2. Что поменять в коде

### 2.1. Убрать TLS из Go-сервера

TLS теперь принимает прокси, поэтому `TLSConfig`, `ClientCAs`, серверный сертификат и `ListenAndServeTLS` из сервера убираются. Сервер слушает обычный HTTP:

```go
srv := &http.Server{
	Addr:    cfg.ListenAddr, // "127.0.0.1:8080"
	Handler: classifier.Middleware(mcpHandler),
}
log.Fatal(srv.ListenAndServe())
```

### 2.2. Конфиг

```yaml
listen_addr: 127.0.0.1:8080

client_auth:
  # Регулярки для CN gateway. Совпадение всегда полное (^...$ добавляется в коде).
  # Всё, что не подошло, считается агентом.
  gateway_cns:
    - 'mcp-gateway'                # ровно mcp-gateway
    - 'mcp-gateway(-[a-z0-9]+)*'   # mcp-gateway, mcp-gateway-1, mcp-gateway-prod-eu
```

```go
type ClientAuthConfig struct {
	GatewayCNs []string `yaml:"gateway_cns"`
}
```

### 2.3. Код: `client.go`

```go
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
)

type ClientKind int

const (
	ClientAgent ClientKind = iota
	ClientGateway
)

type ctxKey struct{}

func ClientKindFrom(ctx context.Context) ClientKind {
	k, _ := ctx.Value(ctxKey{}).(ClientKind)
	return k
}

// cnRe находит CN в DN вида "CN=mcp-gateway,O=Acme" (учитывает экранированные \,).
var cnRe = regexp.MustCompile(`(?:^|,)\s*CN=((?:\\.|[^,\\])*)`)

// splitOutsideQuotes делит s по sep, пропуская sep внутри "...".
func splitOutsideQuotes(s string, sep byte) []string {
	var parts []string
	inQuotes, start := false, 0
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '\\':
			i++ // пропускаем экранированный символ
		case s[i] == '"':
			inQuotes = !inQuotes
		case s[i] == sep && !inQuotes:
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	return append(parts, s[start:])
}

// ClientCN возвращает CN клиента из последнего элемента XFCC:
// его добавил ближайший прокси, предыдущие элементы мог прислать клиент.
func ClientCN(r *http.Request) string {
	elems := splitOutsideQuotes(strings.Join(r.Header.Values("X-Forwarded-Client-Cert"), ","), ',')
	for _, kv := range splitOutsideQuotes(elems[len(elems)-1], ';') {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.EqualFold(strings.TrimSpace(k), "Subject") {
			if m := cnRe.FindStringSubmatch(strings.Trim(strings.TrimSpace(v), `"`)); m != nil {
				return strings.ReplaceAll(m[1], `\`, "")
			}
		}
	}
	return ""
}

type Classifier struct{ gateway []*regexp.Regexp }

func NewClassifier(gatewayPatterns []string) (*Classifier, error) {
	c := &Classifier{}
	for _, p := range gatewayPatterns {
		re, err := regexp.Compile(`^(?:` + p + `)$`) // всегда полное совпадение
		if err != nil {
			return nil, fmt.Errorf("bad gateway CN pattern %q: %w", p, err)
		}
		c.gateway = append(c.gateway, re)
	}
	return c, nil
}

func (c *Classifier) Kind(cn string) ClientKind {
	for _, re := range c.gateway {
		if re.MatchString(cn) {
			return ClientGateway
		}
	}
	return ClientAgent
}

func (c *Classifier) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cn := ClientCN(r)
		if cn == "" { // нет XFCC — запрос пришёл не через прокси
			log.Printf("rejected: no client CN in X-Forwarded-Client-Cert")
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		ctx := context.WithValue(r.Context(), ctxKey{}, c.Kind(cn))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
```

Запрос без XFCC отклоняется с 403, а не считается агентом. Отсутствие заголовка означает, что запрос пришёл в обход прокси или прокси настроен неправильно. Если такие запросы всё-таки нужно пропускать как агента, уберите блок `if cn == ""`.

### 2.4. Подключение и использование

```go
classifier, err := NewClassifier(cfg.ClientAuth.GatewayCNs)
if err != nil {
	log.Fatal(err) // кривая регулярка в конфиге — падаем при старте
}

srv := &http.Server{
	Addr:    cfg.ListenAddr,
	Handler: classifier.Middleware(mcpHandler),
}
```

В обработчике MCP-инструмента:

```go
if ClientKindFrom(ctx) == ClientGateway {
	// логика для MCP Gateway
} else {
	// логика для агента
}
```

> Проверьте, что `ctx` в tool handler вашего MCP SDK — это контекст HTTP-запроса. В `mark3labs/mcp-go` его пробрасывают через `server.WithHTTPContextFunc`.

---

## 3. Безопасность: когда XFCC можно доверять

Заголовок — это просто текст. Код безопасен только при соблюдении трёх условий.

1. **Приложение недоступно в обход прокси.** Если Envoy работает отдельным sidecar, приложение слушает только `127.0.0.1`. В Istio нужен `PeerAuthentication` с `mode: STRICT`, а порт приложения не должен быть опубликован мимо прокси.
2. **Прокси не пробрасывает XFCC клиента как есть.** В Envoy это параметр `forward_client_cert_details`:
   - `SANITIZE_SET` — лучший вариант: прокси удаляет заголовок клиента и ставит свой.
   - `APPEND_FORWARD` тоже подходит: прокси дописывает свой элемент в конец, а код читает именно последний.
   - `FORWARD_ONLY` **нельзя**: клиент сможет подделать CN.
3. **В XFCC есть `Subject`.** В Envoy для этого нужно `set_current_client_cert_details.subject: true`.

Цена ошибки выше, чем раньше. Теперь любой CN, не похожий на gateway, — это агент, поэтому подделать нужно только CN gateway. А шаблоны в `gateway_cns` должны быть узкими: `mcp-gateway.*` пропустит и `mcp-gatewayEVIL`.

### Если прокси два (Istio: ingress gateway → sidecar)

Тогда последний элемент XFCC добавляет sidecar, и в нём описан сам **ingress gateway**: в элементе есть только `URI=spiffe://...` и нет `Subject`. CN клиента окажется в **предпоследнем** элементе, и текущий код вернёт 403.

**Перед выкаткой залогируйте реальный заголовок** `r.Header.Values("X-Forwarded-Client-Cert")` на своём окружении. Если элемент в нём один, код подходит как есть. Если элементов два, код нужно доработать: проверять, что `URI` последнего элемента принадлежит вашему ingress, и брать CN из предпоследнего. Без проверки URI любой под в mesh сможет подделать CN.

> У сервисов внутри Istio в сертификатах обычно **нет CN**, только SPIFFE URI. Если gateway работает внутри mesh, различать клиентов нужно по `URI`, а не по CN.

---

## 4. Тестирование

### 4.1. Unit-тесты

`client_test.go`:

```go
package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMiddleware(t *testing.T) {
	cl, err := NewClassifier([]string{`mcp-gateway`})
	if err != nil {
		t.Fatal(err)
	}
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
		{"gateway", []string{`Hash=abc;Subject="CN=mcp-gateway";URI=`}, 200, ClientGateway},
		{"agent", []string{`Hash=abc;Subject="CN=ai-agent";URI=`}, 200, ClientAgent},
		{"any other cn is agent", []string{`Subject="CN=whatever"`}, 200, ClientAgent},
		{"cn not first in dn", []string{`Subject="O=Acme,CN=mcp-gateway"`}, 200, ClientGateway},
		{"comma inside quotes", []string{`By=spiffe://a;Subject="CN=mcp-gateway,OU=x,O=y"`}, 200, ClientGateway},
		{"prefix is not enough", []string{`Subject="CN=mcp-gateway-evil"`}, 200, ClientAgent},
		{"spoof in earlier element", []string{`Subject="CN=mcp-gateway",Hash=b;Subject="CN=ai-agent"`}, 200, ClientAgent},
		{"spoof as separate header", []string{`Subject="CN=mcp-gateway"`, `Subject="CN=ai-agent"`}, 200, ClientAgent},
		{"no header", nil, 403, ClientAgent},
		{"no subject", []string{`Hash=abc;URI=spiffe://x`}, 403, ClientAgent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got = -1
			req := httptest.NewRequest("GET", "/", nil)
			for _, v := range tt.xfcc {
				req.Header.Add("X-Forwarded-Client-Cert", v)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tt.wantCode {
				t.Fatalf("code=%d, want %d", rec.Code, tt.wantCode)
			}
			if rec.Code == 200 && got != tt.wantKind {
				t.Fatalf("kind=%v, want %v", got, tt.wantKind)
			}
		})
	}
}

func TestGatewayPatterns(t *testing.T) {
	cl, _ := NewClassifier([]string{`mcp-gateway(-[a-z0-9]+)*`})
	for cn, want := range map[string]ClientKind{
		"mcp-gateway":         ClientGateway,
		"mcp-gateway-1":       ClientGateway,
		"mcp-gateway-prod-eu": ClientGateway,
		"mcp-gatewayX":        ClientAgent,
		"evil-mcp-gateway":    ClientAgent,
		"ai-agent":            ClientAgent,
	} {
		if got := cl.Kind(cn); got != want {
			t.Errorf("%s: got %v want %v", cn, got, want)
		}
	}
	if _, err := NewClassifier([]string{`(`}); err == nil {
		t.Error("expected error for bad pattern")
	}
}
```

```powershell
go test ./...
```

### 4.2. Быстрая проверка: curl с заголовком, без прокси

Прокси можно сымитировать, передав заголовок вручную. Так проверяется логика ветвления, но не mTLS.

```powershell
$curl = "C:\Program Files\Git\mingw64\bin\curl.exe"

& $curl -H 'X-Forwarded-Client-Cert: Hash=x;Subject="CN=mcp-gateway"' http://127.0.0.1:8080/   # gateway
& $curl -H 'X-Forwarded-Client-Cert: Hash=x;Subject="CN=ai-agent"' http://127.0.0.1:8080/      # агент
& $curl http://127.0.0.1:8080/                                                                 # 403
```

### 4.3. Полная проверка: настоящий mTLS через Envoy в Docker

Понадобятся Docker Desktop и сертификаты из `mtls-local-certs-windows.md` в папке `certs`.

**1. Запустите приложение на `:8080`**, а не на `127.0.0.1:8080`, чтобы Envoy из контейнера до него достучался. Это настройка только для локального теста.

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

# агент подставляет заголовок с CN gateway → всё равно агент
& $curl --cacert ca.crt --cert ai-agent.crt --key ai-agent.key -H 'X-Forwarded-Client-Cert: Subject="CN=mcp-gateway"' https://localhost:8443/

# без клиентского сертификата → Envoy обрывает handshake
& $curl --cacert ca.crt https://localhost:8443/
```

Ожидаемый результат (проверено на Envoy 1.31.2):

| Запрос | Результат |
|---|---|
| сертификат `mcp-gateway` | ветка gateway |
| сертификат `ai-agent` или любой другой CN от вашего CA | ветка агента |
| агент + поддельный заголовок | ветка агента: `SANITIZE_SET` выкинул подделку |
| без сертификата | `alert certificate required` |
| сертификат не от вашего CA | `alert unknown ca` |

---

## 5. Чек-лист перед продом

- [ ] Из Go-сервера убран TLS, сервер слушает HTTP.
- [ ] Приложение недоступно в обход прокси.
- [ ] В прокси `forward_client_cert_details` = `SANITIZE_SET` или `APPEND_FORWARD`, **не** `FORWARD_ONLY`.
- [ ] В прокси включено `subject: true`.
- [ ] Реальный XFCC на окружении посмотрен в логах: элемент один (если два, см. раздел 3).
- [ ] Шаблоны `gateway_cns` узкие, без `.*` в конце.
- [ ] Для MCP-маршрута отключён таймаут ответа (`timeout: 0s`), иначе прокси оборвёт стриминг.