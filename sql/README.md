# SQL Scripts для настройки репликации

## Описание

Эти SQL скрипты настраивают PostgreSQL для репликации данных между контурами через Kafka.

📋 **Быстрая справка:** [CHEATSHEET.md](CHEATSHEET.md) - все команды в одном месте!

## Порядок выполнения

### Вариант А: Автоматическая установка (РЕКОМЕНДУЕТСЯ) 🚀

```bash
psql -U your_user -d your_database -f 00_master_setup.sql
```

Этот скрипт выполнит все шаги автоматически:
- Создаст служебные таблицы
- Создаст функции триггеров
- Создаст helper функции для миграции
- Покажет summary

**Затем настройте свои таблицы:**

```sql
-- Одна команда для полной настройки таблицы!
SELECT setup_table_for_replication('users');
SELECT setup_table_for_replication('orders');
SELECT setup_table_for_replication('products');
```

### Вариант Б: Пошаговая установка

#### 1. Создание служебных таблиц

```bash
psql -U your_user -d your_database -f 01_create_tables.sql
```

**Что создается:**
- `replication_queue` - очередь событий для репликации
- `processed_events` - таблица для идемпотентности
- Индексы для производительности
- Функции для очистки старых записей

#### 2. Создание триггеров

```bash
psql -U your_user -d your_database -f 02_create_replication_trigger.sql
```

**Что создается:**
- `generic_replication_trigger()` - универсальная функция триггера с защитой от петли
- `increment_version_on_update()` - автоинкремент версии
- `setup_replication_for_table()` - helper для быстрой настройки таблицы
- `remove_replication_from_table()` - удаление триггеров

#### 3. Helper функции для миграции

```bash
psql -U your_user -d your_database -f 04_migrate_existing_tables.sql
```

**Что создается:**
- `prepare_table_for_replication()` - добавляет обязательные поля
- `setup_table_for_replication()` - полная настройка (поля + триггеры)
- `generate_migration_script()` - генератор миграционных скриптов

#### 4. Настройка ваших таблиц

**Простой способ (одна команда):**

```sql
SELECT setup_table_for_replication('users');
```

**Ручной способ:**

```sql
-- 1. Добавить обязательные поля
ALTER TABLE your_table 
    ADD COLUMN version BIGINT DEFAULT 1,
    ADD COLUMN updated_at TIMESTAMPTZ DEFAULT NOW(),
    ADD COLUMN updated_by VARCHAR(50);

-- 2. Создать триггеры
SELECT setup_replication_for_table('your_table');
```

## Защита от петли репликации

Триггер автоматически проверяет `application_name`:

```sql
-- В триггере:
IF current_setting('application_name', true) = 'replicator_consumer' THEN
    RETURN NULL;  -- Не записываем в replication_queue
END IF;
```

**Как работает:**
- Обычные приложения: application_name = любое → триггер срабатывает ✅
- ReplicatorConsumer: application_name = 'replicator_consumer' → триггер НЕ срабатывает ✅

**Подключение ReplicatorConsumer:**
```go
// Go код
connString := "host=localhost dbname=mydb user=myuser password=mypass application_name=replicator_consumer"
db, _ := sql.Open("postgres", connString)
```

Или в psql:
```bash
PGAPPNAME=replicator_consumer psql -U myuser -d mydb
```

## Быстрый старт

### Полная настройка (2 команды) ⚡

```bash
# 1. Мастер-скрипт (всё автоматически)
psql -U your_user -d your_database -f 00_master_setup.sql

# 2. Настройка ваших таблиц
psql -U your_user -d your_database
```

```sql
-- Настроить таблицы (одна команда на таблицу!)
SELECT setup_table_for_replication('users');
SELECT setup_table_for_replication('orders');
SELECT setup_table_for_replication('products');
```

## Миграция существующих таблиц

### Автоматическая миграция нескольких таблиц

```sql
-- Вариант 1: Список конкретных таблиц
DO $$
DECLARE
    t VARCHAR;
BEGIN
    FOR t IN 
        SELECT unnest(ARRAY['users', 'orders', 'products', 'categories'])
    LOOP
        RAISE NOTICE 'Setting up: %', t;
        PERFORM setup_table_for_replication(t);
    END LOOP;
END $$;
```

### Миграция ВСЕХ таблиц схемы

```sql
-- Осторожно! Настроит ВСЕ таблицы в схеме public
DO $$
DECLARE
    t VARCHAR;
BEGIN
    FOR t IN 
        SELECT table_name::VARCHAR
        FROM information_schema.tables
        WHERE table_schema = 'public'
          AND table_type = 'BASE TABLE'
          AND table_name NOT IN ('replication_queue', 'processed_events')
    LOOP
        RAISE NOTICE 'Setting up: %', t;
        PERFORM setup_table_for_replication(t);
    END LOOP;
END $$;
```

