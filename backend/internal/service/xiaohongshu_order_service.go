package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"ticket-backend/internal/xiaohongshu"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	xiaohongshuPaymentMethod       = "xiaohongshu"
	xiaohongshuOrderOperationLease = 30 * time.Second
)

const (
	miniappOrderNotCreatedCode       = "order_not_created"
	miniappOrderPayloadMismatchCode  = "idempotency_payload_mismatch"
	miniappOrderRecoveryRequiredCode = "existing_order_recovery_required"
)

// MiniappOrderCreateError carries a safe, typed outcome for the storefront.
// OrderNo is only populated when an existing order was authenticated in the
// request-id scope; no payment token is included in this error.
type MiniappOrderCreateError struct {
	Code    string
	OrderNo string
	Message string
	cause   error
}

func (e *MiniappOrderCreateError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}

func (e *MiniappOrderCreateError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

type xiaohongshuOrderIntent struct {
	MappingID    uint   `json:"mapping_id"`
	Quantity     int    `json:"quantity"`
	UseDate      string `json:"use_date"`
	GuestName    string `json:"guest_name"`
	ContactPhone string `json:"contact_phone"`
}

var miniappContactPhonePattern = regexp.MustCompile(`^[0-9+()\-\s]{6,20}$`)

func validateMiniappOrderContact(name, phone string) error {
	name = strings.TrimSpace(name)
	phone = strings.TrimSpace(phone)
	if name == "" || len(name) > 50 {
		return errors.New("请填写联系人姓名")
	}
	if phone == "" || len(phone) > 20 || !miniappContactPhonePattern.MatchString(phone) || !strings.ContainsAny(phone, "0123456789") {
		return errors.New("请填写有效的手机号")
	}
	return nil
}

func newMiniappOrderCreateError(code, orderNo, message string) *MiniappOrderCreateError {
	return &MiniappOrderCreateError{Code: code, OrderNo: orderNo, Message: message}
}

func newMiniappOrderCreateErrorWithCause(code, orderNo, message string, cause error) *MiniappOrderCreateError {
	return &MiniappOrderCreateError{Code: code, OrderNo: orderNo, Message: message, cause: cause}
}

func miniappOrderIntentFingerprint(input MiniappOrderCreateInput) string {
	useDate := strings.TrimSpace(input.UseDate)
	if parsed, err := time.ParseInLocation("2006-01-02", useDate, time.Local); err == nil {
		useDate = parsed.Format("2006-01-02")
	}
	intent := xiaohongshuOrderIntent{
		MappingID: input.MappingID, Quantity: input.Quantity, UseDate: useDate,
		GuestName: strings.TrimSpace(input.GuestName), ContactPhone: strings.TrimSpace(input.ContactPhone),
	}
	raw, _ := json.Marshal(intent)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func isMiniappOrderNotCreatedError(err error) bool {
	return errors.Is(err, ErrMiniappPromotionQuote) ||
		errors.Is(err, ErrMiniappPromotionMapping) ||
		errors.Is(err, ErrMiniappPromotionUnavailable)
}

func lockXiaohongshuOrderAccountTx(tx *gorm.DB, tenantID, accountID uint) (*model.ChannelAccount, error) {
	if tx == nil || tenantID == 0 || accountID == 0 {
		return nil, ErrMiniappUnavailable
	}
	var account model.ChannelAccount
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND type = ? AND status IN ?", accountID, tenantID, "xiaohongshu", []string{"active", "sandbox"}).First(&account).Error; err != nil {
		return nil, ErrMiniappUnavailable
	}
	return &account, nil
}

// XiaohongshuOrderService owns the durable ordinary-order operation. The
// storefront facade delegates to it, while the operation itself keeps the
// remote order facts recoverable across process restarts.
type XiaohongshuOrderService struct {
	NewXiaohongshuClient func(appID, secret, environment string) *xiaohongshu.Client
	// EncryptVoucher is a test seam. Production uses EncryptAES through the
	// nil fallback in the issuance service.
	EncryptVoucher func(string) (string, error)
	Now            func() time.Time
}

func (s MiniappService) orderService() XiaohongshuOrderService {
	return XiaohongshuOrderService{
		NewXiaohongshuClient: s.NewXiaohongshuClient,
		Now:                  s.Now,
	}
}

// MiniappService keeps the storefront-facing compatibility surface stable
// while the order implementation lives in its focused module.
func (s MiniappService) ListXiaohongshuOrders(customer *model.MiniappCustomer, page, pageSize int) (*MiniappOrderPage, error) {
	return s.orderService().ListXiaohongshuOrders(customer, page, pageSize)
}

func (s MiniappService) CreateXiaohongshuOrder(ctx context.Context, customer *model.MiniappCustomer, input MiniappOrderCreateInput) (*MiniappOrderResult, error) {
	return s.orderService().CreateXiaohongshuOrder(ctx, customer, input)
}

func (s MiniappService) GetXiaohongshuOrder(ctx context.Context, customer *model.MiniappCustomer, orderNo string) (*MiniappOrderResult, error) {
	return s.orderService().GetXiaohongshuOrder(ctx, customer, orderNo)
}

func (s MiniappService) ProcessPendingXiaohongshuOrders(ctx context.Context, now time.Time, limit int) (int, error) {
	return s.orderService().ProcessPendingXiaohongshuOrders(ctx, now, limit)
}

func (s MiniappService) loadOrderResult(customer *model.MiniappCustomer, requestID string) (*MiniappOrderResult, error) {
	return s.orderService().loadOrderResult(customer, requestID)
}

func (s MiniappService) orderResult(link *model.XiaohongshuOrderLink, order *model.Order, includePayToken bool) (*MiniappOrderResult, error) {
	return s.orderService().orderResult(link, order, includePayToken)
}

func (s MiniappService) failXiaohongshuOrder(link *model.XiaohongshuOrderLink, order *model.Order, message string) {
	s.orderService().failXiaohongshuOrder(link, order, message)
}

func (s XiaohongshuOrderService) ListXiaohongshuOrders(customer *model.MiniappCustomer, page, pageSize int) (*MiniappOrderPage, error) {
	if customer == nil || customer.ID == 0 {
		return nil, ErrMiniappUnauthenticated
	}
	if page < 1 {
		page = 1
	}
	if pageSize != 10 && pageSize != 20 && pageSize != 40 {
		pageSize = 10
	}

	base := model.DB.Table("xiaohongshu_order_links AS link").
		Where("link.miniapp_customer_id = ? AND link.channel_account_id = ? AND link.tenant_id = ? AND link.deleted_at IS NULL", customer.ID, customer.ChannelAccountID, customer.TenantID)
	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, err
	}

	type orderRow struct {
		OrderNo              string
		ProductName          string
		ImageURL             string
		Quantity             int
		TotalAmount          float64
		Status               string
		PlatformPaymentState string
		CreatedAt            time.Time
		ExpiresAt            *time.Time
		PackageID            uint
	}
	var rows []orderRow
	err := base.
		Select(`orders.order_no, item.product_name, COALESCE(xhs_config.image_url, '') AS image_url,
			item.quantity, orders.total_amount, orders.status AS status, link.state AS platform_payment_state,
			orders.created_at, link.pay_token_expires_at AS expires_at,
			COALESCE(hotel_package.id, 0) AS package_id`).
		Joins("JOIN orders ON orders.id = link.order_id AND orders.tenant_id = link.tenant_id AND orders.deleted_at IS NULL").
		Joins("JOIN order_items AS item ON item.order_id = orders.id AND item.deleted_at IS NULL").
		Joins("LEFT JOIN channel_product_mappings AS mapping ON mapping.channel_account_id = link.channel_account_id AND mapping.product_id = item.product_id AND mapping.deleted_at IS NULL").
		Joins("LEFT JOIN xiaohongshu_product_configs AS xhs_config ON xhs_config.channel_product_mapping_id = mapping.id AND xhs_config.tenant_id = link.tenant_id AND xhs_config.deleted_at IS NULL").
		Joins("LEFT JOIN scenic_hotel_packages AS hotel_package ON hotel_package.product_id = item.product_id AND hotel_package.tenant_id = item.fulfillment_tenant_id AND hotel_package.deleted_at IS NULL").
		Order("orders.created_at DESC, orders.id DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	items := make([]MiniappOrderSummary, 0, len(rows))
	for _, row := range rows {
		kind := "ticket"
		if row.PackageID != 0 {
			kind = "scenic_hotel_package"
		}
		items = append(items, MiniappOrderSummary{
			OrderNo: row.OrderNo, ProductName: row.ProductName, ProductKind: kind, ImageURL: row.ImageURL,
			Quantity: row.Quantity, AmountCents: moneyCents(row.TotalAmount), Status: row.Status,
			CoreOrderStatus: row.Status, PlatformPaymentState: row.PlatformPaymentState,
			CreatedAt: row.CreatedAt, ExpiresAt: row.ExpiresAt,
		})
	}
	return &MiniappOrderPage{Items: items, Total: total, Page: page, PageSize: pageSize}, nil
}

func (s XiaohongshuOrderService) GetXiaohongshuOrder(ctx context.Context, customer *model.MiniappCustomer, orderNo string) (*MiniappOrderResult, error) {
	if customer == nil || customer.ID == 0 {
		return nil, ErrMiniappUnauthenticated
	}
	var link model.XiaohongshuOrderLink
	var order model.Order
	if err := model.DB.Table("xiaohongshu_order_links AS link").Select("link.*").
		Joins("JOIN orders AS orders ON orders.id = link.order_id AND orders.tenant_id = link.tenant_id").
		Where("link.miniapp_customer_id = ? AND link.channel_account_id = ? AND link.tenant_id = ? AND orders.order_no = ?", customer.ID, customer.ChannelAccountID, customer.TenantID, strings.TrimSpace(orderNo)).
		First(&link).Error; err != nil {
		return nil, gorm.ErrRecordNotFound
	}
	if err := model.DB.Where("id = ? AND tenant_id = ?", link.OrderID, customer.TenantID).First(&order).Error; err != nil {
		return nil, err
	}
	if (link.State == "paid" && link.VoucherIssuanceStatus != "pending") || link.State == "cancelled" || link.State == "failed" {
		return s.orderResult(&link, &order, false)
	}
	return s.refreshXiaohongshuOrder(ctx, customer, &link, &order)
}

func (s XiaohongshuOrderService) loadOrderResult(customer *model.MiniappCustomer, requestID string) (*MiniappOrderResult, error) {
	var link model.XiaohongshuOrderLink
	if err := model.DB.Where("miniapp_customer_id = ? AND channel_account_id = ? AND tenant_id = ? AND client_request_id = ?", customer.ID, customer.ChannelAccountID, customer.TenantID, requestID).First(&link).Error; err != nil {
		return nil, err
	}
	var order model.Order
	if err := model.DB.Where("id = ? AND tenant_id = ?", link.OrderID, customer.TenantID).First(&order).Error; err != nil {
		return nil, err
	}
	return s.orderResult(&link, &order, link.State == "unpaid")
}

func (s XiaohongshuOrderService) orderResult(link *model.XiaohongshuOrderLink, order *model.Order, includePayToken bool) (*MiniappOrderResult, error) {
	result := &MiniappOrderResult{
		OriginalAmountCents: order.OriginalAmountCents, DiscountCents: order.DiscountCents,
		OrderNo: order.OrderNo, PlatformOrderID: link.PlatformOrderID, AmountCents: moneyCents(order.TotalAmount),
		ContactName: order.ContactName, ContactPhone: order.ContactPhone,
		Status: order.Status, CoreOrderStatus: order.Status, PlatformPaymentState: link.State, VoucherIssuanceStatus: link.VoucherIssuanceStatus, ExpiresAt: link.PayTokenExpiresAt,
	}
	type presentationRow struct {
		ProductName string
		ImageURL    string
		Quantity    int
		PackageID   uint
	}
	var presentation presentationRow
	if err := model.DB.Table("order_items AS item").
		Select("item.product_name, item.quantity, COALESCE(xhs_config.image_url, '') AS image_url, COALESCE(hotel_package.id, 0) AS package_id").
		Joins("LEFT JOIN channel_product_mappings AS mapping ON mapping.channel_account_id = ? AND mapping.product_id = item.product_id AND mapping.deleted_at IS NULL", link.ChannelAccountID).
		Joins("LEFT JOIN xiaohongshu_product_configs AS xhs_config ON xhs_config.channel_product_mapping_id = mapping.id AND xhs_config.tenant_id = ? AND xhs_config.deleted_at IS NULL", link.TenantID).
		Joins("LEFT JOIN scenic_hotel_packages AS hotel_package ON hotel_package.product_id = item.product_id AND hotel_package.tenant_id = item.fulfillment_tenant_id AND hotel_package.deleted_at IS NULL").
		Where("item.order_id = ? AND item.deleted_at IS NULL", order.ID).
		Order("item.id ASC").Limit(1).Scan(&presentation).Error; err != nil {
		return nil, err
	}
	result.ProductName = presentation.ProductName
	result.ImageURL = presentation.ImageURL
	result.Quantity = presentation.Quantity
	result.ProductKind = "ticket"
	if presentation.PackageID != 0 {
		result.ProductKind = "scenic_hotel_package"
		var stay MiniappHotelStay
		if err := model.DB.Model(&model.HotelReservation{}).
			Select("MIN(hotel_name) AS hotel_name, MIN(room_type_name) AS room_type_name, MIN(rate_plan_name) AS rate_plan_name, MIN(check_in_date) AS check_in_date, MAX(check_out_date) AS check_out_date, SUM(rooms) AS rooms").
			Where("order_id = ? AND sales_tenant_id = ? AND status NOT IN ?", order.ID, order.TenantID, []string{"cancelled", "refunded"}).
			Scan(&stay).Error; err != nil {
			return nil, err
		}
		if stay.HotelName != "" {
			var guest struct{ GuestName, ContactPhone string }
			_ = model.DB.Table("hotel_reservations AS reservation").
				Select("COALESCE(ticket.visitor_name, orders.contact_name) AS guest_name, COALESCE(ticket.visitor_phone, orders.contact_phone) AS contact_phone").
				Joins("JOIN tickets AS ticket ON ticket.id = reservation.ticket_id").
				Joins("JOIN orders ON orders.id = reservation.order_id").
				Where("reservation.order_id = ? AND reservation.sales_tenant_id = ? AND reservation.status NOT IN ?", order.ID, order.TenantID, []string{"cancelled", "refunded"}).
				Order("reservation.id ASC").Limit(1).Scan(&guest).Error
			stay.GuestName, stay.ContactPhone = guest.GuestName, guest.ContactPhone
			result.HotelStay = &stay
		}
		type entitlementRow struct {
			EntitlementNo, Status, HotelName, RoomTypeName, GuestName, ContactPhone, PlatformSyncStatus string
			ValidFrom, ValidUntil                                                                       time.Time
			CheckInDate, CheckOutDate                                                                   *time.Time
			RescheduleCount, MaxReschedules, Nights, MinAdvanceDays                                     int
		}
		var entitlements []entitlementRow
		if err := model.DB.Table("scenic_hotel_package_entitlements AS entitlement").
			Select(`entitlement.entitlement_no, entitlement.status, entitlement.valid_from, entitlement.valid_until,
				entitlement.reschedule_count, package.max_reschedules, package.nights, package.min_advance_days, entitlement.platform_sync_status,
				reservation.check_in_date, reservation.check_out_date, reservation.hotel_name, reservation.room_type_name,
				COALESCE(ticket.visitor_name, '') AS guest_name, COALESCE(ticket.visitor_phone, '') AS contact_phone`).
			Joins("JOIN scenic_hotel_packages AS package ON package.id = entitlement.package_id").
			Joins("JOIN tickets AS ticket ON ticket.id = entitlement.ticket_id").
			Joins("LEFT JOIN hotel_reservations AS reservation ON reservation.id = entitlement.reservation_id").
			Where("entitlement.order_id = ? AND entitlement.sales_tenant_id = ?", order.ID, order.TenantID).
			Order("entitlement.id ASC").Scan(&entitlements).Error; err != nil {
			return nil, err
		}
		result.PackageEntitlements = make([]MiniappPackageEntitlement, 0, len(entitlements))
		for _, row := range entitlements {
			result.PackageEntitlements = append(result.PackageEntitlements, MiniappPackageEntitlement{
				EntitlementNo: row.EntitlementNo, Status: row.Status, ValidFrom: row.ValidFrom, ValidUntil: row.ValidUntil,
				CheckInDate: row.CheckInDate, CheckOutDate: row.CheckOutDate, HotelName: row.HotelName,
				RoomTypeName: row.RoomTypeName, GuestName: row.GuestName, ContactPhone: row.ContactPhone,
				RescheduleCount: row.RescheduleCount, MaxReschedules: row.MaxReschedules,
				Nights: row.Nights, MinAdvanceDays: row.MinAdvanceDays,
				PlatformSyncStatus: row.PlatformSyncStatus,
			})
		}
	}
	if err := populateMiniappRefundApplicationProjection(result, link, order); err != nil {
		return nil, err
	}
	if includePayToken && link.PayTokenCiphertext != "" {
		payToken, err := utils.DecryptAES(link.PayTokenCiphertext)
		if err != nil {
			return nil, err
		}
		result.PayToken = payToken
	}
	if link.State == "paid" && link.VoucherIssuanceStatus == "ready" && order.Status != "refunded" && order.Status != "cancelled" {
		var pendingRefunds int64
		if err := model.DB.Model(&model.Refund{}).Where("tenant_id = ? AND order_no = ? AND method = ? AND status IN ?", order.TenantID, order.OrderNo, "xiaohongshu", []string{"pending", "processing", "submitted", "manual_review"}).Count(&pendingRefunds).Error; err != nil {
			return nil, err
		}
		result.RefundPending = pendingRefunds > 0
		if result.RefundPending {
			return result, nil
		}
		var tickets []model.Ticket
		if err := model.DB.Model(&model.Ticket{}).
			Where("order_id = ? AND tenant_id = ? AND status IN ?", order.ID, order.TenantID, []string{"unused", "active", "issued", "used"}).
			Where(`NOT EXISTS (SELECT 1 FROM scenic_hotel_package_entitlements e WHERE e.order_id = ? AND e.deleted_at IS NULL)
				OR EXISTS (SELECT 1 FROM scenic_hotel_package_entitlements e WHERE e.order_id = ? AND e.ticket_id = tickets.id AND e.status = 'booked' AND e.deleted_at IS NULL)`, order.ID, order.ID).
			Order("id ASC").Find(&tickets).Error; err != nil {
			return nil, err
		}
		for _, ticket := range tickets {
			result.TicketCodes = append(result.TicketCodes, ticket.TicketCode)
			result.Tickets = append(result.Tickets, MiniappTicket{Code: ticket.TicketCode, Status: ticket.Status, CheckInCount: ticket.CheckInCount})
		}
	}
	return result, nil
}

func (s XiaohongshuOrderService) failXiaohongshuOrder(link *model.XiaohongshuOrderLink, order *model.Order, message string) {
	_ = model.Write(func(tx *gorm.DB) error {
		return tx.Model(link).Updates(map[string]interface{}{"state": "failed", "last_error": truncateChannelError(message)}).Error
	})
	_ = (&OrderService{}).Cancel(order.OrderNo, order.TenantID)
}

func (s XiaohongshuOrderService) CreateXiaohongshuOrder(ctx context.Context, customer *model.MiniappCustomer, input MiniappOrderCreateInput) (*MiniappOrderResult, error) {
	if customer == nil || customer.ID == 0 {
		return nil, ErrMiniappUnauthenticated
	}
	input.ClientRequestID = strings.TrimSpace(input.ClientRequestID)
	if input.MappingID == 0 || input.Quantity <= 0 || input.Quantity > 100 || input.ClientRequestID == "" || len(input.ClientRequestID) > 100 {
		return nil, errors.New("请选择票种、数量并提供有效的请求编号")
	}
	input.GuestName, input.ContactPhone = strings.TrimSpace(input.GuestName), strings.TrimSpace(input.ContactPhone)
	fingerprint := miniappOrderIntentFingerprint(input)
	var account model.ChannelAccount
	var mapping model.ChannelProductMapping
	var product model.Product
	var config model.XiaohongshuProductConfig
	var operation model.XiaohongshuOrderOperation
	var order model.Order
	var link model.XiaohongshuOrderLink
	duplicate := false
	var duplicateOrder model.Order
	var openID string
	promotions := MiniappPromotionService{Now: s.Now}
	// Persist the order, grant reservation, customer idempotency link and
	// encrypted provider request atomically. External I/O only starts after commit.
	err := model.Write(func(tx *gorm.DB) error {
		lockedAccount, err := lockXiaohongshuOrderAccountTx(tx, customer.TenantID, customer.ChannelAccountID)
		if err != nil {
			return err
		}
		account = *lockedAccount

		var previous model.XiaohongshuOrderLink
		findErr := tx.Where("tenant_id = ? AND channel_account_id = ? AND miniapp_customer_id = ? AND client_request_id = ? AND deleted_at IS NULL", customer.TenantID, account.ID, customer.ID, input.ClientRequestID).First(&previous).Error
		if findErr == nil {
			if err := tx.Preload("Items").Where("id = ? AND tenant_id = ?", previous.OrderID, customer.TenantID).First(&duplicateOrder).Error; err != nil {
				return err
			}
			if err := validateExistingXiaohongshuOrderRequestTx(tx, &previous, &duplicateOrder, fingerprint); err != nil {
				return err
			}
			duplicate = true
			return nil
		}
		if !errors.Is(findErr, gorm.ErrRecordNotFound) {
			return findErr
		}
		if held, err := HasXiaohongshuRefundAccountHoldTx(tx, customer.TenantID, account.ID); err != nil {
			return err
		} else if held {
			return newMiniappOrderCreateErrorWithCause(miniappOrderNotCreatedCode, "", "店铺订单售后核对中，暂不可购买，请稍后重试", ErrXiaohongshuRefundHold)
		}
		if _, err := lockMiniappPromotionCustomerTx(tx, customer); err != nil {
			return err
		}
		if _, err := lockMiniappPromotionActivityTx(tx, customer.TenantID, account.ID); err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND channel_account_id = ? AND status = ?", input.MappingID, account.ID, "active").First(&mapping).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return newMiniappOrderCreateError(miniappOrderNotCreatedCode, "", "票种当前不可购买")
			}
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND status = ?", mapping.ProductID, customer.TenantID, "online").First(&product).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return newMiniappOrderCreateError(miniappOrderNotCreatedCode, "", "票种当前不可购买")
			}
			return err
		}
		if product.ProductKind == "hotel" {
			// Hotel products have no scenic ticket or voucher entitlement. Keep the
			// existing ticket order/Saga path fail-closed until a dedicated hotel
			// order and reservation protocol is enabled for this channel.
			return newMiniappOrderCreateError(miniappOrderNotCreatedCode, "", "酒店产品暂未开放小红书交易，请先完成住宿订单协议联调")
		}
		if product.CodeMode == "order" && input.Quantity > 1 {
			return newMiniappOrderCreateError(miniappOrderNotCreatedCode, "", "该票种为整单一码，小红书暂只支持每单购买一份，请分次下单")
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("channel_product_mapping_id = ? AND tenant_id = ? AND channel_account_id = ? AND sync_status IN ? AND audit_status = ?", mapping.ID, customer.TenantID, account.ID, []string{"submitted", "synced"}, "approved").First(&config).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return newMiniappOrderCreateError(miniappOrderNotCreatedCode, "", "票种尚未通过小红书商品审核")
			}
			return err
		}
		var hotelPackage model.ScenicHotelPackage
		hasHotelPackage := false
		if err := tx.Where("tenant_id = ? AND product_id = ?", customer.TenantID, product.ID).First(&hotelPackage).Error; err == nil {
			hasHotelPackage = true
			if hotelPackage.Status != "online" {
				return newMiniappOrderCreateError(miniappOrderNotCreatedCode, "", "酒景套餐当前不可购买")
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var useDate *time.Time
		if value := strings.TrimSpace(input.UseDate); value != "" {
			parsed, err := time.ParseInLocation("2006-01-02", value, time.Local)
			if err != nil {
				return newMiniappOrderCreateError(miniappOrderNotCreatedCode, "", "请选择有效的使用日期")
			}
			input.UseDate = parsed.Format("2006-01-02")
			useDate = &parsed
		} else {
			input.UseDate = ""
		}
		deferredPackage := hasHotelPackage && hotelPackage.BookingMode == "after_purchase"
		if ((hasHotelPackage && !deferredPackage) || (!hasHotelPackage && product.StockType == "daily")) && useDate == nil {
			if hasHotelPackage {
				return newMiniappOrderCreateError(miniappOrderNotCreatedCode, "", "请选择入住日期")
			}
			return newMiniappOrderCreateError(miniappOrderNotCreatedCode, "", "请选择游玩日期")
		}
		if hasHotelPackage && !deferredPackage && (input.GuestName == "" || input.ContactPhone == "" || len(input.GuestName) > 50 || len(input.ContactPhone) > 20) {
			return newMiniappOrderCreateError(miniappOrderNotCreatedCode, "", "请填写有效的入住人和联系电话")
		}
		totalCents := mapping.ChannelSaleCents * int64(input.Quantity)
		if totalCents <= 0 {
			return newMiniappOrderCreateError(miniappOrderNotCreatedCode, "", "票种售价无效")
		}
		if account.Environment == "sandbox" && totalCents > 10 {
			return newMiniappOrderCreateError(miniappOrderNotCreatedCode, "", "测试小程序单笔订单金额不能超过 0.10 元")
		}
		if contactErr := validateMiniappOrderContact(input.GuestName, input.ContactPhone); contactErr != nil {
			return newMiniappOrderCreateError(miniappOrderNotCreatedCode, "", contactErr.Error())
		}
		// Keep deterministic local qualification errors ahead of credential
		// decryption, so an invalid session cannot hide a price or catalog
		// rejection that the caller can act on.
		openID, err = utils.DecryptAES(customer.OpenIDCiphertext)
		if err != nil || strings.TrimSpace(openID) == "" {
			return ErrMiniappUnauthenticated
		}
		if secret, secretErr := utils.DecryptAES(account.SecretCiphertext); secretErr != nil || strings.TrimSpace(secret) == "" {
			return ErrMiniappUnavailable
		}
		externalID, err := randomXiaohongshuOrderID()
		if err != nil {
			return err
		}
		order = model.Order{
			TenantID: customer.TenantID, Channel: "xiaohongshu", ChannelAccountID: account.ID,
			ExternalNo: &externalID, ContactName: input.GuestName, ContactPhone: input.ContactPhone,
			Items: []model.OrderItem{{ProductID: product.ID, Quantity: input.Quantity, UseDate: useDate}},
		}
		link = model.XiaohongshuOrderLink{
			TenantID: customer.TenantID, ChannelAccountID: account.ID, MiniappCustomerID: customer.ID,
			ClientRequestID: input.ClientRequestID, ExternalOrderID: externalID, State: "creating",
		}
		quote, err := promotions.LoadLockedQuoteTx(tx, customer, mapping.ID, input.Quantity, totalCents)
		if err != nil {
			if isMiniappOrderNotCreatedError(err) {
				return newMiniappOrderCreateError(miniappOrderNotCreatedCode, "", err.Error())
			}
			return err
		}
		if (input.QuoteToken != "" || quote.DiscountCents > 0) && !promotions.ValidateQuoteToken(customer, mapping.ID, input.Quantity, quote, input.QuoteToken) {
			return newMiniappOrderCreateError(miniappOrderNotCreatedCode, "", "价格或立减资格已变化，请确认最新金额后重新提交")
		}
		if err := (&OrderService{}).createTx(tx, &order, func(tx *gorm.DB, created *model.Order) error {
			return applyMiniappPromotionPriceTx(tx, created, quote)
		}); err != nil {
			return err
		}
		if quote.DiscountCents > 0 {
			if err := promotions.ReserveGrantForOrderTx(tx, &order, quote); err != nil {
				return err
			}
		}
		link.OrderID = order.ID
		if err := tx.Create(&link).Error; err != nil {
			return err
		}
		request := xiaohongshu.OrderUpsertRequest{
			ExternalOrderID: externalID, OpenID: openID, Path: miniappPathWithOrder(config.OrderPath, order.OrderNo),
			CreatedAt: order.CreatedAt.Unix(), ExpiresAt: order.ExpiresAt.Unix(),
			Products: []xiaohongshu.OrderProduct{{ExternalProductID: mapping.ExternalCode, ExternalSKUID: config.ExternalSKUID, Count: input.Quantity, SalePrice: order.OriginalAmountCents, RealPrice: moneyCents(order.TotalAmount)}},
			Price:    xiaohongshu.OrderPrice{OrderPrice: moneyCents(order.TotalAmount)},
		}
		if order.DiscountCents > 0 {
			request.Products[0].Discounts = []xiaohongshu.Discount{{Name: "限时随机立减", Price: order.DiscountCents, Count: 1}}
		}
		payloadCiphertext, err := encryptXiaohongshuOrderOperationPayloadWithFingerprint(request, fingerprint, config.ProductType)
		if err != nil {
			return err
		}
		nextAttempt := s.now()
		operation = model.XiaohongshuOrderOperation{
			TenantID: customer.TenantID, ChannelAccountID: account.ID, XiaohongshuOrderLinkID: link.ID,
			RequestPayloadCiphertext: payloadCiphertext, Status: "pending", NextAttemptAt: &nextAttempt,
		}
		return tx.Create(&operation).Error
	})
	if err != nil {
		return nil, err
	}
	if duplicate {
		return s.loadOrderResult(customer, input.ClientRequestID)
	}
	if _, err := s.processXiaohongshuOrderOperation(ctx, operation.ID); err != nil {
		return nil, err
	}
	if err := model.DB.Where("id = ? AND tenant_id = ?", link.ID, customer.TenantID).First(&link).Error; err != nil {
		return nil, err
	}
	if err := model.DB.Where("id = ? AND tenant_id = ?", order.ID, customer.TenantID).First(&order).Error; err != nil {
		return nil, err
	}
	return s.orderResult(&link, &order, link.State == "unpaid")
}

