package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
)

type timeSlotRule struct {
	Code     string `json:"code"`
	Start    string `json:"start"`
	End      string `json:"end"`
	Capacity int    `json:"capacity"`
}

// salePolicyContext carries quantities already validated in the current
// order. Historical orders are queried from PostgreSQL, but a single request can
// contain multiple lines for the same product and visitor. Keeping this
// context inside the transaction prevents that request from bypassing a
// phone/identity purchase limit by splitting its lines.
type salePolicyContext struct {
	phone map[string]int
	id    map[string]int
}

func newSalePolicyContext() *salePolicyContext {
	return &salePolicyContext{phone: make(map[string]int), id: make(map[string]int)}
}

// validateSalePolicyTx is deliberately called inside the order write
// transaction. It validates the supplier's immutable policy against the
// visitor data supplied by the seller, so a client cannot bypass the policy by
// calling a different sales channel.
func validateSalePolicyTx(tx *gorm.DB, product *model.Product, order *model.Order, item *model.OrderItem, context *salePolicyContext) error {
	if product == nil || order == nil || item == nil {
		return errors.New("product and order are required")
	}
	if order.Channel == "window" && (product.RealNameRequired || product.LimitPerPhone > 0 || product.LimitPerID > 0 || strings.TrimSpace(product.RegionLimit) != "") {
		return fmt.Errorf("票种“%s”需要游客信息，不能通过非实名窗口销售", product.Name)
	}
	if item.VisitorName == "" {
		item.VisitorName = order.ContactName
	}
	if item.VisitorPhone == "" {
		item.VisitorPhone = order.ContactPhone
	}
	if item.VisitorID == "" {
		item.VisitorID = order.VisitorID
	}
	if item.VisitorRegion == "" {
		item.VisitorRegion = order.VisitorRegion
	}
	if len(item.Visitors) > 0 {
		expected := item.Quantity
		if product.CodeMode == "order" {
			expected = 1
		}
		if len(item.Visitors) != expected {
			return fmt.Errorf("product %s requires exactly %d per-ticket visitors", product.Name, expected)
		}
		for i := range item.Visitors {
			item.Visitors[i].Name = strings.TrimSpace(item.Visitors[i].Name)
			item.Visitors[i].Phone = strings.TrimSpace(item.Visitors[i].Phone)
			item.Visitors[i].IdentityNo = strings.TrimSpace(item.Visitors[i].IdentityNo)
			item.Visitors[i].Region = strings.TrimSpace(item.Visitors[i].Region)
			if item.Visitors[i].Name == "" {
				return fmt.Errorf("product %s visitor %d name is required", product.Name, i+1)
			}
			if product.RealNameRequired && item.Visitors[i].IdentityNo == "" {
				return fmt.Errorf("product %s visitor %d identity number is required", product.Name, i+1)
			}
			if err := validateRegion(product.RegionLimit, item.Visitors[i].Region); err != nil {
				return fmt.Errorf("%s visitor %d: %w", product.Name, i+1, err)
			}
		}
	} else if product.RealNameRequired && item.Quantity > 1 && product.CodeMode != "order" {
		return fmt.Errorf("product %s requires one visitor per ticket", product.Name)
	}
	if product.RealNameRequired && len(item.Visitors) == 0 && (strings.TrimSpace(item.VisitorName) == "" || strings.TrimSpace(item.VisitorID) == "") {
		return fmt.Errorf("product %s requires visitor name and identity number", product.Name)
	}
	if product.LimitPerPhone > 0 {
		if strings.TrimSpace(item.VisitorPhone) == "" {
			return fmt.Errorf("product %s requires a visitor phone for purchase limit", product.Name)
		}
		used, err := countPriorQuantity(tx, product.ID, item.VisitorPhone, "phone")
		if err != nil {
			return err
		}
		key := fmt.Sprintf("%d:%s", product.ID, strings.TrimSpace(item.VisitorPhone))
		if context != nil {
			used += context.phone[key]
		}
		if used+item.Quantity > product.LimitPerPhone {
			return fmt.Errorf("phone purchase limit exceeded for %s", product.Name)
		}
		if context != nil {
			context.phone[key] += item.Quantity
		}
	}
	if product.LimitPerID > 0 {
		if strings.TrimSpace(item.VisitorID) == "" {
			return fmt.Errorf("product %s requires an identity number for purchase limit", product.Name)
		}
		used, err := countPriorQuantity(tx, product.ID, item.VisitorID, "id")
		if err != nil {
			return err
		}
		key := fmt.Sprintf("%d:%s", product.ID, strings.TrimSpace(item.VisitorID))
		if context != nil {
			used += context.id[key]
		}
		if used+item.Quantity > product.LimitPerID {
			return fmt.Errorf("identity purchase limit exceeded for %s", product.Name)
		}
		if context != nil {
			context.id[key] += item.Quantity
		}
	}
	if err := validateRegion(product.RegionLimit, item.VisitorRegion); err != nil {
		return fmt.Errorf("%s: %w", product.Name, err)
	}
	if err := validateTimeSlot(product.TimeSlotConfig, item); err != nil {
		return fmt.Errorf("%s: %w", product.Name, err)
	}
	return nil
}

