Для вашей задачи хватит трёх запросов:

Один раз создать вебхук.
Принимать его событие.
По каждому событию запрашивать список коммитов.

Все пути начинаются с https://<bitbucket>/rest/api/latest.

1. Создать вебхук на проект (один раз на проект)

Запрос

http
POST /rest/api/latest/projects/{projectKey}/webhooks
Authorization: Bearer <token с правами admin на проект>
Content-Type: application/json

{
  "name": "commit-processor",
  "url": "https://your-service/bitbucket/webhook",
  "active": true,
  "events": ["repo:refs_changed"],
  "configuration": {
    "secret": "your-secret"
  }
}

Ответ 201 Created

json
{
  "id": 12,
  "name": "commit-processor",
  "createdDate": 1760270000000,
  "updatedDate": 1760270000000,
  "events": ["repo:refs_changed"],
  "configuration": { "secret": "your-secret" },
  "url": "https://your-service/bitbucket/webhook",
  "active": true
}

Вебхук наследуют все репозитории проекта, включая созданные позже.

2. Что пришлёт хук: repo:refs_changed

Заголовки

http
POST /bitbucket/webhook
Content-Type: application/json; charset=utf-8
X-Event-Key: repo:refs_changed
X-Request-Id: 2b6a3c1e-7a1f-4b0e-9c2d-5f4e8a9b1c3d
X-Hub-Signature: sha256=5f2c...e91a

Тело

json
{
  "eventKey": "repo:refs_changed",
  "date": "2026-10-12T15:13:00+0300",
  "actor": {
    "name": "ivanov",
    "emailAddress": "ivanov@example.com",
    "id": 101,
    "displayName": "Ivan Ivanov",
    "active": true,
    "slug": "ivanov",
    "type": "NORMAL"
  },
  "repository": {
    "slug": "backend",
    "id": 84,
    "name": "backend",
    "scmId": "git",
    "state": "AVAILABLE",
    "forkable": true,
    "public": false,
    "project": {
      "key": "PRJ",
      "id": 3,
      "name": "Project",
      "public": false,
      "type": "NORMAL"
    }
  },
  "changes": [
    {
      "ref": {
        "id": "refs/heads/main",
        "displayId": "main",
        "type": "BRANCH"
      },
      "refId": "refs/heads/main",
      "fromHash": "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678",
      "toHash": "f9e8d7c6b5a40312f1e0d9c8b7a6958473625140",
      "type": "UPDATE"
    }
  ]
}

Что брать из тела:

repository.project.key и repository.slug — куда запрашивать коммиты.
changes[].fromHash и toHash — диапазон новых коммитов.
changes[].type:
UPDATE — обычный push;
ADD — новая ветка, fromHash = 0000000000000000000000000000000000000000;
DELETE — удаление ветки, новых коммитов нет.
ref.type — BRANCH или TAG. Теги можно пропускать.

В новых версиях Bitbucket в тело добавлены поля commits (до 5 последних) и toCommit. Полного списка там нет, поэтому коммиты берём запросом 3.

На хук отвечайте 200 или 204.

3. Получить новые коммиты

Запрос

http
GET /rest/api/latest/projects/PRJ/repos/backend/commits?since=a1b2c3d4e5f60718293a4b5c6d7e8f9012345678&until=f9e8d7c6b5a40312f1e0d9c8b7a6958473625140&start=0&limit=100
Authorization: Bearer <token с REPO_READ>

Ответ 200 OK

json
{
  "size": 2,
  "limit": 100,
  "start": 0,
  "isLastPage": true,
  "values": [
    {
      "id": "f9e8d7c6b5a40312f1e0d9c8b7a6958473625140",
      "displayId": "f9e8d7c6b5a",
      "author": {
        "name": "ivanov",
        "emailAddress": "ivanov@example.com",
        "displayName": "Ivan Ivanov"
      },
      "authorTimestamp": 1760271180000,
      "committer": {
        "name": "ivanov",
        "emailAddress": "ivanov@example.com"
      },
      "committerTimestamp": 1760271180000,
      "message": "fix: handle empty payload",
      "parents": [
        { "id": "0c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6a7b8c9d", "displayId": "0c1d2e3f4a5" }
      ]
    },
    {
      "id": "0c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6a7b8c9d",
      "displayId": "0c1d2e3f4a5",
      "author": {
        "name": "ivanov",
        "emailAddress": "ivanov@example.com",
        "displayName": "Ivan Ivanov"
      },
      "authorTimestamp": 1760271000000,
      "committer": {
        "name": "ivanov",
        "emailAddress": "ivanov@example.com"
      },
      "committerTimestamp": 1760271000000,
      "message": "feat: add webhook handler",
      "parents": [
        { "id": "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678", "displayId": "a1b2c3d4e5f" }
      ]
    }
  ]
}
Коммиты идут от новых к старым.
Если isLastPage: false, в ответе будет nextPageStart. Повторите запрос со start={nextPageStart}.
Для type: ADD параметр since не передавайте, иначе придёт вся история ветки. Вместо него подставьте голову основной ветки или отсекайте уже обработанные коммиты по хэшу.