package consumer

import (
	"fmt"
	"strings"

	"github.com/rs/zerolog"
	"gorm.io/gorm"
)

// EventApplier применяет события к БД
type EventApplier struct {
	db     *gorm.DB
	config Config
	logger zerolog.Logger
}

// NewEventApplier создает новый EventApplier
func NewEventApplier(db *gorm.DB, cfg Config, logger zerolog.Logger) *EventApplier {
	return &EventApplier{
		db:     db,
		config: cfg,
		logger: logger.With().Str("component", "applier").Logger(),
	}
}

// Apply применяет событие к БД
func (a *EventApplier) Apply(tx *gorm.DB, event ReplicationEvent) error {
	switch event.Operation {
	case "INSERT":
		return a.applyInsert(tx, event)
	case "UPDATE":
		return a.applyUpdate(tx, event)
	case "DELETE":
		return a.applyDelete(tx, event)
	default:
		return fmt.Errorf("unknown operation: %s", event.Operation)
	}
}

// applyInsert применяет INSERT (или UPDATE если запись уже существует)
func (a *EventApplier) applyInsert(tx *gorm.DB, event ReplicationEvent) error {
	if event.After == nil {
		return fmt.Errorf("INSERT event must have 'after' data")
	}

	tableName := event.Table
	data := event.After
	primaryKeyValue := event.GetPrimaryKeyValue()
	incomingVersion := event.GetVersion()

	a.logger.Debug().
		Str("table", tableName).
		Interface("primary_key", primaryKeyValue).
		Int64("version", incomingVersion).
		Msg("Applying INSERT")

	// Проверяем существование записи
	var existingVersion int64
	result := tx.Table(tableName).
		Select("version").
		Where("id = ?", primaryKeyValue).
		Scan(&existingVersion)

	if result.Error != nil && result.Error != gorm.ErrRecordNotFound {
		return fmt.Errorf("failed to check existing record: %w", result.Error)
	}

	// Запись уже существует - конфликт
	if result.Error == nil {
		a.logger.Warn().
			Str("table", tableName).
			Interface("primary_key", primaryKeyValue).
			Int64("existing_version", existingVersion).
			Int64("incoming_version", incomingVersion).
			Msg("INSERT conflict: record already exists")

		// Применяем conflict resolution
		return a.resolveConflict(tx, tableName, primaryKeyValue, existingVersion, incomingVersion, data)
	}

	// Запись не существует - делаем INSERT
	columns, values := a.buildInsertSQL(data)
	
	sql := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		tableName,
		strings.Join(columns, ", "),
		strings.Join(makePlaceholders(len(values)), ", "),
	)

	if err := tx.Exec(sql, values...).Error; err != nil {
		return fmt.Errorf("failed to insert: %w", err)
	}

	a.logger.Debug().
		Str("table", tableName).
		Interface("primary_key", primaryKeyValue).
		Msg("INSERT applied")

	return nil
}

// applyUpdate применяет UPDATE с проверкой версии
func (a *EventApplier) applyUpdate(tx *gorm.DB, event ReplicationEvent) error {
	if event.After == nil {
		return fmt.Errorf("UPDATE event must have 'after' data")
	}

	tableName := event.Table
	data := event.After
	primaryKeyValue := event.GetPrimaryKeyValue()
	incomingVersion := event.GetVersion()

	a.logger.Debug().
		Str("table", tableName).
		Interface("primary_key", primaryKeyValue).
		Int64("version", incomingVersion).
		Msg("Applying UPDATE")

	// Проверяем существование и версию
	var existingVersion int64
	result := tx.Table(tableName).
		Select("version").
		Where("id = ?", primaryKeyValue).
		Scan(&existingVersion)

	if result.Error == gorm.ErrRecordNotFound {
		// Запись не существует - делаем INSERT (может быть INSERT пришел позже)
		a.logger.Warn().
			Str("table", tableName).
			Interface("primary_key", primaryKeyValue).
			Msg("UPDATE on non-existing record, converting to INSERT")
		return a.applyInsert(tx, event)
	}

	if result.Error != nil {
		return fmt.Errorf("failed to check existing record: %w", result.Error)
	}

	// Проверка версии (conflict resolution)
	if existingVersion >= incomingVersion {
		return a.handleVersionConflict(tableName, primaryKeyValue, existingVersion, incomingVersion)
	}

	// Применяем UPDATE
	setClauses, values := a.buildUpdateSQL(data)
	values = append(values, primaryKeyValue) // Добавляем ID для WHERE

	sql := fmt.Sprintf("UPDATE %s SET %s WHERE id = ?",
		tableName,
		strings.Join(setClauses, ", "),
	)

	if err := tx.Exec(sql, values...).Error; err != nil {
		return fmt.Errorf("failed to update: %w", err)
	}

	a.logger.Debug().
		Str("table", tableName).
		Interface("primary_key", primaryKeyValue).
		Int64("old_version", existingVersion).
		Int64("new_version", incomingVersion).
		Msg("UPDATE applied")

	return nil
}

