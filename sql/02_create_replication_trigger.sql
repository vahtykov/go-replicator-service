-- ===================================================================
-- Универсальная функция триггера для репликации
-- ===================================================================

CREATE OR REPLACE FUNCTION generic_replication_trigger()
RETURNS TRIGGER AS $$
BEGIN
    -- ============================================================
    -- ЗАЩИТА ОТ ПЕТЛИ РЕПЛИКАЦИИ #1
    -- Проверяем session_replication_role
    -- Если установлен 'replica', значит это ReplicatorConsumer
    -- применяет данные, и триггер НЕ должен срабатывать
    -- ============================================================
    IF current_setting('session_replication_role') = 'replica' THEN
        RETURN NULL;  -- Не записываем в replication_queue
    END IF;

    -- ============================================================
    -- ЗАЩИТА ОТ ПЕТЛИ РЕПЛИКАЦИИ #2 (опционально)
    -- Дополнительная проверка по application_name
    -- Если ReplicatorConsumer подключается с application_name='replicator_consumer',
    -- можно добавить еще одну проверку
    -- ============================================================
    -- IF current_setting('application_name', true) = 'replicator_consumer' THEN
    --     RETURN NULL;
    -- END IF;

    -- ============================================================
    -- Обработка INSERT
    -- ============================================================
    IF TG_OP = 'INSERT' THEN
        INSERT INTO replication_queue (table_name, operation, record_data)
        VALUES (
            TG_TABLE_NAME::VARCHAR,
            'INSERT',
            row_to_json(NEW)::JSONB
        );
        
        RETURN NEW;
    
    -- ============================================================
    -- Обработка UPDATE
    -- ============================================================
    ELSIF TG_OP = 'UPDATE' THEN
        INSERT INTO replication_queue (table_name, operation, record_data)
        VALUES (
            TG_TABLE_NAME::VARCHAR,
            'UPDATE',
            jsonb_build_object(
                'before', row_to_json(OLD)::JSONB,
                'after', row_to_json(NEW)::JSONB
            )
        );
        
        RETURN NEW;
    
    -- ============================================================
    -- Обработка DELETE
    -- ============================================================
    ELSIF TG_OP = 'DELETE' THEN
        INSERT INTO replication_queue (table_name, operation, record_data)
        VALUES (
            TG_TABLE_NAME::VARCHAR,
            'DELETE',
            row_to_json(OLD)::JSONB
        );
        
        RETURN OLD;
    END IF;
    
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

COMMENT ON FUNCTION generic_replication_trigger IS 
'Универсальная функция триггера для репликации DML операций.
Защищена от петли репликации через проверку session_replication_role.
Используется для AFTER INSERT OR UPDATE OR DELETE триггеров.';

-- ===================================================================
-- Вспомогательная функция для инкремента версии при UPDATE
-- ===================================================================

CREATE OR REPLACE FUNCTION increment_version_on_update()
RETURNS TRIGGER AS $$
BEGIN
    -- Автоматически инкрементируем version при UPDATE
    IF TG_OP = 'UPDATE' THEN
        NEW.version = OLD.version + 1;
        NEW.updated_at = NOW();
    ELSIF TG_OP = 'INSERT' THEN
        -- При INSERT устанавливаем version=1 если не задано
        NEW.version = COALESCE(NEW.version, 1);
        NEW.updated_at = COALESCE(NEW.updated_at, NOW());
    END IF;
    
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

COMMENT ON FUNCTION increment_version_on_update IS 
'Автоматически инкрементирует поле version при UPDATE.
Используется в BEFORE триггере.';

-- ===================================================================
-- Функция для создания триггеров на таблице (helper)
-- ===================================================================

CREATE OR REPLACE FUNCTION setup_replication_for_table(
    p_table_name VARCHAR,
    p_schema_name VARCHAR DEFAULT 'public'
)
RETURNS TEXT AS $$
DECLARE
    v_trigger_name_version VARCHAR;
    v_trigger_name_replication VARCHAR;
    v_result TEXT;
BEGIN
    v_trigger_name_version := p_table_name || '_version_trigger';
    v_trigger_name_replication := p_table_name || '_replication_trigger';
    
    -- Создаем BEFORE триггер для инкремента версии
    EXECUTE format(
        'CREATE TRIGGER %I
         BEFORE INSERT OR UPDATE ON %I.%I
         FOR EACH ROW
         EXECUTE FUNCTION increment_version_on_update()',
        v_trigger_name_version,
        p_schema_name,
        p_table_name
    );
    
    -- Создаем AFTER триггер для репликации
    EXECUTE format(
        'CREATE TRIGGER %I
         AFTER INSERT OR UPDATE OR DELETE ON %I.%I
         FOR EACH ROW
         EXECUTE FUNCTION generic_replication_trigger()',
        v_trigger_name_replication,
        p_schema_name,
        p_table_name
    );
    
    v_result := format(
        'Replication triggers created for table %I.%I: %I, %I',
        p_schema_name,
        p_table_name,
        v_trigger_name_version,
        v_trigger_name_replication
    );
    
    RETURN v_result;
END;
$$ LANGUAGE plpgsql;

COMMENT ON FUNCTION setup_replication_for_table IS 
'Создает два триггера на таблице:
1. BEFORE триггер для автоматического инкремента version
2. AFTER триггер для репликации в replication_queue

Использование: SELECT setup_replication_for_table(''users'');';

-- ===================================================================
-- Функция для удаления триггеров с таблицы
-- ===================================================================

CREATE OR REPLACE FUNCTION remove_replication_from_table(
    p_table_name VARCHAR,
    p_schema_name VARCHAR DEFAULT 'public'
)
RETURNS TEXT AS $$
DECLARE
    v_trigger_name_version VARCHAR;
    v_trigger_name_replication VARCHAR;
BEGIN
    v_trigger_name_version := p_table_name || '_version_trigger';
    v_trigger_name_replication := p_table_name || '_replication_trigger';
    
    EXECUTE format(
        'DROP TRIGGER IF EXISTS %I ON %I.%I',
        v_trigger_name_version,
        p_schema_name,
        p_table_name
    );
    
    EXECUTE format(
        'DROP TRIGGER IF EXISTS %I ON %I.%I',
        v_trigger_name_replication,
        p_schema_name,
        p_table_name
    );
    
    RETURN format(
        'Replication triggers removed from table %I.%I',
        p_schema_name,
        p_table_name
    );
END;
$$ LANGUAGE plpgsql;

COMMENT ON FUNCTION remove_replication_from_table IS 
'Удаляет триггеры репликации с таблицы.
Использование: SELECT remove_replication_from_table(''users'');';

