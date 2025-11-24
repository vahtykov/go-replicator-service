package database

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"time"
)

// ReplicationQueue представляет запись в таблице replication_queue
type ReplicationQueue struct {
	ID              int64      `gorm:"column:id;primaryKey;autoIncrement"`
	Table           string     `gorm:"column:table_name;type:varchar(255);not null"`
	Operation       string     `gorm:"column:operation;type:varchar(10);not null"` // INSERT, UPDATE, DELETE
	RecordData      JSONB      `gorm:"column:record_data;type:jsonb;not null"`
	PrimaryKeyValue string     `gorm:"column:primary_key_value;type:varchar(255)"` // Для partition key в Kafka
	CreatedAt       time.Time  `gorm:"column:created_at;type:timestamptz;default:now()"`
	Published       bool       `gorm:"column:published;type:boolean;default:false"`
	PublishedAt     *time.Time `gorm:"column:published_at;type:timestamptz"`
}

// TableName возвращает имя таблицы для GORM
func (ReplicationQueue) TableName() string {
	return "replication_queue"
}

// ProcessedEvent представляет запись в таблице processed_events
type ProcessedEvent struct {
	EventID     string    `gorm:"column:event_id;primaryKey;type:varchar(255)"`
	ProcessedAt time.Time `gorm:"column:processed_at;type:timestamptz;default:now()"`
}

// TableName возвращает имя таблицы для GORM
func (ProcessedEvent) TableName() string {
	return "processed_events"
}

// JSONB представляет PostgreSQL JSONB тип
type JSONB map[string]interface{}

// Value реализует driver.Valuer для JSONB
func (j JSONB) Value() (driver.Value, error) {
	if j == nil {
		return nil, nil
	}
	return json.Marshal(j)
}

// Scan реализует sql.Scanner для JSONB
func (j *JSONB) Scan(value interface{}) error {
	if value == nil {
		*j = nil
		return nil
	}

	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("failed to unmarshal JSONB value")
	}

	var result map[string]interface{}
	if err := json.Unmarshal(bytes, &result); err != nil {
		return err
	}

	*j = result
	return nil
}

// MarshalJSON реализует json.Marshaler
func (j JSONB) MarshalJSON() ([]byte, error) {
	if j == nil {
		return []byte("null"), nil
	}
	return json.Marshal(map[string]interface{}(j))
}

// UnmarshalJSON реализует json.Unmarshaler
func (j *JSONB) UnmarshalJSON(data []byte) error {
	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		return err
	}
	*j = result
	return nil
}

