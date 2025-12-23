package api

import (
	"net/http"
	"strconv"
	"ticket-backend/internal/model"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
)

type PaymentController struct {
	Service service.PaymentService
}

func (c *PaymentController) Pay(ctx *gin.Context) {
	var req model.Payment
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	tenantID := ctx.GetUint("tenant_id")
	if err := c.Service.CreatePayment(tenantID, &req); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusCreated, req)
}

func (c *PaymentController) Query(ctx *gin.Context) {
	id, _ := strconv.Atoi(ctx.Param("id"))
	payment, err := c.Service.GetStatus(uint(id))
	if err != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "Payment not found"})
		return
	}
	ctx.JSON(http.StatusOK, payment)
}
