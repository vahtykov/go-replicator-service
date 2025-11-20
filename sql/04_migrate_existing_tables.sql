-- ===================================================================
-- Миграция существующих таблиц для репликации
-- ===================================================================
-- Используйте этот скрипт для добавления обязательных полей
-- в существующие таблицы БД
-- ===================================================================

-- ===================================================================
-- Функция для подготовки таблицы к репликации
-- ===================================================================

CREATE OR REPLACE FUNCTION prepare_table_for_replication(
    p_table_name VARCHAR,
    p_schema_name VARCHAR DEFAULT 'public'
)
RETURNS TEXT AS $$
DECLARE
    v_has_version BOOLEAN;
    v_has_updated_at BOOLEAN;
    v_has_updated_by BOOLEAN;
    v_result TEXT := '';
BEGIN
    -- Проверяем наличие колонок
    SELECT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = p_schema_name
          AND table_name = p_table_name
          AND column_name = 'version'
    ) INTO v_has_version;
    
    SELECT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = p_schema_name
          AND table_name = p_table_name
          AND column_name = 'updated_at'
    ) INTO v_has_updated_at;
    
    SELECT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = p_schema_name
          AND table_name = p_table_name
          AND column_name = 'updated_by'
    ) INTO v_has_updated_by;
    
    -- Добавляем недостающие колонки
    IF NOT v_has_version THEN
        EXECUTE format(
            'ALTER TABLE %I.%I ADD COLUMN version BIGINT DEFAULT 1',
            p_schema_name,
            p_table_name
        );
        v_result := v_result || 'Added column: version. ';
    END IF;
    
    IF NOT v_has_updated_at THEN
        EXECUTE format(
            'ALTER TABLE %I.%I ADD COLUMN updated_at TIMESTAMPTZ DEFAULT NOW()',
            p_schema_name,
            p_table_name
        );
        v_result := v_result || 'Added column: updated_at. ';
    END IF;
    
    IF NOT v_has_updated_by THEN
        EXECUTE format(
            'ALTER TABLE %I.%I ADD COLUMN updated_by VARCHAR(50)',
            p_schema_name,
            p_table_name
        );
        v_result := v_result || 'Added column: updated_by. ';
    END IF;
    
    -- Инициализируем значения для существующих записей
    EXECUTE format(
        'UPDATE %I.%I 
         SET version = COALESCE(version, 1),
             updated_at = COALESCE(updated_at, NOW())
         WHERE version IS NULL OR updated_at IS NULL',
        p_schema_name,
        p_table_name
    );
    
    IF v_result = '' THEN
        v_result := 'Table already has all required columns.';
    ELSE
        v_result := v_result || 'Initialized existing records.';
    END IF;
    
    RETURN format('Table %I.%I prepared: %s', p_schema_name, p_table_name, v_result);
END;
$$ LANGUAGE plpgsql;

COMMENT ON FUNCTION prepare_table_for_replication IS 
'Добавляет обязательные поля (version, updated_at, updated_by) в таблицу.
Безопасно вызывать несколько раз - проверяет наличие полей.
Инициализирует значения для существующих записей.

Использование: SELECT prepare_table_for_replication(''users'');';

-- ===================================================================
-- Функция для полной настройки таблицы (поля + триггеры)
-- ===================================================================

CREATE OR REPLACE FUNCTION setup_table_for_replication(
    p_table_name VARCHAR,
    p_schema_name VARCHAR DEFAULT 'public'
)
RETURNS TEXT AS $$
DECLARE
    v_result TEXT;
    v_prepare_result TEXT;
    v_trigger_result TEXT;
BEGIN
    -- Шаг 1: Подготовить поля
    SELECT prepare_table_for_replication(p_table_name, p_schema_name)
    INTO v_prepare_result;
    
    -- Шаг 2: Создать триггеры
    SELECT setup_replication_for_table(p_table_name, p_schema_name)
    INTO v_trigger_result;
    
    v_result := format(
        E'Full setup completed for %I.%I:\n1. %s\n2. %s',
        p_schema_name,
        p_table_name,
        v_prepare_result,
        v_trigger_result
    );
    
    RETURN v_result;
END;
$$ LANGUAGE plpgsql;

COMMENT ON FUNCTION setup_table_for_replication IS 
'Полная настройка таблицы для репликации:
1. Добавляет обязательные поля (version, updated_at, updated_by)
2. Создает триггеры репликации

Использование: SELECT setup_table_for_replication(''users'');';

-- ===================================================================
-- Пример миграции для нескольких таблиц
-- ===================================================================

-- Вариант 1: Полная настройка (поля + триггеры) - РЕКОМЕНДУЕТСЯ
-- ------------------------------------------------------------
DO $$
DECLARE
    t VARCHAR;
BEGIN
    FOR t IN 
        SELECT unnest(ARRAY[
            'users',
            'orders',
            'order_items',
            'products',
            'categories'
        ])
    LOOP
        -- Проверяем, существует ли таблица
        IF EXISTS (
            SELECT 1 FROM information_schema.tables 
            WHERE table_schema = 'public' AND table_name = t
        ) THEN
            RAISE NOTICE 'Setting up table: %', t;
            PERFORM setup_table_for_replication(t);
        ELSE
            RAISE NOTICE 'Table % does not exist, skipping', t;
        END IF;
    END LOOP;
