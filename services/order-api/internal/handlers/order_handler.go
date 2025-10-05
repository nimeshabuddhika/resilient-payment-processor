package handlers

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nimeshabuddhika/resilient-payment-processor/libs/go-common"
	"github.com/nimeshabuddhika/resilient-payment-processor/services/order-api/internal/services"
	"github.com/nimeshabuddhika/resilient-payment-processor/services/order-api/internal/views"
	"go.uber.org/zap"
)

type OrderHandler struct {
	logger  *zap.Logger
	service services.OrderService
}

func NewOrderHandler(logger *zap.Logger, svc services.OrderService) *OrderHandler {
	return &OrderHandler{logger: logger, service: svc}
}

// RegisterRoutes registers order routes on the provided Gin engine.
func (h *OrderHandler) RegisterRoutes(r *gin.Engine) {
	// basic health check
	r.GET("/health", func(c *gin.Context) { c.Status(http.StatusOK) })

	r.POST("/orders", h.CreateOrder)
}

func (h *OrderHandler) CreateOrder(c *gin.Context) {
	traceID := c.GetHeader("X-Trace-Id")
	if traceID == "" {
		traceID = generateTraceID()
	}

	var req views.OrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("invalid request payload", zap.Error(err))
		c.JSON(http.StatusBadRequest, go_common.ErrorResponse{
			Code:    go_common.ErrInvalidInput,
			Message: "invalid request payload",
			TraceID: traceID,
		})
		return
	}

	orderID, err := h.service.CreateOrder(c.Request.Context(), req)
	if err != nil {
		h.logger.Error("failed to create order", zap.Error(err))
		c.JSON(http.StatusInternalServerError, go_common.ErrorResponse{
			Code:    go_common.ERRServerError,
			Message: "failed to create order",
			TraceID: traceID,
		})
		return
	}

	c.JSON(http.StatusCreated, go_common.APIResponse{
		TraceID: traceID,
		Data: map[string]interface{}{
			"orderId": orderID,
		},
	})
}

func generateTraceID() string {
	return fmt.Sprintf("trc_%d_%d", time.Now().UnixNano(), os.Getpid())
}
