package repository

import (
	"context"
	"errors"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/agenda-v2/internal/domain"
	"github.com/agenda-v2/internal/logger"
)

type ApplicationGatewayRouteRepository struct{ db *gorm.DB }

func NewApplicationGatewayRouteRepository(db *gorm.DB) *ApplicationGatewayRouteRepository {
	return &ApplicationGatewayRouteRepository{db: db}
}

func (r *ApplicationGatewayRouteRepository) ListByApplicationEnv(ctx context.Context, appID int64, env domain.Environment) ([]*domain.ApplicationGatewayRoute, error) {
	var routes []*domain.ApplicationGatewayRoute
	if err := r.db.WithContext(ctx).
		Where("application_id = ? AND env = ?", appID, env).
		Order("sort_order, id").
		Find(&routes).Error; err != nil {
		logger.L().Error("failed to list application gateway routes",
			zap.Int64("application_id", appID), zap.String("env", string(env)), zap.Error(err))
		return nil, err
	}
	return routes, nil
}

func (r *ApplicationGatewayRouteRepository) ListByApplication(ctx context.Context, appID int64) ([]*domain.ApplicationGatewayRoute, error) {
	var routes []*domain.ApplicationGatewayRoute
	if err := r.db.WithContext(ctx).
		Where("application_id = ?", appID).
		Order("env, sort_order, id").
		Find(&routes).Error; err != nil {
		logger.L().Error("failed to list gateway routes by application", zap.Int64("application_id", appID), zap.Error(err))
		return nil, err
	}
	return routes, nil
}

func (r *ApplicationGatewayRouteRepository) ListByApplicationIDs(ctx context.Context, appIDs []int64) ([]*domain.ApplicationGatewayRoute, error) {
	if len(appIDs) == 0 {
		return nil, nil
	}
	var routes []*domain.ApplicationGatewayRoute
	if err := r.db.WithContext(ctx).
		Where("application_id IN ?", appIDs).
		Order("application_id, env, sort_order, id").
		Find(&routes).Error; err != nil {
		logger.L().Error("failed to list gateway routes by applications", zap.Error(err))
		return nil, err
	}
	return routes, nil
}

func (r *ApplicationGatewayRouteRepository) FindByRouteKey(ctx context.Context, routeKey string) (*domain.ApplicationGatewayRoute, error) {
	var route domain.ApplicationGatewayRoute
	if err := r.db.WithContext(ctx).
		Where("route_key = ?", routeKey).
		First(&route).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		logger.L().Error("failed to find gateway route by route key", zap.String("route_key", routeKey), zap.Error(err))
		return nil, err
	}
	return &route, nil
}

func (r *ApplicationGatewayRouteRepository) FindByHostPath(ctx context.Context, host, pathPrefix string) (*domain.ApplicationGatewayRoute, error) {
	var route domain.ApplicationGatewayRoute
	if err := r.db.WithContext(ctx).
		Where("host = ? AND path_prefix = ?", host, pathPrefix).
		First(&route).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		logger.L().Error("failed to find gateway route by host path",
			zap.String("host", host), zap.String("path_prefix", pathPrefix), zap.Error(err))
		return nil, err
	}
	return &route, nil
}

func (r *ApplicationGatewayRouteRepository) SyncByApplicationEnv(ctx context.Context, appID int64, env domain.Environment, routes []*domain.ApplicationGatewayRoute) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing []*domain.ApplicationGatewayRoute
		if err := tx.
			Where("application_id = ? AND env = ?", appID, env).
			Find(&existing).Error; err != nil {
			logger.L().Error("failed to list existing gateway routes for sync",
				zap.Int64("application_id", appID), zap.String("env", string(env)), zap.Error(err))
			return err
		}

		existingByKey := make(map[string]*domain.ApplicationGatewayRoute, len(existing))
		for _, route := range existing {
			existingByKey[route.RouteKey] = route
		}

		requested := make(map[string]struct{}, len(routes))
		for _, route := range routes {
			route.ApplicationID = appID
			route.Env = env
			requested[route.RouteKey] = struct{}{}

			if old := existingByKey[route.RouteKey]; old != nil {
				route.ID = old.ID
				if err := tx.Model(&domain.ApplicationGatewayRoute{}).
					Where("id = ?", old.ID).
					Updates(map[string]interface{}{
						"host":                 route.Host,
						"path_prefix":          route.PathPrefix,
						"strip_prefix":         route.StripPrefix,
						"backend_path":         route.BackendPath,
						"enabled":              route.Enabled,
						"backend_mode":         route.BackendMode,
						"instance_select_mode": route.InstanceSelectMode,
						"instance_header":      route.InstanceHeader,
						"sort_order":           route.SortOrder,
					}).Error; err != nil {
					logger.L().Error("failed to update gateway route",
						zap.Int64("id", old.ID), zap.String("route_key", route.RouteKey), zap.Error(err))
					return err
				}
				continue
			}

			if err := tx.Create(route).Error; err != nil {
				logger.L().Error("failed to create gateway route",
					zap.Int64("application_id", appID), zap.String("env", string(env)),
					zap.String("route_key", route.RouteKey), zap.Error(err))
				return err
			}
		}

		for _, old := range existing {
			if _, ok := requested[old.RouteKey]; ok {
				continue
			}
			if !old.Enabled {
				continue
			}
			if err := tx.Model(&domain.ApplicationGatewayRoute{}).
				Where("id = ?", old.ID).
				Update("enabled", false).Error; err != nil {
				logger.L().Error("failed to disable omitted gateway route",
					zap.Int64("id", old.ID), zap.String("route_key", old.RouteKey), zap.Error(err))
				return err
			}
		}
		return nil
	})
}

func (r *ApplicationGatewayRouteRepository) DeleteByApplication(ctx context.Context, appID int64) error {
	if err := r.db.WithContext(ctx).
		Where("application_id = ?", appID).
		Delete(&domain.ApplicationGatewayRoute{}).Error; err != nil {
		logger.L().Error("failed to delete application gateway routes", zap.Int64("application_id", appID), zap.Error(err))
		return err
	}
	return nil
}
