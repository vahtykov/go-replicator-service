# SQL Cheatsheet - Быстрая справка

## Установка

```bash
# Полная автоматическая установка
psql -U postgres -d mydb -f 00_master_setup.sql
```

## Настройка таблиц

```sql
-- Полная настройка (поля + триггеры) - РЕКОМЕНДУЕТСЯ
SELECT setup_table_for_replication('users');

-- Только добавить поля (без триггеров)
SELECT prepare_table_for_replication('users');

-- Только триггеры (если поля уже есть)
SELECT setup_replication_for_table('users');
```

## Удаление репликации

```sql
-- Удалить триггеры с таблицы
SELECT remove_replication_from_table('users');

-- Удалить поля (вручную)
ALTER TABLE users 
    DROP COLUMN version,
    DROP COLUMN updated_at,
    DROP COLUMN updated_by;
```

## Тестирование

```sql
-- 1. Вставка
INSERT INTO users (name, email) VALUES ('Test', 'test@example.com');

-- 2. Проверка очереди
SELECT * FROM replication_queue WHERE table_name = 'users' ORDER BY id DESC LIMIT 1;

-- 3. Проверка версии
SELECT id, name, version, updated_at FROM users ORDER BY id DESC LIMIT 1;

-- 4. Обновление
UPDATE users SET name = 'Updated' WHERE name = 'Test';

-- 5. Проверка версии (должна увеличиться)
SELECT id, name, version FROM users WHERE name = 'Updated';

-- 6. Удаление
DELETE FROM users WHERE name = 'Updated';
```

## Тест защиты от петли

```sql
-- Имитация ReplicatorConsumer
-- Устанавливаем application_name как у ReplicatorConsumer
SET application_name = 'replicator_consumer';

INSERT INTO users (id, name, email, version) VALUES (999, 'Test Loop', 'loop@test.com', 1);

-- Возвращаем обычное application_name
RESET application_name;

-- Проверка: событие НЕ должно быть в очереди
SELECT COUNT(*) FROM replication_queue WHERE record_data->>'id' = '999';
-- Результат: 0 (защита работает!)

-- Очистка
DELETE FROM users WHERE id = 999;
```

**Или подключиться с нужным application_name:**
```bash
# В отдельной сессии psql
PGAPPNAME=replicator_consumer psql -U myuser -d mydb

# Теперь все операции в этой сессии не будут триггерить репликацию
INSERT INTO users (id, name, email, version) VALUES (999, 'Test', 'test@test.com', 1);
```

## Мониторинг

```sql
-- Размер очереди неопубликованных событий
SELECT COUNT(*) FROM replication_queue WHERE NOT published;

-- По таблицам
SELECT 
    table_name,
    COUNT(*) FILTER (WHERE NOT published) as unpublished,
    COUNT(*) FILTER (WHERE published) as published
FROM replication_queue
GROUP BY table_name;

-- Старые неопубликованные (возможна проблема!)
SELECT 
    table_name,
    MIN(created_at) as oldest,
    COUNT(*) as count
FROM replication_queue
WHERE NOT published AND created_at < NOW() - INTERVAL '1 hour'
GROUP BY table_name;

-- Размер таблиц
SELECT 
    'replication_queue' as table_name,
    pg_size_pretty(pg_total_relation_size('replication_queue')) as size,
    (SELECT COUNT(*) FROM replication_queue) as rows
UNION ALL
SELECT 
    'processed_events',
    pg_size_pretty(pg_total_relation_size('processed_events')),
    (SELECT COUNT(*) FROM processed_events);
```

## Очистка

```sql
-- Очистить старые опубликованные события (старше 7 дней)
SELECT cleanup_replication_queue(7);

-- Очистить обработанные события (старше 30 дней)
SELECT cleanup_processed_events(30);

-- Очистить ВСЁ (ОСТОРОЖНО!)
TRUNCATE replication_queue;
TRUNCATE processed_events;
```

## Просмотр триггеров

```sql
-- Все триггеры репликации
SELECT 
    tablename,
    triggername,
    tgenabled
FROM pg_trigger t
JOIN pg_class c ON t.tgrelid = c.oid
WHERE triggername LIKE '%replication_trigger' 
   OR triggername LIKE '%version_trigger'
ORDER BY tablename;

-- Триггеры конкретной таблицы
SELECT 
    tgname as trigger_name,
    CASE tgenabled 
        WHEN 'O' THEN 'enabled'
        WHEN 'D' THEN 'disabled'
    END as status
FROM pg_trigger t
JOIN pg_class c ON t.tgrelid = c.oid
WHERE c.relname = 'users';
```

## Управление триггерами

```sql
-- Отключить триггер
ALTER TABLE users DISABLE TRIGGER users_replication_trigger;

-- Включить триггер
ALTER TABLE users ENABLE TRIGGER users_replication_trigger;

-- Отключить все триггеры на таблице
ALTER TABLE users DISABLE TRIGGER ALL;

-- Включить все триггеры
ALTER TABLE users ENABLE TRIGGER ALL;
```

## Анализ событий

```sql
-- Последние 10 событий
SELECT 
    id,
    table_name,
    operation,
    created_at,
    published
FROM replication_queue 
ORDER BY id DESC 
LIMIT 10;

-- Последнее событие по таблице
SELECT 
    operation,
    record_data,
    created_at
FROM replication_queue
WHERE table_name = 'users'
ORDER BY id DESC
LIMIT 1;

-- События UPDATE с изменениями
SELECT 
    id,
    table_name,
    record_data->'before'->>'name' as old_name,
    record_data->'after'->>'name' as new_name,
    created_at
FROM replication_queue
WHERE operation = 'UPDATE' AND table_name = 'users'
ORDER BY id DESC
LIMIT 10;
```

