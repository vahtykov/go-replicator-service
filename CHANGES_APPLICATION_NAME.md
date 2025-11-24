# Изменения: Защита через application_name

## Что изменилось

Защита от петли репликации переведена с `session_replication_role` на `application_name`, так как учетная запись PostgreSQL не имеет прав на изменение `session_replication_role`.

---

## Основное изменение в триггере

### ❌ Старый способ (требует специальных прав)

```sql
IF current_setting('session_replication_role') = 'replica' THEN
    RETURN NULL;
END IF;
```

### ✅ Новый способ (работает с обычной учетной записью)

```sql
IF current_setting('application_name', true) = 'replicator_consumer' THEN
    RETURN NULL;
END IF;
```

---

## Как использовать в ReplicatorConsumer

### Подключение к PostgreSQL

```go
// Go код
connString := fmt.Sprintf(
    "host=%s port=%d dbname=%s user=%s password=%s application_name=replicator_consumer",
    cfg.DB.Host,
    cfg.DB.Port,
    cfg.DB.Database,
    cfg.DB.User,
    cfg.DB.Password,
)

db, err := sql.Open("postgres", connString)
if err != nil {
    return err
}
```

### Или через URL

```go
dsn := "postgresql://user:password@host:5432/database?application_name=replicator_consumer"
db, err := sql.Open("postgres", dsn)
```

### Применение изменений

```go
func (r *ReplicatorConsumer) applyEvent(event ReplicationEvent) error {
    tx, err := r.db.Begin()
    if err != nil {
        return err
    }
    defer tx.Rollback()
    
    // application_name уже установлен при подключении
    // Триггер автоматически проверит и не запишет в replication_queue
    
    // Применяем изменения
    _, err = tx.Exec("INSERT INTO users (...) VALUES (...)")
    if err != nil {
        return err
    }
    
    return tx.Commit()
}
```

---

## Проверка в psql

### Тест защиты

```bash
# Подключиться с application_name='replicator_consumer'
PGAPPNAME=replicator_consumer psql -U myuser -d mydb
```

```sql
-- В этой сессии триггер не будет срабатывать
INSERT INTO users (id, name, email, version) 
VALUES (999, 'Test', 'test@example.com', 1);

-- Проверка: событие НЕ должно попасть в replication_queue
SELECT COUNT(*) FROM replication_queue WHERE record_data->>'id' = '999';
-- Результат: 0 ✅

-- Очистка
DELETE FROM users WHERE id = 999;
```

### Или в текущей сессии

```sql
-- Установить application_name
SET application_name = 'replicator_consumer';

INSERT INTO users (id, name, email, version) 
VALUES (999, 'Test', 'test@example.com', 1);

-- Вернуть обычное значение
RESET application_name;

-- Проверка
SELECT COUNT(*) FROM replication_queue WHERE record_data->>'id' = '999';
-- Результат: 0 ✅
```

---

## Проверка текущего application_name

```sql
-- Узнать application_name текущей сессии
SELECT current_setting('application_name');

-- Посмотреть все подключения и их application_name
SELECT 
    pid,
    usename,
    application_name,
    state,
    query
FROM pg_stat_activity
WHERE datname = current_database();
```

---

## Обновленные файлы

Следующие файлы были обновлены:

### SQL скрипты:
- ✅ `sql/02_create_replication_trigger.sql` - изменена функция `generic_replication_trigger()`
- ✅ `sql/03_setup_example.sql` - обновлены примеры тестирования

### Документация:
- ✅ `ARCHITECTURE_OVERVIEW.md` - секция "Защита от петли репликации"
- ✅ `HYBRID_SOLUTION.md` - добавлено примечание
- ✅ `README.md` - обновлены ключевые особенности
- ✅ `sql/README.md` - обновлена секция защиты
- ✅ `sql/CHEATSHEET.md` - обновлены примеры
- ✅ `sql/FILES.md` - обновлена секция безопасности

---

## Преимущества нового подхода

