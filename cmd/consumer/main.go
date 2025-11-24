package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vahtykov/go-replicator-service/internal/config"
	"github.com/vahtykov/go-replicator-service/internal/consumer"
	"github.com/vahtykov/go-replicator-service/internal/database"
	"github.com/vahtykov/go-replicator-service/internal/kafka"
	"github.com/vahtykov/go-replicator-service/internal/logger"
)

var (
	configPath = flag.String("config", "config.consumer.yaml", "Path to configuration file")
	version    = "1.0.0"
)

func main() {
	flag.Parse()

	// Загружаем конфигурацию
	cfg, err := config.LoadConsumer(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Инициализируем логгер
	log := logger.New(logger.Config{
		Level:  cfg.Logging.Level,
		Format: cfg.Logging.Format,
		Color:  cfg.Logging.Color,
	})

	log.Info().
		Str("version", version).
		Str("service", cfg.Service.Name).
		Str("contour", cfg.Service.Contour).
		Msg("Starting ReplicatorConsumer")

	// Подключаемся к PostgreSQL
	// ВАЖНО: Устанавливаем application_name для защиты от петли репликации
	db, err := database.Connect(database.Config{
		Host:            cfg.Database.Host,
		Port:            cfg.Database.Port,
		Database:        cfg.Database.Database,
		User:            cfg.Database.User,
		Password:        cfg.Database.Password,
		SSLMode:         cfg.Database.SSLMode,
		MaxOpenConns:    cfg.Database.MaxOpenConns,
		MaxIdleConns:    cfg.Database.MaxIdleConns,
		ConnMaxLifetime: cfg.Database.ConnMaxLifetime,
		LogQueries:      cfg.Database.LogQueries,
		ApplicationName: cfg.Database.ApplicationName, // ← Критично!
	}, log)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to connect to database")
	}

	log.Info().
		Str("application_name", cfg.Database.ApplicationName).
		Msg("Database connection established with application_name")

	// Создаем Kafka consumer
	kafkaConsumer, err := kafka.NewConsumer(kafka.ConsumerConfig{
		Brokers:           cfg.Kafka.Brokers,
		SSLEnabled:        cfg.Kafka.SSLEnabled,
		SSLCACert:         cfg.Kafka.SSLCACert,
		SSLClientCert:     cfg.Kafka.SSLClientCert,
		SSLClientKey:      cfg.Kafka.SSLClientKey,
		ConsumerGroup:     cfg.Kafka.ConsumerGroup,
		AutoOffsetReset:   cfg.Kafka.AutoOffsetReset,
		EnableAutoCommit:  cfg.Kafka.EnableAutoCommit,
		SessionTimeoutMs:  cfg.Kafka.SessionTimeoutMs,
		MaxPollIntervalMs: cfg.Kafka.MaxPollIntervalMs,
		Topics:            cfg.Kafka.Topics,
	}, log)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create Kafka consumer")
	}
	defer kafkaConsumer.Close()

	// Создаем Consumer
	cons := consumer.New(db, kafkaConsumer, consumer.Config{
		MyContour:          cfg.Service.Contour,
		Database:           cfg.Database.Database,
		BatchSize:          cfg.Processing.BatchSize,
		EventTimeout:       cfg.Processing.EventTimeout,
		ConflictResolution: cfg.Processing.ConflictResolution,
	}, log)

	// Контекст с graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Канал для сигналов остановки
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Запускаем Consumer в отдельной горутине
	errChan := make(chan error, 1)
	go func() {
		if err := cons.Start(ctx); err != nil && err != context.Canceled {
			errChan <- err
		}
	}()

	// Ждем сигнал остановки или ошибку
	select {
	case sig := <-sigChan:
		log.Info().
			Str("signal", sig.String()).
			Msg("Received shutdown signal")
		
		// Graceful shutdown
		cancel()
		
		// Даем время на завершение текущей обработки
		time.Sleep(2 * time.Second)
		
		// Выводим метрики
		processed, skipped, failed := cons.GetMetrics()
		log.Info().
			Int64("processed", processed).
			Int64("skipped", skipped).
			Int64("failed", failed).
			Msg("Consumer metrics")
		
		log.Info().Msg("ReplicatorConsumer stopped gracefully")
		
	case err := <-errChan:
		log.Fatal().Err(err).Msg("Consumer failed")
	}
}