## Статистика

```sql
-- Распределение операций
SELECT 
    table_name,
    operation,
    COUNT(*) as count
FROM replication_queue
GROUP BY table_name, operation
ORDER BY count DESC;

-- События за последний час
SELECT 
    table_name,
    COUNT(*) as events_count
FROM replication_queue
WHERE created_at > NOW() - INTERVAL '1 hour'
GROUP BY table_name
ORDER BY events_count DESC;

-- Средняя скорость репликации
SELECT 
    table_name,
    COUNT(*) as events,
    MIN(created_at) as first_event,
    MAX(created_at) as last_event,
    EXTRACT(EPOCH FROM (MAX(created_at) - MIN(created_at))) / COUNT(*) as avg_seconds_per_event
FROM replication_queue
WHERE created_at > NOW() - INTERVAL '1 day'
GROUP BY table_name;
```

## Миграция всех таблиц

```sql
-- Настроить репликацию для всех таблиц схемы
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

## Проверка готовности

```sql
-- Какие таблицы готовы к репликации
SELECT 
    t.table_name,
    EXISTS (
        SELECT 1 FROM information_schema.columns c
        WHERE c.table_name = t.table_name AND c.column_name = 'version'
    ) as has_version,
    EXISTS (
        SELECT 1 FROM pg_trigger tr
        JOIN pg_class c ON tr.tgrelid = c.oid
        WHERE c.relname = t.table_name AND tr.tgname LIKE '%replication_trigger'
    ) as has_trigger
FROM information_schema.tables t
WHERE t.table_schema = 'public' AND t.table_type = 'BASE TABLE'
  AND t.table_name NOT IN ('replication_queue', 'processed_events')
ORDER BY t.table_name;
```

## Backup и Restore

```sql
-- Backup replication_queue
COPY (SELECT * FROM replication_queue WHERE NOT published) 
TO '/tmp/replication_queue_backup.csv' CSV HEADER;

-- Restore
COPY replication_queue(id, table_name, operation, record_data, created_at, published, published_at)
FROM '/tmp/replication_queue_backup.csv' CSV HEADER;
```

## Performance Tuning

```sql
-- Настроить autovacuum для активных таблиц
ALTER TABLE replication_queue SET (
    autovacuum_vacuum_scale_factor = 0.05,
    autovacuum_analyze_scale_factor = 0.02
);

-- Индексы (уже созданы автоматически, но для справки)
CREATE INDEX IF NOT EXISTS idx_repl_queue_unpublished 
    ON replication_queue(created_at) WHERE NOT published;

-- Анализ таблицы
ANALYZE replication_queue;

-- Очистка
VACUUM ANALYZE replication_queue;
```

## Troubleshooting

```sql
-- Проверка зависших событий (не опубликованы > 1 часа)
SELECT 
    id,
    table_name,
    operation,
    created_at,
    NOW() - created_at as age
FROM replication_queue
WHERE NOT published 
  AND created_at < NOW() - INTERVAL '1 hour'
ORDER BY created_at
LIMIT 20;

-- Повторная публикация (если Publisher был выключен)
UPDATE replication_queue 
SET published = FALSE 
WHERE id BETWEEN 1000 AND 2000;  -- осторожно с диапазоном!

-- Проверка блокировок
SELECT 
    pid,
    usename,
    application_name,
    state,
    query
FROM pg_stat_activity
WHERE query LIKE '%replication_queue%';
```

## Полный откат

```sql
-- ОСТОРОЖНО! Полностью удаляет репликацию

-- 1. Удалить триггеры со всех таблиц
DO $$
DECLARE
    t VARCHAR;
BEGIN
    FOR t IN 
        SELECT table_name::VARCHAR
        FROM information_schema.tables
        WHERE table_schema = 'public' AND table_type = 'BASE TABLE'
    LOOP
        BEGIN
            PERFORM remove_replication_from_table(t);
        EXCEPTION WHEN OTHERS THEN
            -- Игнорируем ошибки (таблица может не иметь триггеров)
        END;
    END LOOP;
END $$;

-- 2. Удалить функции
DROP FUNCTION IF EXISTS generic_replication_trigger() CASCADE;
DROP FUNCTION IF EXISTS increment_version_on_update() CASCADE;
DROP FUNCTION IF EXISTS setup_replication_for_table(VARCHAR, VARCHAR) CASCADE;
DROP FUNCTION IF EXISTS setup_table_for_replication(VARCHAR, VARCHAR) CASCADE;
DROP FUNCTION IF EXISTS prepare_table_for_replication(VARCHAR, VARCHAR) CASCADE;
DROP FUNCTION IF EXISTS remove_replication_from_table(VARCHAR, VARCHAR) CASCADE;
DROP FUNCTION IF EXISTS cleanup_replication_queue(INT) CASCADE;
DROP FUNCTION IF EXISTS cleanup_processed_events(INT) CASCADE;
DROP FUNCTION IF EXISTS generate_migration_script(VARCHAR[], VARCHAR) CASCADE;

-- 3. Удалить таблицы
DROP TABLE IF EXISTS processed_events CASCADE;
DROP TABLE IF EXISTS replication_queue CASCADE;
```

