package publisher

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ReplicationEvent представляет событие репликации в формате Kafka
type ReplicationEvent struct {
	EventID   string                 `json:"event_id"`
	Timestamp time.Time              `json:"timestamp"`
	Source    SourceInfo             `json:"source"`
	Table     string                 `json:"table"`
	Operation string                 `json:"operation"` // INSERT, UPDATE, DELETE
	PrimaryKey map[string]interface{} `json:"primary_key"`
	Before    map[string]interface{} `json:"before,omitempty"`
	After     map[string]interface{} `json:"after,omitempty"`
}

// SourceInfo содержит информацию об источнике события
type SourceInfo struct {
	Contour  string `json:"contour"`
	Database string `json:"database"`
}

// NewReplicationEvent создает новое событие репликации
func NewReplicationEvent(
	contour string,
	database string,
	tableName string,
	operation string,
	recordData map[string]interface{},
) *ReplicationEvent {
	event := &ReplicationEvent{
		EventID:   uuid.New().String(),
		Timestamp: time.Now().UTC(),
		Source: SourceInfo{
			Contour:  contour,
			Database: database,
		},
		Table:     tableName,
		Operation: operation,
		PrimaryKey: make(map[string]interface{}),
		Before:    make(map[string]interface{}),
		After:     make(map[string]interface{}),
	}

	// Извлекаем primary key
	if id, ok := recordData["id"]; ok {
		event.PrimaryKey["id"] = id
	}

	// Заполняем Before/After в зависимости от операции
	switch operation {
	case "INSERT":
		event.After = recordData
		event.Before = nil
	case "UPDATE":
		event.After = recordData
		// Для UPDATE в триггере нет OLD данных (это можно улучшить)
		event.Before = nil
	case "DELETE":
		event.Before = recordData
		event.After = nil
	}

	return event
}

// ToJSON сериализует событие в JSON
func (e *ReplicationEvent) ToJSON() ([]byte, error) {
	return json.Marshal(e)
}

// ExtractPartitionKey извлекает ключ для партиционирования Kafka
func (e *ReplicationEvent) ExtractPartitionKey() []byte {
	// Используем primary key как partition key
	if id, ok := e.PrimaryKey["id"]; ok {
		// Конвертируем id в строку
		return []byte(fmt.Sprintf("%v", id))
	}
	// Fallback на event_id
	return []byte(e.EventID)
}

