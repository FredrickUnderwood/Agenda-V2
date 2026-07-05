package domain

import "time"

// User is a built-in platform account (replaces the external user-core identity).
// Role drives permissions via internal/auth. PasswordHash is a bcrypt hash and is
// never serialized. Table name `user` is a MySQL reserved word, so it is always
// backtick-quoted in DDL (see 0003_user.sql); GORM quotes it in generated SQL.
type User struct {
	ID           int64     `json:"id"           gorm:"primaryKey;autoIncrement"`
	Username     string    `json:"username"     gorm:"uniqueIndex;size:64;not null"`
	PasswordHash string    `json:"-"            gorm:"size:255;not null"`
	DisplayName  string    `json:"display_name" gorm:"size:128;not null;default:''"`
	Role         string    `json:"role"         gorm:"size:16;not null;default:member"`
	IsActive     bool      `json:"is_active"    gorm:"not null;default:true"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (User) TableName() string { return "user" }
