package contract

// AgendaContainerFilesDir is the in-container mount point for an application
// environment's platform-managed files (uploaded through the console, stored on
// the host under <workspace_root>/run/<app>/<env>/.files). The deploy pipeline
// bind-mounts the host directory here read-only in every augmented service, the
// same way it mounts AgendaContainerLogDir, so an app can reference a credential
// by a fixed in-container path regardless of which machine it lands on.
const AgendaContainerFilesDir = "/agenda/files"

// AgendaFilesDirEnv is the env var carrying AgendaContainerFilesDir into the
// container, so an app can locate the directory without hardcoding it.
const AgendaFilesDirEnv = "AGENDA_FILES_DIR"

// Query parameters of the node file endpoints.
const (
	NodeFileQueryPath      = "path"
	NodeFileQueryMode      = "mode"
	NodeFileQueryOverwrite = "overwrite"
)

// DefaultFileMode is the permission a file is created with when the caller does
// not ask for one. Uploads are credentials more often than not, so the default
// is owner-only rather than the usual 0644.
const DefaultFileMode = "0600"

// FileStat is what the receiving side reports about a file on a machine — both
// as the result of writing one (PUT) and as the result of inspecting one (stat).
//
// SHA256 is computed by whichever side actually holds the bytes on disk, never
// by the sender: the point of the checksum is to describe what is on the
// machine now, which is also what makes it able to detect an out-of-band edit.
// It is empty when the file does not exist or is a directory.
type FileStat struct {
	Exists bool  `json:"exists"`
	IsDir  bool  `json:"is_dir"`
	Size   int64 `json:"size"`
	// Mode is the octal permission string, e.g. "0600".
	Mode string `json:"mode"`
	// ModTimeUnix is the file's mtime in seconds since the epoch, 0 when absent.
	ModTimeUnix int64  `json:"mod_time_unix"`
	SHA256      string `json:"sha256"`
}
