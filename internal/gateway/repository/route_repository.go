package repository

import (
	"context"
	"errors"

	alog "github.com/FredrickUnderwood/agenda-go-sdk/log"
	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/domain"
	"github.com/bytedance/sonic"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type RouteRepository struct {
	db *gorm.DB
}

func NewRouteRepository(db *gorm.DB) *RouteRepository {
	return &RouteRepository{db: db}
}

func (r *RouteRepository) LoadEnabledRoutes(ctx context.Context) ([]domain.Route, error) {
	var routes []domain.Route
	if err := r.db.WithContext(ctx).
		Where("status = ?", domain.RouteStatusEnabled).
		Order("host ASC").
		Order("LENGTH(path_prefix) DESC").
		Order("path_prefix ASC").
		Find(&routes).Error; err != nil {
		alog.L().Error("load enabled routes failed", zap.Error(err))
		return nil, err
	}
	if err := r.fillCurrentBackends(ctx, routes); err != nil {
		return nil, err
	}
	return routes, nil
}

func (r *RouteRepository) ListRoutes(ctx context.Context) ([]domain.Route, error) {
	var routes []domain.Route
	if err := r.db.WithContext(ctx).
		Order("route_key ASC").
		Find(&routes).Error; err != nil {
		alog.L().Error("list routes failed", zap.Error(err))
		return nil, err
	}
	if err := r.fillCurrentBackends(ctx, routes); err != nil {
		return nil, err
	}
	return routes, nil
}

func (r *RouteRepository) GetRoute(ctx context.Context, routeKey string) (domain.Route, error) {
	var route domain.Route
	if err := r.db.WithContext(ctx).
		Where("route_key = ?", routeKey).
		First(&route).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return domain.Route{}, domain.ErrRouteNotFound
		}
		alog.L().Error("get route failed", zap.String("route_key", routeKey), zap.Error(err))
		return domain.Route{}, err
	}
	backends, err := r.listBackends(ctx, route.ID, route.CurrentReleaseID)
	if err != nil {
		return domain.Route{}, err
	}
	route.Backends = backends
	return route, nil
}

func (r *RouteRepository) UpsertRoute(
	ctx context.Context,
	route domain.Route,
	backends []domain.Backend,
	operator string,
	reason string,
) (domain.Route, error) {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		oldReleaseID := ""
		var existing domain.Route
		err := tx.Where("route_key = ?", route.RouteKey).First(&existing).Error
		switch {
		case err == nil:
			oldReleaseID = existing.CurrentReleaseID
			route.ID = existing.ID
			if err := tx.Model(&domain.Route{}).Where("id = ?", existing.ID).Updates(map[string]any{
				"application_id":       route.ApplicationID,
				"service_name":         route.ServiceName,
				"env":                  route.Env,
				"host":                 route.Host,
				"path_prefix":          route.PathPrefix,
				"strip_prefix":         route.StripPrefix,
				"current_release_id":   route.CurrentReleaseID,
				"status":               route.Status,
				"instance_select_mode": route.InstanceSelectMode,
				"instance_header":      route.InstanceHeader,
				"timeout_ms":           route.TimeoutMs,
			}).Error; err != nil {
				alog.L().Error("update route failed", zap.String("route_key", route.RouteKey), zap.Error(err))
				return err
			}
		case errors.Is(err, gorm.ErrRecordNotFound):
			if err := tx.Create(&route).Error; err != nil {
				alog.L().Error("create route failed", zap.String("route_key", route.RouteKey), zap.Error(err))
				return err
			}
		default:
			alog.L().Error("select route before upsert failed", zap.String("route_key", route.RouteKey), zap.Error(err))
			return err
		}

		if err := tx.Where("route_id = ? AND release_id = ?", route.ID, route.CurrentReleaseID).
			Delete(&domain.Backend{}).Error; err != nil {
			alog.L().Error("delete route backends failed",
				zap.Int64("route_id", route.ID),
				zap.String("release_id", route.CurrentReleaseID),
				zap.Error(err),
			)
			return err
		}
		for i := range backends {
			backends[i].RouteID = route.ID
			backends[i].ReleaseID = route.CurrentReleaseID
		}
		if len(backends) > 0 {
			if err := tx.Create(&backends).Error; err != nil {
				alog.L().Error("create route backends failed",
					zap.Int64("route_id", route.ID),
					zap.String("release_id", route.CurrentReleaseID),
					zap.Error(err),
				)
				return err
			}
		}
		if oldReleaseID == route.CurrentReleaseID {
			return nil
		}
		return insertHistory(tx, route.ID, oldReleaseID, route.CurrentReleaseID, operator, reason, backends)
	})
	if err != nil {
		return domain.Route{}, err
	}
	return r.GetRoute(ctx, route.RouteKey)
}

