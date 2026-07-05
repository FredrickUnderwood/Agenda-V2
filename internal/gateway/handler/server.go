package handler

import (
	"context"
	"net/http"
	"time"

	alog "github.com/FredrickUnderwood/agenda-go-sdk/log"
	coreauth "github.com/FredrickUnderwood/agenda-v2/internal/auth"
	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/application"
	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/auth"
	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/config"
	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/service"
	ginzap "github.com/gin-contrib/zap"
	"github.com/gin-gonic/gin"
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

	httpSrv *http.Server
}

func NewServer(
	cfg *config.Config,
	db *gorm.DB,
	authMgr *coreauth.Manager,
	routes *service.RouteService,
	gateway *application.GatewayApplication,
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
	return s
}

func (s *Server) registerRoutes() {
	s.engine.GET("/-/health", s.health)
	s.engine.GET("/-/ready", s.ready)

	routes := s.engine.Group("/-/routes")
	routes.Use(s.authMiddleware())
	{
		routes.GET("", requirePerm(auth.PermRouteRead), s.listRoutes)
		routes.GET("/:routeKey", requirePerm(auth.PermRouteRead), s.getRoute)
		routes.PUT("/:routeKey", requirePerm(auth.PermRouteUpdate), s.upsertRoute)
		routes.POST("/:routeKey/rollback", requirePerm(auth.PermRouteRollback), s.rollbackRoute)
	}

	s.engine.NoRoute(s.proxy)
}

func (s *Server) Start() error {
	return s.httpSrv.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpSrv == nil {
		return nil
	}
	return s.httpSrv.Shutdown(ctx)
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