func validateExistingXiaohongshuOrderRequestTx(tx *gorm.DB, link *model.XiaohongshuOrderLink, order *model.Order, fingerprint string) error {
	if tx == nil || link == nil || order == nil || link.OrderID == 0 || order.OrderNo == "" {
		return errors.New("小红书既有订单关联不完整")
	}
	var operation model.XiaohongshuOrderOperation
	err := tx.Where("tenant_id = ? AND channel_account_id = ? AND xiaohongshu_order_link_id = ?", link.TenantID, link.ChannelAccountID, link.ID).First(&operation).Error
	if err == nil {
		payload, decryptErr := decryptXiaohongshuOrderOperationPayload(operation.RequestPayloadCiphertext)
		if decryptErr != nil {
			return newMiniappOrderCreateError(miniappOrderRecoveryRequiredCode, order.OrderNo, "请打开原订单详情核对后继续")
		}
		if payload.Fingerprint != "" {
			if payload.Fingerprint != fingerprint {
				return newMiniappOrderCreateError(miniappOrderPayloadMismatchCode, order.OrderNo, "同一请求编号已用于另一组下单参数，请核对原订单信息")
			}
			return nil
		}
		return newMiniappOrderCreateError(miniappOrderRecoveryRequiredCode, order.OrderNo, "请打开原订单详情核对后继续")
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return newMiniappOrderCreateError(miniappOrderRecoveryRequiredCode, order.OrderNo, "请打开原订单详情核对后继续")
}

func (s XiaohongshuOrderService) refreshXiaohongshuOrder(ctx context.Context, customer *model.MiniappCustomer, link *model.XiaohongshuOrderLink, order *model.Order) (*MiniappOrderResult, error) {
	var account model.ChannelAccount
	if err := model.DB.Where("id = ? AND tenant_id = ? AND type = ? AND status IN ?", link.ChannelAccountID, link.TenantID, "xiaohongshu", []string{"active", "sandbox"}).First(&account).Error; err != nil {
		return nil, ErrMiniappUnavailable
	}
	openID, err := utils.DecryptAES(customer.OpenIDCiphertext)
	if err != nil {
		return nil, ErrMiniappUnauthenticated
	}
	secret, err := utils.DecryptAES(account.SecretCiphertext)
	if err != nil {
		return nil, ErrMiniappUnavailable
	}
	newClient := s.NewXiaohongshuClient
	if newClient == nil {
		newClient = xiaohongshu.NewClient
	}
	platform, err := newClient(account.AppID, secret, account.Environment).GetGuaranteeOrder(ctx, xiaohongshu.GuaranteeOrderRequest{ExternalOrderID: link.ExternalOrderID, OpenID: openID, OrderType: 1})
	if err != nil {
		_ = model.Write(func(tx *gorm.DB) error {
			return tx.Model(link).Updates(map[string]interface{}{"last_queried_at": s.now(), "last_error": truncateChannelError(err.Error())}).Error
		})
		return nil, err
	}
	if link.PlatformOrderID != "" && platform.OrderID != "" && link.PlatformOrderID != platform.OrderID {
		return nil, errors.New("小红书订单编号不匹配")
	}
	switch platform.OrderStatus {
	case 6, 7:
		if platform.PayAmount != moneyCents(order.TotalAmount) {
			return nil, errors.New("小红书支付金额与本地订单不一致")
		}
		if err := s.completeXiaohongshuOrder(link, order, platform); err != nil {
			return nil, err
		}
		if err := model.DB.Where("id = ? AND tenant_id = ?", link.ID, link.TenantID).First(link).Error; err != nil {
			return nil, err
		}
		if err := model.DB.Where("id = ? AND tenant_id = ?", order.ID, order.TenantID).First(order).Error; err != nil {
			return nil, err
		}
		return s.orderResult(link, order, false)
	case 71, 998:
		if err := model.Write(func(tx *gorm.DB) error {
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", link.ID, link.TenantID).First(link).Error; err != nil {
				return err
			}
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Preload("Items.Tickets").Where("id = ? AND tenant_id = ?", order.ID, order.TenantID).First(order).Error; err != nil {
				return err
			}
			if err := cancelOrderTxProvider(tx, order, false, true); err != nil {
				return err
			}
			return tx.Model(link).Updates(map[string]interface{}{"state": "cancelled", "last_queried_at": s.now(), "last_error": ""}).Error
		}); err != nil {
			return nil, err
		}
		link.State, order.Status = "cancelled", "cancelled"
		return s.orderResult(link, order, false)
	default:
		if link.PayTokenExpiresAt != nil && !link.PayTokenExpiresAt.After(s.now()) && order.Status == "unpaid" {
			// A token timeout is not proof of non-payment. Keep inventory and
			// the discount reserved until an authoritative closed/paid result.
			_ = model.Write(func(tx *gorm.DB) error {
				return tx.Model(link).Updates(map[string]interface{}{"last_queried_at": s.now(), "last_error": "支付期限已过，等待平台确认订单最终状态"}).Error
			})
			return s.orderResult(link, order, false)
		}
		_ = model.Write(func(tx *gorm.DB) error {
			return tx.Model(link).Updates(map[string]interface{}{"last_queried_at": s.now(), "last_error": ""}).Error
		})
		return s.orderResult(link, order, true)
	}
}

