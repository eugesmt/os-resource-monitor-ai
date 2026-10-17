# System Monitor AI — руководство пользователя

System Monitor AI — локальный мониторинг компьютера с AI-анализом. Приложение
собирает реальные метрики ОС, показывает их в браузере, сохраняет историю и
может отвечать на вопросы через настроенный OpenAI-compatible endpoint.

Конфигурация AI задаётся на сервере через `AI_API_KEY`, `AI_BASE_URL`,
`AI_MODEL` и `AI_JSON_MODE`. В web UI выбор модели или provider отсутствует.
Если endpoint/model не настроены, мониторинг работает, а AI-задачи завершаются
с ошибкой конфигурации.

Для установки и запуска сначала используйте [README.md](README.md). После
запуска откройте `http://127.0.0.1:8080`.

## Первый запуск

1. Запустите `make run` или standalone binary.
2. Дождитесь статуса `Live` и появления первого snapshot.
3. Проверьте карточки CPU, памяти, сети, диска, GPU и список процессов.
4. При необходимости выберите или создайте профиль.
5. Задайте вопрос в панели `AI Assistant`.

Сервер по умолчанию слушает loopback. HTTP API, `/mcp` и `/reports/` не имеют
authentication или TLS, поэтому не публикуйте listener в интернет. Для
локального использования оставляйте `ADDR=127.0.0.1:8080`.

## Мониторинг

Главная страница показывает:

- CPU и загрузку отдельных ядер;
- использованную и общую RAM;
- входящий и исходящий сетевой трафик;
- чтение и запись дисков;
- GPU, видеопамять и температуру, если доступен NVIDIA NVML;
- последние процессы, отсортированные по CPU;
- графики CPU/RAM и историю GPU.

История в browser UI — оперативный RingBuffer и исчезает после перезапуска.
`GET /api/events` передаёт новые snapshots через SSE; при разрыве соединения
UI показывает `Переподключение…`.

Если данных ещё нет, `/api/snapshot` возвращает `503`. Недоступная секция
отмечается в `missing_data`, а не подменяется достоверным нулём.

## Профили

Откройте `Профиль` в верхней панели.

Профиль содержит:

- обязательный `user_id` длиной до 1000 символов;
- стиль ответа длиной до 1000 символов;
- формат ответа длиной до 1000 символов;
- до 12 непустых ограничений, каждое длиной до 1000 символов.

Профиль сохраняется в SQLite и используется как preferences для будущих AI
запросов. `user_id` также выбирает отдельную conversation scope:
`profile:<user_id>`. Поэтому у разных профилей независимы chat history, task
state и контекст разговора.

Профили можно создавать, выбирать и изменять. Удаления профиля в текущем UI
нет.

API:

```bash
curl -sS http://127.0.0.1:8080/api/ai/profiles
curl -sS http://127.0.0.1:8080/api/ai/profile
curl -sS -X PUT http://127.0.0.1:8080/api/ai/profile \
  -H 'Content-Type: application/json' \
  -d '{"user_id":"demo","style":"кратко","format":"списками","constraints":[]}'
```

`PUT /api/ai/profile` делает upsert. Ошибки валидации возвращаются как
`{"error":"..."}` со статусом `400`.

## Память агента

Панель `Память агента` содержит три слоя:

| Слой | Назначение | Участие в AI-контексте |
|---|---|---|
| `short_term` | текущая conversation history | история компилируется отдельно из событий диалога |
| `working` | факты, ограничения и решения текущей работы в активной conversation/profile scope | добавляется явно |
| `long_term` | долговременные предпочтения и знания | добавляется явно |

Выберите вкладку, укажите `kind` и `content`, затем нажмите `Сохранить`.
`kind` в UI ограничен 80 символами, `content` — 1000 символами. Backend
отклоняет пустые значения и неизвестный слой.

