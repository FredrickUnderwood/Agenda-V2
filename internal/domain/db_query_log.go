package domain

import "time"

// DBQueryLog is one audit record for a console SQL query. Both successful and
// failed attempts are recorded.
//
// ResultSnapshot holds a capped JSON copy of the result set so a user can
// revisit what a past query returned without re-running it. That means real
// database rows land in the control-plane database, so the snapshot is
// encrypted at rest with secret.Box (like DatabaseInstance.Password) and rows
// are purged after a configurable retention window.
//
// InstanceName and Env are denormalized copies: an audit trail has to stay
// readable after the instance it refers to is deleted.
type DBQueryLog struct {
	ID           int64       `json:"id"            gorm:"primaryKey;autoIncrement"`
	InstanceID   int64       `json:"instance_id"   gorm:"index:idx_instance_created,priority:1;not null;default:0"`
	InstanceName string      `json:"instance_name" gorm:"size:128;not null;default:''"`
	Env          Environment `json:"env"           gorm:"size:16;not null;default:''"`

	// UserID is 0 for the static service token and for dev mode (no auth
	// configured); Username then carries "service" / "dev".
	UserID   int64  `json:"user_id"  gorm:"index:idx_user_created,priority:1;not null;default:0"`
	Username string `json:"username" gorm:"size:128;not null;default:''"`

	DatabaseName string `json:"database_name" gorm:"size:128;not null;default:''"`
	Statement    string `json:"statement"     gorm:"type:text;not null"`

	// ResultSnapshot is an "enc:v1:..." blob wrapping the JSON snapshot; it is
	// decrypted by the service on read and never serialized from the model.
	ResultSnapshot  string `json:"-"                gorm:"type:mediumtext;not null"`
	ResultTruncated bool   `json:"result_truncated" gorm:"not null"`

	RowCount   int    `json:"row_count"   gorm:"not null;default:0"`
	DurationMS int    `json:"duration_ms" gorm:"not null;default:0"`
	Success    bool   `json:"success"     gorm:"not null"`
	Error      string `json:"error"       gorm:"size:1024;not null;default:''"`

	CreatedAt time.Time `json:"created_at" gorm:"index:idx_user_created,priority:2;index:idx_instance_created,priority:2;index:idx_created"`
}

func (DBQueryLog) TableName() string { return "db_query_log" }