func (s XiaohongshuOrderService) completeXiaohongshuOrder(link *model.XiaohongshuOrderLink, order *model.Order, platform *xiaohongshu.GuaranteeOrderResponse) error {
	if err := s.recordXiaohongshuPayment(link, order, platform); err != nil {
		return err
	}
	if err := s.issueXiaohongshuVouchers(link, order, platform.Vouchers); err != nil {
		// The provider payment is already durable. Preserve the issuance failure
		// separately for reconciliation instead of rolling payment/order back.
		if persistErr := s.recordXiaohongshuVoucherIssuanceFailure(link, err); persistErr != nil {
			return errors.Join(err, persistErr)
		}
		return err
	}
	return nil
}

func (s XiaohongshuOrderService) recordXiaohongshuPayment(link *model.XiaohongshuOrderLink, order *model.Order, platform *xiaohongshu.GuaranteeOrderResponse) error {
	if link == nil || order == nil || platform == nil {
		return errors.New("xiaohongshu payment completion requires link, order, and provider response")
	}
	return model.Write(func(tx *gorm.DB) error {
		var lockedLink model.XiaohongshuOrderLink
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", link.ID, link.TenantID).First(&lockedLink).Error; err != nil {
			return err
		}
		var lockedOrder model.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", order.ID, order.TenantID).First(&lockedOrder).Error; err != nil {
			return err
		}
		amountCents := moneyCents(lockedOrder.TotalAmount)
		// The HTTP response may predate a concurrent authoritative closure.
		// Never recreate payment after cancellation released stock or a grant.
		if (lockedLink.State != "creating" && lockedLink.State != "unpaid" && lockedLink.State != "paid") ||
			(lockedOrder.Status != "unpaid" && lockedOrder.Status != "paid") {
			return errors.New("小红书订单已结束，拒绝过期的支付查询结果")
		}
		var payment model.Payment
		err := tx.Where("tenant_id = ? AND idempotency_key = ?", lockedOrder.TenantID, fmt.Sprintf("xiaohongshu:%d", lockedLink.ID)).First(&payment).Error
		// Xiaohongshu has no trusted local payment callback: the guarantee-order
		// query is the payment fact. Record that fact first, then settle the core
		// order from all paid/partially-refunded payments. This intentionally
		// differs from Ctrip's markOrderAsPaidTx path, which receives an already
		// persisted payment through its protocol handler.
		if errors.Is(err, gorm.ErrRecordNotFound) {
			now := s.now()
			payment = model.Payment{TenantID: lockedOrder.TenantID, PaymentNo: generatePaymentNo(), IdempotencyKey: fmt.Sprintf("xiaohongshu:%d", lockedLink.ID), OrderNo: lockedOrder.OrderNo, Amount: centsMoney(amountCents), AmountCents: amountCents, Method: xiaohongshuPaymentMethod, PayType: "life_gpay", Status: "paid", TransactionID: platform.TradeNo, PaidAt: &now}
			if err := tx.Create(&payment).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else if payment.AmountCents != amountCents || payment.Status != "paid" || (payment.TransactionID != "" && platform.TradeNo != "" && payment.TransactionID != platform.TradeNo) {
			return errors.New("小红书支付流水与本地记录不一致")
		}
		if err := settleOrderIfFullyPaidTx(tx, &lockedOrder); err != nil {
			return err
		}
		if lockedOrder.PromotionGrantID > 0 {
			if err := (MiniappPromotionService{Now: s.Now}).ConsumeGrantForOrderTx(tx, &lockedOrder); err != nil {
				return err
			}
		}
		return tx.Model(&lockedLink).Updates(map[string]interface{}{"state": "paid", "trade_no": platform.TradeNo, "pay_channel": platform.PayChannel, "last_queried_at": s.now()}).Error
	})
}