Working memory следует активной conversation/profile scope; она не является
отдельным изолированным хранилищем для каждого `task_id`. Используйте её для
текущих фактов, ограничений и решений этой scope.

Память сохраняется в SQLite и восстанавливается после перезапуска. Кнопка
`Удалить` отображается для `working` и `long_term`; short-term представляется
историей диалога и в UI отдельно не удаляется.

Важно: пользовательский `scope` не выбирается вручную. Backend связывает
новую запись с активной profile/conversation scope, даже если клиент передал
другое значение `scope`. После смены профиля отображается другой контекст.

API:

```bash
curl -sS http://127.0.0.1:8080/api/ai/memory
curl -sS -X POST http://127.0.0.1:8080/api/ai/memory \
  -H 'Content-Type: application/json' \
  -d '{"layer":"long_term","kind":"preference","content":"Отвечать на русском"}'
```

Ответ POST содержит созданную запись и статус `201`. Удаление выполняется по
ID записи:

```bash
curl -i -X DELETE http://127.0.0.1:8080/api/ai/memory/<memory-id>
```

## Инварианты ассистента

Инварианты — обязательные правила проекта, независимые от chat history и
memory. В панели `Инварианты ассистента` создайте:

- `ID` — до 128 символов;
- категорию — до 64 символов;
- правило — до 2000 символов;
- признак `Активен`.

Всего допускается не более 32 правил, ID должны быть уникальны. `PUT` заменяет
весь список инвариантов, а не только одну запись. В UI сохранение одной строки
собирает текущий список, заменяет элемент с тем же ID и отправляет его целиком.

Только активные правила добавляются в system context AI. При конфликте AI
возвращает `assistant.v2` типа `invariant_conflict` с отказом и
`invariant_ids` нарушенных правил.

API:

```bash
curl -sS http://127.0.0.1:8080/api/ai/invariants
curl -sS -X PUT http://127.0.0.1:8080/api/ai/invariants \
  -H 'Content-Type: application/json' \
  -d '{"items":[{"id":"no-auto-kill","category":"safety","statement":"Не завершать процессы автоматически","enabled":true}]}'
curl -i -X DELETE http://127.0.0.1:8080/api/ai/invariants/no-auto-kill
```

DELETE возвращает `204` при успехе, `404` для неизвестного ID. Ошибки
валидации возвращаются как `{"error":"..."}`.

## AI-задачи

### Обычный вопрос

В панели AI введите, например:

```text
Почему компьютер работает медленно?
```

Обычно первый запрос запускает фоновую task и возвращает `202 Accepted`:

```bash
curl -i -X POST http://127.0.0.1:8080/api/ai/ask \
  -H 'Content-Type: application/json' \
  -d '{"question":"Почему компьютер работает медленно?"}'
```

Ответ содержит `task_state` с `task_id`, `phase`, `status`, `outcome` и
`version`. Фоновый lifecycle:

```text
planning → execution → validation → done
```

Состояние опрашивается до завершения:

```bash
curl -sS http://127.0.0.1:8080/api/ai/task/state
```

Когда `phase=done` и `outcome=completed`, результат можно получить так:

```bash
curl -sS http://127.0.0.1:8080/api/ai/task/result
```

Ответ имеет вид `{"content":"..."}`. Поле `content` содержит JSON
`assistant.v2`, а не готовый HTML. После завершения текущей task endpoint
возвращает последний assistant event текущей conversation scope; отдельной
привязки результата к `task_id` или event ID нет и endpoint не принимает
`task_id`.
До завершения он возвращает `409`.

Если task уже открыта и не ожидает пользователя, новый `/api/ai/ask`
возвращает `409`. Если task ожидает пользовательский ввод, тот же endpoint
может вернуть `200` с `answer` и обновлённым `task_state`.

### Управление task

Получить состояние:

```bash
curl -sS http://127.0.0.1:8080/api/ai/task/state
```

Поставить на паузу или возобновить:

