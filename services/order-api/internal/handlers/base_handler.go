package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

type BaseHandler struct {
	logger *zap.Logger
}

func NewBaseHandler(logger *zap.Logger) *BaseHandler {
	return &BaseHandler{logger: logger}
}

func (b *BaseHandler) RegisterRoutes(r *gin.Engine) {
	r.GET("/health", b.GetHealth)
	r.GET("/metrics", gin.WrapH(promhttp.Handler())) // Expose for scrape
}

func (b *BaseHandler) GetHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "ok",
	})
}
