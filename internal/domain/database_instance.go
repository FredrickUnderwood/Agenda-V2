package domain

import "time"

// DatabaseEngine identifies the wire protocol agenda-node speaks to a
// registered database.
type DatabaseEngine string

const (
	DatabaseEngineMySQL DatabaseEngine = "mysql"
	DatabaseEngineRedis DatabaseEngine = "redis"
)

func (e DatabaseEngine) Valid() bool {
	return e == DatabaseEngineMySQL || e == DatabaseEngineRedis
}

func DefaultDatabaseEngine(e DatabaseEngine) DatabaseEngine {
	if e == "" {
		return DatabaseEngineMySQL
	}
	return e
}

// DefaultPortForEngine is the port to register when the operator did not give
// one. Each engine has exactly one conventional port, so asking for it would be
// asking a question whose answer is already known.
func DefaultPortForEngine(e DatabaseEngine) int {
	if e == DatabaseEngineRedis {
		return 6379
	}
	return 3306
}

// DatabaseInstance is a database registered for read-only querying from the
// console.
//
// It deliberately has no host field: the database must live on the bound
// Machine, and agenda-node reaches it over that machine's own loopback or
// container bridge (its configured proxy_backend_host). Nothing has to publish
// the database port to the network — that is the entire point of relaying
// queries through the node instead of connecting from the control plane, and
// it mirrors the invariant logs/metrics/health already follow.
//
// MachineID must therefore reference an agent-mode machine: an SSH machine has
// no resident agenda-node to relay through.
type DatabaseInstance struct {
	ID        int64          `json:"id"         gorm:"primaryKey;autoIncrement"`
	Name      string         `json:"name"       gorm:"uniqueIndex;size:128;not null"`
	Engine    DatabaseEngine `json:"engine"     gorm:"size:16;not null;default:mysql"`
	MachineID int64          `json:"machine_id" gorm:"index;not null;default:0"`
	Port      int            `json:"port"       gorm:"not null;default:3306"`
	Username  string         `json:"username"   gorm:"size:128;not null;default:''"`

	// Password is encrypted at rest with secret.Box exactly like
	// Machine.AgentToken, and is never serialized — the plaintext only ever
	// exists in memory on its way to the node.
	Password string `json:"-" gorm:"size:512;not null;default:''"`

	// DefaultDatabase is the schema a MySQL query opens with when the console
	// does not name one. For Redis it holds the default numeric DB index
	// ("0"..) as text — one column serving both because it answers the same
	// question, "which namespace does a statement land in by default".
	DefaultDatabase string `json:"default_database" gorm:"size:128;not null;default:''"`

	// Env drives who may query this instance (service.AuthorizeQuery): prod and
	// stage are admin-only, test is open to any authenticated user. It is the
	// extension point for a finer-grained ACL later.
	Env         Environment `json:"env"         gorm:"index;size:16;not null;default:prod"`
	Description string      `json:"description" gorm:"size:512;not null;default:''"`

	// Enabled carries NO gorm default tag on purpose. GORM omits a zero-valued
	// field from the INSERT when the field declares a default, so `default:true`
	// would make an explicitly-disabled instance silently come back enabled —
	// the same trap that broke gateway health gating. Without the tag the value
	// is always written, and the service supplies the "new instances are
	// enabled" default instead.
	Enabled bool `json:"enabled" gorm:"not null"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (DatabaseInstance) TableName() string { return "database_instance" }
