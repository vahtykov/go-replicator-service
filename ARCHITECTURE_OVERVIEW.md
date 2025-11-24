# Архитектура сервиса репликации данных между контурами

## Концепция

**Real-time репликация** через триггеры PostgreSQL и Kafka для всех таблиц.

---

## Общая схема системы

```
┌────────────────────────────────────────────────────────────────┐
│                     КОНТУР A (Active)                           │
├────────────────────────────────────────────────────────────────┤
│                                                                 │
│  ┌──────────────┐                                              │
│  │ Приложение   │                                              │
│  │ (без изм.)   │                                              │
│  └──────┬───────┘                                              │
│         │ INSERT/UPDATE/DELETE                                 │
│         ↓                                                       │
│  ┌──────────────────────────────────┐                         │
│  │      PostgreSQL A                │                         │
│  │                                  │                         │
│  │  ┌────────────────────────────┐  │                         │
│  │  │ Бизнес-таблицы             │  │                         │
│  │  │ (users, orders, products)  │  │                         │
│  │  │                            │  │                         │
│  │  │ [AFTER триггеры на всех]   │  │                         │
│  │  └─────────────┬──────────────┘  │                         │
│  │                ↓                  │                         │
│  │  ┌────────────────────────────┐  │                         │
│  │  │ replication_queue            │  │                         │
│  │  │ processed_events           │  │                         │
│  │  └────────────────────────────┘  │                         │
│  └──────────────────────────────────┘                         │
│                 ↓                                               │
│  ┌──────────────────────────────┐                             │
│  │  ReplicatorPublisher         │                             │
│  │  (каждые 1 сек)              │                             │
│  │                              │                             │
│  │  • Читает replication_queue    │                             │
│  │  • Публикует в Kafka         │                             │
│  │  • Помечает published=true   │                             │
│  └──────────────┬───────────────┘                             │
│                 │                                               │
└─────────────────┼───────────────────────────────────────────────┘
                  ↓
     ┌────────────────────────────────────────┐
     │    Kafka (георезервированная)          │
     │                                         │
     │  Topics (по таблицам):                  │
     │  • users_changes                        │
     │  • orders_changes                       │
     │  • products_changes                     │
     │                                         │
     │  Retention: 7 days                      │
     │  Partitions: по Primary Key             │
     └────────────┬────────────────────────┬───┘
                  │                        │
                  ↓                        ↓
     ┌────────────────────────┐  ┌────────────────────────┐
     │ ReplicatorConsumer     │  │ ReplicatorConsumer     │
     │ Consumer Group A       │  │ Consumer Group B       │
     │                        │  │                        │
     │ • Читает Kafka         │  │ • Читает Kafka         │
     │ • Фильтрует события    │  │ • Фильтрует события    │
     │   от своего контура    │  │   от своего контура    │
     │ • Проверяет версии     │  │ • Проверяет версии     │
     │ • Отключает триггеры   │  │ • Отключает триггеры   │
     │ • Применяет в БД       │  │ • Применяет в БД       │
     └────────────┬───────────┘  └────────────┬───────────┘
                  ↓                           ↓
     ┌────────────────────────┐  ┌────────────────────────┐
     │   PostgreSQL A         │  │   PostgreSQL B         │
     │   (применяет из B)     │  │   (применяет из A)     │
     └────────────────────────┘  └────────────────────────┘
```

---

## Компоненты системы

### 1. **Триггеры PostgreSQL**
- Устанавливаются на **все реплицируемые таблицы**
- Тип: `AFTER INSERT OR UPDATE OR DELETE`
- Автоматически захватывают все DML операции
- Записывают в служебную таблицу `replication_queue`
- **Не требуют изменений в коде приложения**

### 2. **ReplicatorPublisher**
- Периодически (каждую секунду) читает `replication_queue`
- Находит записи с `published = FALSE`
- Формирует события в формате JSON
- Публикует события в Kafka
- Помечает записи как `published = TRUE`
- **Гарантирует at-least-once delivery**
- Работает в транзакции (чтение + update флага)

