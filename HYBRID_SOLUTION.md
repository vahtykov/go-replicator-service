# Гибридное решение: Триггеры + Периодический опрос (БЕЗ прямого доступа между контурами)

## Ключевое ограничение
❗ **Между контурами НЕТ прямого сетевого доступа**  
❗ **Единственный канал связи - георезервированная Kafka**

---

## Правильная архитектура

### Для КРИТИЧНЫХ таблиц (real-time через триггеры)

```
КОНТУР A (Active)                           КОНТУР B (Passive)

┌─────────────────────┐                    ┌─────────────────────┐
│   Приложение        │                    │   Приложение        │
│   INSERT/UPDATE     │                    │   (не активно)      │
└──────────┬──────────┘                    └─────────────────────┘
           ↓
┌─────────────────────┐
│   PostgreSQL        │
│   ┌───────────────┐ │
│   │ Триггеры      │ │
│   │ ↓             │ │
│   │ repl_log      │ │
│   └───────────────┘ │
└──────────┬──────────┘
           ↓
┌─────────────────────┐
│ Outbox Publisher    │
│ (читает repl_log)   │
└──────────┬──────────┘
           ↓
           │
    ┌──────────────────────────────────┐
    │        Kafka (geo-replicated)     │
    │     Topic: users_changes          │
    └──────────┬───────────────────┬───┘
               ↓                   ↓
┌─────────────────────┐  ┌─────────────────────┐
│ Replicator Service  │  │ Replicator Service  │
│ (Consumer Group A)  │  │ (Consumer Group B)  │
└──────────┬──────────┘  └──────────┬──────────┘
           ↓                        ↓
┌─────────────────────┐  ┌─────────────────────┐
│   PostgreSQL A      │  │   PostgreSQL B      │
│   (применяет)       │  │   (применяет)       │
└─────────────────────┘  └─────────────────────┘
```

---

### Для НЕКРИТИЧНЫХ таблиц (периодический опрос)

**ВАЖНО:** Здесь НЕТ "diff между БД". Вместо этого:
- Каждый контур **сам** читает **свою локальную БД**
- Находит изменения с последней синхронизации (WHERE updated_at > last_sync)
- Публикует в Kafka
- Другой контур читает из Kafka и применяет

```
КОНТУР A                                    КОНТУР B

┌─────────────────────┐                    ┌─────────────────────┐
│   PostgreSQL A      │                    │   PostgreSQL B      │
│                     │                    │                     │
│ SELECT * FROM tbl   │                    │ SELECT * FROM tbl   │
│ WHERE updated_at >  │                    │ WHERE updated_at >  │
│   last_sync         │                    │   last_sync         │
└──────────┬──────────┘                    └──────────┬──────────┘
           ↓                                          ↓
┌─────────────────────┐                    ┌─────────────────────┐
│ Sync Service A      │                    │ Sync Service B      │
│ (каждые 5 мин)      │                    │ (каждые 5 мин)      │
│                     │                    │                     │
│ 1. SELECT changed   │                    │ 1. SELECT changed   │
│ 2. Publish to Kafka │                    │ 2. Publish to Kafka │
└──────────┬──────────┘                    └──────────┬──────────┘
           ↓                                          ↓
           │                                          │
    ┌──────────────────────────────────┐             │
    │        Kafka                      │◄────────────┘
    │  Topic: products_changes_periodic │
    └──────────┬───────────────────┬───┘
               ↓                   ↓
┌─────────────────────┐  ┌─────────────────────┐
│ Replicator Service  │  │ Replicator Service  │
│ (Consumer Group A)  │  │ (Consumer Group B)  │
│                     │  │                     │
│ Читает СВОЙ же      │  │ Читает СВОЙ же      │
│ контур, но          │  │ контур, но          │
│ применяет изменения │  │ применяет изменения │
│ из ДРУГОГО          │  │ из ДРУГОГО          │
└──────────┬──────────┘  └──────────┬──────────┘
           ↓                        ↓
┌─────────────────────┐  ┌─────────────────────┐
│   PostgreSQL A      │  │   PostgreSQL B      │
└─────────────────────┘  └─────────────────────┘
```

