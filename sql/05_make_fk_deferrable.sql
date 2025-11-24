-- =====================================================
-- Миграция: Сделать все FK constraints DEFERRABLE
-- =====================================================
-- 
-- Назначение: Позволяет ReplicatorConsumer применять события
--             в любом порядке без нарушения referential integrity
--
-- Использование:
--   psql -U postgres -d your_database -f sql/05_make_fk_deferrable.sql
--
-- =====================================================

DO $$
DECLARE
    r RECORD;
    v_alter_sql TEXT;
    v_recreate_sql TEXT;
    v_count INTEGER := 0;
BEGIN
    RAISE NOTICE 'Starting FK constraints migration to DEFERRABLE...';
    RAISE NOTICE '';
    
    -- Перебираем все FK constraints
    FOR r IN 
        SELECT 
            tc.constraint_name,
            tc.table_schema,
            tc.table_name,
            kcu.column_name,
            ccu.table_schema AS foreign_table_schema,
            ccu.table_name AS foreign_table_name,
            ccu.column_name AS foreign_column_name,
            rc.update_rule,
            rc.delete_rule,
            pg_get_constraintdef(pgc.oid) as constraint_def,
            pgc.condeferrable,
            pgc.condeferred
        FROM information_schema.table_constraints AS tc 
        JOIN information_schema.key_column_usage AS kcu
            ON tc.constraint_name = kcu.constraint_name
            AND tc.table_schema = kcu.table_schema
        JOIN information_schema.constraint_column_usage AS ccu
            ON ccu.constraint_name = tc.constraint_name
            AND ccu.table_schema = tc.table_schema
        JOIN information_schema.referential_constraints AS rc
            ON rc.constraint_name = tc.constraint_name
            AND rc.constraint_schema = tc.table_schema
        JOIN pg_constraint AS pgc
            ON pgc.conname = tc.constraint_name
        WHERE tc.constraint_type = 'FOREIGN KEY'
            AND tc.table_schema NOT IN ('pg_catalog', 'information_schema')
        ORDER BY tc.table_name, tc.constraint_name
    LOOP
        -- Пропускаем FK которые уже DEFERRABLE
        IF r.condeferrable THEN
            RAISE NOTICE '[SKIP] %.% - already DEFERRABLE', r.table_name, r.constraint_name;
            CONTINUE;
        END IF;
        
        RAISE NOTICE '[PROCESSING] %.%', r.table_name, r.constraint_name;
        
        -- Формируем SQL для удаления старого constraint
        v_alter_sql := format(
            'ALTER TABLE %I.%I DROP CONSTRAINT %I',
            r.table_schema,
            r.table_name,
            r.constraint_name
        );
        
        -- Формируем SQL для создания нового DEFERRABLE constraint
        -- Извлекаем только часть определения FK
        v_recreate_sql := format(
            'ALTER TABLE %I.%I ADD CONSTRAINT %I FOREIGN KEY (%I) REFERENCES %I.%I(%I) ON UPDATE %s ON DELETE %s DEFERRABLE INITIALLY IMMEDIATE',
            r.table_schema,
            r.table_name,
            r.constraint_name,
            r.column_name,
            r.foreign_table_schema,
            r.foreign_table_name,
            r.foreign_column_name,
            r.update_rule,
            r.delete_rule
        );
        
        -- Выполняем миграцию
        BEGIN
            -- Удаляем старый constraint
            EXECUTE v_alter_sql;
            RAISE NOTICE '  - Dropped old constraint';
            
            -- Создаем новый DEFERRABLE constraint
            EXECUTE v_recreate_sql;
            RAISE NOTICE '  - Created DEFERRABLE constraint';
            
            v_count := v_count + 1;
            
        EXCEPTION WHEN OTHERS THEN
            RAISE WARNING '  - ERROR: % (SQLSTATE: %)', SQLERRM, SQLSTATE;
            RAISE WARNING '  - SQL: %', v_recreate_sql;
        END;
        
        RAISE NOTICE '';
    END LOOP;
    
    RAISE NOTICE '';
    RAISE NOTICE '==============================================';
    RAISE NOTICE 'Migration completed!';
    RAISE NOTICE 'Total FK constraints migrated: %', v_count;
    RAISE NOTICE '==============================================';
    
END $$;

-- Проверка результата
SELECT 
    tc.table_name,
    tc.constraint_name,
    CASE 
        WHEN pgc.condeferrable THEN '✓ DEFERRABLE'
        ELSE '✗ NOT DEFERRABLE'
    END as status,
    CASE 
        WHEN pgc.condeferred THEN 'INITIALLY DEFERRED'
        ELSE 'INITIALLY IMMEDIATE'
    END as initially
FROM information_schema.table_constraints AS tc 
JOIN pg_constraint AS pgc
    ON pgc.conname = tc.constraint_name
WHERE tc.constraint_type = 'FOREIGN KEY'
    AND tc.table_schema NOT IN ('pg_catalog', 'information_schema')
ORDER BY tc.table_name, tc.constraint_name;