### 3. **ReplicatorConsumer**
- Читает события из Kafka (своя consumer group на каждом контуре)
- **Фильтрует события от своего контура** (защита от петли)
- Проверяет идемпотентность через `processed_events`
- Проверяет версии данных (conflict resolution)
- **Отключает триггеры** перед применением (`session_replication_role = 'replica'`)
- Применяет изменения в локальную БД
- Включает триггеры обратно (`session_replication_role = 'origin'`)

### 4. **Kafka**
- Георезервированная (доступна обоим контурам)
- Отдельный топик для каждой таблицы
- **Partition key = Primary Key** (сохранение порядка изменений одной записи)
- Retention: 7 дней (для disaster recovery)
- Consumer group на каждом контуре

---

## Поток данных

```
Приложение
    ↓
PostgreSQL → [Триггер AFTER] → replication_queue
                                      ↓
                              ReplicatorPublisher (каждые 1 сек)
                                      ↓
                                   Kafka
                                      ↓
                              ReplicatorConsumer
                                      ↓
                              [Отключает триггеры]
                                      ↓
                          PostgreSQL (другой контур)

Задержка репликации: 1-3 секунды
```

---

## 🔒 Защита от петли репликации (КРИТИЧНО!)

### Проблема

**Без защиты возникает бесконечная петля:**

```
КОНТУР A:
1. Приложение: INSERT INTO users (id=1, name='John')
2. Триггер срабатывает → replication_queue
3. ReplicatorPublisher → Kafka (source=contour_a)

КОНТУР B:
4. ReplicatorConsumer читает событие (source=contour_a)
5. Применяет: INSERT INTO users (id=1, name='John')
6. ❌ Триггер срабатывает СНОВА → replication_queue
7. ReplicatorPublisher → Kafka (source=contour_b)

КОНТУР A:
8. ReplicatorConsumer читает событие (source=contour_b)
9. Применяет: INSERT (уже есть) или UPDATE
10. ❌ Триггер срабатывает → петля!
```

### Решение: Проверка application_name в триггере

**Используем `application_name` для идентификации ReplicatorConsumer:**

```sql
-- В триггере (уже реализовано в generic_replication_trigger):
IF current_setting('application_name', true) = 'replicator_consumer' THEN
    RETURN NULL;  -- Не записываем в replication_queue
END IF;
```

**В ReplicatorConsumer при подключении к БД:**

```go
// Go код
connString := fmt.Sprintf(
    "host=%s port=%d dbname=%s user=%s password=%s application_name=replicator_consumer",
    cfg.DB.Host, cfg.DB.Port, cfg.DB.Database, cfg.DB.User, cfg.DB.Password,
)
db, err := sql.Open("postgres", connString)
```

**Или через DSN параметры:**
```
postgresql://user:password@host:5432/database?application_name=replicator_consumer
```

**Применение изменений:**

```go
func (r *ReplicatorConsumer) applyEvent(event ReplicationEvent) error {
    tx, _ := r.db.Begin()
    defer tx.Rollback()
    
    // application_name уже установлен при подключении
    // Триггер автоматически пропустит эту операцию
    
    tx.Exec("INSERT INTO users ...")
    
    return tx.Commit()
}
```

**Как работает:**
- ReplicatorConsumer подключается с `application_name=replicator_consumer`
- Триггер проверяет application_name перед записью в replication_queue
- Если application_name = 'replicator_consumer', триггер не срабатывает
- Обычные приложения подключаются без этого параметра → триггер срабатывает ✅

### Пошаговая схема с защитой

```
КОНТУР A (Active):
1. Приложение → INSERT users (application_name = 'my_app' или любое)
2. Триггер проверяет application_name ≠ 'replicator_consumer'
3. Триггер СРАБАТЫВАЕТ → replication_queue ✅
4. ReplicatorPublisher → Kafka (source=contour_a)

КОНТУР B (Passive):
5. ReplicatorConsumer (подключен с application_name='replicator_consumer'):
   - BEGIN;
   - INSERT users (триггер проверяет application_name)
   - Триггер видит 'replicator_consumer' → НЕ СРАБАТЫВАЕТ ✅
   - COMMIT;
6. replication_queue остается ПУСТЫМ ✅
7. Нет новых событий в Kafka ✅
8. Петля ПРЕДОТВРАЩЕНА ✅
```

