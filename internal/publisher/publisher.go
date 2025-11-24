package publisher

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"gorm.io/gorm"

	"github.com/vahtykov/go-replicator-service/internal/database"
	"github.com/vahtykov/go-replicator-service/internal/kafka"
)

// Publisher читает replication_queue и публикует в Kafka
type Publisher struct {
	db           *gorm.DB
	producer     *kafka.Producer
	config       Config
	logger       zerolog.Logger
	
	// Метрики
	processedCount int64
	failedCount    int64
}

// Config представляет конфигурацию Publisher
type Config struct {
	Contour      string
	Database     string
	PollInterval time.Duration
	BatchSize    int
}

// New создает новый Publisher
func New(db *gorm.DB, producer *kafka.Producer, cfg Config, logger zerolog.Logger) *Publisher {
	return &Publisher{
		db:       db,
		producer: producer,
		config:   cfg,
		logger:   logger.With().Str("component", "publisher").Logger(),
	}
}

// Start запускает процесс публикации
func (p *Publisher) Start(ctx context.Context) error {
	p.logger.Info().
		Str("contour", p.config.Contour).
		Dur("poll_interval", p.config.PollInterval).
		Int("batch_size", p.config.BatchSize).
		Msg("Publisher started")

	ticker := time.NewTicker(p.config.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			p.logger.Info().Msg("Publisher stopped by context")
			return ctx.Err()
			
		case <-ticker.C:
			if err := p.processBatch(ctx); err != nil {
				p.logger.Error().
					Err(err).
					Msg("Failed to process batch")
				p.failedCount++
			}
		}
	}
}

// processBatch обрабатывает один батч записей из replication_queue
func (p *Publisher) processBatch(ctx context.Context) error {
	startTime := time.Now()
	
	// Начинаем транзакцию
	tx := p.db.WithContext(ctx).Begin()
	if tx.Error != nil {
		return fmt.Errorf("failed to begin transaction: %w", tx.Error)
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			p.logger.Error().Interface("panic", r).Msg("Panic in processBatch")
		}
	}()

	// Читаем непубликованные записи с блокировкой
	// FOR UPDATE SKIP LOCKED позволяет избежать deadlocks и масштабировать publisher
	var records []database.ReplicationQueue
	result := tx.
		Clauses(ForUpdateSkipLocked()).
		Where("published = ?", false).
		Order("id ASC").
		Limit(p.config.BatchSize).
		Find(&records)

	if result.Error != nil {
		tx.Rollback()
		return fmt.Errorf("failed to fetch records: %w", result.Error)
	}

	// Если записей нет, завершаем
	if len(records) == 0 {
		tx.Rollback()
		return nil
	}

	p.logger.Debug().
		Int("count", len(records)).
		Msg("Processing batch")

	// Публикуем записи в Kafka
	publishedIDs := make([]int64, 0, len(records))
	
	for _, record := range records {
		if err := p.publishRecord(ctx, record); err != nil {
			// При ошибке публикации откатываем всю транзакцию
			tx.Rollback()
			return fmt.Errorf("failed to publish record %d: %w", record.ID, err)
		}
		publishedIDs = append(publishedIDs, record.ID)
	}

	// Помечаем записи как опубликованные
	now := time.Now()
	result = tx.Model(&database.ReplicationQueue{}).
		Where("id IN ?", publishedIDs).
		Updates(map[string]interface{}{
			"published":    true,
			"published_at": now,
		})

	if result.Error != nil {
		tx.Rollback()
		return fmt.Errorf("failed to update published status: %w", result.Error)
	}

	// Коммитим транзакцию
	if err := tx.Commit().Error; err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	// Обновляем метрики
	p.processedCount += int64(len(records))
	
	elapsed := time.Since(startTime)
	p.logger.Info().
		Int("count", len(records)).
		Dur("duration_ms", elapsed).
		Int64("total_processed", p.processedCount).
		Msg("Batch published successfully")

	return nil
}

// publishRecord публикует одну запись в Kafka
func (p *Publisher) publishRecord(ctx context.Context, record database.ReplicationQueue) error {
	// Конвертируем JSONB в map
	recordData := map[string]interface{}(record.RecordData)

	// Создаем событие репликации
	event := NewReplicationEvent(
		p.config.Contour,
		p.config.Database,
		record.Table,
		record.Operation,
		recordData,
	)

	// Сериализуем в JSON
	eventJSON, err := event.ToJSON()
	if err != nil {
		return fmt.Errorf("failed to serialize event: %w", err)
	}

	// Определяем топик (table_name + "_changes")
	topic := record.Table + "_changes"

	// Partition key - primary key записи (для сохранения порядка)
	partitionKey := []byte(record.PrimaryKeyValue)
	if record.PrimaryKeyValue == "" {
		partitionKey = event.ExtractPartitionKey()
	}

	// Публикуем в Kafka (синхронно для гарантии доставки)
	if err := p.producer.Produce(topic, partitionKey, eventJSON); err != nil {
		return fmt.Errorf("failed to produce to kafka: %w", err)
	}

	p.logger.Debug().
		Str("event_id", event.EventID).
		Str("topic", topic).
		Str("table", record.Table).
		Str("operation", record.Operation).
		Msg("Event published")

	return nil
}

// GetMetrics возвращает метрики publisher
func (p *Publisher) GetMetrics() (processed int64, failed int64) {
	return p.processedCount, p.failedCount
}

