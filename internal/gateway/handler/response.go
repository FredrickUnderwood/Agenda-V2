package handler

import (
	"errors"
	"net/http"

	alog "github.com/FredrickUnderwood/agenda-go-sdk/log"
	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/domain"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

type ErrorBody struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func writeError(c *gin.Context, err error) {
	status, body := toHTTPError(err)
	if status >= http.StatusInternalServerError {
		alog.L().Error("request failed", zap.String("path", c.Request.URL.Path), zap.Error(err))
	} else {
		alog.L().Warn("request rejected", zap.String("path", c.Request.URL.Path), zap.Error(err))
	}
	c.JSON(status, ErrorResponse{Error: body})
}

func toHTTPError(err error) (int, ErrorBody) {
	switch {
	case errors.Is(err, domain.ErrRouteNotFound):
		return http.StatusNotFound, ErrorBody{Code: 404, Message: "route not found"}
	case errors.Is(err, domain.ErrRollbackUnavailable):
		return http.StatusBadRequest, ErrorBody{Code: 400, Message: "rollback unavailable"}
	case domain.IsInvalidParam(err):
		return http.StatusBadRequest, ErrorBody{Code: 400, Message: err.Error()}
	default:
		return http.StatusInternalServerError, ErrorBody{Code: 500, Message: "internal server error"}
	}
}