### Дополнительная защита: Фильтрация событий по source.contour

Для дополнительной надежности используйте фильтрацию на уровне ReplicatorConsumer:

```go
func (r *ReplicatorConsumer) shouldProcess(event ReplicationEvent) bool {
    // Пропускаем события от СВОЕГО контура
    if event.Source.Contour == r.config.MyContour {
        r.logger.Debug("Skipping own event", zap.String("event_id", event.EventID))
        return false
    }
    return true
}
```

**Двойная защита:**
1. **Триггер** проверяет `application_name` - основная защита
2. **ReplicatorConsumer** фильтрует по `source.contour` - дополнительная защита

**Преимущества:**
- Если триггер случайно срабатывает, фильтрация по contour предотвратит дубликаты
- Defense in depth - два уровня защиты
- Защита от ошибок конфигурации

---

## Ключевые механизмы

### Разрешение конфликтов

```
Каждая таблица содержит:
- version BIGINT          (инкрементируется при каждом UPDATE)
- updated_at TIMESTAMPTZ  (timestamp последнего изменения)
- updated_by VARCHAR(50)  (контур-источник: 'contour_a' или 'contour_b')
```

**Стратегия:** Last-Write-Wins на основе version.

```sql
INSERT INTO users (...) VALUES (...)
ON CONFLICT (id) DO UPDATE SET ...
WHERE users.version < EXCLUDED.version;  -- Применяем только если version новее
```

### Идемпотентность

```sql
-- Проверка перед применением
IF EXISTS(SELECT 1 FROM processed_events WHERE event_id = $1) THEN
    SKIP (уже обработано)
ELSE
    APPLY + INSERT INTO processed_events (event_id)
END IF
```

Гарантирует, что событие не будет применено дважды при Kafka retry.

---

## Структура данных в Kafka

### Формат сообщения (JSON)

```json
{
  "event_id": "uuid",
  "timestamp": "2025-11-18T10:30:00Z",
  "source": {
    "contour": "active",
    "database": "main_db"
  },
  "table": "users",
  "operation": "UPDATE",
  "primary_key": {"id": 123},
  "before": {"id": 123, "name": "John", "version": 5, ...},
  "after": {"id": 123, "name": "Jane", "version": 6, ...}
}
```

### Partition Key

```
Key = Primary Key значение
```

**Важно:** Все изменения одной записи попадают в одну партицию Kafka, что **гарантирует порядок**.

---

## Требования к таблицам БД

### Обязательные поля

```sql
-- Добавить в ВСЕ реплицируемые таблицы:
ALTER TABLE users ADD COLUMN version BIGINT DEFAULT 1;
ALTER TABLE users ADD COLUMN updated_at TIMESTAMPTZ DEFAULT NOW();
ALTER TABLE users ADD COLUMN updated_by VARCHAR(50); -- контур-источник ('contour_a' или 'contour_b')
```

### Служебные таблицы

```sql
-- Для захвата изменений
CREATE TABLE replication_queue (
    id BIGSERIAL PRIMARY KEY,
    table_name VARCHAR(255) NOT NULL,
    operation VARCHAR(10) NOT NULL,      -- INSERT/UPDATE/DELETE
    record_data JSONB NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    published BOOLEAN DEFAULT FALSE,
    published_at TIMESTAMPTZ
);
CREATE INDEX idx_repl_log_unpublished ON replication_queue(published) WHERE NOT published;

-- Для идемпотентности
CREATE TABLE processed_events (
    event_id VARCHAR(255) PRIMARY KEY,
    processed_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX idx_processed_events_timestamp ON processed_events(processed_at);
```

---

## ✅ Достоинства подхода

### 1. **Не требует изменений в приложении**
- Триггеры PostgreSQL захватывают изменения автоматически
- Приложение продолжает работать как обычно
- Репликация полностью прозрачна для бизнес-логики

### 2. **Надежность**
- Даже при падении Kafka данные не теряются (хранятся в `replication_queue`)
- At-least-once delivery с идемпотентностью на стороне consumer
- Kafka retention 7 дней - можно восстановить при длительном сбое
- ReplicatorPublisher продолжит публикацию после восстановления Kafka