func (s XiaohongshuOrderService) issueXiaohongshuVouchers(link *model.XiaohongshuOrderLink, order *model.Order, vouchers []xiaohongshu.VoucherInfo) error {
	if link == nil || order == nil {
		return errors.New("xiaohongshu voucher issuance requires link and order")
	}
	return model.Write(func(tx *gorm.DB) error {
		var lockedLink model.XiaohongshuOrderLink
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND state = ?", link.ID, link.TenantID, "paid").First(&lockedLink).Error; err != nil {
			return err
		}
		var lockedOrder model.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND status = ?", order.ID, order.TenantID, "paid").First(&lockedOrder).Error; err != nil {
			return err
		}
		return s.applyXiaohongshuVoucherIssuanceTx(tx, &lockedLink, &lockedOrder, vouchers)
	})
}

func (s XiaohongshuOrderService) recordXiaohongshuVoucherIssuanceFailure(link *model.XiaohongshuOrderLink, cause error) error {
	if link == nil || cause == nil {
		return nil
	}
	return model.Write(func(tx *gorm.DB) error {
		return tx.Model(&model.XiaohongshuOrderLink{}).
			Where("id = ? AND tenant_id = ? AND state = ? AND voucher_issuance_status = ?", link.ID, link.TenantID, "paid", "pending").
			Updates(map[string]interface{}{
				"voucher_issuance_attempt_count":   gorm.Expr("voucher_issuance_attempt_count + 1"),
				"voucher_issuance_last_attempt_at": s.now(),
				"voucher_issuance_last_error":      truncateChannelError(cause.Error()),
			}).Error
	})
}

