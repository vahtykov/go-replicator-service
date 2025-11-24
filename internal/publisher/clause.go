package publisher

import (
	"gorm.io/gorm/clause"
)

// ForUpdateSkipLocked создает GORM clause для SELECT ... FOR UPDATE SKIP LOCKED
func ForUpdateSkipLocked() clause.Expression {
	return clause.Locking{
		Strength: "UPDATE",
		Options:  "SKIP LOCKED",
	}
}