### 3. **Real-time репликация**
- Задержка 1-3 секунды
- Подходит для большинства бизнес-сценариев
- Passive контур всегда почти актуален

### 4. **Масштабируемость**
- Kafka партиции для параллельной обработки
- Можно добавлять ReplicatorConsumer instances
- Partition key = Primary Key гарантирует порядок изменений одной записи

### 5. **Наблюдаемость**
- Все изменения проходят через Kafka (audit trail)
- Легко отслеживать replication lag
- Возможность replay событий для восстановления

### 6. **Географическая распределенность**
- Контуры изолированы (нет прямого сетевого доступа)
- Единственный канал связи - георезервированная Kafka
- Автоматическое переключение при failover активного контура

---

## ⚠️ Ограничения и риски

### 1. **Нет транзакционности между таблицами**

Приложение не использует транзакции, поэтому каждая DML операция реплицируется независимо.

**Проблема:**
```
INSERT INTO orders (id=1, user_id=100);
INSERT INTO order_items (order_id=1, product_id=500);

→ Две независимые репликации
→ Временно на пассивном контуре order без items
```

**Митигация:** Использовать версионирование данных для eventual consistency.

### 2. **Referential Integrity**

**Проблема:** FK constraints могут нарушаться при репликации (child до parent).

**Решение:** При применении в ReplicatorConsumer временно отключать FK:
```sql
SET CONSTRAINTS ALL DEFERRED;  -- в начале транзакции
-- apply changes
COMMIT;  -- проверка FK при коммите
```

### 3. **Задержка репликации 1-3 секунды**

Пассивный контур всегда отстает на время публикации и обработки.

**Риск:** При failover данные за последние секунды могут отсутствовать.

**Митигация:** 
- Graceful shutdown активного контура (дождаться публикации всех событий)
- Дать время пассивному контуру догнать перед активацией

### 4. **Конфликты при переключении**

**Сценарий:**
```
T1: Контур A активен: user.balance=100, version=5
T2: Событие в Kafka (еще не обработано)
T3: Failover на контур B
T4: Контур B изменяет: user.balance=80, version=6
T5: Старое событие (version=5) доходит до контура B
```

**Результат:** version=5 < version=6 → событие игнорируется ✅

**Требование:** ОБЯЗАТЕЛЬНО использовать версионирование!

### 5. **Overhead триггеров**

- Триггеры добавляют ~10-20% overhead на запись
- Таблица `replication_queue` растет

**Митигация:**
- Периодически очищать `replication_queue` (WHERE published = TRUE AND created_at < NOW() - INTERVAL '7 days')
- Индекс на `(published)` для быстрого поиска unpublished

### 6. **Нет поддержки DDL**

Изменения схемы (ALTER TABLE, CREATE INDEX) не реплицируются.

**Решение:** 
- Выполнять миграции вручную на обоих контурах
- Использовать tools: Flyway, Liquibase
- Координировать DDL с командой

---

## Мониторинг

### Критичные метрики

```
1. Replication Lag
   - Разница между event.timestamp и NOW()
   - Alert: > 10 секунд

2. Kafka Consumer Lag
   - Необработанные сообщения в топиках
   - Alert: > 1000 сообщений

3. Error Rate
   - Failed события / Total события
   - Alert: > 1%

4. Unpublished Events
   - SELECT COUNT(*) FROM replication_queue WHERE NOT published
   - Alert: > 10000
```

---

## Итого

**Real-time репликация через Kafka** - pragmatic решение для Active-Passive failover между географически распределенными контурами.

### Когда подходит:
✅ Active-Passive (один контур резервный)  
✅ Асинхронная репликация приемлема (1-3 сек lag)  
✅ Георезервированная Kafka доступна  
✅ Нельзя использовать встроенную PostgreSQL репликацию  

### Ключевые требования:
⚠️ **Версионирование данных обязательно**  
⚠️ **Защита от петли репликации критична**  
⚠️ **Мониторинг replication lag**  
⚠️ **Периодическая очистка replication_queue**

