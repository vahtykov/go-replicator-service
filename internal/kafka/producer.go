package kafka

import (
	"fmt"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"github.com/rs/zerolog"
)

// ProducerConfig представляет конфигурацию Kafka producer
type ProducerConfig struct {
	Brokers       []string
	SSLEnabled    bool
	SSLCACert     string
	SSLClientCert string
	SSLClientKey  string
	
	// Producer настройки
	Acks         string
	Compression  string
	MaxInFlight  int
	BatchSize    int
	LingerMs     int
}

// Producer обертка над confluent-kafka-go Producer
type Producer struct {
	producer *kafka.Producer
	logger   zerolog.Logger
}

// NewProducer создает новый Kafka producer
func NewProducer(cfg ProducerConfig, logger zerolog.Logger) (*Producer, error) {
	// Базовая конфигурация
	configMap := kafka.ConfigMap{
		"bootstrap.servers": joinBrokers(cfg.Brokers),
		"acks":              cfg.Acks,
		"compression.type":  cfg.Compression,
		"max.in.flight.requests.per.connection": cfg.MaxInFlight,
		"batch.size":     cfg.BatchSize,
		"linger.ms":      cfg.LingerMs,
		"client.id":      "replicator-publisher",
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

	// Создаем producer
	producer, err := kafka.NewProducer(&configMap)
	if err != nil {
		return nil, fmt.Errorf("failed to create kafka producer: %w", err)
	}

	logger.Info().
		Strs("brokers", cfg.Brokers).
		Str("acks", cfg.Acks).
		Str("compression", cfg.Compression).
		Msg("Kafka producer created successfully")

	p := &Producer{
		producer: producer,
		logger:   logger,
	}

	// Запускаем горутину для обработки delivery reports
	go p.handleDeliveryReports()

	return p, nil
}

// Produce отправляет сообщение в Kafka
func (p *Producer) Produce(topic string, key []byte, value []byte) error {
	message := &kafka.Message{
		TopicPartition: kafka.TopicPartition{
			Topic:     &topic,
			Partition: kafka.PartitionAny,
		},
		Key:   key,
		Value: value,
	}

	// Отправляем сообщение (асинхронно)
	deliveryChan := make(chan kafka.Event, 1)
	if err := p.producer.Produce(message, deliveryChan); err != nil {
		return fmt.Errorf("failed to produce message: %w", err)
	}

	// Ждем подтверждения доставки
	e := <-deliveryChan
	m := e.(*kafka.Message)

	if m.TopicPartition.Error != nil {
		return fmt.Errorf("delivery failed: %w", m.TopicPartition.Error)
	}

	p.logger.Debug().
		Str("topic", topic).
		Int32("partition", m.TopicPartition.Partition).
		Int64("offset", int64(m.TopicPartition.Offset)).
		Msg("Message delivered successfully")

	return nil
}

// ProduceAsync отправляет сообщение асинхронно (для батчей)
func (p *Producer) ProduceAsync(topic string, key []byte, value []byte) error {
	message := &kafka.Message{
		TopicPartition: kafka.TopicPartition{
			Topic:     &topic,
			Partition: kafka.PartitionAny,
		},
		Key:   key,
		Value: value,
	}

	// Отправляем асинхронно (delivery report обрабатывается в handleDeliveryReports)
	if err := p.producer.Produce(message, nil); err != nil {
		return fmt.Errorf("failed to produce message: %w", err)
	}

	return nil
}

// Flush ждет доставки всех сообщений
func (p *Producer) Flush(timeoutMs int) int {
	remaining := p.producer.Flush(timeoutMs)
	if remaining > 0 {
		p.logger.Warn().
			Int("remaining", remaining).
			Msg("Some messages were not flushed")
	}
	return remaining
}

// Close закрывает producer
func (p *Producer) Close() {
	p.logger.Info().Msg("Closing Kafka producer...")
	p.producer.Flush(10000) // 10 секунд на flush
	p.producer.Close()
	p.logger.Info().Msg("Kafka producer closed")
}

// handleDeliveryReports обрабатывает асинхронные delivery reports
func (p *Producer) handleDeliveryReports() {
	for e := range p.producer.Events() {
		switch ev := e.(type) {
		case *kafka.Message:
			if ev.TopicPartition.Error != nil {
				p.logger.Error().
					Err(ev.TopicPartition.Error).
					Str("topic", *ev.TopicPartition.Topic).
					Msg("Message delivery failed")
			} else {
				p.logger.Debug().
					Str("topic", *ev.TopicPartition.Topic).
					Int32("partition", ev.TopicPartition.Partition).
					Int64("offset", int64(ev.TopicPartition.Offset)).
					Msg("Message delivered")
			}
		}
	}
}

// joinBrokers объединяет список брокеров в строку
func joinBrokers(brokers []string) string {
	result := ""
	for i, broker := range brokers {
		if i > 0 {
			result += ","
		}
		result += broker
	}
	return result
}

