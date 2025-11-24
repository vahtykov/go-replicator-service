package database

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Config представляет конфигурацию подключения к БД
type Config struct {
	Host            string
	Port            int
	Database        string
	User            string
	Password        string
	SSLMode         string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	LogQueries      bool
	ApplicationName string // Для защиты от петли репликации
}

// Connect устанавливает соединение с PostgreSQL через GORM
func Connect(cfg Config, log zerolog.Logger) (*gorm.DB, error) {
	// Формируем DSN
	dsn := fmt.Sprintf(
		"host=%s port=%d dbname=%s user=%s password=%s sslmode=%s",
		cfg.Host, cfg.Port, cfg.Database, cfg.User, cfg.Password, cfg.SSLMode,
	)
	
	// Добавляем application_name если указан
	if cfg.ApplicationName != "" {
		dsn += fmt.Sprintf(" application_name=%s", cfg.ApplicationName)
		log.Info().Str("application_name", cfg.ApplicationName).Msg("Using custom application_name")
	}

	// Настройка GORM logger
	var gormLogger logger.Interface
	if cfg.LogQueries {
		gormLogger = &GormZerologLogger{
			Logger: log,
			SlowThreshold: 200 * time.Millisecond,
		}
	} else {
		gormLogger = logger.Default.LogMode(logger.Silent)
	}

	// GORM конфигурация
	gormConfig := &gorm.Config{
		Logger: gormLogger,
		PrepareStmt: false, // Отключаем prepared statements как требуется
		NowFunc: func() time.Time {
			return time.Now().UTC()
		},
	}

	// Открываем соединение
	db, err := gorm.Open(postgres.Open(dsn), gormConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	// Получаем *sql.DB для настройки connection pool
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get sql.DB: %w", err)
	}

	// Настройка connection pool
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)

	// Проверка соединения
	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	log.Info().
		Str("host", cfg.Host).
		Int("port", cfg.Port).
		Str("database", cfg.Database).
		Str("ssl_mode", cfg.SSLMode).
		Msg("Successfully connected to PostgreSQL")

	return db, nil
}

// GormZerologLogger - адаптер для логирования GORM запросов через zerolog
type GormZerologLogger struct {
	Logger        zerolog.Logger
	SlowThreshold time.Duration
}

// LogMode реализует logger.Interface
func (l *GormZerologLogger) LogMode(level logger.LogLevel) logger.Interface {
	return l
}

// Info реализует logger.Interface
func (l *GormZerologLogger) Info(ctx context.Context, msg string, data ...interface{}) {
	l.Logger.Info().Msgf(msg, data...)
}

// Warn реализует logger.Interface
func (l *GormZerologLogger) Warn(ctx context.Context, msg string, data ...interface{}) {
	l.Logger.Warn().Msgf(msg, data...)
}

// Error реализует logger.Interface
func (l *GormZerologLogger) Error(ctx context.Context, msg string, data ...interface{}) {
	l.Logger.Error().Msgf(msg, data...)
}

// Trace реализует logger.Interface для логирования SQL запросов
func (l *GormZerologLogger) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	elapsed := time.Since(begin)
	sql, rows := fc()

	event := l.Logger.Debug()

	if err != nil {
		event = l.Logger.Error().Err(err)
	} else if elapsed > l.SlowThreshold {
		event = l.Logger.Warn().Dur("slow_query_threshold", l.SlowThreshold)
	}

	event.
		Dur("duration_ms", elapsed).
		Int64("rows", rows).
		Str("sql", sql).
		Msg("SQL Query")
}

