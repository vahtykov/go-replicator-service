package consumer

import (
	"encoding/json"
	"time"
)

// ReplicationEvent представляет событие репликации из Kafka
type ReplicationEvent struct {
	EventID    string                 `json:"event_id"`
	Timestamp  time.Time              `json:"timestamp"`
	Source     SourceInfo             `json:"source"`
	Table      string                 `json:"table"`
	Operation  string                 `json:"operation"` // INSERT, UPDATE, DELETE
	PrimaryKey map[string]interface{} `json:"primary_key"`
	Before     map[string]interface{} `json:"before,omitempty"`
	After      map[string]interface{} `json:"after,omitempty"`
}

// SourceInfo содержит информацию об источнике события
type SourceInfo struct {
	Contour  string `json:"contour"`
	Database string `json:"database"`
}

// FromJSON десериализует событие из JSON
func (e *ReplicationEvent) FromJSON(data []byte) error {
	return json.Unmarshal(data, e)
}

// GetPrimaryKeyValue возвращает значение primary key (предполагается id)
func (e *ReplicationEvent) GetPrimaryKeyValue() interface{} {
	if id, ok := e.PrimaryKey["id"]; ok {
		return id
	}
	return nil
}

// GetVersion возвращает версию записи из After или Before
func (e *ReplicationEvent) GetVersion() int64 {
	var data map[string]interface{}
	
	if e.After != nil {
		data = e.After
	} else if e.Before != nil {
		data = e.Before
	}
	
	if data == nil {
		return 0
	}
	
	if version, ok := data["version"]; ok {
		// Может быть float64 (из JSON) или int64
		switch v := version.(type) {
		case float64:
			return int64(v)
		case int64:
			return v
		case int:
			return int64(v)
		}
	}
	
	return 0
}

