-- ===================================================================
-- MASTER SETUP SCRIPT
-- Полная настройка репликации для PostgreSQL
-- ===================================================================
-- Этот скрипт выполняет полную настройку:
-- 1. Создает служебные таблицы
-- 2. Создает функции триггеров
-- 3. Создает helper функции для миграции
-- 4. Настраивает указанные таблицы для репликации
-- ===================================================================

\echo '============================================================'
\echo 'PostgreSQL Replication Setup - Master Script'
\echo '============================================================'
\echo ''

-- Проверка версии PostgreSQL
DO $$
DECLARE
    v_version INT;
BEGIN
    SELECT current_setting('server_version_num')::INT INTO v_version;
    
    IF v_version < 100000 THEN
        RAISE EXCEPTION 'PostgreSQL version 10+ required. Current version: %', 
            current_setting('server_version');
    END IF;
    
    RAISE NOTICE 'PostgreSQL version check: OK (version %)', current_setting('server_version');
END $$;

\echo ''
\echo '------------------------------------------------------------'
\echo 'Step 1/4: Creating service tables...'
\echo '------------------------------------------------------------'

\i 01_create_tables.sql

\echo ''
\echo '------------------------------------------------------------'
\echo 'Step 2/4: Creating trigger functions...'
\echo '------------------------------------------------------------'

\i 02_create_replication_trigger.sql

\echo ''
\echo '------------------------------------------------------------'
\echo 'Step 3/4: Creating migration helpers...'
\echo '------------------------------------------------------------'

\i 04_migrate_existing_tables.sql

\echo ''
\echo '------------------------------------------------------------'
\echo 'Step 4/4: Setup complete! Summary:'
\echo '------------------------------------------------------------'

-- Показываем статус
SELECT 
    'replication_queue' as table_name,
    pg_size_pretty(pg_total_relation_size('replication_queue')) as size,
    (SELECT COUNT(*) FROM replication_queue) as row_count
UNION ALL
SELECT 
    'processed_events',
    pg_size_pretty(pg_total_relation_size('processed_events')),
    (SELECT COUNT(*) FROM processed_events);

\echo ''
\echo 'Available functions:'
\echo '  • setup_table_for_replication(table_name)  - Full setup (fields + triggers)'
\echo '  • prepare_table_for_replication(table_name) - Add fields only'
\echo '  • setup_replication_for_table(table_name)   - Add triggers only'
\echo '  • remove_replication_from_table(table_name) - Remove triggers'
\echo '  • cleanup_replication_queue(days)           - Cleanup old events'
\echo ''
\echo '============================================================'
\echo 'Setup completed successfully!'
\echo '============================================================'
\echo ''
\echo 'Next steps:'
\echo '1. Setup replication for your tables:'
\echo '   SELECT setup_table_for_replication(''users'');'
\echo '   SELECT setup_table_for_replication(''orders'');'
\echo ''
\echo '2. Test replication:'
\echo '   INSERT INTO users (name, email) VALUES (''Test'', ''test@example.com'');'
\echo '   SELECT * FROM replication_queue ORDER BY id DESC LIMIT 1;'
\echo ''
\echo '3. See examples:'
\echo '   \\i 03_setup_example.sql'
\echo ''

