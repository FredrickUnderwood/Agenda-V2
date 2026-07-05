package domain

import "time"

type AuthType string

const (
	AuthTypeSSHKey   AuthType = "sshkey"
	AuthTypePassword AuthType = "password"
)

// Machine is a DB-managed SSH deploy target. MachineType constrains which env
// a target may bind to it (e.g. a prod target must use a prod machine).
type Machine struct {
	ID            int64       `json:"id"                     gorm:"primaryKey;autoIncrement"`
	Name          string      `json:"name"                   gorm:"uniqueIndex;size:128;not null"`
	MachineType   Environment `json:"machine_type"           gorm:"index;size:16;not null;default:prod"`
	Host          string      `json:"host"                   gorm:"size:255;not null"`
	Port          int         `json:"port"                   gorm:"not null;default:22"`
	User          string      `json:"user"                   gorm:"size:64;not null;default:''"`
	AuthType      AuthType    `json:"auth_type"              gorm:"size:16;not null;default:sshkey"`
	SSHKeyPath    string      `json:"ssh_key_path,omitempty" gorm:"size:512;not null;default:''"`
	Password      string      `json:"-"                      gorm:"size:255;not null;default:''"`
	WorkspaceRoot string      `json:"workspace_root"         gorm:"size:512;not null;default:''"`
	CreatedAt     time.Time   `json:"created_at"`
	UpdatedAt     time.Time   `json:"updated_at"`
}

func (Machine) TableName() string { return "machine" }