func countPriorQuantity(tx *gorm.DB, productID uint, value, field string) (int, error) {
	column := "visitor_phone"
	if field == "id" {
		column = "visitor_id"
	}
	var total int64
	query := fmt.Sprintf("SELECT COALESCE(SUM(oi.quantity), 0) FROM order_items oi JOIN orders o ON o.id = oi.order_id WHERE oi.fulfillment_product_id = ? AND oi.%s = ? AND o.environment = 'production' AND o.status NOT IN ?", column)
	if err := tx.Raw(query, productID, strings.TrimSpace(value), []string{"cancelled", "refunded"}).Scan(&total).Error; err != nil {
		return 0, err
	}
	return int(total), nil
}

func validateRegion(config, region string) error {
	config = strings.TrimSpace(config)
	if config == "" {
		return nil
	}
	var allowed []string
	if err := json.Unmarshal([]byte(config), &allowed); err != nil {
		return fmt.Errorf("invalid region policy")
	}
	if strings.TrimSpace(region) == "" {
		return errors.New("visitor region is required")
	}
	for _, candidate := range allowed {
		if strings.EqualFold(strings.TrimSpace(candidate), strings.TrimSpace(region)) {
			return nil
		}
	}
	return errors.New("visitor region is not allowed")
}

func parseTimeSlots(config string) ([]timeSlotRule, error) {
	config = strings.TrimSpace(config)
	if config == "" {
		return nil, nil
	}
	var rules []timeSlotRule
	if err := json.Unmarshal([]byte(config), &rules); err == nil {
		for i := range rules {
			if rules[i].Code == "" {
				rules[i].Code = rules[i].Start + "-" + rules[i].End
			}
		}
		return rules, nil
	}
	var values []string
	if err := json.Unmarshal([]byte(config), &values); err != nil {
		return nil, errors.New("invalid time slot policy")
	}
	for _, value := range values {
		parts := strings.SplitN(strings.TrimSpace(value), "-", 2)
		if len(parts) != 2 {
			return nil, errors.New("invalid time slot policy")
		}
		rules = append(rules, timeSlotRule{Code: value, Start: strings.TrimSpace(parts[0]), End: strings.TrimSpace(parts[1])})
	}
	return rules, nil
}

func validateTimeSlot(config string, item *model.OrderItem) error {
	rules, err := parseTimeSlots(config)
	if err != nil {
		return err
	}
	if len(rules) == 0 {
		item.StockSlot = strings.TrimSpace(item.StockSlot)
		return nil
	}
	if item.UseDate == nil {
		return errors.New("visit date is required for time-slot products")
	}
	if strings.TrimSpace(item.StockSlot) == "" {
		return errors.New("time slot is required")
	}
	for _, rule := range rules {
		if rule.Code == item.StockSlot {
			return nil
		}
	}
	return errors.New("time slot is not available")
}

func slotCapacity(product *model.Product, slot string) (int, error) {
	rules, err := parseTimeSlots(product.TimeSlotConfig)
	if err != nil {
		return 0, err
	}
	if slot == "" || len(rules) == 0 {
		return product.DailyStock, nil
	}
	for _, rule := range rules {
		if rule.Code == slot {
			if rule.Capacity < 0 {
				return 0, errors.New("time slot capacity cannot be negative")
			}
			if rule.Capacity > 0 {
				return rule.Capacity, nil
			}
			return product.DailyStock, nil
		}
	}
	return 0, fmt.Errorf("time slot %s is not available", slot)
}

func isVisitDateValid(target *time.Time, start, end *time.Time) bool {
	if target == nil {
		return true
	}
	date := startOfDay(*target)
	if start != nil && date.Before(startOfDay(*start)) {
		return false
	}
	if end != nil && date.After(startOfDay(*end)) {
		return false
	}
	return true
}
