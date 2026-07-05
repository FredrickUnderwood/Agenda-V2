package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/FredrickUnderwood/agenda-v2/internal/logger"
)

// Success writes a 200 with the given payload.
func Success(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, data)
}

// Created writes a 201 with the given payload.
func Created(c *gin.Context, data interface{}) {
	c.JSON(http.StatusCreated, data)
}

// NoContent writes a 204.
func NoContent(c *gin.Context) {
	c.Status(http.StatusNoContent)
}

// FailWith logs the error at the handler boundary and writes the given
// status. This is the layering "collection point" — internal error detail
// stops here, never returned to the client beyond err.Error().
func FailWith(c *gin.Context, status int, err error) {
	logger.L().Error("request failed",
		zap.String("method", c.Request.Method),
		zap.String("path", c.FullPath()),
		zap.Int("status", status),
		zap.Error(err),
	)
	c.JSON(status, gin.H{"error": err.Error()})
}

// FailMessage writes a status with a plain message (no underlying error).
func FailMessage(c *gin.Context, status int, msg string) {
	c.JSON(status, gin.H{"error": msg})
}