### Генерация миграционного скрипта

```sql
-- Сгенерировать SQL скрипт для проверки
SELECT sql_statement FROM generate_migration_script(ARRAY['users', 'orders']);

-- Сохранить в файл (из psql)
\o migration_generated.sql
SELECT sql_statement FROM generate_migration_script(ARRAY['users', 'orders', 'products']);
\o

-- Проверить и выполнить
\i migration_generated.sql
```

### Тестирование

```sql
-- INSERT
INSERT INTO users (id, name, email) VALUES (1, 'Test', 'test@example.com');

-- Проверить событие
SELECT * FROM replication_queue WHERE table_name = 'users' ORDER BY id DESC LIMIT 1;

-- Результат:
-- id | table_name | operation | record_data                                  | published
-- ---+------------+-----------+---------------------------------------------+----------
--  1 | users      | INSERT    | {"id": 1, "name": "Test", "version": 1, ...} | false

-- UPDATE
UPDATE users SET name = 'Updated' WHERE id = 1;

-- Проверить версию
SELECT id, name, version FROM users WHERE id = 1;
-- version должна стать 2

-- Проверить событие UPDATE
SELECT 
    operation,
    record_data->'before'->>'name' as old_name,
    record_data->'after'->>'name' as new_name,
    record_data->'after'->>'version' as version
FROM replication_queue 
WHERE table_name = 'users' 
ORDER BY id DESC LIMIT 1;
```

### Тестирование защиты от петли

```sql
-- Имитируем ReplicatorConsumer
-- Устанавливаем application_name
SET application_name = 'replicator_consumer';

INSERT INTO users (id, name, email, version) 
VALUES (2, 'From Replicator', 'rep@example.com', 1);

-- Возвращаем обычное application_name
RESET application_name;

-- Проверяем replication_queue
SELECT COUNT(*) FROM replication_queue WHERE record_data->>'id' = '2';
-- Должно быть 0 (событие НЕ попало в очередь) ✅

-- Очистка
DELETE FROM users WHERE id = 2;
```

**Или тест через отдельное подключение:**
```bash
# Подключаемся с application_name='replicator_consumer'
PGAPPNAME=replicator_consumer psql -U myuser -d mydb -c \
  "INSERT INTO users (id, name, email, version) VALUES (2, 'Test', 'test@example.com', 1);"

# Проверяем
psql -U myuser -d mydb -c \
  "SELECT COUNT(*) FROM replication_queue WHERE record_data->>'id' = '2';"
# Результат: 0
```

## Управление триггерами

### Добавить репликацию на таблицу

```sql
SELECT setup_replication_for_table('table_name');
```

### Удалить репликацию с таблицы

```sql
SELECT remove_replication_from_table('table_name');
```

### Посмотреть все триггеры репликации

```sql
SELECT 
    tablename,
    triggername
FROM pg_trigger t
JOIN pg_class c ON t.tgrelid = c.oid
WHERE triggername LIKE '%replication_trigger' 
   OR triggername LIKE '%version_trigger'
ORDER BY tablename;
```

### Отключить триггер временно

```sql
-- Отключить
ALTER TABLE users DISABLE TRIGGER users_replication_trigger;

-- Включить обратно
ALTER TABLE users ENABLE TRIGGER users_replication_trigger;
```

## Обслуживание

### Очистка старых записей

```sql
-- Очистить replication_queue (опубликованные события старше 7 дней)
SELECT cleanup_replication_queue(7);

-- Очистить processed_events (старше 30 дней)
SELECT cleanup_processed_events(30);
```

Рекомендуется запускать через cron или pg_cron:

```sql
-- Настроить автоматическую очистку через pg_cron
SELECT cron.schedule(
    'cleanup-replication-queue',
    '0 2 * * *',  -- Каждый день в 2:00
    'SELECT cleanup_replication_queue(7);'
);

SELECT cron.schedule(
    'cleanup-processed-events',
    '0 3 * * *',  -- Каждый день в 3:00
    'SELECT cleanup_processed_events(30);'
);
```

### Мониторинг

```sql
-- Размер очереди неопубликованных событий
SELECT COUNT(*) FROM replication_queue WHERE NOT published;

-- Статистика по таблицам
SELECT 
    table_name,
    COUNT(*) as events_count,
    COUNT(*) FILTER (WHERE published) as published,
    COUNT(*) FILTER (WHERE NOT published) as unpublished
FROM replication_queue
GROUP BY table_name;

-- Старые неопубликованные события (возможна проблема)
SELECT 
    table_name,
    MIN(created_at) as oldest_event,
    COUNT(*) as count
FROM replication_queue
WHERE NOT published
  AND created_at < NOW() - INTERVAL '1 hour'
GROUP BY table_name;
```

