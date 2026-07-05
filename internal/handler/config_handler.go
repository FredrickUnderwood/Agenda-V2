package handler

import (
	"strings"

	"github.com/gin-gonic/gin"
)

func (s *Server) getConfig(c *gin.Context) {
	redactedTokens := make(map[string]string, len(s.cfg.Git.Tokens))
	for host := range s.cfg.Git.Tokens {
		redactedTokens[host] = "***"
	}
	redactedMachines := make(map[string]interface{}, len(s.cfg.Machines))
	for name, m := range s.cfg.Machines {
		redactedMachines[name] = gin.H{
			"machine_type":   m.MachineType,
			"host":           m.Host,
			"user":           m.User,
			"port":           m.Port,
			"workspace_root": m.WorkspaceRoot,
		}
	}
	Success(c, gin.H{
		"server": gin.H{
			"addr":       s.cfg.Server.Addr,
			"auth_token": redact(s.cfg.Server.AuthToken),
		},
		"database": gin.H{
			"dsn": redactDSN(s.cfg.Database.DSN),
		},
		"git": gin.H{
			"tokens":            redactedTokens,
			"operation_timeout": s.cfg.Git.OperationTimeout.String(),
		},
		"gateway": gin.H{
			"enabled":  s.cfg.Gateway.Enabled,
			"base_url": s.cfg.Gateway.BaseURL,
		},
		"machines":       redactedMachines,
		"workspace_root": s.cfg.WorkspaceRoot,
		"deploy": gin.H{
			"max_output_bytes": s.cfg.Deploy.MaxOutputBytes,
			"default_timeout":  s.cfg.Deploy.DefaultTimeout.String(),
		},
	})
}

func redact(s string) string {
	if s == "" {
		return ""
	}
	return "***"
}

func redactDSN(dsn string) string {
	if dsn == "" {
		return ""
	}
	at := strings.Index(dsn, "@")
	colon := strings.Index(dsn, ":")
	if at < 0 || colon < 0 || colon >= at {
		return "***"
	}
	return dsn[:colon+1] + "***" + dsn[at:]
}