// applyDelete применяет DELETE
func (a *EventApplier) applyDelete(tx *gorm.DB, event ReplicationEvent) error {
	tableName := event.Table
	primaryKeyValue := event.GetPrimaryKeyValue()

	a.logger.Debug().
		Str("table", tableName).
		Interface("primary_key", primaryKeyValue).
		Msg("Applying DELETE")

	// Проверяем существование
	var existingVersion int64
	result := tx.Table(tableName).
		Select("version").
		Where("id = ?", primaryKeyValue).
		Scan(&existingVersion)

	if result.Error == gorm.ErrRecordNotFound {
		// Запись уже удалена - это нормально (идемпотентность)
		a.logger.Debug().
			Str("table", tableName).
			Interface("primary_key", primaryKeyValue).
			Msg("DELETE on non-existing record (already deleted)")
		return nil
	}

	if result.Error != nil {
		return fmt.Errorf("failed to check existing record: %w", result.Error)
	}

	// Удаляем запись
	sql := fmt.Sprintf("DELETE FROM %s WHERE id = ?", tableName)
	if err := tx.Exec(sql, primaryKeyValue).Error; err != nil {
		return fmt.Errorf("failed to delete: %w", err)
	}

	a.logger.Debug().
		Str("table", tableName).
		Interface("primary_key", primaryKeyValue).
		Msg("DELETE applied")

	return nil
}

// resolveConflict разрешает конфликт при INSERT на существующую запись
func (a *EventApplier) resolveConflict(tx *gorm.DB, tableName string, primaryKey interface{}, existingVersion, incomingVersion int64, data map[string]interface{}) error {
	switch a.config.ConflictResolution {
	case "last_write_wins":
		if incomingVersion > existingVersion {
			// Incoming версия новее - делаем UPDATE
			a.logger.Info().
				Str("table", tableName).
				Interface("primary_key", primaryKey).
				Int64("existing_version", existingVersion).
				Int64("incoming_version", incomingVersion).
				Msg("Conflict resolved: updating with newer version")

			setClauses, values := a.buildUpdateSQL(data)
			values = append(values, primaryKey)

			sql := fmt.Sprintf("UPDATE %s SET %s WHERE id = ?", tableName, strings.Join(setClauses, ", "))
			return tx.Exec(sql, values...).Error
		}
		
		// Existing версия новее или равна - пропускаем
		a.logger.Info().
			Str("table", tableName).
			Interface("primary_key", primaryKey).
			Int64("existing_version", existingVersion).
			Int64("incoming_version", incomingVersion).
			Msg("Conflict resolved: skipping older version")
		return nil

	case "skip":
		// Просто пропускаем
		a.logger.Info().
			Str("table", tableName).
			Interface("primary_key", primaryKey).
			Msg("Conflict resolved: skipping (policy=skip)")
		return nil

	case "error":
		// Возвращаем ошибку
		return fmt.Errorf("conflict: record already exists (policy=error)")

	default:
		return fmt.Errorf("unknown conflict resolution strategy: %s", a.config.ConflictResolution)
	}
}

// handleVersionConflict обрабатывает конфликт версий при UPDATE
func (a *EventApplier) handleVersionConflict(tableName string, primaryKey interface{}, existingVersion, incomingVersion int64) error {
	switch a.config.ConflictResolution {
	case "last_write_wins":
		// Existing версия новее - пропускаем
		a.logger.Info().
			Str("table", tableName).
			Interface("primary_key", primaryKey).
			Int64("existing_version", existingVersion).
			Int64("incoming_version", incomingVersion).
			Msg("Version conflict: skipping older version")
		return nil

	case "skip":
		a.logger.Info().
			Str("table", tableName).
			Interface("primary_key", primaryKey).
			Msg("Version conflict: skipping (policy=skip)")
		return nil

	case "error":
		return fmt.Errorf("version conflict: existing=%d >= incoming=%d (policy=error)", existingVersion, incomingVersion)

	default:
		return fmt.Errorf("unknown conflict resolution strategy: %s", a.config.ConflictResolution)
	}
}

// buildInsertSQL строит списки колонок и значений для INSERT
func (a *EventApplier) buildInsertSQL(data map[string]interface{}) ([]string, []interface{}) {
	columns := make([]string, 0, len(data))
	values := make([]interface{}, 0, len(data))

	for key, value := range data {
		columns = append(columns, key)
		values = append(values, value)
	}

	return columns, values
}

// buildUpdateSQL строит SET clause и значения для UPDATE
func (a *EventApplier) buildUpdateSQL(data map[string]interface{}) ([]string, []interface{}) {
	setClauses := make([]string, 0, len(data))
	values := make([]interface{}, 0, len(data))

	for key, value := range data {
		// Пропускаем id (не обновляем primary key)
		if key == "id" {
			continue
		}
		setClauses = append(setClauses, fmt.Sprintf("%s = ?", key))
		values = append(values, value)
	}

	return setClauses, values
}

// makePlaceholders создает плейсхолдеры для SQL ($1, $2, ...) или (?, ?, ...)
func makePlaceholders(count int) []string {
	placeholders := make([]string, count)
	for i := 0; i < count; i++ {
		placeholders[i] = "?"
	}
	return placeholders
}

