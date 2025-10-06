package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	common "github.com/nimeshabuddhika/resilient-payment-processor/libs/go-common"
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
func (h *OrderHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/orders", h.CreateOrder)
}

func (h *OrderHandler) CreateOrder(c *gin.Context) {
	traceID, err := common.GetTraceID(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, common.ErrorResponse{
			Code:    common.ERRServerError,
			Message: err.Error(),
		})
		return
	}

	var req views.OrderRequest
	if err = c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, common.ErrorResponse{
			Code:    common.ErrInvalidInput,
			Message: "invalid request body",
			Details: err.Error(),
		})
		return
	}

	orderID, err := h.service.CreateOrder(c.Request.Context(), traceID, "userId", req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, common.ErrorResponse{
			Code:    common.ERRServerError,
			Message: "failed to create order",
		})
		return
	}

	c.JSON(http.StatusCreated, common.APIResponse{
		Data: map[string]interface{}{
			"orderId": orderID,
		},
	})
}
