package kafka

import (
	"fmt"
	"time"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"github.com/rs/zerolog"
)

// ConsumerConfig представляет конфигурацию Kafka consumer
type ConsumerConfig struct {
	Brokers           []string
	SSLEnabled        bool
	SSLCACert         string
	SSLClientCert     string
	SSLClientKey      string
	
	// Consumer настройки
	ConsumerGroup      string
	AutoOffsetReset    string
	EnableAutoCommit   bool
	SessionTimeoutMs   int
	MaxPollIntervalMs  int
	Topics             []string
}

// Consumer обертка над confluent-kafka-go Consumer
type Consumer struct {
	consumer *kafka.Consumer
	logger   zerolog.Logger
	topics   []string
}

// NewConsumer создает новый Kafka consumer
func NewConsumer(cfg ConsumerConfig, logger zerolog.Logger) (*Consumer, error) {
	// Базовая конфигурация
	configMap := kafka.ConfigMap{
		"bootstrap.servers":        joinBrokers(cfg.Brokers),
		"group.id":                 cfg.ConsumerGroup,
		"auto.offset.reset":        cfg.AutoOffsetReset,
		"enable.auto.commit":       cfg.EnableAutoCommit,
		"session.timeout.ms":       cfg.SessionTimeoutMs,
		"max.poll.interval.ms":     cfg.MaxPollIntervalMs,
		"client.id":                "replicator-consumer",
	}

	// SSL конфигурация
	if cfg.SSLEnabled {
		configMap["security.protocol"] = "SSL"
		
		if cfg.SSLCACert != "" {
			configMap["ssl.ca.location"] = cfg.SSLCACert
		}
		if cfg.SSLClientCert != "" {
			configMap["ssl.certificate.location"] = cfg.SSLClientCert
		}
		if cfg.SSLClientKey != "" {
			configMap["ssl.key.location"] = cfg.SSLClientKey
		}
		
		logger.Info().
			Bool("ssl_enabled", true).
			Str("ca_cert", cfg.SSLCACert).
			Msg("Kafka SSL enabled")
	}

	// Создаем consumer
	consumer, err := kafka.NewConsumer(&configMap)
	if err != nil {
		return nil, fmt.Errorf("failed to create kafka consumer: %w", err)
	}

	// Подписываемся на топики
	if err := consumer.SubscribeTopics(cfg.Topics, nil); err != nil {
		consumer.Close()
		return nil, fmt.Errorf("failed to subscribe to topics: %w", err)
	}

	logger.Info().
		Strs("brokers", cfg.Brokers).
		Str("group_id", cfg.ConsumerGroup).
		Strs("topics", cfg.Topics).
		Msg("Kafka consumer created successfully")

	return &Consumer{
		consumer: consumer,
		logger:   logger,
		topics:   cfg.Topics,
	}, nil
}

// Poll читает сообщение из Kafka
func (c *Consumer) Poll(timeout time.Duration) (*kafka.Message, error) {
	event := c.consumer.Poll(int(timeout.Milliseconds()))
	
	if event == nil {
		return nil, nil
	}

	switch e := event.(type) {
	case *kafka.Message:
		c.logger.Debug().
			Str("topic", *e.TopicPartition.Topic).
			Int32("partition", e.TopicPartition.Partition).
			Int64("offset", int64(e.TopicPartition.Offset)).
			Msg("Message received")
		return e, nil
		
	case kafka.Error:
		c.logger.Error().
			Err(e).
			Msg("Kafka error")
		return nil, e
		
	default:
		c.logger.Debug().
			Interface("event", e).
			Msg("Ignored Kafka event")
		return nil, nil
	}
}

// Commit подтверждает обработку сообщения
func (c *Consumer) Commit(message *kafka.Message) error {
	_, err := c.consumer.CommitMessage(message)
	if err != nil {
		return fmt.Errorf("failed to commit message: %w", err)
	}
	
	c.logger.Debug().
		Str("topic", *message.TopicPartition.Topic).
		Int32("partition", message.TopicPartition.Partition).
		Int64("offset", int64(message.TopicPartition.Offset)).
		Msg("Message committed")
	
	return nil
}

// Close закрывает consumer
func (c *Consumer) Close() {
	c.logger.Info().Msg("Closing Kafka consumer...")
	if err := c.consumer.Close(); err != nil {
		c.logger.Error().Err(err).Msg("Failed to close Kafka consumer")
	} else {
		c.logger.Info().Msg("Kafka consumer closed")
	}
}

// GetMetadata возвращает метаданные Kafka
func (c *Consumer) GetMetadata() (*kafka.Metadata, error) {
	metadata, err := c.consumer.GetMetadata(nil, true, 5000)
	if err != nil {
		return nil, fmt.Errorf("failed to get metadata: %w", err)
	}
	return metadata, nil
}

