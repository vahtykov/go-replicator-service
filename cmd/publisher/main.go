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
	"github.com/vahtykov/go-replicator-service/internal/database"
	"github.com/vahtykov/go-replicator-service/internal/kafka"
	"github.com/vahtykov/go-replicator-service/internal/logger"
	"github.com/vahtykov/go-replicator-service/internal/publisher"
)

var (
	configPath = flag.String("config", "config.publisher.yaml", "Path to configuration file")
	version    = "1.0.0"
)

func main() {
	flag.Parse()

	// Загружаем конфигурацию
	cfg, err := config.Load(*configPath)
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
		Msg("Starting ReplicatorPublisher")

	// Подключаемся к PostgreSQL
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
	}, log)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to connect to database")
	}

	// Создаем Kafka producer
	kafkaProducer, err := kafka.NewProducer(kafka.ProducerConfig{
		Brokers:       cfg.Kafka.Brokers,
		SSLEnabled:    cfg.Kafka.SSLEnabled,
		SSLCACert:     cfg.Kafka.SSLCACert,
		SSLClientCert: cfg.Kafka.SSLClientCert,
		SSLClientKey:  cfg.Kafka.SSLClientKey,
		Acks:          cfg.Kafka.Acks,
		Compression:   cfg.Kafka.Compression,
		MaxInFlight:   cfg.Kafka.MaxInFlight,
		BatchSize:     cfg.Kafka.BatchSize,
		LingerMs:      cfg.Kafka.LingerMs,
	}, log)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create Kafka producer")
	}
	defer kafkaProducer.Close()

	// Создаем Publisher
	pub := publisher.New(db, kafkaProducer, publisher.Config{
		Contour:      cfg.Service.Contour,
		Database:     cfg.Database.Database,
		PollInterval: cfg.Service.PollInterval,
		BatchSize:    cfg.Service.BatchSize,
	}, log)

	// Контекст с graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Канал для сигналов остановки
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Запускаем Publisher в отдельной горутине
	errChan := make(chan error, 1)
	go func() {
		if err := pub.Start(ctx); err != nil && err != context.Canceled {
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
		processed, failed := pub.GetMetrics()
		log.Info().
			Int64("processed", processed).
			Int64("failed", failed).
			Msg("Publisher metrics")
		
		log.Info().Msg("ReplicatorPublisher stopped gracefully")
		
	case err := <-errChan:
		log.Fatal().Err(err).Msg("Publisher failed")
	}
}

