package handler

import (
	"context"
	"net/http"
	"time"

	coreauth "github.com/FredrickUnderwood/agenda-v2/internal/auth"
	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/application"
	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/auth"
	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/config"
	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/edgetls"
	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/service"
	alog "github.com/FredrickUnderwood/agenda-v2/sdk/go/log"
	ginzap "github.com/gin-contrib/zap"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"gorm.io/gorm"
)

type Server struct {
	cfg           *config.Config
	engine        *gin.Engine
	db            *gorm.DB
	authMgr       *coreauth.Manager
	serviceTokens []serviceTokenEntry
	routes        *service.RouteService
	gateway       *application.GatewayApplication
	tlsMgr        *edgetls.Manager

	httpSrv  *http.Server
	httpsSrv *http.Server
}

func NewServer(
	cfg *config.Config,
	db *gorm.DB,
	authMgr *coreauth.Manager,
	routes *service.RouteService,
	gateway *application.GatewayApplication,
	tlsMgr *edgetls.Manager,
) *Server {
	gin.SetMode(gin.ReleaseMode)
	s := &Server{
		cfg:           cfg,
		engine:        gin.New(),
		db:            db,
		authMgr:       authMgr,
		serviceTokens: buildServiceTokens(cfg.ServiceTokens),
		routes:        routes,
		gateway:       gateway,
		tlsMgr:        tlsMgr,
	}
	s.engine.Use(
		ginzap.Ginzap(alog.L(), time.RFC3339, true),
		ginzap.RecoveryWithZap(alog.L(), true),
	)
	s.registerRoutes()
	s.httpSrv = &http.Server{
		Addr:              cfg.Server.Addr,
		Handler:           s.engine,
		ReadHeaderTimeout: 10 * time.Second,
	}
	// When edge TLS is enabled the same gin engine also serves the HTTPS data
	// plane; CertMagic supplies certificates via TLSConfig.GetCertificate, so no
	// cert/key files are passed to ListenAndServeTLS.
	if tlsMgr != nil {
		s.httpsSrv = &http.Server{
			Addr:              tlsMgr.HTTPSAddr(),
			Handler:           s.engine,
			TLSConfig:         tlsMgr.TLSConfig(),
			ReadHeaderTimeout: 10 * time.Second,
		}
	}
	return s
}

func (s *Server) registerRoutes() {
	s.engine.GET("/-/health", s.health)
	s.engine.GET("/-/ready", s.ready)
	s.engine.GET("/-/metrics", gin.WrapH(promhttp.Handler()))

	routes := s.engine.Group("/-/routes")
	routes.Use(s.authMiddleware())
	{
		routes.GET("", requirePerm(auth.PermRouteRead), s.listRoutes)
		routes.GET("/:routeKey", requirePerm(auth.PermRouteRead), s.getRoute)
		routes.PUT("/:routeKey", requirePerm(auth.PermRouteUpdate), s.upsertRoute)
		routes.POST("/:routeKey/rollback", requirePerm(auth.PermRouteRollback), s.rollbackRoute)
	}

	tlsAdmin := s.engine.Group("/-/tls")
	tlsAdmin.Use(s.authMiddleware())
	{
		tlsAdmin.PUT("", requirePerm(auth.PermTLSUpdate), s.updateTLSConfig)
	}

	s.engine.NoRoute(s.proxy)
}

func (s *Server) Start() error {
	return s.httpSrv.ListenAndServe()
}

// StartTLS blocks serving the HTTPS data plane. It returns http.ErrServerClosed
// immediately when edge TLS is disabled, so callers can treat it uniformly with
// Start without special-casing the nil listener.
func (s *Server) StartTLS() error {
	if s.httpsSrv == nil {
		return http.ErrServerClosed
	}
	// Certs come from TLSConfig.GetCertificate (CertMagic); no files here.
	return s.httpsSrv.ListenAndServeTLS("", "")
}

func (s *Server) Shutdown(ctx context.Context) error {
	var firstErr error
	if s.httpSrv != nil {
		if err := s.httpSrv.Shutdown(ctx); err != nil {
			firstErr = err
		}
	}
	if s.httpsSrv != nil {
		if err := s.httpsSrv.Shutdown(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *Server) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (s *Server) ready(c *gin.Context) {
	sqlDB, err := s.db.DB()
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "db error"})
		return
	}
	if err := sqlDB.PingContext(c.Request.Context()); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "db unreachable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ready"})
}

// proxy is the data-plane entrypoint (gin's NoRoute catch-all, unauthenticated
// by default). If the matched route's configured instance header is present
// on the request, pinning is only honored when the route has instance
// selection enabled AND the caller presents a valid X-Service-Token with the
// route.invoke perm — otherwise the request is rejected before it ever
// reaches GatewayApplication's own (redundant, defense-in-depth) checks.
func (s *Server) proxy(c *gin.Context) {
	mode, header, found := s.gateway.LookupRouteConfig(c.Request.Host, c.Request.URL.Path)
	pinnedInstance := ""
	if found && header != "" {
		if v := c.GetHeader(header); v != "" {
			if mode != domain.InstanceSelectModeEnabled {
				c.AbortWithStatusJSON(http.StatusBadRequest, ErrorResponse{Error: ErrorBody{
					Code:    400,
					Message: "instance selection is disabled for this route",
				}})
				return
			}
			token := c.GetHeader(HeaderServiceToken)
			item, ok := matchServiceToken(s.serviceTokens, token)
			if token == "" || !ok || !item.perms[auth.PermRouteInvoke] {
				c.AbortWithStatusJSON(http.StatusUnauthorized, ErrorResponse{Error: ErrorBody{
					Code:    401,
					Message: "pinning an instance requires a valid service token with route.invoke",
				}})
				return
			}
			pinnedInstance = v
		}
	}
	s.gateway.ServeProxy(c.Writer, c.Request, pinnedInstance)
}