func (s XiaohongshuOrderService) ProcessPendingXiaohongshuOrders(ctx context.Context, now time.Time, limit int) (int, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	var operationIDs []uint
	if err := model.DB.Model(&model.XiaohongshuOrderOperation{}).
		Where("status IN ?", []string{"pending", "remote_succeeded"}).
		Where("next_attempt_at IS NULL OR next_attempt_at <= ?", now).
		Order("updated_at ASC, id ASC").Limit(limit).Pluck("id", &operationIDs).Error; err != nil {
		return 0, err
	}
	processed := 0
	for _, operationID := range operationIDs {
		completed, err := s.processXiaohongshuOrderOperation(ctx, operationID)
		if err != nil {
			continue
		}
		if completed {
			processed++
		}
	}
	var links []model.XiaohongshuOrderLink
	if err := model.DB.Table("xiaohongshu_order_links AS link").Select("link.*").
		Where("link.deleted_at IS NULL").
		Where("(link.state IN ? OR (link.state = ? AND link.voucher_issuance_status = ?))", []string{"creating", "unpaid"}, "paid", "pending").
		Where("link.last_queried_at IS NULL OR link.last_queried_at < ?", now.Add(-20*time.Second)).
		Where(`(link.state <> ? OR NOT EXISTS (
			SELECT 1 FROM xiaohongshu_order_operations AS operation
			WHERE operation.xiaohongshu_order_link_id = link.id
				AND operation.tenant_id = link.tenant_id
				AND operation.deleted_at IS NULL
		))`, "creating").
		Order("link.last_queried_at ASC NULLS FIRST, link.id ASC").Limit(limit).Find(&links).Error; err != nil {
		return 0, err
	}
	for i := range links {
		// Advance the durable attempt cursor before any scoped lookup or remote
		// call. A malformed/partial legacy row must not occupy every batch.
		if err := s.markXiaohongshuOrderQueryAttempt(&links[i], now); err != nil {
			continue
		}
		if links[i].State == "creating" {
			// A durable operation owns every new create attempt. Never cancel its
			// local order merely because the link is still creating: the remote
			// call may have succeeded and the operation may only need a retry.
			var operation model.XiaohongshuOrderOperation
			if err := model.DB.Where("xiaohongshu_order_link_id = ? AND tenant_id = ?", links[i].ID, links[i].TenantID).First(&operation).Error; err == nil {
				continue
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			if links[i].CreatedAt.Before(now.Add(-5 * time.Minute)) {
				var order model.Order
				if model.DB.Where("id = ? AND tenant_id = ?", links[i].OrderID, links[i].TenantID).First(&order).Error == nil && order.Status == "unpaid" {
					_ = (&OrderService{}).Cancel(order.OrderNo, order.TenantID)
				}
				_ = model.Write(func(tx *gorm.DB) error {
					return tx.Model(&links[i]).Updates(map[string]interface{}{"state": "failed", "last_error": "创建小红书订单超时"}).Error
				})
			}
			continue
		}
		var customer model.MiniappCustomer
		var order model.Order
		if model.DB.Where("id = ? AND tenant_id = ?", links[i].MiniappCustomerID, links[i].TenantID).First(&customer).Error != nil || model.DB.Where("id = ? AND tenant_id = ?", links[i].OrderID, links[i].TenantID).First(&order).Error != nil {
			continue
		}
		if _, err := s.refreshXiaohongshuOrder(ctx, &customer, &links[i], &order); err != nil {
			continue
		}
		processed++
	}
	return processed, nil
}

func (s XiaohongshuOrderService) markXiaohongshuOrderQueryAttempt(link *model.XiaohongshuOrderLink, attemptedAt time.Time) error {
	if link == nil || link.ID == 0 || link.TenantID == 0 {
		return errors.New("xiaohongshu order link is required")
	}
	return model.Write(func(tx *gorm.DB) error {
		return tx.Model(&model.XiaohongshuOrderLink{}).
			Where("id = ? AND tenant_id = ? AND (last_queried_at IS NULL OR last_queried_at < ?)", link.ID, link.TenantID, attemptedAt).
			Update("last_queried_at", attemptedAt).Error
	})
}

func (s XiaohongshuOrderService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

type xiaohongshuOrderOperationPayload struct {
	Request     xiaohongshu.OrderUpsertRequest `json:"request"`
	ProductType int                            `json:"product_type"`
	Fingerprint string                         `json:"fingerprint,omitempty"`
}

func encryptXiaohongshuOrderOperationPayload(request xiaohongshu.OrderUpsertRequest, productTypes ...int) (string, error) {
	return encryptXiaohongshuOrderOperationPayloadWithFingerprint(request, "", productTypes...)
}

func encryptXiaohongshuOrderOperationPayloadWithFingerprint(request xiaohongshu.OrderUpsertRequest, fingerprint string, productTypes ...int) (string, error) {
	productType := 0
	if len(productTypes) == 1 {
		productType = productTypes[0]
	}
	raw, err := json.Marshal(xiaohongshuOrderOperationPayload{Request: request, ProductType: productType, Fingerprint: fingerprint})
	if err != nil {
		return "", err
	}
	return utils.EncryptAES(string(raw))
}

func decryptXiaohongshuOrderOperationPayload(ciphertext string) (*xiaohongshuOrderOperationPayload, error) {
	raw, err := utils.DecryptAES(ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decrypt xiaohongshu order operation: %w", err)
	}
	var payload xiaohongshuOrderOperationPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, fmt.Errorf("decode xiaohongshu order operation: %w", err)
	}
	if strings.TrimSpace(payload.Request.ExternalOrderID) == "" || strings.TrimSpace(payload.Request.OpenID) == "" || payload.Request.Price.OrderPrice <= 0 {
		return nil, errors.New("xiaohongshu order operation payload is incomplete")
	}
	return &payload, nil
}

func (s XiaohongshuOrderService) processXiaohongshuOrderOperation(ctx context.Context, operationID uint) (bool, error) {
	for step := 0; step < 3; step++ {
		operation, claimed, err := s.claimXiaohongshuOrderOperation(operationID)
		if err != nil || !claimed {
			return false, err
		}
		var completed bool
		switch operation.Status {
		case "pending":
			completed, err = s.executeXiaohongshuOrderRemoteStep(ctx, operation)
		case "remote_succeeded":
			completed, err = s.finalizeXiaohongshuOrderOperation(operation)
		default:
			return operation.Status == "completed", nil
		}
		if err != nil {
			if persistErr := s.deferXiaohongshuOrderOperation(operation, err); persistErr != nil {
				return false, errors.Join(err, persistErr)
			}
			return false, err
		}
		if completed {
			return true, nil
		}
	}
	return false, nil
}

func (s XiaohongshuOrderService) claimXiaohongshuOrderOperation(operationID uint) (*model.XiaohongshuOrderOperation, bool, error) {
	var operation model.XiaohongshuOrderOperation
	claimed := false
	now := s.now()
	err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", operationID).First(&operation).Error; err != nil {
			return err
		}
		if operation.Status == "completed" || (operation.NextAttemptAt != nil && operation.NextAttemptAt.After(now)) {
			return nil
		}
		leaseUntil := now.Add(xiaohongshuOrderOperationLease)
		if err := tx.Model(&operation).Update("next_attempt_at", leaseUntil).Error; err != nil {
			return err
		}
		operation.NextAttemptAt = &leaseUntil
		claimed = true
		return nil
	})
	return &operation, claimed, err
}

