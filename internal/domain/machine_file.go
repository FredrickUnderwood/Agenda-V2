package domain

import "time"

// FileScope says what a MachineFile is for, which decides both where it lands
// and who is expected to consume it.
type FileScope string

const (
	// FileScopeAppEnv is a file uploaded for an (application, environment). The
	// platform picks its path — <workspace_root>/run/<app>/<env>/.files/<name>
	// on every machine hosting that environment — and bind-mounts the directory
	// read-only into every container at contract.AgendaContainerFilesDir. This
	// is the scope credentials belong in: one upload covers every instance of
	// the environment, including a blue/green pair.
	FileScopeAppEnv FileScope = "app_env"
	// FileScopeMachine is a raw upload to an operator-chosen absolute path on
	// one machine. Nothing mounts it anywhere; it exists for one-off files that
	// are not part of an application's environment.
	FileScopeMachine FileScope = "machine"
)

func (s FileScope) Valid() bool {
	return s == FileScopeAppEnv || s == FileScopeMachine
}

// FileVerifyStatus is the outcome of re-checking an uploaded file against the
// checksum recorded when it was written.
//
// The four values are deliberately not collapsed into a boolean: "the node is
// unreachable so we don't know" is a fundamentally different answer from "we
// looked and the file is gone", and reporting the first as the second would
// turn every node restart into a false alarm about missing credentials.
type FileVerifyStatus string

const (
	FileVerifyPending     FileVerifyStatus = ""
	FileVerifyOK          FileVerifyStatus = "ok"
	FileVerifyChanged     FileVerifyStatus = "changed"
	FileVerifyMissing     FileVerifyStatus = "missing"
	FileVerifyUnreachable FileVerifyStatus = "unreachable"
)

// MachineFile is one upload of one file to one machine.
//
// Every upload appends a row; rows are never rewritten in place except to
// record a verification result. The current state of a path is therefore the
// newest row for that (machine_id, path), and everything older is the history
// of what used to be there — which is the question "has this credential been
// rotated, and by whom" needs answered.
//
// The file's *contents* are deliberately not stored here. The control plane
// holds only the metadata needed to find the file again and to prove it has not
// changed; a copy of every production credential in the platform database would
// be a larger liability than the convenience is worth. The cost of that choice
// is that the platform cannot re-push a file to a machine that never received
// one — which is exactly what LastVerifyStatus and the deploy-time check exist
// to make visible.
type MachineFile struct {
	ID    int64     `json:"id"    gorm:"primaryKey;autoIncrement"`
	Scope FileScope `json:"scope" gorm:"size:16;not null;default:'app_env'"`

	// ApplicationID/Env are set for FileScopeAppEnv only. AppName is a
	// denormalized copy: an audit row has to stay readable after the
	// application it referred to is deleted, the same reason DBQueryLog keeps
	// its own instance name.
	ApplicationID int64       `json:"application_id" gorm:"index:idx_machine_file_app_env,priority:1;not null;default:0"`
	AppName       string      `json:"app_name"       gorm:"size:128;not null;default:''"`
	Env           Environment `json:"env"            gorm:"index:idx_machine_file_app_env,priority:2;size:16;not null;default:''"`

	MachineID   int64  `json:"machine_id"   gorm:"index:idx_machine_file_machine_path,priority:1;not null;default:0"`
	MachineName string `json:"machine_name" gorm:"size:128;not null;default:''"`

	// Path is the absolute path on the machine; FileName is its base name (for
	// app_env scope, the name the operator chose and the name the container
	// sees under contract.AgendaContainerFilesDir).
	Path     string `json:"path"      gorm:"size:1024;not null"`
	FileName string `json:"file_name" gorm:"size:255;not null;default:''"`

	Size int64 `json:"size" gorm:"not null;default:0"`
	// SHA256 is what the receiving machine computed for the bytes it wrote —
	// never what the uploader computed — so a later verification compares two
	// readings of the same disk.
	SHA256 string `json:"sha256" gorm:"size:64;not null;default:''"`
	Mode   string `json:"mode"   gorm:"size:8;not null;default:''"`

	UserID   int64  `json:"user_id"  gorm:"not null;default:0"`
	Username string `json:"username" gorm:"size:128;not null;default:''"`

	CreatedAt time.Time `json:"created_at" gorm:"index:idx_machine_file_machine_path,priority:3;index:idx_machine_file_app_env,priority:3"`

	LastVerifiedAt   *time.Time       `json:"last_verified_at"`
	LastVerifyStatus FileVerifyStatus `json:"last_verify_status" gorm:"size:16;not null;default:''"`
	// LastVerifySHA256 is what the machine reported at the last check. It is
	// kept alongside the status so a "changed" result can show what it changed
	// to, not merely that it changed.
	LastVerifySHA256 string `json:"last_verify_sha256" gorm:"size:64;not null;default:''"`
	LastVerifyError  string `json:"last_verify_error"  gorm:"size:512;not null;default:''"`

	// Current marks the newest row for its (machine_id, path) — the one that
	// describes what is on disk now. Derived at read time, never stored.
	Current bool `json:"current" gorm:"-"`
}

func (MachineFile) TableName() string { return "machine_file_upload" }
