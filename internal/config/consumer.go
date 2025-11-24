package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// ConsumerConfig представляет конфигурацию ReplicatorConsumer
type ConsumerConfig struct {
	Service    ConsumerServiceConfig    `yaml:"service"`
	Database   DatabaseConfig           `yaml:"database"`
	Kafka      ConsumerKafkaConfig      `yaml:"kafka"`
	Logging    LoggingConfig            `yaml:"logging"`
	Processing ProcessingConfig         `yaml:"processing"`
}

// ConsumerServiceConfig содержит настройки сервиса
type ConsumerServiceConfig struct {
	Name    string `yaml:"name"`
	Contour string `yaml:"contour"`
}

// ConsumerKafkaConfig содержит настройки Kafka consumer
type ConsumerKafkaConfig struct {
	Brokers            []string `yaml:"brokers"`
	SSLEnabled         bool     `yaml:"ssl_enabled"`
	SSLCACert          string   `yaml:"ssl_ca_cert"`
	SSLClientCert      string   `yaml:"ssl_client_cert"`
	SSLClientKey       string   `yaml:"ssl_client_key"`
	
	// Consumer настройки
	ConsumerGroup      string   `yaml:"consumer_group"`
	AutoOffsetReset    string   `yaml:"auto_offset_reset"`
	EnableAutoCommit   bool     `yaml:"enable_auto_commit"`
	SessionTimeoutMs   int      `yaml:"session_timeout_ms"`
	MaxPollIntervalMs  int      `yaml:"max_poll_interval_ms"`
	Topics             []string `yaml:"topics"`
}

// ProcessingConfig содержит настройки обработки
type ProcessingConfig struct {
	BatchSize          int           `yaml:"batch_size"`
	EventTimeout       time.Duration `yaml:"event_timeout"`
	ConflictResolution string        `yaml:"conflict_resolution"`
}

// LoadConsumer загружает конфигурацию Consumer из YAML файла
func LoadConsumer(configPath string) (*ConsumerConfig, error) {
	// Читаем YAML файл
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg ConsumerConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	// Переопределяем значениями из переменных окружения
	cfg.overrideFromEnv()

	// Валидация
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return &cfg, nil
}

// overrideFromEnv переопределяет значения из переменных окружения
func (c *ConsumerConfig) overrideFromEnv() {
	// Database
	if val := os.Getenv("DB_HOST"); val != "" {
		c.Database.Host = val
	}
	if val := os.Getenv("DB_PORT"); val != "" {
		fmt.Sscanf(val, "%d", &c.Database.Port)
	}
	if val := os.Getenv("DB_DATABASE"); val != "" {
		c.Database.Database = val
	}
	if val := os.Getenv("DB_USER"); val != "" {
		c.Database.User = val
	}
	if val := os.Getenv("DB_PASSWORD"); val != "" {
		c.Database.Password = val
	}
	if val := os.Getenv("DB_SSL_MODE"); val != "" {
		c.Database.SSLMode = val
	}

	// Kafka
	if val := os.Getenv("KAFKA_BROKERS"); val != "" {
		c.Kafka.Brokers = []string{val}
	}
	if val := os.Getenv("KAFKA_SSL_ENABLED"); val == "true" || val == "Y" {
		c.Kafka.SSLEnabled = true
	}
	if val := os.Getenv("KAFKA_CONSUMER_GROUP"); val != "" {
		c.Kafka.ConsumerGroup = val
	}

	// Service
	if val := os.Getenv("CONTOUR"); val != "" {
		c.Service.Contour = val
	}
}

// validate проверяет корректность конфигурации
func (c *ConsumerConfig) validate() error {
	// Service validation
	if c.Service.Name == "" {
		return fmt.Errorf("service.name is required")
	}
	if c.Service.Contour == "" {
		return fmt.Errorf("service.contour is required")
	}

	// Database validation
	if c.Database.Host == "" {
		return fmt.Errorf("database.host is required")
	}
	if c.Database.Port <= 0 {
		return fmt.Errorf("database.port must be positive")
	}
	if c.Database.Database == "" {
		return fmt.Errorf("database.database is required")
	}
	if c.Database.User == "" {
		return fmt.Errorf("database.user is required")
	}

	// Kafka validation
	if len(c.Kafka.Brokers) == 0 {
		return fmt.Errorf("kafka.brokers is required")
	}
	if c.Kafka.ConsumerGroup == "" {
		return fmt.Errorf("kafka.consumer_group is required")
	}
	if len(c.Kafka.Topics) == 0 {
		return fmt.Errorf("kafka.topics is required")
	}

	// Processing validation
	validStrategies := map[string]bool{
		"last_write_wins": true,
		"skip":            true,
		"error":           true,
	}
	if !validStrategies[c.Processing.ConflictResolution] {
		return fmt.Errorf("invalid processing.conflict_resolution: %s", c.Processing.ConflictResolution)
	}

	// Logging validation
	validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	if !validLevels[c.Logging.Level] {
		return fmt.Errorf("invalid logging.level: %s", c.Logging.Level)
	}

	return nil
}