---

## Детальное объяснение периодического опроса

### Проблема классического diff
```
❌ НЕПРАВИЛЬНО (требует доступ между контурами):
Контур A → SELECT from DB_A
        → SELECT from DB_B  (НЕТ ДОСТУПА!)
        → Compare
        → Replicate difference
```

### Правильный подход: Change Data Capture через polling

```
✅ ПРАВИЛЬНО (каждый контур работает локально):

Контур A:
  1. SELECT * FROM products 
     WHERE updated_at > '2024-11-10 10:00:00'  (последняя синхронизация)
  2. Нашлось 10 записей
  3. Публикуем эти 10 записей в Kafka → products_changes_periodic
     с пометкой source=contour_a

Контур B:
  1. Читает из Kafka (source=contour_a)
  2. Применяет эти изменения в свою БД
```

---

## Структура таблиц для гибридного варианта

### Обязательные поля во ВСЕХ таблицах

```sql
-- Для критичных И некритичных таблиц:
ALTER TABLE users ADD COLUMN version BIGINT DEFAULT 1;
ALTER TABLE users ADD COLUMN updated_at TIMESTAMPTZ DEFAULT NOW();
ALTER TABLE users ADD COLUMN updated_by VARCHAR(50); -- 'contour_a' или 'contour_b'

-- Для некритичных таблиц (soft delete):
ALTER TABLE products ADD COLUMN deleted_at TIMESTAMPTZ;
```

### Таблица для отслеживания последней синхронизации

```sql
CREATE TABLE sync_state (
    table_name VARCHAR(255) PRIMARY KEY,
    last_sync_timestamp TIMESTAMPTZ NOT NULL,
    last_sync_version BIGINT,
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

-- Для каждой некритичной таблицы:
INSERT INTO sync_state (table_name, last_sync_timestamp) 
VALUES ('products', '1970-01-01'::timestamptz);
```

---

## Компоненты системы

### Компонент 1: Триггеры (для критичных таблиц)

```sql
-- Пример для таблицы users (критичная)
CREATE TABLE replication_log (
    id BIGSERIAL PRIMARY KEY,
    table_name VARCHAR(255) NOT NULL,
    operation VARCHAR(10) NOT NULL,
    record_data JSONB NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    published BOOLEAN DEFAULT FALSE
);

CREATE OR REPLACE FUNCTION users_replication_trigger()
RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'INSERT' OR TG_OP = 'UPDATE' THEN
        INSERT INTO replication_log (table_name, operation, record_data)
        VALUES (
            'users', 
            TG_OP, 
            jsonb_build_object(
                'id', NEW.id,
                'name', NEW.name,
                'email', NEW.email,
                'version', NEW.version,
                'updated_at', NEW.updated_at,
                'updated_by', NEW.updated_by
            )
        );
    ELSIF TG_OP = 'DELETE' THEN
        INSERT INTO replication_log (table_name, operation, record_data)
        VALUES (
            'users', 
            'DELETE', 
            jsonb_build_object('id', OLD.id)
        );
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER users_repl_trigger
AFTER INSERT OR UPDATE OR DELETE ON users
FOR EACH ROW EXECUTE FUNCTION users_replication_trigger();
```

---

### Компонент 2: Outbox Publisher (для критичных таблиц)