✅ **Не требует специальных прав** - работает с обычной учетной записью PostgreSQL  
✅ **Простая настройка** - достаточно указать параметр при подключении  
✅ **Явная идентификация** - легко увидеть ReplicatorConsumer в `pg_stat_activity`  
✅ **Надежность** - проверка application_name так же надежна, как и session_replication_role  

---

## Дополнительная защита (рекомендуется)

Используйте двойную защиту для максимальной надежности:

### 1. Триггер проверяет application_name
```sql
-- Уже реализовано в generic_replication_trigger()
IF current_setting('application_name', true) = 'replicator_consumer' THEN
    RETURN NULL;
END IF;
```

### 2. ReplicatorConsumer фильтрует по source.contour
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

**Defense in depth:** Если триггер случайно сработает, фильтрация по contour предотвратит дубликаты.

---

## Migration Guide

Если у вас уже установлены триггеры со старым способом:

### Шаг 1: Обновить функцию триггера

```sql
\i sql/02_create_replication_trigger.sql
```

Это перезапишет функцию `generic_replication_trigger()` новой версией.

### Шаг 2: Проверить, что триггеры используют новую функцию

```sql
SELECT 
    tablename,
    triggername
FROM pg_trigger t
JOIN pg_class c ON t.tgrelid = c.oid
WHERE triggername LIKE '%replication_trigger'
ORDER BY tablename;
```

Триггеры автоматически начнут использовать обновленную функцию.

### Шаг 3: Обновить ReplicatorConsumer

Убедитесь, что ReplicatorConsumer подключается с `application_name=replicator_consumer`:

```go
connString += "?application_name=replicator_consumer"
```

### Шаг 4: Тестирование

```bash
# Тест защиты
PGAPPNAME=replicator_consumer psql -U myuser -d mydb -c \
  "INSERT INTO users (id, name, version) VALUES (9999, 'Test Loop', 1);"

# Проверка
psql -U myuser -d mydb -c \
  "SELECT COUNT(*) FROM replication_queue WHERE record_data->>'id' = '9999';"
# Должно быть: 0

# Очистка
psql -U myuser -d mydb -c "DELETE FROM users WHERE id = 9999;"
```

---

## FAQ

### Q: Можно ли использовать другое значение application_name?

**A:** Да, можно изменить в триггере:

```sql
-- В generic_replication_trigger()
IF current_setting('application_name', true) = 'your_custom_name' THEN
    RETURN NULL;
END IF;
```

И в ReplicatorConsumer:
```go
connString += "?application_name=your_custom_name"
```

### Q: Что если забыть установить application_name в ReplicatorConsumer?

**A:** Триггер сработает, и событие попадет в replication_queue → возникнет петля репликации.

**Решение:** Используйте дополнительную защиту через фильтрацию по `source.contour`.

### Q: Можно ли видеть application_name в логах?

**A:** Да:

```sql
-- Включить логирование подключений
ALTER SYSTEM SET log_connections = 'on';
SELECT pg_reload_conf();

-- Посмотреть текущие подключения
SELECT pid, usename, application_name, client_addr FROM pg_stat_activity;
```

### Q: Безопасно ли полагаться только на application_name?

**A:** Да, это стандартный механизм PostgreSQL. Но для дополнительной надежности используйте двойную защиту (триггер + фильтрация по contour).

---

## Итого

Переход на `application_name` завершен. Все файлы обновлены, документация актуализирована.

**Ключевые изменения:**
1. ✅ Триггер проверяет `application_name` вместо `session_replication_role`
2. ✅ ReplicatorConsumer должен подключаться с `application_name=replicator_consumer`
3. ✅ Не требуются специальные права в PostgreSQL
4. ✅ Рекомендуется двойная защита (триггер + фильтрация)

**Следующие шаги:**
1. Обновить SQL функции (уже сделано)
2. Реализовать ReplicatorConsumer с правильным connection string
3. Протестировать защиту от петли
4. Настроить мониторинг application_name в pg_stat_activity

