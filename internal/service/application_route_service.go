package service

import (
	"context"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/logger"
	"go.uber.org/zap"
)

// ListGatewayRoutes is independent of instances: routes remain manageable even
// after the last instance in their environment has been deleted.
func (s *ApplicationService) ListGatewayRoutes(ctx context.Context, appID int64) ([]*domain.ApplicationGatewayRoute, error) {
	if _, err := s.apps.GetByID(ctx, appID); err != nil {
		return nil, err
	}
	routes, err := s.routes.ListByApplication(ctx, appID)
	if err != nil {
		return nil, err
	}
	if err := s.attachRouteBackends(ctx, routes); err != nil {
		return nil, err
	}
	return routes, nil
}

func (s *ApplicationService) GetGatewayRoute(ctx context.Context, appID, routeID int64) (*domain.ApplicationGatewayRoute, error) {
	return s.routes.GetByApplicationID(ctx, appID, routeID)
}

func (s *ApplicationService) DisableGatewayRoute(ctx context.Context, appID, routeID int64) error {
	if _, err := s.GetGatewayRoute(ctx, appID, routeID); err != nil {
		return err
	}
	if err := s.routes.Disable(ctx, appID, routeID); err != nil {
		return err
	}
	logger.L().Info("application gateway route disabled", zap.Int64("application_id", appID), zap.Int64("route_id", routeID))
	return nil
}

func (s *ApplicationService) DeleteGatewayRoute(ctx context.Context, appID, routeID int64) error {
	if err := s.routes.Delete(ctx, appID, routeID); err != nil {
		return err
	}
	logger.L().Info("application gateway route deleted", zap.Int64("application_id", appID), zap.Int64("route_id", routeID))
	return nil
}
