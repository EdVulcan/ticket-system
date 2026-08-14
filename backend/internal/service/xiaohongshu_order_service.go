package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// XiaohongshuOrderService owns the durable ordinary-order operation. The
// storefront facade delegates to it, while the operation itself keeps the
// remote order facts recoverable across process restarts.
type XiaohongshuOrderService struct {
	NewXiaohongshuClient func(appID, secret, environment string) *xiaohongshu.Client
	Now                  func() time.Time
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
	if link.State == "paid" || link.State == "cancelled" || link.State == "failed" {
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
		OrderNo: order.OrderNo, PlatformOrderID: link.PlatformOrderID, AmountCents: moneyCents(order.TotalAmount),
		Status: order.Status, CoreOrderStatus: order.Status, PlatformPaymentState: link.State, ExpiresAt: link.PayTokenExpiresAt,
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
	if includePayToken && link.PayTokenCiphertext != "" {
		payToken, err := utils.DecryptAES(link.PayTokenCiphertext)
		if err != nil {
			return nil, err
		}
		result.PayToken = payToken
	}
	if link.State == "paid" && order.Status != "refunded" && order.Status != "cancelled" {
		if err := model.DB.Model(&model.Ticket{}).
			Where("order_id = ? AND tenant_id = ? AND status IN ?", order.ID, order.TenantID, []string{"unused", "active", "issued", "used"}).
			Where(`NOT EXISTS (SELECT 1 FROM scenic_hotel_package_entitlements e WHERE e.order_id = ? AND e.deleted_at IS NULL)
				OR EXISTS (SELECT 1 FROM scenic_hotel_package_entitlements e WHERE e.order_id = ? AND e.ticket_id = tickets.id AND e.status = 'booked' AND e.deleted_at IS NULL)`, order.ID, order.ID).
			Order("id ASC").Pluck("ticket_code", &result.TicketCodes).Error; err != nil {
			return nil, err
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
	if existing, err := s.loadOrderResult(customer, input.ClientRequestID); err == nil {
		return existing, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	var account model.ChannelAccount
	var mapping model.ChannelProductMapping
	var product model.Product
	var config model.XiaohongshuProductConfig
	if err := model.DB.Where("id = ? AND tenant_id = ? AND type = ? AND status IN ?", customer.ChannelAccountID, customer.TenantID, "xiaohongshu", []string{"active", "sandbox"}).First(&account).Error; err != nil {
		return nil, ErrMiniappUnavailable
	}
	if err := model.DB.Where("id = ? AND channel_account_id = ? AND status = ?", input.MappingID, account.ID, "active").First(&mapping).Error; err != nil {
		return nil, errors.New("票种当前不可购买")
	}
	if err := model.DB.Where("id = ? AND tenant_id = ? AND status = ?", mapping.ProductID, customer.TenantID, "online").First(&product).Error; err != nil {
		return nil, errors.New("票种当前不可购买")
	}
	if err := model.DB.Where("channel_product_mapping_id = ? AND tenant_id = ? AND channel_account_id = ? AND sync_status = ?", mapping.ID, customer.TenantID, account.ID, "synced").First(&config).Error; err != nil {
		return nil, errors.New("票种尚未完成小红书商品同步")
	}
	var hotelPackage model.ScenicHotelPackage
	hasHotelPackage := false
	if err := model.DB.Where("tenant_id = ? AND product_id = ?", customer.TenantID, product.ID).First(&hotelPackage).Error; err == nil {
		hasHotelPackage = true
		if hotelPackage.Status != "online" {
			return nil, errors.New("酒景套餐当前不可购买")
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	var useDate *time.Time
	if value := strings.TrimSpace(input.UseDate); value != "" {
		parsed, err := time.ParseInLocation("2006-01-02", value, time.Local)
		if err != nil {
			return nil, errors.New("请选择有效的使用日期")
		}
		useDate = &parsed
	}
	deferredPackage := hasHotelPackage && hotelPackage.BookingMode == "after_purchase"
	if ((hasHotelPackage && !deferredPackage) || (!hasHotelPackage && product.StockType == "daily")) && useDate == nil {
		if hasHotelPackage {
			return nil, errors.New("请选择入住日期")
		}
		return nil, errors.New("请选择游玩日期")
	}
	input.GuestName, input.ContactPhone = strings.TrimSpace(input.GuestName), strings.TrimSpace(input.ContactPhone)
	if hasHotelPackage && !deferredPackage && (input.GuestName == "" || input.ContactPhone == "" || len(input.GuestName) > 50 || len(input.ContactPhone) > 20) {
		return nil, errors.New("请填写有效的入住人和联系电话")
	}
	totalCents := mapping.ChannelSaleCents * int64(input.Quantity)
	if totalCents <= 0 {
		return nil, errors.New("票种售价无效")
	}
	if account.Environment == "sandbox" && totalCents > 10 {
		return nil, errors.New("测试小程序单笔订单金额不能超过 0.10 元")
	}
	externalID, err := randomXiaohongshuOrderID()
	if err != nil {
		return nil, err
	}
	order := model.Order{
		TenantID: customer.TenantID, Channel: "xiaohongshu", ChannelAccountID: account.ID,
		ExternalNo: &externalID, ContactName: input.GuestName, ContactPhone: input.ContactPhone,
		Items: []model.OrderItem{{ProductID: product.ID, Quantity: input.Quantity, UseDate: useDate}},
	}
	if err := (&OrderService{}).Create(&order); err != nil {
		return nil, err
	}
	link := model.XiaohongshuOrderLink{
		TenantID: customer.TenantID, ChannelAccountID: account.ID, MiniappCustomerID: customer.ID,
		OrderID: order.ID, ClientRequestID: input.ClientRequestID, ExternalOrderID: externalID, State: "creating",
	}
	if err := model.Write(func(tx *gorm.DB) error { return tx.Create(&link).Error }); err != nil {
		_ = (&OrderService{}).Cancel(order.OrderNo, customer.TenantID)
		if existing, findErr := s.loadOrderResult(customer, input.ClientRequestID); findErr == nil {
			return existing, nil
		}
		return nil, err
	}

	openID, err := utils.DecryptAES(customer.OpenIDCiphertext)
	if secret, secretErr := utils.DecryptAES(account.SecretCiphertext); secretErr != nil || strings.TrimSpace(secret) == "" {
		s.failXiaohongshuOrder(&link, &order, "xiaohongshu channel secret is unavailable")
		return nil, ErrMiniappUnavailable
	}
	if err != nil || strings.TrimSpace(openID) == "" {
		s.failXiaohongshuOrder(&link, &order, "小程序用户身份解密失败")
		return nil, ErrMiniappUnauthenticated
	}
	expiresAt := s.now().Add(DefaultOrderReservationTTL)
	request := xiaohongshu.OrderUpsertRequest{
		ExternalOrderID: externalID, OpenID: openID, Path: miniappPathWithOrder(config.OrderPath, order.OrderNo),
		CreatedAt: s.now().Unix(), ExpiresAt: expiresAt.Unix(),
		Products: []xiaohongshu.OrderProduct{{ExternalProductID: mapping.ExternalCode, ExternalSKUID: config.ExternalSKUID, Count: input.Quantity, SalePrice: mapping.ChannelSaleCents, RealPrice: totalCents}},
		Price:    xiaohongshu.OrderPrice{OrderPrice: totalCents},
	}
	payloadCiphertext, err := encryptXiaohongshuOrderOperationPayload(request)
	if err != nil {
		s.failXiaohongshuOrder(&link, &order, "xiaohongshu order request encryption failed")
		return nil, err
	}
	nextAttempt := s.now()
	operation := model.XiaohongshuOrderOperation{
		TenantID: customer.TenantID, ChannelAccountID: account.ID, XiaohongshuOrderLinkID: link.ID,
		RequestPayloadCiphertext: payloadCiphertext, Status: "pending", NextAttemptAt: &nextAttempt,
	}
	if err := model.Write(func(tx *gorm.DB) error { return tx.Create(&operation).Error }); err != nil {
		s.failXiaohongshuOrder(&link, &order, err.Error())
		return nil, err
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
		link.State = "paid"
		order.Status = "paid"
		return s.orderResult(link, order, false)
	case 71, 998:
		if order.Status == "unpaid" {
			if err := (&OrderService{}).Cancel(order.OrderNo, order.TenantID); err != nil {
				return nil, err
			}
		}
		_ = model.Write(func(tx *gorm.DB) error {
			return tx.Model(link).Updates(map[string]interface{}{"state": "cancelled", "last_queried_at": s.now(), "last_error": ""}).Error
		})
		link.State = "cancelled"
		order.Status = "cancelled"
		return s.orderResult(link, order, false)
	default:
		if link.PayTokenExpiresAt != nil && !link.PayTokenExpiresAt.After(s.now()) && order.Status == "unpaid" {
			if err := (&OrderService{}).Cancel(order.OrderNo, order.TenantID); err != nil {
				return nil, err
			}
			_ = model.Write(func(tx *gorm.DB) error {
				return tx.Model(link).Updates(map[string]interface{}{"state": "cancelled", "last_queried_at": s.now(), "last_error": ""}).Error
			})
			link.State = "cancelled"
			order.Status = "cancelled"
			return s.orderResult(link, order, false)
		}
		_ = model.Write(func(tx *gorm.DB) error {
			return tx.Model(link).Updates(map[string]interface{}{"last_queried_at": s.now(), "last_error": ""}).Error
		})
		return s.orderResult(link, order, true)
	}
}

func (s XiaohongshuOrderService) completeXiaohongshuOrder(link *model.XiaohongshuOrderLink, order *model.Order, platform *xiaohongshu.GuaranteeOrderResponse) error {
	return model.Write(func(tx *gorm.DB) error {
		var lockedLink model.XiaohongshuOrderLink
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", link.ID, link.TenantID).First(&lockedLink).Error; err != nil {
			return err
		}
		var lockedOrder model.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", order.ID, order.TenantID).First(&lockedOrder).Error; err != nil {
			return err
		}
		if lockedLink.State == "paid" && lockedOrder.Status == "paid" {
			return nil
		}
		amountCents := moneyCents(lockedOrder.TotalAmount)
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
		voucherError := ""
		if len(platform.Vouchers) > 0 {
			var tickets []model.Ticket
			if err := tx.Where("order_id = ? AND tenant_id = ?", lockedOrder.ID, lockedOrder.TenantID).Order("id ASC").Find(&tickets).Error; err != nil {
				return err
			}
			if len(tickets) != len(platform.Vouchers) {
				voucherError = fmt.Sprintf("小红书券码数量 %d 与本地票数 %d 不一致", len(platform.Vouchers), len(tickets))
			} else {
				for index, voucher := range platform.Vouchers {
					ciphertext, err := utils.EncryptAES(voucher.Code)
					if err != nil {
						return err
					}
					row := model.XiaohongshuVoucherLink{TenantID: lockedOrder.TenantID, ChannelAccountID: lockedLink.ChannelAccountID, XiaohongshuOrderLinkID: lockedLink.ID, TicketID: tickets[index].ID, VoucherCodeHash: hashMiniappValue(voucher.Code), VoucherCodeCiphertext: ciphertext, Status: voucher.Status}
					if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "ticket_id"}}, DoUpdates: clause.AssignmentColumns([]string{"voucher_code_hash", "voucher_code_ciphertext", "status", "updated_at"})}).Create(&row).Error; err != nil {
						return err
					}
				}
			}
		}
		return tx.Model(&lockedLink).Updates(map[string]interface{}{"state": "paid", "trade_no": platform.TradeNo, "pay_channel": platform.PayChannel, "last_queried_at": s.now(), "last_error": voucherError}).Error
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
	if err := model.DB.Where("state IN ?", []string{"creating", "unpaid"}).Where("last_queried_at IS NULL OR last_queried_at < ?", now.Add(-20*time.Second)).Order("id ASC").Limit(limit).Find(&links).Error; err != nil {
		return 0, err
	}
	for i := range links {
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

func (s XiaohongshuOrderService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

type xiaohongshuOrderOperationPayload struct {
	Request xiaohongshu.OrderUpsertRequest `json:"request"`
}

func encryptXiaohongshuOrderOperationPayload(request xiaohongshu.OrderUpsertRequest) (string, error) {
	raw, err := json.Marshal(xiaohongshuOrderOperationPayload{Request: request})
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
