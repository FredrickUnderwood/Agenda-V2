package repository

import (
	"context"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/logger"
)

type ApplicationGatewayRouteBackendRepository struct{ db *gorm.DB }

func NewApplicationGatewayRouteBackendRepository(db *gorm.DB) *ApplicationGatewayRouteBackendRepository {
	return &ApplicationGatewayRouteBackendRepository{db: db}
}

func (r *ApplicationGatewayRouteBackendRepository) ListByRouteIDs(ctx context.Context, routeIDs []int64) ([]*domain.ApplicationGatewayRouteBackend, error) {
	if len(routeIDs) == 0 {
		return nil, nil
	}
	var rows []*domain.ApplicationGatewayRouteBackend
	if err := r.db.WithContext(ctx).
		Where("route_id IN ?", routeIDs).
		Order("route_id, id").
		Find(&rows).Error; err != nil {
		logger.L().Error("failed to list gateway route backends", zap.Error(err))
		return nil, err
	}
	return rows, nil
}

// DeleteByTargetID removes every route pin that points at one instance.
//
// Needed when an instance record is deleted: a backend_mode=selected route
// stores target_ids, and a row left pointing at a deleted instance makes the
// next application save fail validation ("backend target N is not a <env>
// instance of this application") — an error the operator has no way to act on,
// since the instance it names no longer exists anywhere in the UI.
func (r *ApplicationGatewayRouteBackendRepository) DeleteByTargetID(ctx context.Context, targetID int64) error {
	if err := r.db.WithContext(ctx).
		Where("target_id = ?", targetID).
		Delete(&domain.ApplicationGatewayRouteBackend{}).Error; err != nil {
		logger.L().Error("failed to delete gateway route backends by target", zap.Int64("target_id", targetID), zap.Error(err))
		return err
	}
	return nil
}

// SyncByRoute replaces the full backend list for a route (delete + insert).
func (r *ApplicationGatewayRouteBackendRepository) SyncByRoute(ctx context.Context, routeID int64, rows []*domain.ApplicationGatewayRouteBackend) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("route_id = ?", routeID).Delete(&domain.ApplicationGatewayRouteBackend{}).Error; err != nil {
			logger.L().Error("failed to clear gateway route backends", zap.Int64("route_id", routeID), zap.Error(err))
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		for _, row := range rows {
			row.RouteID = routeID
		}
		if err := tx.Create(&rows).Error; err != nil {
			logger.L().Error("failed to create gateway route backends", zap.Int64("route_id", routeID), zap.Error(err))
			return err
		}
		return nil
	})
}
