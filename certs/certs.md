# Локальные сертификаты для mTLS (Windows, PowerShell)

Инструкция для локальной проверки MCP-сервера, который различает клиентов по CN сертификата: `mcp-gateway` и `ai-agent`.

Нужен Git for Windows: в нём есть `openssl.exe` и `curl.exe`, которые принимают PEM-файлы.

---

## Подготовка

```powershell
$openssl = "C:\Program Files\Git\usr\bin\openssl.exe"
mkdir certs; cd certs
```

Здесь задаётся путь к `openssl`, создаётся папка `certs` и выполняется переход в неё. Все файлы будут лежать там.

> Если `openssl` есть в PATH, вместо `& $openssl` можно писать просто `openssl`.

---

## 1. CA

```powershell
& $openssl req -x509 -new -nodes -newkey rsa:2048 -keyout ca.key -out ca.crt -days 365 -subj "/CN=Local Test CA"
```

Это ваш локальный удостоверяющий центр, им подписываются все остальные сертификаты.

- `ca.crt` указывается на сервере в `ClientCAs`: по нему сервер проверяет, что клиентский сертификат настоящий. Им же клиент (curl) проверяет сертификат сервера.
- `ca.key` нужен только для подписи сертификатов на следующих шагах.

## 2. Файлы расширений

```powershell
"basicConstraints=CA:FALSE`nkeyUsage=digitalSignature,keyEncipherment`nextendedKeyUsage=serverAuth`nsubjectAltName=DNS:localhost,IP:127.0.0.1" | Set-Content -Encoding ascii server.ext
"basicConstraints=CA:FALSE`nkeyUsage=digitalSignature,keyEncipherment`nextendedKeyUsage=clientAuth" | Set-Content -Encoding ascii client.ext
```

Эти файлы задают, для чего можно использовать сертификат. Go проверяет такие поля строго:

- `serverAuth` и `subjectAltName=localhost` нужны серверному сертификату. Без SAN клиент на Go и curl отклонят его, CN для имени хоста уже не учитывается.
- `clientAuth` нужен клиентским сертификатам. Без него `RequireAndVerifyClientCert` может их не принять.
- `CA:FALSE` запрещает использовать эти сертификаты для подписи других.

`-Encoding ascii` нужен потому, что PowerShell по умолчанию может записать файл в UTF-16 или с BOM, а openssl такие файлы не читает.

## 3. Серверный сертификат

```powershell
& $openssl req -new -nodes -newkey rsa:2048 -keyout server.key -out server.csr -subj "/CN=localhost"
& $openssl x509 -req -in server.csr -CA ca.crt -CAkey ca.key -CAcreateserial -out server.crt -days 365 -extfile server.ext
```

С ним MCP-сервер поднимает HTTPS: `ListenAndServeTLS("server.crt", "server.key")`. Первая команда создаёт ключ и запрос на подпись (CSR), вторая подписывает его вашим CA с расширениями из `server.ext`.

## 4. Клиент mcp-gateway

```powershell
& $openssl req -new -nodes -newkey rsa:2048 -keyout mcp-gateway.key -out mcp-gateway.csr -subj "/CN=mcp-gateway"
& $openssl x509 -req -in mcp-gateway.csr -CA ca.crt -CAkey ca.key -CAcreateserial -out mcp-gateway.crt -days 365 -extfile client.ext
```

С этим сертификатом подключается MCP Gateway. `CN=mcp-gateway` — то самое значение, по которому код выбирает ветку gateway. Подпись вашим CA нужна, чтобы сервер принял сертификат на TLS-рукопожатии.

## 5. Клиент ai-agent

```powershell
& $openssl req -new -nodes -newkey rsa:2048 -keyout ai-agent.key -out ai-agent.csr -subj "/CN=ai-agent"
& $openssl x509 -req -in ai-agent.csr -CA ca.crt -CAkey ca.key -CAcreateserial -out ai-agent.crt -days 365 -extfile client.ext
```

То же самое для агента: `CN=ai-agent` направит запрос в ветку агента.

## 6. Проверка сертификатов

```powershell
& $openssl x509 -in mcp-gateway.crt -noout -subject
& $openssl x509 -in ai-agent.crt -noout -subject
& $openssl verify -CAfile ca.crt server.crt mcp-gateway.crt ai-agent.crt
```

Первые две команды показывают CN в каждом клиентском сертификате, так легко поймать опечатку. Третья проверяет, что все сертификаты подписаны вашим CA. Для каждого файла должно быть `OK`, иначе сервер их не примет.

---

## Проверка сервера curl'ом

Используйте `curl.exe` из Git for Windows. Встроенный в Windows curl не принимает PEM-ключи через `--cert`/`--key`, а просто `curl` в PowerShell 5 — это алиас `Invoke-WebRequest`.

```powershell
$curl = "C:\Program Files\Git\mingw64\bin\curl.exe"
```

Команды запускаются из папки `certs`. Порт и путь (`:8443/`) замените на свои. Например, если MCP-эндпоинт у вас `/mcp`, адрес будет `https://localhost:8443/mcp`.

### Запрос от MCP Gateway

```powershell
& $curl --cacert ca.crt --cert mcp-gateway.crt --key mcp-gateway.key https://localhost:8443/
```

Клиент предъявляет сертификат с `CN=mcp-gateway`, и сервер должен выполнить ветку gateway.

- `--cacert ca.crt` нужен, чтобы curl доверял сертификату сервера (он подписан вашим CA).
- `--cert` и `--key` — клиентский сертификат и его ключ.

### Запрос от агента

```powershell
& $curl --cacert ca.crt --cert ai-agent.crt --key ai-agent.key https://localhost:8443/
```

То же самое с `CN=ai-agent`: сервер должен выполнить ветку агента.

### Запрос без сертификата

```powershell
& $curl --cacert ca.crt https://localhost:8443/
```

Проверяет, что сервер без клиентского сертификата не пускает. При `RequireAndVerifyClientCert` curl должен упасть с ошибкой TLS handshake.

### Отладка

```powershell
& $curl -v --cacert ca.crt --cert mcp-gateway.crt --key mcp-gateway.key https://localhost:8443/
```

С флагом `-v` в выводе видно, какой сертификат прислал сервер и отправил ли curl клиентский.

---

## Итоговые файлы

| Файл | Где используется |
|---|---|
| `ca.crt` | сервер (`ClientCAs`), клиенты (`--cacert` / `RootCAs`) |
| `ca.key` | только для подписи сертификатов, никому не передаётся |
| `server.crt`, `server.key` | MCP-сервер (`ListenAndServeTLS`) |
| `mcp-gateway.crt`, `mcp-gateway.key` | клиент MCP Gateway |
| `ai-agent.crt`, `ai-agent.key` | клиент агента |

Файлы `.csr`, `.ext` и `ca.srl` — промежуточные, их можно удалить.

> Все ключи — только для локальных тестов. Не коммитьте их; добавьте `certs/` в `.gitignore`.