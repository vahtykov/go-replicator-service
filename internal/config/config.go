package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config представляет конфигурацию приложения
type Config struct {
	Service  ServiceConfig  `yaml:"service"`
	Database DatabaseConfig `yaml:"database"`
	Kafka    KafkaConfig    `yaml:"kafka"`
	Logging  LoggingConfig  `yaml:"logging"`
}

// ServiceConfig содержит настройки сервиса
type ServiceConfig struct {
	Name         string        `yaml:"name"`
	Contour      string        `yaml:"contour"`
	PollInterval time.Duration `yaml:"poll_interval"`
	BatchSize    int           `yaml:"batch_size"`
}

// DatabaseConfig содержит настройки подключения к PostgreSQL
type DatabaseConfig struct {
	Host            string        `yaml:"host"`
	Port            int           `yaml:"port"`
	Database        string        `yaml:"database"`
	User            string        `yaml:"user"`
	Password        string        `yaml:"password"`
	SSLMode         string        `yaml:"ssl_mode"`
	MaxOpenConns    int           `yaml:"max_open_conns"`
	MaxIdleConns    int           `yaml:"max_idle_conns"`
	ConnMaxLifetime time.Duration `yaml:"conn_max_lifetime"`
	LogQueries      bool          `yaml:"log_queries"`
	ApplicationName string        `yaml:"application_name"` // Для защиты от петли репликации
}

// KafkaConfig содержит настройки Kafka
type KafkaConfig struct {
	Brokers       []string `yaml:"brokers"`
	SSLEnabled    bool     `yaml:"ssl_enabled"`
	SSLCACert     string   `yaml:"ssl_ca_cert"`
	SSLClientCert string   `yaml:"ssl_client_cert"`
	SSLClientKey  string   `yaml:"ssl_client_key"`
	
	// Producer настройки
	Acks         string `yaml:"acks"`
	Compression  string `yaml:"compression"`
	MaxInFlight  int    `yaml:"max_in_flight"`
	BatchSize    int    `yaml:"batch_size"`
	LingerMs     int    `yaml:"linger_ms"`
}

// LoggingConfig содержит настройки логирования
type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
	Color  bool   `yaml:"color"`
}

// Load загружает конфигурацию из YAML файла с поддержкой переменных окружения
func Load(configPath string) (*Config, error) {
	// Читаем YAML файл
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
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
func (c *Config) overrideFromEnv() {
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
		// Поддержка comma-separated списка
		c.Kafka.Brokers = []string{val}
	}
	if val := os.Getenv("KAFKA_SSL_ENABLED"); val == "true" || val == "Y" {
		c.Kafka.SSLEnabled = true
	}
	if val := os.Getenv("KAFKA_SSL_CA_CERT"); val != "" {
		c.Kafka.SSLCACert = val
	}
	if val := os.Getenv("KAFKA_SSL_CLIENT_CERT"); val != "" {
		c.Kafka.SSLClientCert = val
	}
	if val := os.Getenv("KAFKA_SSL_CLIENT_KEY"); val != "" {
		c.Kafka.SSLClientKey = val
	}

	// Service
	if val := os.Getenv("CONTOUR"); val != "" {
		c.Service.Contour = val
	}
}

// validate проверяет корректность конфигурации
func (c *Config) validate() error {
	// Service validation
	if c.Service.Name == "" {
		return fmt.Errorf("service.name is required")
	}
	if c.Service.Contour == "" {
		return fmt.Errorf("service.contour is required")
	}
	if c.Service.BatchSize <= 0 {
		return fmt.Errorf("service.batch_size must be positive")
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

	// Logging validation
	validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	if !validLevels[c.Logging.Level] {
		return fmt.Errorf("invalid logging.level: %s", c.Logging.Level)
	}

	return nil
}

