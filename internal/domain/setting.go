package domain

import "time"

type SettingType string

const (
	SettingTypeString SettingType = "string"
	SettingTypeInt    SettingType = "int"
	SettingTypeBool   SettingType = "bool"
	SettingTypeJSON   SettingType = "json"
)

// Setting is a runtime-mutable configuration value stored in the DB, as opposed
// to bootstrap config that lives in agenda-v2.yaml (DB DSN, listen addr, the
// encryption master key). Anything that rotates or that an operator should be
// able to change without restarting the process — e.g. a GitHub token — belongs
// here.
//
// Secret values (IsSecret) are stored encrypted at rest: Value holds an
// "enc:v1:..." blob when security.master_key is configured. The plaintext only
// ever lives in the SettingService's in-memory cache, never in the DB and never
// in an API response.
//
// The Key column is named setting_key to avoid the MySQL reserved word "key".
type Setting struct {
	ID        int64       `json:"id"         gorm:"primaryKey;autoIncrement"`
	Key       string      `json:"key"        gorm:"column:setting_key;uniqueIndex;size:191;not null"`
	Value     string      `json:"value"      gorm:"type:text;not null"`
	Type      SettingType `json:"type"       gorm:"size:16;not null;default:string"`
	IsSecret  bool        `json:"is_secret"  gorm:"not null;default:false"`
	UpdatedBy int64       `json:"updated_by" gorm:"not null;default:0"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
}

func (Setting) TableName() string { return "setting" }