## Структура события в replication_queue

### INSERT

```json
{
  "id": 1,
  "name": "John",
  "email": "john@example.com",
  "version": 1,
  "updated_at": "2025-11-18T10:30:00Z",
  "updated_by": null
}
```

### UPDATE

```json
{
  "before": {
    "id": 1,
    "name": "John",
    "version": 1,
    ...
  },
  "after": {
    "id": 1,
    "name": "Jane",
    "version": 2,
    ...
  }
}
```

### DELETE

```json
{
  "id": 1,
  "name": "John",
  "email": "john@example.com",
  "version": 2,
  ...
}
```

## Производительность

### Индексы

Скрипт автоматически создает индексы:
- `idx_repl_queue_unpublished` - для быстрого поиска неопубликованных событий
- `idx_repl_queue_table` - для фильтрации по таблице
- `idx_processed_events_timestamp` - для очистки старых записей

### Overhead триггеров

- **INSERT**: +10-15% времени (один INSERT в replication_queue)
- **UPDATE**: +10-15% времени
- **DELETE**: +10-15% времени

### Рекомендации

1. Регулярно очищайте `replication_queue` (старые опубликованные записи)
2. Мониторьте размер очереди неопубликованных событий
3. Используйте VACUUM на `replication_queue` (таблица с высокой активностью)

```sql
-- Настроить autovacuum для replication_queue
ALTER TABLE replication_queue SET (
    autovacuum_vacuum_scale_factor = 0.05,
    autovacuum_analyze_scale_factor = 0.02
);
```

## Troubleshooting

### Проблема: События не попадают в replication_queue

**Проверка 1:** Убедитесь, что триггеры созданы

```sql
SELECT triggername FROM pg_trigger t
JOIN pg_class c ON t.tgrelid = c.oid
WHERE c.relname = 'your_table';
```

**Проверка 2:** Убедитесь, что триггеры включены

```sql
SELECT tgname, tgenabled 
FROM pg_trigger t
JOIN pg_class c ON t.tgrelid = c.oid
WHERE c.relname = 'your_table';
-- tgenabled = 'O' означает включен
```

### Проблема: Петля репликации

**Симптомы:** События бесконечно дублируются

**Решение:** Проверьте, что ReplicatorConsumer подключается с `application_name='replicator_consumer'`

```go
// В Go коде ReplicatorConsumer:
connString := fmt.Sprintf(
    "host=%s port=%d dbname=%s user=%s password=%s application_name=replicator_consumer",
    cfg.DB.Host, cfg.DB.Port, cfg.DB.Database, cfg.DB.User, cfg.DB.Password,
)
db, err := sql.Open("postgres", connString)
```

**Проверка текущего application_name:**
```sql
SELECT application_name FROM pg_stat_activity WHERE pid = pg_backend_pid();
```

### Проблема: replication_queue растет слишком быстро

**Причина:** ReplicatorPublisher не успевает публиковать или не запущен

**Решение:**
1. Проверить, что ReplicatorPublisher запущен
2. Увеличить частоту опроса
3. Добавить больше instances ReplicatorPublisher
4. Проверить Kafka доступность

## Откат изменений

```sql
-- Удалить триггеры со всех таблиц
SELECT remove_replication_from_table('users');
SELECT remove_replication_from_table('orders');
-- ... для остальных таблиц

-- Удалить функции
DROP FUNCTION IF EXISTS generic_replication_trigger() CASCADE;
DROP FUNCTION IF EXISTS increment_version_on_update() CASCADE;
DROP FUNCTION IF EXISTS setup_replication_for_table(VARCHAR, VARCHAR) CASCADE;
DROP FUNCTION IF EXISTS remove_replication_from_table(VARCHAR, VARCHAR) CASCADE;
DROP FUNCTION IF EXISTS cleanup_replication_queue(INT) CASCADE;
DROP FUNCTION IF EXISTS cleanup_processed_events(INT) CASCADE;

-- Удалить таблицы
DROP TABLE IF EXISTS processed_events CASCADE;
DROP TABLE IF EXISTS replication_queue CASCADE;

-- Удалить поля из бизнес-таблиц (опционально)
ALTER TABLE users DROP COLUMN IF EXISTS version;
ALTER TABLE users DROP COLUMN IF EXISTS updated_at;
ALTER TABLE users DROP COLUMN IF EXISTS updated_by;
```

