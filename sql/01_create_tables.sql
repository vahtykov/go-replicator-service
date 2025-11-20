-- ===================================================================
-- Служебные таблицы для репликации
-- ===================================================================

-- Таблица для очереди репликации (outbox pattern)
CREATE TABLE IF NOT EXISTS replication_queue (
    id BIGSERIAL PRIMARY KEY,
    table_name VARCHAR(255) NOT NULL,
    operation VARCHAR(10) NOT NULL,      -- INSERT, UPDATE, DELETE
    record_data JSONB NOT NULL,          -- Полные данные записи
    created_at TIMESTAMPTZ DEFAULT NOW(),
    published BOOLEAN DEFAULT FALSE,
    published_at TIMESTAMPTZ,
    
    CONSTRAINT chk_operation CHECK (operation IN ('INSERT', 'UPDATE', 'DELETE'))
);

-- Индексы для производительности
CREATE INDEX IF NOT EXISTS idx_repl_queue_unpublished 
    ON replication_queue(created_at) 
    WHERE NOT published;

CREATE INDEX IF NOT EXISTS idx_repl_queue_table 
    ON replication_queue(table_name, published);

-- Комментарии
COMMENT ON TABLE replication_queue IS 'Очередь событий для репликации между контурами';
COMMENT ON COLUMN replication_queue.table_name IS 'Имя таблицы, в которой произошло изменение';
COMMENT ON COLUMN replication_queue.operation IS 'Тип операции: INSERT, UPDATE, DELETE';
COMMENT ON COLUMN replication_queue.record_data IS 'JSON с полными данными записи после изменения';
COMMENT ON COLUMN replication_queue.published IS 'Флаг, опубликовано ли событие в Kafka';

-- ===================================================================
-- Таблица для идемпотентности (отслеживание обработанных событий)
-- ===================================================================

CREATE TABLE IF NOT EXISTS processed_events (
    event_id VARCHAR(255) PRIMARY KEY,
    table_name VARCHAR(255),
    processed_at TIMESTAMPTZ DEFAULT NOW()
);

-- Индекс для очистки старых записей
CREATE INDEX IF NOT EXISTS idx_processed_events_timestamp 
    ON processed_events(processed_at);

COMMENT ON TABLE processed_events IS 'Отслеживание обработанных событий для идемпотентности';

-- ===================================================================
-- Функция для периодической очистки старых записей
-- ===================================================================

CREATE OR REPLACE FUNCTION cleanup_replication_queue(retention_days INT DEFAULT 7)
RETURNS TABLE(deleted_count BIGINT) AS $$
DECLARE
    v_deleted_count BIGINT;
BEGIN
    -- Удаляем опубликованные записи старше retention_days
    DELETE FROM replication_queue
    WHERE published = TRUE 
      AND created_at < NOW() - (retention_days || ' days')::INTERVAL;
    
    GET DIAGNOSTICS v_deleted_count = ROW_COUNT;
    
    RETURN QUERY SELECT v_deleted_count;
END;
$$ LANGUAGE plpgsql;

COMMENT ON FUNCTION cleanup_replication_queue IS 
    'Очистка старых опубликованных записей из replication_queue. Запускать периодически (например, раз в день).';

-- Пример использования:
-- SELECT cleanup_replication_queue(7);  -- Удалить записи старше 7 дней

-- ===================================================================
-- Функция для очистки processed_events
-- ===================================================================

CREATE OR REPLACE FUNCTION cleanup_processed_events(retention_days INT DEFAULT 30)
RETURNS TABLE(deleted_count BIGINT) AS $$
DECLARE
    v_deleted_count BIGINT;
BEGIN
    DELETE FROM processed_events
    WHERE processed_at < NOW() - (retention_days || ' days')::INTERVAL;
    
    GET DIAGNOSTICS v_deleted_count = ROW_COUNT;
    
    RETURN QUERY SELECT v_deleted_count;
END;
$$ LANGUAGE plpgsql;

COMMENT ON FUNCTION cleanup_processed_events IS 
    'Очистка старых записей из processed_events. Запускать периодически.';