```go
// Читает replication_log и публикует в Kafka
func (p *OutboxPublisher) Run(ctx context.Context) error {
    ticker := time.NewTicker(1 * time.Second) // Каждую секунду
    
    for {
        select {
        case <-ticker.C:
            if err := p.publishBatch(ctx); err != nil {
                p.logger.Error("Failed to publish batch", zap.Error(err))
            }
        case <-ctx.Done():
            return ctx.Err()
        }
    }
}

func (p *OutboxPublisher) publishBatch(ctx context.Context) error {
    tx, _ := p.db.BeginTx(ctx)
    defer tx.Rollback()
    
    // Читаем unpublished события
    rows, _ := tx.Query(`
        SELECT id, table_name, operation, record_data 
        FROM replication_log 
        WHERE published = FALSE 
        ORDER BY id 
        LIMIT 100
    `)
    
    var eventIDs []int64
    
    for rows.Next() {
        var id int64
        var tableName, operation string
        var recordData []byte
        
        rows.Scan(&id, &tableName, &operation, &recordData)
        
        // Публикуем в Kafka
        message := &kafka.Message{
            Topic: tableName + "_changes",
            Key:   extractPrimaryKey(recordData),
            Value: buildReplicationEvent(tableName, operation, recordData),
        }
        
        if err := p.producer.SendMessage(message); err != nil {
            return err
        }
        
        eventIDs = append(eventIDs, id)
    }
    
    // Помечаем как published
    if len(eventIDs) > 0 {
        tx.Exec("UPDATE replication_log SET published = TRUE WHERE id = ANY($1)", pq.Array(eventIDs))
    }
    
    return tx.Commit()
}
```

---

### Компонент 3: Sync Service (для некритичных таблиц)

```go
type SyncService struct {
    db       *sql.DB
    producer *kafka.Producer
    config   SyncConfig
    logger   *zap.Logger
}

type SyncConfig struct {
    Tables []TableSyncConfig
    Interval time.Duration // 5 минут
}

type TableSyncConfig struct {
    Name       string
    Topic      string
    PrimaryKey []string
}

func (s *SyncService) Run(ctx context.Context) error {
    ticker := time.NewTicker(s.config.Interval)
    
    for {
        select {
        case <-ticker.C:
            for _, table := range s.config.Tables {
                if err := s.syncTable(ctx, table); err != nil {
                    s.logger.Error("Failed to sync table", 
                        zap.String("table", table.Name), 
                        zap.Error(err))
                }
            }
        case <-ctx.Done():
            return ctx.Err()
        }
    }
}

func (s *SyncService) syncTable(ctx context.Context, tableConfig TableSyncConfig) error {
    // 1. Получаем last_sync_timestamp
    var lastSync time.Time
    err := s.db.QueryRow(`
        SELECT last_sync_timestamp 
        FROM sync_state 
        WHERE table_name = $1
    `, tableConfig.Name).Scan(&lastSync)
    
    if err != nil {
        return err
    }
    
    // 2. Читаем изменения с последней синхронизации
    query := fmt.Sprintf(`
        SELECT * FROM %s 
        WHERE updated_at > $1 
        ORDER BY updated_at, version
        LIMIT 1000
    `, tableConfig.Name)
    
    rows, err := s.db.Query(query, lastSync)
    if err != nil {
        return err
    }
    defer rows.Close()
    
    var maxTimestamp time.Time
    var count int
    
    // 3. Публикуем каждое изменение в Kafka
    for rows.Next() {
        record := s.scanRow(rows) // Читаем всю строку
        
        // Определяем операцию
        operation := "UPDATE"
        if record["deleted_at"] != nil {
            operation = "DELETE"
        }
        
        // Строим событие
        event := buildReplicationEvent(
            tableConfig.Name,
            operation,
            record,
        )
        
        // Публикуем в Kafka
        message := &kafka.Message{
            Topic: tableConfig.Topic,
            Key:   extractPrimaryKey(tableConfig.PrimaryKey, record),
            Value: event,
        }
        
        if err := s.producer.SendMessage(message); err != nil {
            return err
        }
        
        // Отслеживаем максимальный timestamp
        if updatedAt := record["updated_at"].(time.Time); updatedAt.After(maxTimestamp) {
            maxTimestamp = updatedAt
        }
        
        count++
    }
    
    // 4. Обновляем last_sync_timestamp
    if count > 0 {
        _, err = s.db.Exec(`
            UPDATE sync_state 
            SET last_sync_timestamp = $1, updated_at = NOW() 
            WHERE table_name = $2
        `, maxTimestamp, tableConfig.Name)
        
        s.logger.Info("Table synced",
            zap.String("table", tableConfig.Name),
            zap.Int("records", count),
            zap.Time("max_timestamp", maxTimestamp),
        )
    }
    
    return err
}
```