```bash
curl -sS -X POST http://127.0.0.1:8080/api/ai/task/state \
  -H 'Content-Type: application/json' \
  -d '{"task_id":"<task-id>","version":2,"action":"pause"}'

curl -sS -X POST http://127.0.0.1:8080/api/ai/task/state \
  -H 'Content-Type: application/json' \
  -d '{"task_id":"<task-id>","version":3,"action":"resume"}'
```

Продолжить checkpoint вручную:

```bash
curl -sS -X POST http://127.0.0.1:8080/api/ai/task/continue \
  -H 'Content-Type: application/json' \
  -d '{"task_id":"<task-id>","version":3}'
```

Отменить:

```bash
curl -sS -X POST http://127.0.0.1:8080/api/ai/task/cancel \
  -H 'Content-Type: application/json' \
  -d '{"task_id":"<task-id>","version":3}'
```

Для pause/resume/continue обязательны `task_id` и положительный `version`.
Изменившаяся версия даёт `409 task state changed`. Для cancel поле `version`
должно присутствовать и быть положительным, но текущая реализация не
проверяет его на совпадение: отменяется актуальное состояние указанной task.

История переходов доступна для диагностики:

```bash
curl -sS 'http://127.0.0.1:8080/api/ai/task/transitions?task_id=<task-id>'
```

История диалога:

```bash
curl -sS http://127.0.0.1:8080/api/ai/history
```

В SQLite сохраняются goal, phase, checkpoints, version, errors, response type,
transition events и assistant/user conversation events.

## HTTP API

Ошибки API, которые формирует `writeAPIError`, имеют общий JSON-формат:

```json
{"error":"описание ошибки"}
```

Для неподдерживаемого HTTP method сервер возвращает `405`; этот ответ может
быть без JSON body. Заголовок `Allow` у общих API handlers имеет generic
значение `GET, POST`, поэтому не используйте его как точный список методов
конкретного route.

### Monitoring routes

| Метод | Route | Назначение |
|---|---|---|
| `GET` | `/api/health` | readiness: `{"ok":true}` |
| `GET` | `/api/snapshot` | последний snapshot |
| `GET` | `/api/history?limit=120` | оперативная история snapshots |
| `GET` | `/api/metrics/summary?...` | durable aggregates из SQLite |
| `GET` | `/api/events` | SSE live snapshots |

Примеры без AI и секретов:

```bash
curl -sS http://127.0.0.1:8080/api/health
curl -sS http://127.0.0.1:8080/api/snapshot
curl -sS 'http://127.0.0.1:8080/api/history?limit=10'
curl -sSN http://127.0.0.1:8080/api/events
```

`limit` по умолчанию равен 120. Нечисловой `limit` молча возвращается к
значению 120. Значения `limit<=0` или больше 300 нормализуются runtime до 300.

Для aggregate summary нужен один из вариантов:

```bash
curl -sS 'http://127.0.0.1:8080/api/metrics/summary?last_minutes=60'
curl -sS 'http://127.0.0.1:8080/api/metrics/summary?from=2026-10-18T10:00:00Z&to=2026-10-18T11:00:00Z'
```

Диапазон должен быть от 3 минут до 7 дней, `to` не может быть в будущем.

### AI routes

| Метод | Route | Назначение |
|---|---|---|
| `POST` | `/api/ai/ask` | вопрос или ответ пользователя для task |
| `GET` | `/api/ai/history` | conversation events |
| `GET` | `/api/ai/task/state` | текущий task state |
| `POST` | `/api/ai/task/state` | pause/resume |
| `POST` | `/api/ai/task/continue` | checkpoint continuation |
| `POST` | `/api/ai/task/cancel` | отмена task |
| `GET` | `/api/ai/task/transitions?task_id=...` | transitions |
| `GET` | `/api/ai/task/result` | результат завершённой текущей task |
| `POST` | `/api/ai/task` | создать task без запуска вопроса |

