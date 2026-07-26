package domain

import "time"

// Notification is one entry in the platform's shared in-app inbox.
// It is a single, team-shared list — there is no per-user scoping or
// per-user read-state anywhere else in this domain model, so IsRead is a
// plain shared flag: whoever reads a notification marks it read for
// everyone, matching this platform's small-team, no-per-user-preferences
// scale (see Application/Machine/Setting, all global resources).
//
// AlertService writes one of these for every alert it sends (in addition to
// whatever external webhook channels are configured), so the inbox is a
// durable record even if a Feishu/DingTalk webhook silently failed.
type Notification struct {
	ID        int64     `json:"id"         gorm:"primaryKey;autoIncrement"`
	Title     string    `json:"title"      gorm:"size:255;not null"`
	Content   string    `json:"content"    gorm:"type:text;not null"`
	Level     string    `json:"level"      gorm:"size:16;not null;default:info"`
	IsRead    bool      `json:"is_read"    gorm:"index;not null;default:false"`
	CreatedAt time.Time `json:"created_at" gorm:"index"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (Notification) TableName() string { return "notification" }
