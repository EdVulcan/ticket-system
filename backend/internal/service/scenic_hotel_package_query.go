package service

import (
	"strings"

	"ticket-backend/internal/model"

	"gorm.io/gorm"
)

func (s *ScenicHotelPackageService) ListReservations(tenantID, hotelID uint, status, orderNo string, page, pageSize int) (*HotelReservationPage, error) {
	if page < 1 {
		page = 1
	}
	if pageSize != 10 && pageSize != 20 && pageSize != 40 {
		pageSize = 20
	}
	query := hotelReservationViewQuery(tenantID).
		Where("reservation.deleted_at IS NULL AND orders.deleted_at IS NULL")
	if hotelID != 0 {
		query = query.Where("reservation.hotel_id = ?", hotelID)
	}
	if status = strings.TrimSpace(status); status != "" {
		query = query.Where("reservation.status = ?", status)
	}
	if orderNo = strings.TrimSpace(orderNo); orderNo != "" {
		query = query.Where("orders.order_no ILIKE ?", "%"+orderNo+"%")
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, err
	}
	var rows []HotelReservationView
	if err := scanHotelReservationViews(query, (page-1)*pageSize, pageSize, &rows); err != nil {
		return nil, err
	}
	return &HotelReservationPage{Data: rows, Total: total, Page: page, PageSize: pageSize}, nil
}

func (s *ScenicHotelPackageService) ListEntitlements(tenantID, hotelID uint, status, orderNo string, page, pageSize int) (*PackageEntitlementPage, error) {
	if page < 1 {
		page = 1
	}
	if pageSize != 10 && pageSize != 20 && pageSize != 40 {
		pageSize = 20
	}
	query := model.DB.Table("scenic_hotel_package_entitlements AS entitlement").
		Joins("JOIN scenic_hotel_packages AS package ON package.id = entitlement.package_id AND package.tenant_id = entitlement.supplier_tenant_id").
		Joins("JOIN orders ON orders.id = entitlement.order_id AND orders.tenant_id = entitlement.sales_tenant_id").
		Joins("JOIN order_items AS item ON item.id = entitlement.order_item_id AND item.order_id = entitlement.order_id").
		Joins("JOIN hotel_properties AS hotel ON hotel.id = package.hotel_id AND hotel.tenant_id = package.tenant_id").
		Joins("JOIN hotel_room_types AS room ON room.id = package.room_type_id AND room.hotel_id = package.hotel_id").
		Where("entitlement.supplier_tenant_id = ? AND entitlement.deleted_at IS NULL AND orders.status IN ?", tenantID, []string{"paid", "completed", "partial_refunded"})
	if hotelID != 0 {
		query = query.Where("package.hotel_id = ?", hotelID)
	}
	if status = strings.TrimSpace(status); status != "" {
		query = query.Where("entitlement.status = ?", status)
	}
	if orderNo = strings.TrimSpace(orderNo); orderNo != "" {
		query = query.Where("orders.order_no ILIKE ?", "%"+orderNo+"%")
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, err
	}
	var rows []PackageEntitlementView
	if err := query.Select("entitlement.*, orders.order_no, item.product_name, hotel.name AS hotel_name, room.name AS room_type_name").
		Order("entitlement.valid_until ASC, entitlement.created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return &PackageEntitlementPage{Data: rows, Total: total, Page: page, PageSize: pageSize}, nil
}

func hotelReservationViewQuery(tenantID uint) *gorm.DB {
	return model.DB.Table("hotel_reservations AS reservation").
		Joins("JOIN orders ON orders.id = reservation.order_id AND orders.tenant_id = reservation.sales_tenant_id").
		Joins("JOIN order_items ON order_items.id = reservation.order_item_id AND order_items.order_id = reservation.order_id").
		Joins("JOIN tickets ON tickets.id = reservation.ticket_id AND tickets.order_item_id = reservation.order_item_id").
		Where("reservation.supplier_tenant_id = ?", tenantID)
}

func scanHotelReservationViews(query *gorm.DB, offset, limit int, rows *[]HotelReservationView) error {
	return query.Select("reservation.*, orders.order_no, COALESCE(NULLIF(tickets.visitor_name, ''), orders.contact_name) AS guest_name, COALESCE(NULLIF(tickets.visitor_phone, ''), orders.contact_phone) AS contact_phone, order_items.product_name, tickets.ticket_code").
		Order("reservation.check_in_date ASC, reservation.created_at DESC").Offset(offset).Limit(limit).Scan(rows).Error
}

func (s *ScenicHotelPackageService) ExportReservations(tenantID, hotelID uint, status, orderNo string) ([]HotelReservationView, error) {
	query := hotelReservationViewQuery(tenantID).Where("reservation.deleted_at IS NULL AND orders.deleted_at IS NULL")
	if hotelID != 0 {
		query = query.Where("reservation.hotel_id = ?", hotelID)
	}
	if status = strings.TrimSpace(status); status != "" {
		query = query.Where("reservation.status = ?", status)
	}
	if orderNo = strings.TrimSpace(orderNo); orderNo != "" {
		query = query.Where("orders.order_no ILIKE ?", "%"+orderNo+"%")
	}
	var rows []HotelReservationView
	if err := scanHotelReservationViews(query, 0, 10000, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}