END $$;

-- Вариант 2: Только добавить поля (без триггеров)
-- ------------------------------------------------------------
-- DO $$
-- DECLARE
--     t VARCHAR;
-- BEGIN
--     FOR t IN 
--         SELECT unnest(ARRAY['users', 'orders', 'products'])
--     LOOP
--         RAISE NOTICE 'Preparing table: %', t;
--         PERFORM prepare_table_for_replication(t);
--     END LOOP;
-- END $$;

-- ===================================================================
-- Проверка результатов миграции
-- ===================================================================

-- Проверить, какие таблицы имеют обязательные поля
SELECT 
    t.table_schema,
    t.table_name,
    EXISTS (
        SELECT 1 FROM information_schema.columns c
        WHERE c.table_schema = t.table_schema
          AND c.table_name = t.table_name
          AND c.column_name = 'version'
    ) as has_version,
    EXISTS (
        SELECT 1 FROM information_schema.columns c
        WHERE c.table_schema = t.table_schema
          AND c.table_name = t.table_name
          AND c.column_name = 'updated_at'
    ) as has_updated_at,
    EXISTS (
        SELECT 1 FROM information_schema.columns c
        WHERE c.table_schema = t.table_schema
          AND c.table_name = t.table_name
          AND c.column_name = 'updated_by'
    ) as has_updated_by
FROM information_schema.tables t
WHERE t.table_schema = 'public'
  AND t.table_type = 'BASE TABLE'
  AND t.table_name NOT IN ('replication_queue', 'processed_events')
ORDER BY t.table_name;

-- Проверить, какие таблицы имеют триггеры репликации
SELECT 
    c.relname as table_name,
    COUNT(*) FILTER (WHERE t.tgname LIKE '%version_trigger') as has_version_trigger,
    COUNT(*) FILTER (WHERE t.tgname LIKE '%replication_trigger') as has_replication_trigger
FROM pg_class c
JOIN pg_namespace n ON c.relnamespace = n.oid
LEFT JOIN pg_trigger t ON t.tgrelid = c.oid
WHERE n.nspname = 'public'
  AND c.relkind = 'r'
  AND c.relname NOT IN ('replication_queue', 'processed_events')
GROUP BY c.relname
ORDER BY c.relname;

-- ===================================================================
-- Функция для генерации миграционного скрипта
-- ===================================================================

CREATE OR REPLACE FUNCTION generate_migration_script(
    p_tables VARCHAR[] DEFAULT NULL,
    p_schema_name VARCHAR DEFAULT 'public'
)
RETURNS TABLE(line_number INT, sql_statement TEXT) AS $$
DECLARE
    v_table VARCHAR;
    v_counter INT := 1;
    v_tables VARCHAR[];
BEGIN
    -- Если таблицы не указаны, берем все таблицы схемы
    IF p_tables IS NULL THEN
        SELECT array_agg(table_name::VARCHAR)
        INTO v_tables
        FROM information_schema.tables
        WHERE table_schema = p_schema_name
          AND table_type = 'BASE TABLE'
          AND table_name NOT IN ('replication_queue', 'processed_events');
    ELSE
        v_tables := p_tables;
    END IF;
    
    -- Заголовок
    line_number := v_counter; 
    sql_statement := '-- ===================================================================';
    v_counter := v_counter + 1;
    RETURN NEXT;
    
    line_number := v_counter;
    sql_statement := '-- Migration script generated at ' || NOW()::TEXT;
    v_counter := v_counter + 1;
    RETURN NEXT;
    
    line_number := v_counter;
    sql_statement := '-- ===================================================================';
    v_counter := v_counter + 1;
    RETURN NEXT;
    
    line_number := v_counter;
    sql_statement := '';
    v_counter := v_counter + 1;
    RETURN NEXT;
    
    -- Для каждой таблицы
    FOREACH v_table IN ARRAY v_tables
    LOOP
        line_number := v_counter;
        sql_statement := format('-- Table: %I', v_table);
        v_counter := v_counter + 1;
        RETURN NEXT;
        
        line_number := v_counter;
        sql_statement := format('SELECT setup_table_for_replication(%L, %L);', v_table, p_schema_name);
        v_counter := v_counter + 1;
        RETURN NEXT;
        
        line_number := v_counter;
        sql_statement := '';
        v_counter := v_counter + 1;
        RETURN NEXT;
    END LOOP;
END;
$$ LANGUAGE plpgsql;

COMMENT ON FUNCTION generate_migration_script IS 
'Генерирует SQL скрипт для миграции таблиц.

Использование:
-- Для всех таблиц:
SELECT sql_statement FROM generate_migration_script();

-- Для конкретных таблиц:
SELECT sql_statement FROM generate_migration_script(ARRAY[''users'', ''orders'']);';

-- ===================================================================
-- Пример использования generate_migration_script
-- ===================================================================

-- Сгенерировать скрипт для всех таблиц
-- SELECT sql_statement FROM generate_migration_script();

-- Сгенерировать скрипт для конкретных таблиц
-- SELECT sql_statement FROM generate_migration_script(ARRAY['users', 'orders', 'products']);

-- Сохранить в файл (из psql):
-- \o migration.sql
-- SELECT sql_statement FROM generate_migration_script();
-- \o

