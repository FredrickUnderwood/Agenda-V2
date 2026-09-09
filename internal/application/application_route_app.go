package application

import (
	"context"
	"strconv"
	"time"

	"github.com/FredrickUnderwood/agenda-v2/internal/contract"
	"github.com/FredrickUnderwood/agenda-v2/internal/logger"
	"github.com/FredrickUnderwood/agenda-v2/internal/service"
	"go.uber.org/zap"
)

type gatewayRouteLifecycle interface {
	DeleteRoute(context.Context, string, contract.RouteOwner) error
	DisableRoute(context.Context, string, contract.RouteOwner) error
}

type routeDeployLocker interface {
	AcquireKey(context.Context, string, time.Duration) (string, error)
	ReleaseKey(context.Context, string, string)
}

type ApplicationRouteApplication struct {
	apps    *service.ApplicationService
	gateway gatewayRouteLifecycle
	locks   routeDeployLocker
}

// A nil gateway is used only when gateway integration is disabled.
func NewApplicationRouteApplication(apps *service.ApplicationService, gateway gatewayRouteLifecycle, locks routeDeployLocker) *ApplicationRouteApplication {
	return &ApplicationRouteApplication{apps: apps, gateway: gateway, locks: locks}
}

func (a *ApplicationRouteApplication) Delete(ctx context.Context, appID, routeID int64) error {
	return a.change(ctx, appID, routeID, true)
}

func (a *ApplicationRouteApplication) Disable(ctx context.Context, appID, routeID int64) error {
	return a.change(ctx, appID, routeID, false)
}

func (a *ApplicationRouteApplication) change(ctx context.Context, appID, routeID int64, remove bool) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	route, err := a.apps.GetGatewayRoute(ctx, appID, routeID)
	if err != nil {
		return err
	}
	logger.L().Info("application route lifecycle requested", zap.Int64("application_id", appID), zap.Int64("route_id", routeID), zap.String("route_key", route.RouteKey), zap.String("env", string(route.Env)), zap.Bool("delete", remove))
	// Also serialize lifecycle requests for routes with no remaining instances.
	routeLock := "gateway-route:" + strconv.FormatInt(routeID, 10)
	token, err := a.locks.AcquireKey(ctx, routeLock, 2*time.Minute)
	if err != nil {
		return err
	}
	defer a.locks.ReleaseKey(context.Background(), routeLock, token)

	// Exclude deployments and decommissions that could write this route back
	// while it is being removed. Listing instances also works for an empty env.
	targets, err := a.apps.ListTargetsByApplication(ctx, appID, route.Env)
	if err != nil {
		return err
	}
	for _, target := range targets {
		key := service.ReleaseLockKey(appID, string(route.Env), target.InstanceName)
		token, err := a.locks.AcquireKey(ctx, key, 2*time.Minute)
		if err != nil {
			return err
		}
		defer a.locks.ReleaseKey(context.Background(), key, token)
	}
	route, err = a.apps.GetGatewayRoute(ctx, appID, routeID)
	if err != nil {
		return err
	}
	if a.gateway != nil {
		owner := contract.RouteOwner{ApplicationID: appID, Env: string(route.Env)}
		if remove {
			err = a.gateway.DeleteRoute(ctx, route.RouteKey, owner)
		} else {
			err = a.gateway.DisableRoute(ctx, route.RouteKey, owner)
		}
		if err != nil {
			return err
		}
	}
	// Release the reservation only after the runtime gateway confirms removal.
	// If the local write fails, the retained row allows an idempotent retry.
	if remove {
		return a.apps.DeleteGatewayRoute(ctx, appID, routeID)
	}
	return a.apps.DisableGatewayRoute(ctx, appID, routeID)
}
