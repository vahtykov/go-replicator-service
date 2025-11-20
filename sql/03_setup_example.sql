-- ===================================================================
-- ПРИМЕР: Подготовка таблиц для репликации
-- ===================================================================

-- ===================================================================
-- Шаг 1: Добавить обязательные поля в существующие таблицы
-- ===================================================================

-- Пример для таблицы users
ALTER TABLE users 
    ADD COLUMN IF NOT EXISTS version BIGINT DEFAULT 1,
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ DEFAULT NOW(),
    ADD COLUMN IF NOT EXISTS updated_by VARCHAR(50);

-- Пример для таблицы orders
ALTER TABLE orders 
    ADD COLUMN IF NOT EXISTS version BIGINT DEFAULT 1,
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ DEFAULT NOW(),
    ADD COLUMN IF NOT EXISTS updated_by VARCHAR(50);

-- Пример для таблицы products
ALTER TABLE products 
    ADD COLUMN IF NOT EXISTS version BIGINT DEFAULT 1,
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ DEFAULT NOW(),
    ADD COLUMN IF NOT EXISTS updated_by VARCHAR(50);

-- ===================================================================
-- Шаг 2: Создать триггеры на таблицах
-- ===================================================================

-- Вариант A: Использовать helper функцию (РЕКОМЕНДУЕТСЯ)
-- ------------------------------------------------------------
SELECT setup_replication_for_table('users');
SELECT setup_replication_for_table('orders');
SELECT setup_replication_for_table('products');

-- Вариант B: Создать триггеры вручную
-- ------------------------------------------------------------
-- Если по какой-то причине не хотите использовать helper функцию:

-- -- Таблица users
-- CREATE TRIGGER users_version_trigger
--     BEFORE INSERT OR UPDATE ON users
--     FOR EACH ROW
--     EXECUTE FUNCTION increment_version_on_update();
-- 
-- CREATE TRIGGER users_replication_trigger
--     AFTER INSERT OR UPDATE OR DELETE ON users
--     FOR EACH ROW
--     EXECUTE FUNCTION generic_replication_trigger();

-- -- Таблица orders
-- CREATE TRIGGER orders_version_trigger
--     BEFORE INSERT OR UPDATE ON orders
--     FOR EACH ROW
--     EXECUTE FUNCTION increment_version_on_update();
-- 
-- CREATE TRIGGER orders_replication_trigger
--     AFTER INSERT OR UPDATE OR DELETE ON orders
--     FOR EACH ROW
--     EXECUTE FUNCTION generic_replication_trigger();

-- ===================================================================
-- Шаг 3: Проверка установленных триггеров
-- ===================================================================

-- Посмотреть все триггеры репликации
SELECT 
    schemaname,
    tablename,
    triggername,
    triggerdef
FROM pg_trigger t
JOIN pg_class c ON t.tgrelid = c.oid
JOIN pg_namespace n ON c.relnamespace = n.oid
WHERE triggername LIKE '%replication_trigger' 
   OR triggername LIKE '%version_trigger'
ORDER BY schemaname, tablename, triggername;

-- ===================================================================
-- Шаг 4: Тестирование репликации
-- ===================================================================

-- 1. Проверим, что replication_queue пуста
SELECT COUNT(*) as queue_size FROM replication_queue WHERE NOT published;

-- 2. Выполним INSERT
INSERT INTO users (id, name, email) 
VALUES (1, 'Test User', 'test@example.com');

-- 3. Проверим, что событие попало в очередь
SELECT 
    id,
    table_name,
    operation,
    record_data,
    created_at,
    published
FROM replication_queue 
WHERE table_name = 'users'
ORDER BY id DESC 
LIMIT 5;

-- 4. Выполним UPDATE
UPDATE users 
SET name = 'Updated User' 
WHERE id = 1;

-- 5. Проверим версию (должна увеличиться)
SELECT id, name, version, updated_at FROM users WHERE id = 1;

-- 6. Проверим событие UPDATE в очереди
SELECT 
    id,
    table_name,
    operation,
    record_data->'before'->>'name' as old_name,
    record_data->'after'->>'name' as new_name,
    record_data->'after'->>'version' as new_version
FROM replication_queue 
WHERE table_name = 'users' AND operation = 'UPDATE'
ORDER BY id DESC 
LIMIT 1;

-- 7. Выполним DELETE
DELETE FROM users WHERE id = 1;

-- 8. Проверим событие DELETE
SELECT 
    id,
    table_name,
    operation,
    record_data->>'id' as deleted_id,
    record_data->>'name' as deleted_name
FROM replication_queue 
WHERE table_name = 'users' AND operation = 'DELETE'
ORDER BY id DESC 
LIMIT 1;

-- ===================================================================
-- Шаг 5: Тестирование защиты от петли репликации
-- ===================================================================

-- Имитируем поведение ReplicatorConsumer
BEGIN;

-- Отключаем триггеры (как делает ReplicatorConsumer)
SET LOCAL session_replication_role = 'replica';

-- Вставляем данные (триггер НЕ должен сработать)
INSERT INTO users (id, name, email, version, updated_at) 
VALUES (2, 'Replicated User', 'replicated@example.com', 1, NOW());

COMMIT;

-- Проверяем, что событие НЕ попало в replication_queue
-- (последнее событие должно быть DELETE id=1, а не INSERT id=2)
SELECT 
    id,
    table_name,
    operation,
    record_data->>'id' as record_id,
    created_at
FROM replication_queue 
ORDER BY id DESC 
LIMIT 3;

-- Результат: 
-- Если защита работает, INSERT для id=2 НЕ должен быть в replication_queue

-- Очистка тестовых данных
DELETE FROM users WHERE id IN (1, 2);
DELETE FROM replication_queue WHERE table_name = 'users';

-- ===================================================================
-- Дополнительные полезные запросы
-- ===================================================================

-- Количество неопубликованных событий по таблицам
SELECT 
    table_name,
    operation,
    COUNT(*) as count,
    MIN(created_at) as oldest_event,
    MAX(created_at) as newest_event
FROM replication_queue
WHERE NOT published
GROUP BY table_name, operation
ORDER BY count DESC;

-- Размер replication_queue
SELECT 
    COUNT(*) as total_events,
    COUNT(*) FILTER (WHERE published) as published_events,
    COUNT(*) FILTER (WHERE NOT published) as unpublished_events,
    pg_size_pretty(pg_total_relation_size('replication_queue')) as table_size
FROM replication_queue;

-- Старые опубликованные события (кандидаты на очистку)
SELECT COUNT(*) as old_published_count
FROM replication_queue
WHERE published = TRUE 
  AND created_at < NOW() - INTERVAL '7 days';

-- Выполнить очистку старых записей
SELECT cleanup_replication_queue(7);  -- Удалить записи старше 7 дней
SELECT cleanup_processed_events(30);  -- Удалить записи старше 30 дней

