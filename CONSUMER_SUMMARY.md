# ReplicatorConsumer - Сводка реализации

## ✅ Что реализовано

### 1. SQL миграция (`sql/05_make_fk_deferrable.sql`)

**Назначение:** Автоматически делает все FK constraints DEFERRABLE INITIALLY IMMEDIATE

**Использование:**
```bash
psql -U postgres -d your_database -f sql/05_make_fk_deferrable.sql
```

**Что делает:**
- Сканирует все FK constraints в БД
- Пересоздает их как `DEFERRABLE INITIALLY IMMEDIATE`
- Выводит отчет о миграции
- Показывает финальный статус всех FK

---

### 2. Kafka Consumer (`internal/kafka/consumer.go`)

**Функциональность:**
- ✅ Подписка на топики Kafka
- ✅ Polling сообщений с timeout
- ✅ SSL поддержка
- ✅ Manual commit (после успешной обработки)
- ✅ Consumer group management

**Ключевые методы:**
- `Poll(timeout)` - читает сообщение
- `Commit(message)` - подтверждает обработку
- `Close()` - graceful shutdown

---

### 3. Модели (`internal/database/models.go`)

**ProcessedEvent:**
```go
type ProcessedEvent struct {
    EventID     string    `gorm:"primaryKey"`
    ProcessedAt time.Time
}
```

Используется для идемпотентности - проверка, что событие не обработано дважды.

---

### 4. Consumer Logic (`internal/consumer/consumer.go`)

**Основной процесс:**
```
1. Poll сообщение из Kafka
2. Парсинг JSON → ReplicationEvent
3. Фильтрация: пропуск событий от своего контура
4. Проверка идемпотентности (processed_events)
5. Применение события к БД
6. Commit в Kafka
```

**Защита от петли репликации:**
```go
if event.Source.Contour == myContour {
    skip  // Пропускаем свои события
}
```

**Метрики:**
- `processedCount` - успешно обработано
- `skippedCount` - пропущено (свои события)
- `failedCount` - ошибки обработки

---

### 5. Event Applier (`internal/consumer/applier.go`)

**Применение DML операций с проверкой версий:**

#### INSERT:
```go
1. Проверить существование записи
2. Если не существует → INSERT
3. Если существует:
   - Проверить версию
   - last_write_wins: если incoming version > existing → UPDATE
   - Иначе → SKIP
```

#### UPDATE:
```go
1. Проверить существование записи
2. Если не существует → INSERT (может прийти раньше)
3. Проверить версию:
   - existing_version >= incoming_version → SKIP
   - existing_version < incoming_version → UPDATE
```

#### DELETE:
```go
1. Проверить существование
2. Если не существует → SKIP (уже удалено)
3. Если существует → DELETE
```

**Conflict Resolution стратегии:**
- `last_write_wins` - применяется версия с большим номером ✅
- `skip` - пропуск при конфликте
- `error` - ошибка при конфликте

---

### 6. Конфигурация (`config.consumer.yaml`)

```yaml
service:
  contour: "contour_a"  # ИМЯ КОНТУРА

database:
  application_name: "replicator_consumer"  # ← КРИТИЧНО!
  
kafka:
  consumer_group: "replicator-consumer-contour_a"
  topics:
    - "users_changes"
    - "orders_changes"
    
processing:
  conflict_resolution: "last_write_wins"
```

**Ключевой параметр:**
```yaml
database.application_name: "replicator_consumer"
```
Используется триггерами для пропуска записи в `replication_queue`.

---

### 7. Main (`cmd/consumer/main.go`)

**Основные компоненты:**
- ✅ Загрузка конфигурации
- ✅ Инициализация логгера
- ✅ Подключение к PostgreSQL с `application_name`
- ✅ Создание Kafka consumer
- ✅ Graceful shutdown (SIGINT/SIGTERM)
- ✅ Вывод метрик при завершении

---

## 🔒 Защита от петли репликации

### Двойная защита:

**1. На уровне триггера (основная):**
```sql
IF current_setting('application_name', true) = 'replicator_consumer' THEN
    RETURN NULL;  -- Не пишем в replication_queue
END IF;
```

**2. На уровне Consumer (дополнительная):**
```go
if event.Source.Contour == myContour {
    skip  // Пропускаем свои события
}
```

---

## 📊 Как работает полный цикл

### Сценарий: INSERT на контуре A