---

### Компонент 4: Replicator Service (общий для обоих типов)

Это тот самый сервис, который я написал ранее - читает из Kafka и применяет в БД.

```go
// Одинаково работает для событий из триггеров И из sync service
func (r *Replicator) ProcessEvent(ctx context.Context, event ReplicationEvent) error {
    // Проверка источника
    if event.Source.Contour == r.config.MyContour {
        // Пропускаем события от своего контура
        return nil
    }
    
    // Идемпотентность
    if r.isProcessed(event.EventID) {
        return nil
    }
    
    // Применяем с проверкой версии
    return r.applyWithConflictResolution(event)
}
```

---

## Конфигурация гибридного решения

```yaml
service:
  name: "replicator-service"
  contour: "active"  # Идентификатор контура

# Критичные таблицы (real-time через триггеры)
critical_tables:
  - name: "users"
    topic: "users_changes"
    primary_key: ["id"]
    replication_mode: "trigger"  # real-time
    
  - name: "orders"
    topic: "orders_changes"
    primary_key: ["id"]
    replication_mode: "trigger"
    
  - name: "transactions"
    topic: "transactions_changes"
    primary_key: ["id"]
    replication_mode: "trigger"

# Некритичные таблицы (периодическая синхронизация)
non_critical_tables:
  - name: "products"
    topic: "products_changes_periodic"
    primary_key: ["id"]
    replication_mode: "periodic"
    sync_interval: "5m"
    
  - name: "categories"
    topic: "categories_changes_periodic"
    primary_key: ["id"]
    replication_mode: "periodic"
    sync_interval: "10m"
    
  - name: "logs"
    topic: "logs_changes_periodic"
    primary_key: ["id"]
    replication_mode: "periodic"
    sync_interval: "15m"
```

---

## Ключевые моменты

### ✅ Преимущества гибридного подхода

1. **Гибкость**: Критичные таблицы реплицируются real-time, некритичные - с задержкой
2. **Производительность**: Триггеры только на критичных таблицах
3. **Надежность**: Даже при сбое Kafka, данные не теряются (replication_log, последующий sync)
4. **Масштабируемость**: Можно добавлять таблицы в любую категорию

### ⚠️ Важные ограничения

1. **Нет транзакционности между таблицами**
   - Если INSERT в users и orders происходит в приложении, они реплицируются независимо
   - Решение: использовать версионирование и идемпотентность

2. **DELETE обрабатывается по-разному**
   - Критичные таблицы: триггер ловит DELETE
   - Некритичные таблицы: используем soft delete (deleted_at)

3. **Задержка репликации**
   - Критичные таблицы: 1-2 секунды (outbox publisher интервал)
   - Некритичные таблицы: 5-15 минут (sync_interval)

4. **Фильтрация своих событий**
   - Каждый контур должен пропускать события с source=my_contour
   - Иначе возникнет бесконечная петля репликации

---

## Защита от петли репликации

### Проблема
```
Контур A: INSERT → Kafka (source=A)
Контур B: Читает из Kafka → INSERT в свою БД
        → Триггер сработал → Kafka (source=B)  ❌
Контур A: Читает (source=B) → INSERT → Триггер → Kafka (source=A)
        БЕСКОНЕЧНАЯ ПЕТЛЯ!
```

### Решение 1: Пропускать свои события
```go
func (r *Replicator) shouldProcess(event ReplicationEvent) bool {
    // Обрабатываем только события с ДРУГОГО контура
    return event.Source.Contour != r.config.MyContour
}
```