`POST /api/ai/task` возвращает `201` с начальным состоянием или `409`, если
уже есть открытая task.

### Profile, memory and invariant routes

| Метод | Route | Назначение |
|---|---|---|
| `GET` | `/api/ai/profile` | активный профиль |
| `PUT` | `/api/ai/profile` | создать/изменить профиль |
| `GET` | `/api/ai/profiles` | список профилей |
| `GET` | `/api/ai/memory` | memory context активной scope |
| `POST` | `/api/ai/memory` | добавить запись |
| `DELETE` | `/api/ai/memory/<id>` | удалить запись |
| `GET` | `/api/ai/invariants` | список правил |
| `PUT` | `/api/ai/invariants` | заменить весь список правил |
| `DELETE` | `/api/ai/invariants/<id>` | удалить правило |

Для методов с JSON body используется `Content-Type: application/json`. Для
неподдерживаемого HTTP method сервер возвращает `405`; список методов лучше
сверять с таблицами выше, а не с generic `Allow` header.

## MCP и диагностический отчёт

Основной сервер предоставляет Streamable HTTP MCP endpoint:

```text
http://127.0.0.1:8080/mcp
```

Внешний MCP-compatible AI agent может вызвать:

- `get_system_overview` — текущий overview без process list;
- `get_top_processes` — до 20 процессов по CPU или memory;
- `get_resource_history` — до 120 samples CPU, memory, load, network или disk;
- `get_metrics_summary` — durable summary за relative/absolute range.

Это отдельный путь от embedded AI: встроенный AI не вызывает MCP tools.

В обычном web UI нет отдельной кнопки или страницы для создания отчёта: отчёт
создаётся внешним MCP agent через tool `save_diagnostic_report`.

При заданном `SYSTEM_MONITOR_MCP_REPORT_DIR`
регистрируется дополнительный tool:

```text
save_diagnostic_report
```

Он принимает Markdown или валидный JSON размером до 128 KiB, создаёт файл с
сгенерированным именем и возвращает `report_id`, относительный `path`, размер и
SHA-256. Расширение файла соответствует `format`: `.md` для Markdown и `.json`
для JSON. Клиент не может передать произвольный путь.

Для HTTP listener `download_url` возвращается только при явном non-wildcard
`ADDR`, например `127.0.0.1:8080` или `[::1]:8080`. При `ADDR=:8080`,
`0.0.0.0:8080` или `[::]:8080` отчёт сохраняется, но URL намеренно не
добавляется, потому что внешний host неоднозначен:

```bash
curl -I http://127.0.0.1:8080/reports/<generated-name>.md
```

В report directory используются атомарная запись и mode `0600`. Сам endpoint
не является read-only, если включён report directory: MCP agent может создавать
файлы только в этой server-controlled директории.

## Частые ошибки

- `503` на `/api/snapshot` — collector ещё не получил snapshot.
- `AI is not configured` — не заданы совместимые `AI_BASE_URL` и `AI_MODEL`.
- `snapshot is stale` — snapshot старше допустимых 10 секунд.
- `409 task is already running` — текущая task ещё выполняется.
- `409 task result is not ready` — task ещё не завершена.
- `409 task state changed` — нужно перечитать state и повторить с новой version.
- `400 profile ...` — нарушены ограничения профиля.
- `400 invalid memory layer` или пустой kind/content — исправьте запись memory.
- `400 invariant ...` — нарушены ограничения или есть duplicate ID.
- `404 task not found` / `404 invariant not found` — объект отсутствует в
  текущей scope.
- `503 ... storage unavailable` — проблема SQLite или backend storage.

Если AI provider вернул malformed JSON, неизвестные поля или неподтверждённые
метрики, ответ отклоняется backend semantic/structural validation. Это может
перевести task в paused state с `last_error`; после исправления provider можно
нажать `Возобновить`.
