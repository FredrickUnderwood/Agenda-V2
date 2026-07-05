package domain

import "time"

type AuthType string

const (
	AuthTypeSSHKey   AuthType = "sshkey"
	AuthTypePassword AuthType = "password"
)

// MachineMode selects how the control plane reaches a machine: classic SSH, or
// via a resident agenda-node agent. It is a per-machine switch — existing SSH
// machines are unaffected; a machine is migrated to agent mode individually.
type MachineMode string

const (
	MachineModeSSH   MachineMode = "ssh"
	MachineModeAgent MachineMode = "agent"
)

// agentOnlineFactor is how many heartbeat intervals may elapse before a machine
// is considered offline (window = factor × interval), giving slack for a single
// missed heartbeat from a network blip.
const agentOnlineFactor = 3

// Machine is a DB-managed deploy target. MachineType constrains which env a
// target may bind to it (e.g. a prod target must use a prod machine). Mode
// selects SSH vs agenda-node agent execution.
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

	// agenda-node agent fields (Mode == agent). AgentBaseURL is the node's
	// management endpoint (jobs/proxy registration); AgentProxyBaseURL is its
	// reverse-proxy endpoint that gateway backends point at; AgentToken is the
	// shared per-machine secret (never serialized, like Password).
	Mode                 MachineMode `json:"mode"                    gorm:"size:16;not null;default:ssh"`
	AgentBaseURL         string      `json:"agent_base_url"          gorm:"size:255;not null;default:''"`
	AgentProxyBaseURL    string      `json:"agent_proxy_base_url"    gorm:"size:255;not null;default:''"`
	AgentToken           string      `json:"-"                       gorm:"size:255;not null;default:''"`
	AgentLastHeartbeatAt *time.Time  `json:"agent_last_heartbeat_at"`
	AgentVersion         string      `json:"agent_version"           gorm:"size:32;not null;default:''"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (Machine) TableName() string { return "machine" }

// Online reports whether an agent-mode machine has sent a heartbeat recently
// enough (within agentOnlineFactor × heartbeatInterval). SSH machines have no
// heartbeat concept and always report false here (their reachability is tested
// on demand via TestConnection).
func (m *Machine) Online(heartbeatInterval time.Duration) bool {
	if m == nil || m.Mode != MachineModeAgent || m.AgentLastHeartbeatAt == nil {
		return false
	}
	if heartbeatInterval <= 0 {
		heartbeatInterval = 15 * time.Second
	}
	return time.Since(*m.AgentLastHeartbeatAt) < agentOnlineFactor*heartbeatInterval
}