### Решение 2: Отключать триггеры при репликации
```sql
-- При применении события из Kafka:
SET session_replication_role = 'replica'; -- Отключает триггеры
INSERT INTO users ...;
SET session_replication_role = 'origin'; -- Включает обратно
```

### Решение 3: Флаг в replication_log
```sql
CREATE TABLE replication_log (
    ...
    is_from_replication BOOLEAN DEFAULT FALSE
);

-- В триггере:
CREATE OR REPLACE FUNCTION users_replication_trigger()
RETURNS TRIGGER AS $$
BEGIN
    -- Не логируем, если это репликация
    IF current_setting('app.is_replication', true) = 'true' THEN
        RETURN NULL;
    END IF;
    
    INSERT INTO replication_log ...;
    RETURN NULL;
END;
$$;

-- При репликации:
SET app.is_replication = 'true';
INSERT INTO users ...;
SET app.is_replication = 'false';
```

**Рекомендую Решение 2** - самое простое и надежное.

---

## Диаграмма потока данных

```
┌─────────────────────────────────────────────────────────────┐
│ КОНТУР A (Active)                                            │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  Приложение → PostgreSQL                                    │
│                   │                                          │
│                   ├─→ [Триггеры на критичных таблицах]      │
│                   │       ↓                                  │
│                   │   replication_log                        │
│                   │       ↓                                  │
│                   │   Outbox Publisher (1 sec) → Kafka      │
│                   │                                          │
│                   └─→ [Некритичные таблицы]                 │
│                           ↓                                  │
│                       Sync Service (5 min) → Kafka          │
│                                                              │
│  Kafka → Replicator Service → PostgreSQL A                  │
│          (применяет изменения с контура B)                  │
└─────────────────────────────────────────────────────────────┘
                              ↕ Kafka
┌─────────────────────────────────────────────────────────────┐
│ КОНТУР B (Passive)                                           │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  Kafka → Replicator Service → PostgreSQL B                  │
│          (применяет изменения с контура A)                  │
│                                                              │
│  PostgreSQL B                                               │
│       │                                                      │
│       └─→ Sync Service (5 min) → Kafka                      │
│           (на случай если что-то изменилось на B)           │
└─────────────────────────────────────────────────────────────┘
```

---

## План реализации (3-4 дня)

### День 1: Подготовка БД
- [ ] Добавить поля version, updated_at, updated_by во все таблицы
- [ ] Создать таблицу replication_log
- [ ] Создать таблицу sync_state
- [ ] Создать таблицу processed_events (для идемпотентности)
- [ ] Создать триггеры на критичные таблицы
- [ ] Изменить DELETE на soft delete (updated_at) в некритичных таблицах

### День 2: Outbox Publisher + Sync Service
- [ ] Реализовать Outbox Publisher для триггеров
- [ ] Реализовать Sync Service для периодического опроса
- [ ] Настроить публикацию в Kafka

### День 3: Replicator Service
- [ ] Реализовать Kafka consumer
- [ ] Реализовать применение событий с проверкой версий
- [ ] Реализовать идемпотентность
- [ ] Добавить фильтрацию своих событий

### День 4: Тестирование и мониторинг
- [ ] Тестирование переключения контуров
- [ ] Настроить мониторинг replication lag
- [ ] Chaos engineering (выключить Kafka, БД, сервисы)
- [ ] Документация

---

## Ответ на ваш вопрос

> Но правильно ли я понимаю, что для периодического diff сервис-репликатор 
> должен обращаться к обеим базам с SELECT запросами, и искать различия в данных?

**Нет, это не так!** 

**Правильно:**
- Sync Service на **каждом контуре** читает только **свою локальную БД**
- Находит записи, измененные с последней синхронизации (WHERE updated_at > last_sync)
- Публикует их в Kafka
- Replicator Service на **другом контуре** читает из Kafka и применяет

**НЕ нужен прямой доступ между контурами** - вся коммуникация идет через Kafka.

Это НЕ "diff двух БД", а **"Change Data Capture через периодический polling локальной БД"**.