```
КОНТУР A (Active):
1. Приложение: INSERT INTO users (id=1, name='John')
2. Триггер проверяет application_name ≠ 'replicator_consumer' → ✅
3. Триггер → replication_queue (published=false)
4. ReplicatorPublisher:
   - SELECT ... FOR UPDATE SKIP LOCKED
   - Publish to Kafka (topic=users_changes, source=contour_a)
   - UPDATE published=true
5. ✅ Событие в Kafka

КОНТУР B (Passive):
6. ReplicatorConsumer:
   - Poll from Kafka
   - Parse event (source=contour_a)
   - Фильтрация: source ≠ my_contour → ✅ process
   - BEGIN;
   - SET CONSTRAINTS ALL DEFERRED
   - Проверка processed_events → не найдено
   - INSERT INTO users (application_name='replicator_consumer')
   - Триггер видит application_name='replicator_consumer' → НЕ срабатывает ✅
   - INSERT INTO processed_events (event_id)
   - COMMIT (FK constraints проверяются здесь)
7. ✅ Данные реплицированы
8. ✅ Петля предотвращена
```

---

## 🚀 Быстрый старт

### 1. Подготовка БД

```bash
# Сделать FK constraints DEFERRABLE
psql -U postgres -d main_db -f sql/05_make_fk_deferrable.sql
```

### 2. Настройка конфигурации

```bash
# Отредактировать config.consumer.yaml
vim config.consumer.yaml

# Важно:
# - service.contour = "contour_a" (имя вашего контура)
# - database.application_name = "replicator_consumer" (обязательно!)
# - kafka.consumer_group = "replicator-consumer-contour_a" (уникальная на контур)
# - kafka.topics = список всех реплицируемых таблиц
```

### 3. Запуск

```bash
# Сборка
make build-consumer

# Запуск
./bin/consumer -config config.consumer.yaml

# Или одной командой
make dev-consumer
```

### 4. Проверка

**Логи:**
```json
{"level":"info","component":"consumer","event_id":"...","table":"users","operation":"INSERT","message":"Event applied successfully"}
```

**Метрики при остановке:**
```json
{"level":"info","processed":1523,"skipped":0,"failed":0,"message":"Consumer metrics"}
```

**БД:**
```sql
-- Проверить обработанные события
SELECT COUNT(*) FROM processed_events;

-- Последние обработанные события
SELECT * FROM processed_events ORDER BY processed_at DESC LIMIT 10;
```

---

## 🧪 Тестирование

### Тест 1: Простая репликация

**На контуре A:**
```sql
INSERT INTO users (name, email, updated_by) 
VALUES ('Test User', 'test@example.com', 'contour_a');
```

**На контуре B (через несколько секунд):**
```sql
SELECT * FROM users WHERE name = 'Test User';
-- Должна появиться запись ✅
```

### Тест 2: Защита от петли

**Проверка application_name:**
```sql
-- На контуре B проверить, что НЕТ записей в replication_queue
-- после применения события из контура A
SELECT COUNT(*) FROM replication_queue 
WHERE table_name = 'users' 
  AND published = false;
-- Должно быть 0 ✅
```

### Тест 3: Conflict Resolution

**На контуре A:**
```sql
UPDATE users SET name = 'Updated A', version = 10 WHERE id = 1;
```

**На контуре B (до репликации):**
```sql
UPDATE users SET name = 'Updated B', version = 5 WHERE id = 1;
```

**Результат:**
- Consumer применит событие с version=10 (newer)
- Финальное имя: 'Updated A' ✅

---

## 📝 Ключевые файлы

```
✅ sql/05_make_fk_deferrable.sql      - Миграция FK
✅ config.consumer.yaml               - Конфигурация
✅ internal/kafka/consumer.go         - Kafka consumer
✅ internal/consumer/consumer.go      - Основная логика
✅ internal/consumer/applier.go       - Применение DML
✅ internal/consumer/event.go         - Модель события
✅ internal/config/consumer.go        - Конфиг структуры
✅ cmd/consumer/main.go               - Точка входа
✅ Makefile                           - Build команды
```

---

## ✅ Соблюдение требований

### Из tasklist:

**2.1) Чтение из Kafka** ✅
- Своя consumer group на каждом контуре
- Manual commit после успешной обработки

**2.2) Фильтрация событий** ✅
- Пропуск событий от своего контура
- Двойная защита: триггер + consumer

**2.3) Идемпотентность** ✅
- Проверка `processed_events` перед применением
- Запись `event_id` после успешной обработки

**2.4) Проверка версии** ✅
- Last-Write-Wins по `version`
- Автоматическое разрешение конфликтов

**2.5) Применение DML** ✅
- `application_name=replicator_consumer`
- `SET CONSTRAINTS ALL DEFERRED`
- INSERT/UPDATE/DELETE с проверкой версий

---

## 🎯 Что дальше

**Оба сервиса готовы:**
1. ✅ ReplicatorPublisher
2. ✅ ReplicatorConsumer

**Следующие шаги:**
1. 🧪 Интеграционное тестирование (два контура)
2. 📊 Настройка мониторинга
3. 🚨 Настройка алертов
4. 📦 Production deployment

---

## Готово! 🎉

ReplicatorConsumer полностью реализован и готов к интеграционному тестированию вместе с ReplicatorPublisher.

