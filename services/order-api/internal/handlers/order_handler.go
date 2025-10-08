package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/utils"
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
	traceID, err := utils.GetTraceID(c)
	if err != nil {
		resp := pkg.ToErrorResponse(h.logger, "", pkg.NewAppError(pkg.ErrServerCode, "failed to retrieve trace id", err))
		c.JSON(resp.Status, resp)
		return
	}
	userIdStr := c.GetHeader("userId") // TODO : User Id should be retrieve from JWT Authorization header. This is for demo purpose only.
	userUUID, err := uuid.Parse(userIdStr)
	if err != nil {
		resp := pkg.ToErrorResponse(h.logger, traceID, pkg.NewAppError(pkg.ErrInvalidInputCode, "failed to parse user UUID from header", err))
		c.JSON(resp.Status, resp)
		return
	}

	var req views.OrderRequest
	if err = c.ShouldBindJSON(&req); err != nil {
		resp := pkg.ToErrorResponse(h.logger, traceID, pkg.NewAppError(pkg.ErrInvalidInputCode, "invalid input", err))
		c.JSON(resp.Status, resp)
		return
	}

	orderID, err := h.service.CreateOrder(c.Request.Context(), traceID, userUUID, req)
	if err != nil {
		resp := pkg.ToErrorResponse(h.logger, traceID, err)
		c.JSON(resp.Status, resp)
		return
	}

	c.JSON(http.StatusCreated, pkg.APIResponse{
		Data: map[string]interface{}{
			"orderId": orderID,
		},
	})
}
