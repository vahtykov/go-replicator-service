package logger

import (
	"io"
	"os"
	"strings"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// Config представляет конфигурацию логгера
type Config struct {
	Level  string
	Format string // "json" или "console"
	Color  bool
}

// New создает и настраивает новый логгер
func New(cfg Config) zerolog.Logger {
	// Определяем уровень логирования
	level := parseLevel(cfg.Level)
	zerolog.SetGlobalLevel(level)

	// Выбираем writer
	var writer io.Writer = os.Stdout

	// Форматирование
	if cfg.Format == "console" {
		writer = zerolog.ConsoleWriter{
			Out:        os.Stdout,
			TimeFormat: "2006-01-02 15:04:05",
			NoColor:    !cfg.Color,
		}
	}

	// Создаем логгер
	logger := zerolog.New(writer).With().
		Timestamp().
		Caller().
		Logger()

	// Устанавливаем как глобальный
	log.Logger = logger

	return logger
}

// parseLevel парсит уровень логирования из строки
func parseLevel(level string) zerolog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return zerolog.DebugLevel
	case "info":
		return zerolog.InfoLevel
	case "warn", "warning":
		return zerolog.WarnLevel
	case "error":
		return zerolog.ErrorLevel
	case "fatal":
		return zerolog.FatalLevel
	case "panic":
		return zerolog.PanicLevel
	default:
		return zerolog.InfoLevel
	}
}