func (s XiaohongshuOrderService) executeXiaohongshuOrderRemoteStep(ctx context.Context, operation *model.XiaohongshuOrderOperation) (bool, error) {
	payload, err := decryptXiaohongshuOrderOperationPayload(operation.RequestPayloadCiphertext)
	if err != nil {
		return false, err
	}
	var account model.ChannelAccount
	if err := model.DB.Where("id = ? AND tenant_id = ? AND type = ? AND status IN ?", operation.ChannelAccountID, operation.TenantID, "xiaohongshu", []string{"active", "sandbox"}).First(&account).Error; err != nil {
		return false, ErrMiniappUnavailable
	}
	secret, err := utils.DecryptAES(account.SecretCiphertext)
	if err != nil || strings.TrimSpace(secret) == "" {
		return false, ErrMiniappUnavailable
	}
	newClient := s.NewXiaohongshuClient
	if newClient == nil {
		newClient = NewMiniappService().NewXiaohongshuClient
	}
	response, err := newClient(account.AppID, secret, account.Environment).UpsertOrder(ctx, payload.Request)
	if err != nil {
		return false, err
	}
	if response.FinalPrice != payload.Request.Price.OrderPrice || response.OpenPayType != "life_gpay" || strings.TrimSpace(response.OrderID) == "" || strings.TrimSpace(response.PayToken) == "" {
		return false, errors.New("xiaohongshu order amount, payment type, or payment token validation failed")
	}
	expiresAt := time.Unix(payload.Request.ExpiresAt, 0)
	if response.ExpiresAt > 0 {
		expiresAt = time.Unix(response.ExpiresAt, 0)
	}
	payTokenCiphertext, err := utils.EncryptAES(response.PayToken)
	if err != nil {
		return false, err
	}
	nextAttempt := s.now()
	result := model.DB.Model(&model.XiaohongshuOrderOperation{}).Where("id = ? AND status = ?", operation.ID, "pending").Updates(map[string]interface{}{
		"status": "remote_succeeded", "platform_order_id": response.OrderID, "pay_token_ciphertext": payTokenCiphertext,
		"pay_token_expires_at": expiresAt, "last_error": "", "next_attempt_at": nextAttempt,
	})
	if result.Error != nil {
		return false, result.Error
	}
	return false, nil
}

