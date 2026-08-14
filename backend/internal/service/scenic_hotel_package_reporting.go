package service

import (
	"errors"
	"strings"
	"time"

	"ticket-backend/internal/model"
)

func (s *ScenicHotelPackageService) BusinessSummary(tenantID, hotelID uint, startDate, endDate string) (*HotelPackageBusinessSummary, error) {
	start, end, err := hotelReportWindow(startDate, endDate)
	if err != nil {
		return nil, err
	}
	var result HotelPackageBusinessSummary
	salesQuery := `
		WITH paid_orders AS (
			SELECT orders.id,
			       COALESCE(MAX(COALESCE(payments.paid_at, payments.created_at)), orders.created_at) AS sold_at
			FROM orders
			LEFT JOIN payments ON payments.tenant_id = orders.tenant_id
			 AND payments.order_no = orders.order_no
			 AND payments.status IN ('paid','partial_refunded','refunded')
			 AND payments.purpose IN ('','order')
			WHERE orders.deleted_at IS NULL AND orders.environment = 'production'
			  AND orders.status IN ('paid','completed','partial_refunded','refunded')
			GROUP BY orders.id, orders.created_at
		), package_facts AS (
			SELECT entitlement.order_item_id, entitlement.package_id
			FROM scenic_hotel_package_entitlements AS entitlement
			WHERE entitlement.supplier_tenant_id = ? AND entitlement.deleted_at IS NULL
			UNION
			SELECT reservation.order_item_id, reservation.package_id
			FROM hotel_reservations AS reservation
			WHERE reservation.supplier_tenant_id = ? AND reservation.deleted_at IS NULL
		), package_sales AS (
			SELECT item.id AS order_item_id, item.quantity, item.price, item.settlement_price,
			       item.product_revision_id, item.fulfillment_product_id, item.fulfillment_tenant_id,
			       package.hotel_settlement_price_cents,
			       COALESCE(ticket.refunded_units, 0) AS refunded_units
			FROM paid_orders AS paid
			JOIN order_items AS item ON item.order_id = paid.id AND item.deleted_at IS NULL
			JOIN package_facts AS fact ON fact.order_item_id = item.id
			JOIN scenic_hotel_packages AS package
			  ON package.id = fact.package_id
			 AND package.tenant_id = item.fulfillment_tenant_id
			LEFT JOIN LATERAL (
				SELECT COUNT(*) FILTER (WHERE tickets.status = 'refunded') AS refunded_units
				FROM tickets WHERE tickets.order_item_id = item.id AND tickets.deleted_at IS NULL
			) AS ticket ON TRUE
			WHERE item.fulfillment_tenant_id = ? AND paid.sold_at >= ? AND paid.sold_at < ?
			  AND (? = 0 OR package.hotel_id = ?)
		)
		SELECT
			COALESCE(SUM(sale.quantity), 0) AS sales_units,
			COALESCE(SUM(sale.quantity), 0) AS package_units,
			COALESCE(SUM(sale.refunded_units), 0) AS refunded_units,
			COALESCE(SUM(CAST(ROUND(sale.price * 100.0) AS BIGINT) * sale.quantity), 0) AS gross_sales_cents,
			COALESCE(SUM(CAST(ROUND(sale.price * 100.0) AS BIGINT) * sale.refunded_units), 0) AS refunded_sales_cents,
			COALESCE(SUM(CAST(ROUND(sale.price * 100.0) AS BIGINT) * (sale.quantity - sale.refunded_units)), 0) AS net_sales_cents,
			COALESCE(SUM(COALESCE(revision.settlement_cents, CAST(ROUND(sale.settlement_price * 100.0) AS BIGINT)) * (sale.quantity - sale.refunded_units)), 0) AS ticket_component_net_cents,
			COALESCE(SUM(sale.hotel_settlement_price_cents * (sale.quantity - sale.refunded_units)), 0) AS hotel_component_net_cents
		FROM package_sales AS sale
		LEFT JOIN product_revisions AS revision ON revision.id = sale.product_revision_id
		 AND revision.product_id = sale.fulfillment_product_id AND revision.tenant_id = sale.fulfillment_tenant_id`
	if err := model.DB.Raw(salesQuery, tenantID, tenantID, tenantID, start, end, hotelID, hotelID).Scan(&result).Error; err != nil {
		return nil, err
	}
	reservationQuery := `
		SELECT
			COUNT(*) AS booking_units,
			COUNT(*) FILTER (WHERE reservation.status = 'reserved') AS pending_units,
			COUNT(*) FILTER (WHERE reservation.status = 'confirmed') AS confirmed_units,
			COUNT(*) FILTER (WHERE reservation.status = 'checked_in') AS checked_in_units,
			COUNT(*) FILTER (WHERE reservation.status = 'checked_out') AS checked_out_units,
			COUNT(*) FILTER (WHERE reservation.status = 'no_show') AS no_show_units
		FROM hotel_reservations AS reservation
		WHERE reservation.supplier_tenant_id = ? AND reservation.deleted_at IS NULL
		  AND reservation.created_at >= ? AND reservation.created_at < ?
		  AND (? = 0 OR reservation.hotel_id = ?)`
	var bookingFacts struct {
		BookingUnits    int64
		PendingUnits    int64
		ConfirmedUnits  int64
		CheckedInUnits  int64
		CheckedOutUnits int64
		NoShowUnits     int64
	}
	if err := model.DB.Raw(reservationQuery, tenantID, start, end, hotelID, hotelID).Scan(&bookingFacts).Error; err != nil {
		return nil, err
	}
	result.BookingUnits, result.PendingUnits, result.ConfirmedUnits = bookingFacts.BookingUnits, bookingFacts.PendingUnits, bookingFacts.ConfirmedUnits
	result.CheckedInUnits, result.CheckedOutUnits, result.NoShowUnits = bookingFacts.CheckedInUnits, bookingFacts.CheckedOutUnits, bookingFacts.NoShowUnits
	stayQuery := `
		SELECT COUNT(*) AS stay_units
		FROM hotel_reservations AS reservation
		WHERE reservation.supplier_tenant_id = ? AND reservation.deleted_at IS NULL
		  AND reservation.status NOT IN ('cancelled','refunded')
		  AND reservation.check_in_date >= ? AND reservation.check_in_date < ?
		  AND (? = 0 OR reservation.hotel_id = ?)`
	var stayFacts struct{ StayUnits int64 }
	if err := model.DB.Raw(stayQuery, tenantID, start, end, hotelID, hotelID).Scan(&stayFacts).Error; err != nil {
		return nil, err
	}
	result.StayUnits = stayFacts.StayUnits
	if err := model.DB.Table("scenic_hotel_package_entitlements AS entitlement").
		Joins("JOIN scenic_hotel_packages AS package ON package.id = entitlement.package_id").
		Joins("JOIN orders ON orders.id = entitlement.order_id AND orders.tenant_id = entitlement.sales_tenant_id").
		Where("entitlement.supplier_tenant_id = ? AND entitlement.status = ? AND orders.status IN ?", tenantID, "pending_booking", []string{"paid", "completed", "partial_refunded"}).
		Where(`COALESCE((
			SELECT MAX(COALESCE(payment.paid_at, payment.created_at))
			FROM payments AS payment
			WHERE payment.tenant_id = orders.tenant_id AND payment.order_no = orders.order_no
			  AND payment.status IN ('paid','partial_refunded','refunded')
			  AND payment.purpose IN ('','order')
		), orders.created_at) >= ?`, start).
		Where(`COALESCE((
			SELECT MAX(COALESCE(payment.paid_at, payment.created_at))
			FROM payments AS payment
			WHERE payment.tenant_id = orders.tenant_id AND payment.order_no = orders.order_no
			  AND payment.status IN ('paid','partial_refunded','refunded')
			  AND payment.purpose IN ('','order')
		), orders.created_at) < ?`, end).
		Where("? = 0 OR package.hotel_id = ?", hotelID, hotelID).Distinct("entitlement.id").Count(&result.AwaitingBookingUnits).Error; err != nil {
		return nil, err
	}
	result.UnallocatedMarginCents = result.NetSalesCents - result.TicketComponentNetCents - result.HotelComponentNetCents
	return &result, nil
}

func hotelReportWindow(startDate, endDate string) (time.Time, time.Time, error) {
	start := dateOnly(time.Now().AddDate(0, 0, -29))
	end := dateOnly(time.Now()).AddDate(0, 0, 1)
	var err error
	if strings.TrimSpace(startDate) != "" {
		start, err = time.ParseInLocation("2006-01-02", strings.TrimSpace(startDate), time.Local)
		if err != nil {
			return time.Time{}, time.Time{}, errors.New("start date is invalid")
		}
	}
	if strings.TrimSpace(endDate) != "" {
		parsed, parseErr := time.ParseInLocation("2006-01-02", strings.TrimSpace(endDate), time.Local)
		if parseErr != nil {
			return time.Time{}, time.Time{}, errors.New("end date is invalid")
		}
		end = parsed.AddDate(0, 0, 1)
	}
	if !start.Before(end) || end.Sub(start) > 366*24*time.Hour {
		return time.Time{}, time.Time{}, errors.New("report date range must be between 1 and 366 days")
	}
	return start, end, nil
}
