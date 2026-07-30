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
	var body struct {
		OrderNo  string `json:"order_no" binding:"required"`
		Method   string `json:"method" binding:"required"`
		PayType  string `json:"pay_type"`
		AuthCode string `json:"auth_code"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req := model.Payment{OrderNo: body.OrderNo, Method: body.Method, PayType: body.PayType, AuthCode: body.AuthCode}

	tenantID := ctx.GetUint("tenant_id")
	if err := c.Service.CreatePayment(tenantID, &req); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusCreated, req)
}

func (c *PaymentController) Query(ctx *gin.Context) {
	id, _ := strconv.Atoi(ctx.Param("id"))
	payment, err := c.Service.GetStatus(uint(id), ctx.GetUint("tenant_id"))
	if err != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "Payment not found"})
		return
	}
	ctx.JSON(http.StatusOK, payment)
}
