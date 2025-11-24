package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"gorm.io/gorm"

	"github.com/vahtykov/go-replicator-service/internal/database"
	kafkapkg "github.com/vahtykov/go-replicator-service/internal/kafka"
)

// Consumer читает события из Kafka и применяет к БД
type Consumer struct {
	db           *gorm.DB
	consumer     *kafkapkg.Consumer
	config       Config
	logger       zerolog.Logger
	applier      *EventApplier
	
	// Метрики
	processedCount int64
	skippedCount   int64
	failedCount    int64
}

// Config представляет конфигурацию Consumer
type Config struct {
	MyContour           string
	Database            string
	BatchSize           int
	EventTimeout        time.Duration
	ConflictResolution  string // last_write_wins, skip, error
}

// New создает новый Consumer
func New(db *gorm.DB, consumer *kafkapkg.Consumer, cfg Config, logger zerolog.Logger) *Consumer {
	return &Consumer{
		db:       db,
		consumer: consumer,
		config:   cfg,
		logger:   logger.With().Str("component", "consumer").Logger(),
		applier:  NewEventApplier(db, cfg, logger),
	}
}

// Start запускает процесс потребления
func (c *Consumer) Start(ctx context.Context) error {
	c.logger.Info().
		Str("contour", c.config.MyContour).
		Str("database", c.config.Database).
		Msg("Consumer started")

	for {
		select {
		case <-ctx.Done():
			c.logger.Info().Msg("Consumer stopped by context")
			return ctx.Err()
			
		default:
			if err := c.processMessage(ctx); err != nil {
				c.logger.Error().
					Err(err).
					Msg("Failed to process message")
				c.failedCount++
				// Не останавливаем consumer при ошибке, продолжаем обработку
			}
		}
	}
}

// processMessage обрабатывает одно сообщение из Kafka
func (c *Consumer) processMessage(ctx context.Context) error {
	// Читаем сообщение из Kafka (timeout 1 секунда)
	message, err := c.consumer.Poll(1 * time.Second)
	if err != nil {
		return fmt.Errorf("failed to poll message: %w", err)
	}
	
	// Если сообщений нет, возвращаемся
	if message == nil {
		return nil
	}

	// Парсим событие
	var event ReplicationEvent
	if err := json.Unmarshal(message.Value, &event); err != nil {
		c.logger.Error().
			Err(err).
			Str("raw_message", string(message.Value)).
			Msg("Failed to parse event")
		// Коммитим сообщение, чтобы не застревать на битом
		c.consumer.Commit(message)
		return fmt.Errorf("failed to parse event: %w", err)
	}

	c.logger.Debug().
		Str("event_id", event.EventID).
		Str("table", event.Table).
		Str("operation", event.Operation).
		Str("source_contour", event.Source.Contour).
		Msg("Event received")

	// Фильтрация: пропускаем события от своего контура
	if !c.shouldProcess(event) {
		c.logger.Debug().
			Str("event_id", event.EventID).
			Str("source_contour", event.Source.Contour).
			Str("my_contour", c.config.MyContour).
			Msg("Skipping own event")
		c.skippedCount++
		// Коммитим, так как событие обработано (пропущено намеренно)
		return c.consumer.Commit(message)
	}

	// Обрабатываем событие
	if err := c.applyEvent(ctx, event); err != nil {
		c.logger.Error().
			Err(err).
			Str("event_id", event.EventID).
			Msg("Failed to apply event")
		// НЕ коммитим при ошибке - Kafka повторит доставку
		return fmt.Errorf("failed to apply event: %w", err)
	}

	// Коммитим успешно обработанное сообщение
	if err := c.consumer.Commit(message); err != nil {
		c.logger.Error().
			Err(err).
			Str("event_id", event.EventID).
			Msg("Failed to commit message")
		return fmt.Errorf("failed to commit message: %w", err)
	}

	c.processedCount++
	c.logger.Info().
		Str("event_id", event.EventID).
		Str("table", event.Table).
		Str("operation", event.Operation).
		Int64("total_processed", c.processedCount).
		Msg("Event applied successfully")

	return nil
}

// shouldProcess определяет, нужно ли обрабатывать событие
func (c *Consumer) shouldProcess(event ReplicationEvent) bool {
	// Пропускаем события от своего контура (защита от петли)
	if event.Source.Contour == c.config.MyContour {
		return false
	}
	return true
}

// applyEvent применяет событие к БД
func (c *Consumer) applyEvent(ctx context.Context, event ReplicationEvent) error {
	// Начинаем транзакцию
	tx := c.db.WithContext(ctx).Begin()
	if tx.Error != nil {
		return fmt.Errorf("failed to begin transaction: %w", tx.Error)
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			c.logger.Error().Interface("panic", r).Msg("Panic in applyEvent")
		}
	}()

	// 1. Проверяем идемпотентность
	var existingEvent database.ProcessedEvent
	result := tx.Where("event_id = ?", event.EventID).First(&existingEvent)
	
	if result.Error == nil {
		// Событие уже обработано
		c.logger.Debug().
			Str("event_id", event.EventID).
			Msg("Event already processed (idempotent skip)")
		tx.Rollback()
		return nil
	} else if result.Error != gorm.ErrRecordNotFound {
		tx.Rollback()
		return fmt.Errorf("failed to check processed_events: %w", result.Error)
	}

	// 2. Откладываем проверку FK constraints
	if err := tx.Exec("SET CONSTRAINTS ALL DEFERRED").Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to set constraints deferred: %w", err)
	}

	// 3. Применяем DML операцию
	if err := c.applier.Apply(tx, event); err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to apply DML: %w", err)
	}

	// 4. Записываем в processed_events
	processedEvent := database.ProcessedEvent{
		EventID:     event.EventID,
		ProcessedAt: time.Now(),
	}
	if err := tx.Create(&processedEvent).Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to insert into processed_events: %w", err)
	}

	// 5. Коммитим транзакцию (здесь проверяются FK constraints)
	if err := tx.Commit().Error; err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// GetMetrics возвращает метрики consumer
func (c *Consumer) GetMetrics() (processed, skipped, failed int64) {
	return c.processedCount, c.skippedCount, c.failedCount
}