func (s XiaohongshuOrderService) finalizeXiaohongshuOrderOperation(operation *model.XiaohongshuOrderOperation) (bool, error) {
	if operation == nil || operation.PlatformOrderID == "" || operation.PayTokenCiphertext == "" || operation.PayTokenExpiresAt == nil {
		return false, errors.New("xiaohongshu remote order result is incomplete")
	}
	now := s.now()
	err := model.Write(func(tx *gorm.DB) error {
		var lockedOperation model.XiaohongshuOrderOperation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND status = ?", operation.ID, operation.TenantID, "remote_succeeded").First(&lockedOperation).Error; err != nil {
			return err
		}
		var link model.XiaohongshuOrderLink
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", lockedOperation.XiaohongshuOrderLinkID, lockedOperation.TenantID).First(&link).Error; err != nil {
			return err
		}
		if link.ChannelAccountID != lockedOperation.ChannelAccountID || (link.PlatformOrderID != "" && link.PlatformOrderID != lockedOperation.PlatformOrderID) {
			return errors.New("xiaohongshu order operation ownership or platform order mismatch")
		}
		if link.State != "creating" && link.State != "unpaid" && link.State != "paid" {
			return fmt.Errorf("cannot finalize xiaohongshu order link in state %s", link.State)
		}
		var order model.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", link.OrderID, lockedOperation.TenantID).First(&order).Error; err != nil {
			return err
		}
		if order.Status != "unpaid" && order.Status != "paid" {
			return fmt.Errorf("cannot finalize xiaohongshu order in state %s", order.Status)
		}
		if order.Status == "unpaid" {
			if err := tx.Model(&order).Where("id = ? AND status = ?", order.ID, "unpaid").Update("expires_at", lockedOperation.PayTokenExpiresAt).Error; err != nil {
				return err
			}
		}
		if link.State != "paid" {
			if err := tx.Model(&link).Updates(map[string]interface{}{
				"platform_order_id": lockedOperation.PlatformOrderID, "pay_token_ciphertext": lockedOperation.PayTokenCiphertext,
				"pay_token_expires_at": lockedOperation.PayTokenExpiresAt, "state": "unpaid", "last_error": "",
			}).Error; err != nil {
				return err
			}
		}
		return tx.Model(&lockedOperation).Updates(map[string]interface{}{"status": "completed", "next_attempt_at": nil, "last_error": "", "completed_at": now}).Error
	})
	return err == nil, err
}

func (s XiaohongshuOrderService) deferXiaohongshuOrderOperation(operation *model.XiaohongshuOrderOperation, cause error) error {
	if operation == nil {
		return errors.New("xiaohongshu order operation is required")
	}
	nextAttempt := s.now().Add(20 * time.Second)
	return model.Write(func(tx *gorm.DB) error {
		return tx.Model(&model.XiaohongshuOrderOperation{}).Where("id = ? AND status IN ?", operation.ID, []string{"pending", "remote_succeeded"}).Updates(map[string]interface{}{
			"attempt_count": gorm.Expr("attempt_count + 1"), "last_error": truncateChannelError(cause.Error()), "next_attempt_at": nextAttempt,
		}).Error
	})
}
