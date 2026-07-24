package handler

import (
	"net/http"

	"github.com/FredrickUnderwood/agenda-v2/internal/contract"
	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/edgetls"
	"github.com/gin-gonic/gin"
)

// updateTLSConfig receives ACME/DNS credentials pushed by the control plane
// (sourced from platform Settings) and hot-applies them to the edge-TLS
// manager. When this gateway is not the TLS edge (GATEWAY_TLS_ENABLED unset,
// so tlsMgr is nil) the push is accepted as a no-op so the control plane's
// periodic sync doesn't error against non-edge gateways.
func (s *Server) updateTLSConfig(c *gin.Context) {
	var req contract.UpdateTLSConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, domain.NewInvalidParamError("invalid json"))
		return
	}
	if s.tlsMgr == nil {
		logCaller(c, "tls config push ignored: edge tls disabled on this gateway")
		c.JSON(http.StatusOK, gin.H{"data": gin.H{"applied": false, "reason": "edge tls disabled"}})
		return
	}
	if err := s.tlsMgr.Reconfigure(edgetls.Config{
		Email:          req.Email,
		CADir:          req.CADir,
		EABKeyID:       req.EABKeyID,
		EABHMACKey:     req.EABHMACKey,
		DNSProvider:    req.DNSProvider,
		AliyunAKID:     req.AliyunAKID,
		AliyunAKSecret: req.AliyunAKSecret,
		StaticDomains:  req.StaticDomains,
	}); err != nil {
		writeError(c, domain.NewInvalidParamError(err.Error()))
		return
	}
	logCaller(c, "tls config applied")
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"applied": true, "ready": s.tlsMgr.Ready()}})
}