func (r *RouteRepository) RollbackRoute(ctx context.Context, routeKey, operator, reason string) (domain.Route, error) {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var route domain.Route
		if err := tx.Where("route_key = ?", routeKey).First(&route).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return domain.ErrRouteNotFound
			}
			alog.L().Error("select route before rollback failed", zap.String("route_key", routeKey), zap.Error(err))
			return err
		}
		var history domain.RouteHistory
		if err := tx.Where(
			"route_id = ? AND to_release_id = ? AND from_release_id <> '' AND from_release_id <> to_release_id",
			route.ID,
			route.CurrentReleaseID,
		).
			Order("id DESC").
			First(&history).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return domain.ErrRollbackUnavailable
			}
			alog.L().Error("select rollback history failed", zap.String("route_key", routeKey), zap.Error(err))
			return err
		}
		var count int64
		if err := tx.Model(&domain.Backend{}).
			Where("route_id = ? AND release_id = ? AND enabled = ? AND healthy = ?", route.ID, history.FromReleaseID, true, true).
			Count(&count).Error; err != nil {
			alog.L().Error("count rollback backends failed", zap.String("route_key", routeKey), zap.Error(err))
			return err
		}
		if count == 0 {
			return domain.ErrRollbackUnavailable
		}
		if err := tx.Model(&domain.Route{}).Where("id = ?", route.ID).Updates(map[string]any{
			"current_release_id": history.FromReleaseID,
		}).Error; err != nil {
			alog.L().Error("update rollback route failed", zap.String("route_key", routeKey), zap.Error(err))
			return err
		}
		return insertHistory(tx, route.ID, route.CurrentReleaseID, history.FromReleaseID, operator, reason, nil)
	})
	if err != nil {
		return domain.Route{}, err
	}
	return r.GetRoute(ctx, routeKey)
}

func (r *RouteRepository) fillCurrentBackends(ctx context.Context, routes []domain.Route) error {
	for i := range routes {
		backends, err := r.listBackends(ctx, routes[i].ID, routes[i].CurrentReleaseID)
		if err != nil {
			return err
		}
		routes[i].Backends = backends
	}
	return nil
}

func (r *RouteRepository) listBackends(ctx context.Context, routeID int64, releaseID string) ([]domain.Backend, error) {
	var backends []domain.Backend
	if err := r.db.WithContext(ctx).
		Where("route_id = ? AND release_id = ?", routeID, releaseID).
		Order("target_key ASC").
		Find(&backends).Error; err != nil {
		alog.L().Error("list route backends failed",
			zap.Int64("route_id", routeID),
			zap.String("release_id", releaseID),
			zap.Error(err),
		)
		return nil, err
	}
	return backends, nil
}

func insertHistory(
	tx *gorm.DB,
	routeID int64,
	fromReleaseID string,
	toReleaseID string,
	operator string,
	reason string,
	backends []domain.Backend,
) error {
	raw, err := sonic.MarshalString(backends)
	if err != nil {
		alog.L().Error("marshal route history backends failed", zap.Error(err))
		return err
	}
	history := domain.RouteHistory{
		RouteID:       routeID,
		FromReleaseID: fromReleaseID,
		ToReleaseID:   toReleaseID,
		Operator:      operator,
		Reason:        reason,
		BackendsJSON:  raw,
	}
	if err := tx.Create(&history).Error; err != nil {
		alog.L().Error("insert route history failed",
			zap.Int64("route_id", routeID),
			zap.String("from_release_id", fromReleaseID),
			zap.String("to_release_id", toReleaseID),
			zap.Error(err),
		)
		return err
	}
	return nil
}
