package service

// Hotel Agent adapters deliberately live in their own module. The generic
// runtime owns task state, provider calls, idempotency and audit; this file
// owns only typed hotel projections and preview/confirmation seams.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"ticket-backend/internal/authz"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const agentModuleHotel = "hotel"

var agentHotelModuleManifest = agentModuleManifest{
	ID:             agentModuleHotel,
	Label:          "酒店住宿",
	Summary:        "酒店、房型、房量、两套价格日历、住宿预约与酒景套餐履约事实",
	KnowledgeFiles: []string{"skills/agent_system.md", "skills/agent_core.md", "skills/agent_hotel.md"},
	OperationTypes: []string{AgentOperationHotelInventoryChange, AgentOperationHotelRateCalendarChange, AgentOperationHotelProductCalendarChange, AgentOperationHotelReservationStatusChange},
	ToolNames: []string{
		"search_hotel_catalog",
		"query_hotel_inventory",
		"query_hotel_rate_calendar",
		"query_hotel_product_calendar",
		"query_hotel_reservations",
		"query_hotel_booking_entitlements",
		"query_hotel_business_summary",
		"prepare_hotel_inventory_change",
		"prepare_hotel_rate_calendar_change",
		"prepare_hotel_product_calendar_change",
		"prepare_hotel_reservation_status_change",
	},
}

func init() {
	agentModuleManifests = append(agentModuleManifests, agentHotelModuleManifest)
	agentToolSpecs = append(agentToolSpecs, agentHotelToolSpecs...)
	for name, handler := range agentHotelToolHandlers {
		agentToolHandlers[name] = handler
	}
}

// Hotel tools that mutate data always produce a durable preview. Package
// reservation status requires both scenic and hotel verticals; the other
// catalog resources require only the hotel vertical.
var agentHotelToolSpecs = []agentToolSpec{
	{Name: "search_hotel_catalog", Description: "查询当前租户的酒店、房型、价格计划和独立酒店产品名称与状态，不返回数据库编号", ModuleID: agentModuleHotel, ActionKind: "query", Permission: authz.PermissionCatalogRead, Capability: "supplier", BusinessType: "hotel", ReadOnly: true, Parameters: jsonRaw(`{"type":"object","properties":{"query":{"type":"string","maxLength":100},"kind":{"type":"string","enum":["all","hotel","room_type","rate_plan","product"]},"limit":{"type":"integer","minimum":1,"maximum":50}},"additionalProperties":false}`)},
	{Name: "query_hotel_inventory", Description: "查询当前租户酒店房型按入住日期的房量、预留、已售、可售和关房事实，不修改房量", ModuleID: agentModuleHotel, ActionKind: "query", Permission: authz.PermissionOperationsRead, Capability: "supplier", BusinessType: "hotel", ReadOnly: true, Parameters: jsonRaw(`{"type":"object","required":["hotel_name","room_type_name","start_date","end_date"],"properties":{"hotel_name":{"type":"string","minLength":1,"maxLength":100},"room_type_name":{"type":"string","minLength":1,"maxLength":100},"start_date":{"type":"string","pattern":"^\\d{4}-\\d{2}-\\d{2}$"},"end_date":{"type":"string","pattern":"^\\d{4}-\\d{2}-\\d{2}$"},"limit":{"type":"integer","minimum":1,"maximum":93}},"additionalProperties":false}`)},
	{Name: "query_hotel_rate_calendar", Description: "查询酒店房型价格计划的入住日期价格日历，区分基础价与入住日覆盖价，不修改价格", ModuleID: agentModuleHotel, ActionKind: "query", Permission: authz.PermissionCatalogRead, Capability: "supplier", BusinessType: "hotel", ReadOnly: true, Parameters: jsonRaw(`{"type":"object","required":["hotel_name","room_type_name","rate_plan_name","start_date","end_date"],"properties":{"hotel_name":{"type":"string","minLength":1,"maxLength":100},"room_type_name":{"type":"string","minLength":1,"maxLength":100},"rate_plan_name":{"type":"string","minLength":1,"maxLength":100},"start_date":{"type":"string","pattern":"^\\d{4}-\\d{2}-\\d{2}$"},"end_date":{"type":"string","pattern":"^\\d{4}-\\d{2}-\\d{2}$"}},"additionalProperties":false}`)},
	{Name: "query_hotel_product_calendar", Description: "查询日历房酒店产品的销售价日历；它与房型价格计划日历是两套独立事实，不修改价格", ModuleID: agentModuleHotel, ActionKind: "query", Permission: authz.PermissionCatalogRead, Capability: "supplier", BusinessType: "hotel", ReadOnly: true, Parameters: jsonRaw(`{"type":"object","required":["hotel_name","product_name","start_date","end_date"],"properties":{"hotel_name":{"type":"string","minLength":1,"maxLength":100},"product_name":{"type":"string","minLength":1,"maxLength":100},"start_date":{"type":"string","pattern":"^\\d{4}-\\d{2}-\\d{2}$"},"end_date":{"type":"string","pattern":"^\\d{4}-\\d{2}-\\d{2}$"}},"additionalProperties":false}`)},
	{Name: "query_hotel_reservations", Description: "查询当前租户酒景套餐住宿预订的订单号、预订号、酒店、房型、日期和履约状态，不返回住客姓名、电话、票码或数据库编号", ModuleID: agentModuleHotel, ActionKind: "query", Permission: authz.PermissionHotelReservationsRead, Capability: "supplier", BusinessTypesAll: []string{"scenic", "hotel"}, ReadOnly: true, Parameters: jsonRaw(`{"type":"object","properties":{"hotel_name":{"type":"string","maxLength":100},"reservation_no":{"type":"string","maxLength":100},"order_no":{"type":"string","maxLength":100},"status":{"type":"string","enum":["reserved","confirmed","checked_in","checked_out","no_show","cancelled","refunded"]},"limit":{"type":"integer","minimum":1,"maximum":50}},"additionalProperties":false}`)},
	{Name: "query_hotel_booking_entitlements", Description: "查询酒景套餐住宿预约权益状态摘要，不返回住客个人信息、票码或数据库编号", ModuleID: agentModuleHotel, ActionKind: "query", Permission: authz.PermissionHotelReservationsRead, Capability: "supplier", BusinessTypesAll: []string{"scenic", "hotel"}, ReadOnly: true, Parameters: jsonRaw(`{"type":"object","properties":{"hotel_name":{"type":"string","maxLength":100},"status":{"type":"string","enum":["pending_booking","booking_pending","booked","cancel_pending","cancelled","refunded","expired"]},"limit":{"type":"integer","minimum":1,"maximum":50}},"additionalProperties":false}`)},
	{Name: "query_hotel_business_summary", Description: "查询酒景套餐售券、预约、入住、离店和未到店的服务器汇总，不修改报表", ModuleID: agentModuleHotel, ActionKind: "query", Permission: authz.PermissionReportsRead, Capability: "supplier", BusinessTypesAll: []string{"scenic", "hotel"}, ReadOnly: true, Parameters: jsonRaw(`{"type":"object","required":["start_date","end_date"],"properties":{"hotel_name":{"type":"string","maxLength":100},"start_date":{"type":"string","pattern":"^\\d{4}-\\d{2}-\\d{2}$"},"end_date":{"type":"string","pattern":"^\\d{4}-\\d{2}-\\d{2}$"}},"additionalProperties":false}`)},
	{Name: "prepare_hotel_inventory_change", Description: "预览按酒店、房型和明确日期范围设置房量或关房状态；确认前不写入，服务端拒绝低于预留加已售的容量", ModuleID: agentModuleHotel, ActionKind: "preview", Permission: authz.PermissionCatalogWrite, Capability: "supplier", BusinessType: "hotel", PreviewOnly: true, RequiresConfirmation: true, Parameters: jsonRaw(`{"type":"object","required":["hotel_name","room_type_name","start_date","end_date"],"properties":{"hotel_name":{"type":"string","minLength":1,"maxLength":100},"room_type_name":{"type":"string","minLength":1,"maxLength":100},"start_date":{"type":"string","pattern":"^\\d{4}-\\d{2}-\\d{2}$"},"end_date":{"type":"string","pattern":"^\\d{4}-\\d{2}-\\d{2}$"},"capacity":{"type":["integer","null"],"minimum":0,"maximum":100000},"closed":{"type":["boolean","null"]}},"additionalProperties":false}`)},
	{Name: "prepare_hotel_rate_calendar_change", Description: "预览设置或清除酒店房型价格计划的入住日覆盖价；确认前不写入，不推测价格", ModuleID: agentModuleHotel, ActionKind: "preview", Permission: authz.PermissionCatalogWrite, Capability: "supplier", BusinessType: "hotel", PreviewOnly: true, RequiresConfirmation: true, Parameters: jsonRaw(`{"type":"object","required":["hotel_name","room_type_name","rate_plan_name","start_date","end_date"],"properties":{"hotel_name":{"type":"string","minLength":1,"maxLength":100},"room_type_name":{"type":"string","minLength":1,"maxLength":100},"rate_plan_name":{"type":"string","minLength":1,"maxLength":100},"start_date":{"type":"string","pattern":"^\\d{4}-\\d{2}-\\d{2}$"},"end_date":{"type":"string","pattern":"^\\d{4}-\\d{2}-\\d{2}$"},"retail_price":{"type":["number","null"],"minimum":0},"settlement_price":{"type":["number","null"],"minimum":0},"clear_override":{"type":["boolean","null"]}},"additionalProperties":false}`)},
	{Name: "prepare_hotel_product_calendar_change", Description: "预览设置或清除日历房酒店产品当前 revision 的销售日覆盖价；预售房明确拒绝，确认前不写入", ModuleID: agentModuleHotel, ActionKind: "preview", Permission: authz.PermissionCatalogWrite, Capability: "supplier", BusinessType: "hotel", PreviewOnly: true, RequiresConfirmation: true, Parameters: jsonRaw(`{"type":"object","required":["hotel_name","product_name","start_date","end_date"],"properties":{"hotel_name":{"type":"string","minLength":1,"maxLength":100},"product_name":{"type":"string","minLength":1,"maxLength":100},"start_date":{"type":"string","pattern":"^\\d{4}-\\d{2}-\\d{2}$"},"end_date":{"type":"string","pattern":"^\\d{4}-\\d{2}-\\d{2}$"},"retail_price":{"type":["number","null"],"minimum":0},"settlement_price":{"type":["number","null"],"minimum":0},"clear_override":{"type":["boolean","null"]}},"additionalProperties":false}`)},
	{Name: "prepare_hotel_reservation_status_change", Description: "预览一个精确酒景套餐住宿预订的入住、离店或未到店登记；不创建预约、不取消、不改期、不退款，确认前不写入", ModuleID: agentModuleHotel, ActionKind: "preview", Permission: authz.PermissionHotelReservationsWrite, Capability: "supplier", BusinessTypesAll: []string{"scenic", "hotel"}, PreviewOnly: true, RequiresConfirmation: true, Parameters: jsonRaw(`{"type":"object","required":["reservation_no","target_status"],"properties":{"reservation_no":{"type":"string","minLength":1,"maxLength":100},"target_status":{"type":"string","enum":["checked_in","checked_out","no_show"]},"reason":{"type":"string","maxLength":500}},"additionalProperties":false}`)},
}

var agentHotelToolHandlers = map[string]agentToolHandler{
	"search_hotel_catalog":                    executeAgentHotelCatalogQuery,
	"query_hotel_inventory":                   executeAgentHotelInventoryQuery,
	"query_hotel_rate_calendar":               executeAgentHotelRateCalendarQuery,
	"query_hotel_product_calendar":            executeAgentHotelProductCalendarQuery,
	"query_hotel_reservations":                executeAgentHotelReservationQuery,
	"query_hotel_booking_entitlements":        executeAgentHotelEntitlementQuery,
	"query_hotel_business_summary":            executeAgentHotelBusinessSummaryQuery,
	"prepare_hotel_inventory_change":          executeAgentHotelInventoryPreview,
	"prepare_hotel_rate_calendar_change":      executeAgentHotelRateCalendarPreview,
	"prepare_hotel_product_calendar_change":   executeAgentHotelProductCalendarPreview,
	"prepare_hotel_reservation_status_change": executeAgentHotelReservationStatusPreview,
}

type agentHotelInventoryCandidate struct {
	HotelName    string `json:"hotel_name,omitempty"`
	RoomTypeName string `json:"room_type_name,omitempty"`
	StartDate    string `json:"start_date,omitempty"`
	EndDate      string `json:"end_date,omitempty"`
	Capacity     *int   `json:"capacity,omitempty"`
	Closed       *bool  `json:"closed,omitempty"`
}

type agentHotelRateCalendarCandidate struct {
	HotelName       string   `json:"hotel_name,omitempty"`
	RoomTypeName    string   `json:"room_type_name,omitempty"`
	RatePlanName    string   `json:"rate_plan_name,omitempty"`
	StartDate       string   `json:"start_date,omitempty"`
	EndDate         string   `json:"end_date,omitempty"`
	RetailPrice     *float64 `json:"retail_price,omitempty"`
	SettlementPrice *float64 `json:"settlement_price,omitempty"`
	ClearOverride   *bool    `json:"clear_override,omitempty"`
}

type agentHotelProductCalendarCandidate struct {
	HotelName       string   `json:"hotel_name,omitempty"`
	ProductName     string   `json:"product_name,omitempty"`
	StartDate       string   `json:"start_date,omitempty"`
	EndDate         string   `json:"end_date,omitempty"`
	RetailPrice     *float64 `json:"retail_price,omitempty"`
	SettlementPrice *float64 `json:"settlement_price,omitempty"`
	ClearOverride   *bool    `json:"clear_override,omitempty"`
}

type agentHotelReservationStatusCandidate struct {
	ReservationNo string `json:"reservation_no,omitempty"`
	TargetStatus  string `json:"target_status,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

type agentHotelInventorySnapshot struct {
	InventoryID uint   `json:"inventory_id,omitempty"`
	StayDate    string `json:"stay_date"`
	Exists      bool   `json:"exists"`
	Capacity    int    `json:"capacity"`
	Reserved    int    `json:"reserved"`
	Sold        int    `json:"sold"`
	Closed      bool   `json:"closed"`
	Hash        string `json:"hash"`
}

type agentHotelInventoryPlan struct {
	Candidate    agentHotelInventoryCandidate  `json:"candidate"`
	HotelID      uint                          `json:"hotel_id"`
	RoomTypeID   uint                          `json:"room_type_id"`
	HotelName    string                        `json:"hotel_name"`
	RoomTypeName string                        `json:"room_type_name"`
	Snapshots    []agentHotelInventorySnapshot `json:"snapshots"`
}

type agentHotelCalendarSnapshot struct {
	RowID       uint   `json:"row_id,omitempty"`
	StayDate    string `json:"stay_date"`
	Exists      bool   `json:"exists"`
	RetailPrice int64  `json:"retail_price_cents"`
	Settlement  int64  `json:"settlement_price_cents"`
	Hash        string `json:"hash"`
}

type agentHotelRateCalendarPlan struct {
	Candidate      agentHotelRateCalendarCandidate `json:"candidate"`
	HotelID        uint                            `json:"hotel_id"`
	RoomTypeID     uint                            `json:"room_type_id"`
	RatePlanID     uint                            `json:"rate_plan_id"`
	HotelName      string                          `json:"hotel_name"`
	RoomTypeName   string                          `json:"room_type_name"`
	RatePlanName   string                          `json:"rate_plan_name"`
	BaseRetail     int64                           `json:"base_retail_price_cents"`
	BaseSettlement int64                           `json:"base_settlement_price_cents"`
	Snapshots      []agentHotelCalendarSnapshot    `json:"snapshots"`
}

type agentHotelProductCalendarPlan struct {
	Candidate      agentHotelProductCalendarCandidate `json:"candidate"`
	HotelID        uint                               `json:"hotel_id"`
	HotelProductID uint                               `json:"hotel_product_id"`
	RevisionID     uint                               `json:"revision_id"`
	HotelName      string                             `json:"hotel_name"`
	ProductName    string                             `json:"product_name"`
	BaseRetail     int64                              `json:"base_retail_price_cents"`
	BaseSettlement int64                              `json:"base_settlement_price_cents"`
	Snapshots      []agentHotelCalendarSnapshot       `json:"snapshots"`
}

type agentHotelReservationStatusPlan struct {
	Candidate     agentHotelReservationStatusCandidate `json:"candidate"`
	ReservationID uint                                 `json:"reservation_id"`
	ReservationNo string                               `json:"reservation_no"`
	HotelName     string                               `json:"hotel_name"`
	RoomTypeName  string                               `json:"room_type_name"`
	CheckInDate   string                               `json:"check_in_date"`
	CheckOutDate  string                               `json:"check_out_date"`
	CurrentStatus string                               `json:"current_status"`
	SnapshotHash  string                               `json:"snapshot_hash"`
}

type agentHotelQueryArgs struct {
	Query string `json:"query"`
	Kind  string `json:"kind"`
	Limit int    `json:"limit"`
}

type agentHotelInventoryQueryArgs struct {
	HotelName    string `json:"hotel_name"`
	RoomTypeName string `json:"room_type_name"`
	StartDate    string `json:"start_date"`
	EndDate      string `json:"end_date"`
	Limit        int    `json:"limit"`
}

type agentHotelRateCalendarQueryArgs struct {
	HotelName    string `json:"hotel_name"`
	RoomTypeName string `json:"room_type_name"`
	RatePlanName string `json:"rate_plan_name"`
	StartDate    string `json:"start_date"`
	EndDate      string `json:"end_date"`
}

type agentHotelProductCalendarQueryArgs struct {
	HotelName   string `json:"hotel_name"`
	ProductName string `json:"product_name"`
	StartDate   string `json:"start_date"`
	EndDate     string `json:"end_date"`
}

type agentHotelReservationQueryArgs struct {
	HotelName     string `json:"hotel_name"`
	ReservationNo string `json:"reservation_no"`
	OrderNo       string `json:"order_no"`
	Status        string `json:"status"`
	Limit         int    `json:"limit"`
}

type agentHotelEntitlementQueryArgs struct {
	HotelName string `json:"hotel_name"`
	Status    string `json:"status"`
	Limit     int    `json:"limit"`
}

type agentHotelSummaryQueryArgs struct {
	HotelName string `json:"hotel_name"`
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
}

func hotelAgentDateRange(startDate, endDate string) ([]time.Time, error) {
	start, end, err := parseHotelDateRange(strings.TrimSpace(startDate), strings.TrimSpace(endDate), "酒店日期")
	if err != nil {
		return nil, err
	}
	result := make([]time.Time, 0, int(end.Sub(start)/(24*time.Hour))+1)
	for date := start; !date.After(end); date = date.AddDate(0, 0, 1) {
		result = append(result, date)
	}
	return result, nil
}

func hotelAgentMoney(value *float64) (int64, error) {
	if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0 || *value > 100000000 {
		return 0, errors.New("酒店价格必须明确且不能为负数")
	}
	cents := int64(math.Round(*value * 100))
	if cents < 0 {
		return 0, errors.New("酒店价格不能为负数")
	}
	return cents, nil
}

func hotelAgentRetailMoney(value *float64) (int64, error) {
	cents, err := hotelAgentMoney(value)
	if err != nil {
		return 0, err
	}
	if cents <= 0 {
		return 0, errors.New("酒店零售价必须大于 0")
	}
	return cents, nil
}

func validateAgentHotelInventoryCandidate(_ string, candidate *agentHotelInventoryCandidate) error {
	if candidate == nil {
		return agentInvalid("AI 未返回酒店房量操作内容")
	}
	if candidate.Capacity == nil && candidate.Closed == nil && strings.TrimSpace(candidate.StartDate) != "" {
		return agentInvalid("酒店房量操作必须提供房量或关房状态")
	}
	if candidate.Capacity != nil && (*candidate.Capacity < 0 || *candidate.Capacity > 100000) {
		return agentInvalid("酒店房量必须在 0 到 100000 之间")
	}
	return nil
}

func validateAgentHotelRateCalendarCandidate(_ string, candidate *agentHotelRateCalendarCandidate) error {
	if candidate == nil {
		return agentInvalid("AI 未返回价格计划日历操作内容")
	}
	if strings.TrimSpace(candidate.HotelName) == "" && strings.TrimSpace(candidate.RoomTypeName) == "" && strings.TrimSpace(candidate.RatePlanName) == "" && strings.TrimSpace(candidate.StartDate) == "" && strings.TrimSpace(candidate.EndDate) == "" && candidate.RetailPrice == nil && candidate.SettlementPrice == nil && candidate.ClearOverride == nil {
		return nil
	}
	if candidate.ClearOverride == nil || !*candidate.ClearOverride {
		if candidate.RetailPrice == nil || candidate.SettlementPrice == nil {
			return agentInvalid("价格计划日历必须提供零售价和结算价，或明确清除覆盖价")
		}
	}
	if candidate.ClearOverride != nil && *candidate.ClearOverride && (candidate.RetailPrice != nil || candidate.SettlementPrice != nil) {
		return agentInvalid("清除价格计划覆盖价时不能同时提供新价格")
	}
	if candidate.RetailPrice != nil && *candidate.RetailPrice <= 0 {
		return agentInvalid("价格计划零售价必须大于 0")
	}
	if candidate.SettlementPrice != nil && *candidate.SettlementPrice < 0 {
		return agentInvalid("价格计划结算价不能为负数")
	}
	if candidate.RetailPrice != nil && candidate.SettlementPrice != nil && *candidate.SettlementPrice > *candidate.RetailPrice {
		return agentInvalid("价格计划结算价不能高于零售价")
	}
	return nil
}

func validateAgentHotelProductCalendarCandidate(_ string, candidate *agentHotelProductCalendarCandidate) error {
	if candidate == nil {
		return agentInvalid("AI 未返回酒店产品日历操作内容")
	}
	if strings.TrimSpace(candidate.HotelName) == "" && strings.TrimSpace(candidate.ProductName) == "" && strings.TrimSpace(candidate.StartDate) == "" && strings.TrimSpace(candidate.EndDate) == "" && candidate.RetailPrice == nil && candidate.SettlementPrice == nil && candidate.ClearOverride == nil {
		return nil
	}
	if candidate.ClearOverride == nil || !*candidate.ClearOverride {
		if candidate.RetailPrice == nil || candidate.SettlementPrice == nil {
			return agentInvalid("酒店产品日历必须提供零售价和结算价，或明确清除覆盖价")
		}
	}
	if candidate.ClearOverride != nil && *candidate.ClearOverride && (candidate.RetailPrice != nil || candidate.SettlementPrice != nil) {
		return agentInvalid("清除酒店产品覆盖价时不能同时提供新价格")
	}
	if candidate.RetailPrice != nil && *candidate.RetailPrice <= 0 {
		return agentInvalid("酒店产品销售价必须大于 0")
	}
	if candidate.SettlementPrice != nil && *candidate.SettlementPrice < 0 {
		return agentInvalid("酒店产品结算价不能为负数")
	}
	if candidate.RetailPrice != nil && candidate.SettlementPrice != nil && *candidate.SettlementPrice > *candidate.RetailPrice {
		return agentInvalid("酒店产品结算价不能高于零售价")
	}
	return nil
}

func validateAgentHotelReservationStatusCandidate(_ string, candidate *agentHotelReservationStatusCandidate) error {
	if candidate == nil {
		return agentInvalid("AI 未返回住宿履约操作内容")
	}
	if strings.TrimSpace(candidate.ReservationNo) == "" && strings.TrimSpace(candidate.TargetStatus) == "" && strings.TrimSpace(candidate.Reason) == "" {
		return nil
	}
	if candidate.TargetStatus != "" && candidate.TargetStatus != "checked_in" && candidate.TargetStatus != "checked_out" && candidate.TargetStatus != "no_show" {
		return agentInvalid("住宿履约状态只支持 checked_in、checked_out 或 no_show")
	}
	if candidate.TargetStatus == "no_show" && strings.TrimSpace(candidate.Reason) == "" {
		return agentInvalid("登记未到店必须填写原因")
	}
	return nil
}

func hotelAgentSnapshotHash(value interface{}) string {
	encoded, _ := json.Marshal(value)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func hotelAgentExactProperty(tx *gorm.DB, tenantID uint, name string) (model.HotelProperty, error) {
	var rows []model.HotelProperty
	name = strings.TrimSpace(name)
	if name == "" {
		return model.HotelProperty{}, agentInvalid("请提供酒店名称")
	}
	if err := tx.Where("tenant_id = ? AND (LOWER(name) = LOWER(?) OR LOWER(code) = LOWER(?))", tenantID, name, name).Order("id ASC").Find(&rows).Error; err != nil {
		return model.HotelProperty{}, err
	}
	if len(rows) == 0 {
		return model.HotelProperty{}, agentInvalid("未找到当前租户内匹配的酒店")
	}
	if len(rows) > 1 {
		return model.HotelProperty{}, agentInvalid("酒店名称匹配多个结果，请补充酒店编码")
	}
	return rows[0], nil
}

func hotelAgentExactRoomType(tx *gorm.DB, tenantID, hotelID uint, name string) (model.HotelRoomType, error) {
	var rows []model.HotelRoomType
	name = strings.TrimSpace(name)
	if name == "" {
		return model.HotelRoomType{}, agentInvalid("请提供房型名称")
	}
	if err := tx.Where("tenant_id = ? AND hotel_id = ? AND (LOWER(name) = LOWER(?) OR LOWER(code) = LOWER(?))", tenantID, hotelID, name, name).Order("id ASC").Find(&rows).Error; err != nil {
		return model.HotelRoomType{}, err
	}
	if len(rows) == 0 {
		return model.HotelRoomType{}, agentInvalid("未找到当前酒店内匹配的房型")
	}
	if len(rows) > 1 {
		return model.HotelRoomType{}, agentInvalid("房型名称匹配多个结果，请补充房型编码")
	}
	return rows[0], nil
}

func hotelAgentExactRatePlan(tx *gorm.DB, tenantID, hotelID, roomTypeID uint, name string) (model.HotelRatePlan, error) {
	var rows []model.HotelRatePlan
	name = strings.TrimSpace(name)
	if name == "" {
		return model.HotelRatePlan{}, agentInvalid("请提供价格计划名称")
	}
	if err := tx.Where("tenant_id = ? AND hotel_id = ? AND room_type_id = ? AND (LOWER(name) = LOWER(?) OR LOWER(code) = LOWER(?))", tenantID, hotelID, roomTypeID, name, name).Order("id ASC").Find(&rows).Error; err != nil {
		return model.HotelRatePlan{}, err
	}
	if len(rows) == 0 {
		return model.HotelRatePlan{}, agentInvalid("未找到当前房型内匹配的价格计划")
	}
	if len(rows) > 1 {
		return model.HotelRatePlan{}, agentInvalid("价格计划名称匹配多个结果，请补充价格计划编码")
	}
	return rows[0], nil
}

func hotelAgentExactProduct(tx *gorm.DB, tenantID, hotelID uint, name string) (model.HotelProduct, model.Product, error) {
	var rows []model.HotelProduct
	name = strings.TrimSpace(name)
	if name == "" {
		return model.HotelProduct{}, model.Product{}, agentInvalid("请提供酒店产品名称")
	}
	// Product is the stable sales identity for hotel products, but it has no
	// business code column. Resolve by the tenant-scoped product name only;
	// duplicate names remain ambiguous and are rejected below.
	if err := tx.Where("tenant_id = ? AND hotel_id = ? AND product_id IN (SELECT id FROM products WHERE tenant_id = ? AND product_kind = ? AND LOWER(name) = LOWER(?))", tenantID, hotelID, tenantID, "hotel", name).Order("id ASC").Find(&rows).Error; err != nil {
		return model.HotelProduct{}, model.Product{}, err
	}
	if len(rows) == 0 {
		return model.HotelProduct{}, model.Product{}, agentInvalid("未找到当前酒店内匹配的独立酒店产品")
	}
	if len(rows) > 1 {
		return model.HotelProduct{}, model.Product{}, agentInvalid("酒店产品名称匹配多个结果，请补充产品编码")
	}
	var product model.Product
	if err := tx.Where("id = ? AND tenant_id = ? AND product_kind = ?", rows[0].ProductID, tenantID, "hotel").First(&product).Error; err != nil {
		return model.HotelProduct{}, model.Product{}, err
	}
	return rows[0], product, nil
}

func agentHotelInventoryPreviewJSON(plan agentHotelInventoryPlan) (string, error) {
	lines := make([]map[string]interface{}, 0, len(plan.Snapshots))
	for _, snapshot := range plan.Snapshots {
		capacity := snapshot.Capacity
		closed := snapshot.Closed
		if plan.Candidate.Capacity != nil {
			capacity = *plan.Candidate.Capacity
		}
		if plan.Candidate.Closed != nil {
			closed = *plan.Candidate.Closed
		}
		lines = append(lines, map[string]interface{}{"stay_date": snapshot.StayDate, "before": map[string]interface{}{"capacity": snapshot.Capacity, "reserved": snapshot.Reserved, "sold": snapshot.Sold, "closed": snapshot.Closed, "available": max(0, snapshot.Capacity-snapshot.Sold)}, "after": map[string]interface{}{"capacity": capacity, "closed": closed, "available": max(0, capacity-snapshot.Sold)}, "change": map[string]interface{}{"capacity": plan.Candidate.Capacity != nil, "closed": plan.Candidate.Closed != nil}})
	}
	data, err := json.Marshal(map[string]interface{}{"operation_type": AgentOperationHotelInventoryChange, "hotel_name": plan.HotelName, "room_type_name": plan.RoomTypeName, "lines": lines, "safety": []string{"确认前不会写入房量。", "服务端会再次锁定每日房量并拒绝低于已预留加已售的容量。", "关房不会释放或改写已预留、已售事实。"}})
	return string(data), err
}

func agentHotelCalendarPreviewJSON(operation string, hotelName, scopeName string, snapshots []agentHotelCalendarSnapshot, candidateRetail, candidateSettlement *float64, clear *bool, baseRetail, baseSettlement int64, safety []string) (string, error) {
	lines := make([]map[string]interface{}, 0, len(snapshots))
	for _, snapshot := range snapshots {
		retail, settlement := snapshot.RetailPrice, snapshot.Settlement
		source := "override"
		if clear != nil && *clear {
			retail, settlement, source = baseRetail, baseSettlement, "base"
		}
		if clear == nil || !*clear {
			if candidateRetail != nil {
				retail = int64(math.Round(*candidateRetail * 100))
			}
			if candidateSettlement != nil {
				settlement = int64(math.Round(*candidateSettlement * 100))
			}
		}
		lines = append(lines, map[string]interface{}{"stay_date": snapshot.StayDate, "before": map[string]interface{}{"retail_price": hotelAgentYuan(snapshot.RetailPrice), "settlement_price": hotelAgentYuan(snapshot.Settlement), "source": map[bool]string{true: "override", false: "base"}[snapshot.Exists]}, "after": map[string]interface{}{"retail_price": hotelAgentYuan(retail), "settlement_price": hotelAgentYuan(settlement), "source": source}})
	}
	data, err := json.Marshal(map[string]interface{}{"operation_type": operation, "hotel_name": hotelName, "scope_name": scopeName, "lines": lines, "safety": safety})
	return string(data), err
}

func hotelAgentYuan(cents int64) float64 { return float64(cents) / 100 }
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// --- Read-only adapters -------------------------------------------------

func executeAgentHotelCatalogQuery(_ *AgentTaskService, request agentToolRequest) (agentToolExecution, error) {
	var args agentHotelQueryArgs
	if err := decodeAgentToolArguments(request.RawArgs, &args); err != nil {
		return agentToolExecution{}, err
	}
	limit := agentQueryLimit(args.Limit)
	kind := strings.TrimSpace(args.Kind)
	if kind == "" {
		kind = "all"
	}
	if !map[string]bool{"all": true, "hotel": true, "room_type": true, "rate_plan": true, "product": true}[kind] {
		return agentToolExecution{}, agentInvalid("酒店目录查询类型不受支持")
	}
	query := strings.TrimSpace(args.Query)
	rows := make([]map[string]interface{}, 0, limit)
	appendRow := func(kind, hotel, name, code, status string) {
		if len(rows) < limit {
			rows = append(rows, map[string]interface{}{"kind": kind, "hotel_name": hotel, "name": name, "code": code, "status": status})
		}
	}
	if kind == "all" || kind == "hotel" {
		var hotels []model.HotelProperty
		q := model.DB.Where("tenant_id = ?", request.TenantID)
		if query != "" {
			q = q.Where("name ILIKE ? OR code ILIKE ?", "%"+query+"%", "%"+query+"%")
		}
		if err := q.Order("name ASC").Limit(limit).Find(&hotels).Error; err != nil {
			return agentToolExecution{}, err
		}
		for _, row := range hotels {
			appendRow("hotel", "", row.Name, row.Code, row.Status)
		}
	}
	if kind == "all" || kind == "room_type" {
		var rooms []struct {
			model.HotelRoomType
			HotelName string `gorm:"column:hotel_name"`
		}
		q := model.DB.Table("hotel_room_types AS room").Select("room.*, hotel.name AS hotel_name").Joins("JOIN hotel_properties AS hotel ON hotel.id = room.hotel_id AND hotel.tenant_id = room.tenant_id").Where("room.tenant_id = ?", request.TenantID)
		if query != "" {
			q = q.Where("room.name ILIKE ? OR room.code ILIKE ?", "%"+query+"%", "%"+query+"%")
		}
		if err := q.Order("room.name ASC").Limit(limit).Scan(&rooms).Error; err != nil {
			return agentToolExecution{}, err
		}
		for _, row := range rooms {
			appendRow("room_type", row.HotelName, row.Name, row.Code, row.Status)
		}
	}
	if kind == "all" || kind == "rate_plan" {
		var plans []struct {
			model.HotelRatePlan
			HotelName    string `gorm:"column:hotel_name"`
			RoomTypeName string `gorm:"column:room_type_name"`
		}
		q := model.DB.Table("hotel_rate_plans AS rate").Select("rate.*, hotel.name AS hotel_name, room.name AS room_type_name").Joins("JOIN hotel_properties AS hotel ON hotel.id = rate.hotel_id AND hotel.tenant_id = rate.tenant_id").Joins("JOIN hotel_room_types AS room ON room.id = rate.room_type_id AND room.tenant_id = rate.tenant_id").Where("rate.tenant_id = ?", request.TenantID)
		if query != "" {
			q = q.Where("rate.name ILIKE ? OR rate.code ILIKE ?", "%"+query+"%", "%"+query+"%")
		}
		if err := q.Order("rate.name ASC").Limit(limit).Scan(&plans).Error; err != nil {
			return agentToolExecution{}, err
		}
		for _, row := range plans {
			appendRow("rate_plan", row.HotelName+" / "+row.RoomTypeName, row.Name, row.Code, row.Status)
		}
	}
	if kind == "all" || kind == "product" {
		var products []struct {
			model.HotelProduct
			HotelName     string `gorm:"column:hotel_name"`
			ProductName   string `gorm:"column:product_name"`
			ProductStatus string `gorm:"column:product_status"`
		}
		q := model.DB.Table("hotel_products AS hp").Select("hp.*, hotel.name AS hotel_name, product.name AS product_name, product.status AS product_status").Joins("JOIN hotel_properties AS hotel ON hotel.id = hp.hotel_id AND hotel.tenant_id = hp.tenant_id").Joins("JOIN products AS product ON product.id = hp.product_id AND product.tenant_id = hp.tenant_id AND product.product_kind = 'hotel'").Where("hp.tenant_id = ?", request.TenantID)
		if query != "" {
			q = q.Where("product.name ILIKE ?", "%"+query+"%")
		}
		if err := q.Order("product.name ASC").Limit(limit).Scan(&products).Error; err != nil {
			return agentToolExecution{}, err
		}
		for _, row := range products {
			appendRow("product", row.HotelName, row.ProductName, fmt.Sprintf("%s", row.SaleMode), row.ProductStatus)
		}
	}
	return agentQueryExecution(agentModuleHotel, "search_hotel_catalog", map[string]interface{}{"query": query, "kind": kind}, rows, len(rows), int64(len(rows)), limit)
}

func executeAgentHotelInventoryQuery(_ *AgentTaskService, request agentToolRequest) (agentToolExecution, error) {
	var args agentHotelInventoryQueryArgs
	if err := decodeAgentToolArguments(request.RawArgs, &args); err != nil {
		return agentToolExecution{}, err
	}
	hotel, err := hotelAgentExactProperty(model.DB, request.TenantID, args.HotelName)
	if err != nil {
		return agentToolExecution{}, err
	}
	room, err := hotelAgentExactRoomType(model.DB, request.TenantID, hotel.ID, args.RoomTypeName)
	if err != nil {
		return agentToolExecution{}, err
	}
	start, end, err := parseHotelDateRange(args.StartDate, args.EndDate, "酒店房量")
	if err != nil {
		return agentToolExecution{}, err
	}
	limit := args.Limit
	if limit <= 0 || limit > 93 {
		limit = 93
	}
	var rows []model.HotelRoomInventory
	if err := model.DB.Where("tenant_id = ? AND hotel_id = ? AND room_type_id = ? AND stay_date BETWEEN ? AND ?", request.TenantID, hotel.ID, room.ID, start, end).Order("stay_date ASC").Limit(limit).Find(&rows).Error; err != nil {
		return agentToolExecution{}, err
	}
	data := make([]map[string]interface{}, 0, len(rows))
	for _, row := range rows {
		data = append(data, map[string]interface{}{"stay_date": row.StayDate.Format("2006-01-02"), "capacity": row.Capacity, "reserved": row.Reserved, "sold": row.Sold, "remaining": max(0, row.Capacity-row.Sold), "available_after_reserved": max(0, row.Capacity-row.Sold-row.Reserved), "closed": row.Closed})
	}
	return agentQueryExecution(agentModuleHotel, "query_hotel_inventory", map[string]interface{}{"hotel_name": hotel.Name, "room_type_name": room.Name, "start_date": args.StartDate, "end_date": args.EndDate}, data, len(data), int64(len(data)), limit)
}

func executeAgentHotelRateCalendarQuery(_ *AgentTaskService, request agentToolRequest) (agentToolExecution, error) {
	var args agentHotelRateCalendarQueryArgs
	if err := decodeAgentToolArguments(request.RawArgs, &args); err != nil {
		return agentToolExecution{}, err
	}
	hotel, err := hotelAgentExactProperty(model.DB, request.TenantID, args.HotelName)
	if err != nil {
		return agentToolExecution{}, err
	}
	room, err := hotelAgentExactRoomType(model.DB, request.TenantID, hotel.ID, args.RoomTypeName)
	if err != nil {
		return agentToolExecution{}, err
	}
	rate, err := hotelAgentExactRatePlan(model.DB, request.TenantID, hotel.ID, room.ID, args.RatePlanName)
	if err != nil {
		return agentToolExecution{}, err
	}
	rows, err := (&HotelService{}).ListRatePlanCalendar(request.TenantID, hotel.ID, room.ID, rate.ID, args.StartDate, args.EndDate)
	if err != nil {
		return agentToolExecution{}, err
	}
	data := make([]map[string]interface{}, 0, len(rows))
	for _, row := range rows {
		data = append(data, map[string]interface{}{"stay_date": row.StayDate, "retail_price": hotelAgentYuan(row.RetailPriceCents), "settlement_price": hotelAgentYuan(row.SettlementPriceCents), "base_retail_price": hotelAgentYuan(row.BaseRetailPriceCents), "base_settlement_price": hotelAgentYuan(row.BaseSettlementPriceCents), "has_override": row.HasOverride, "source": row.Source})
	}
	return agentQueryExecution(agentModuleHotel, "query_hotel_rate_calendar", map[string]interface{}{"hotel_name": hotel.Name, "room_type_name": room.Name, "rate_plan_name": rate.Name, "start_date": args.StartDate, "end_date": args.EndDate}, data, len(data), int64(len(data)), len(data))
}

func executeAgentHotelProductCalendarQuery(_ *AgentTaskService, request agentToolRequest) (agentToolExecution, error) {
	var args agentHotelProductCalendarQueryArgs
	if err := decodeAgentToolArguments(request.RawArgs, &args); err != nil {
		return agentToolExecution{}, err
	}
	hotel, err := hotelAgentExactProperty(model.DB, request.TenantID, args.HotelName)
	if err != nil {
		return agentToolExecution{}, err
	}
	hp, product, err := hotelAgentExactProduct(model.DB, request.TenantID, hotel.ID, args.ProductName)
	if err != nil {
		return agentToolExecution{}, err
	}
	rows, err := (&HotelProductService{}).ListCalendar(request.TenantID, hp.ID, args.StartDate, args.EndDate)
	if err != nil {
		return agentToolExecution{}, err
	}
	data := make([]map[string]interface{}, 0, len(rows))
	for _, row := range rows {
		data = append(data, map[string]interface{}{"stay_date": row.StayDate, "retail_price": hotelAgentYuan(row.RetailPriceCents), "settlement_price": hotelAgentYuan(row.SettlementPriceCents), "base_retail_price": hotelAgentYuan(row.BaseRetailPriceCents), "base_settlement_price": hotelAgentYuan(row.BaseSettlementPriceCents), "has_override": row.HasOverride, "source": row.Source})
	}
	return agentQueryExecution(agentModuleHotel, "query_hotel_product_calendar", map[string]interface{}{"hotel_name": hotel.Name, "product_name": product.Name, "start_date": args.StartDate, "end_date": args.EndDate}, data, len(data), int64(len(data)), len(data))
}

func executeAgentHotelReservationQuery(_ *AgentTaskService, request agentToolRequest) (agentToolExecution, error) {
	var args agentHotelReservationQueryArgs
	if err := decodeAgentToolArguments(request.RawArgs, &args); err != nil {
		return agentToolExecution{}, err
	}
	limit := agentQueryLimit(args.Limit)
	query := model.DB.Table("hotel_reservations AS reservation").Joins("JOIN hotel_properties AS hotel ON hotel.id = reservation.hotel_id AND hotel.tenant_id = reservation.supplier_tenant_id").Joins("JOIN hotel_room_types AS room ON room.id = reservation.room_type_id AND room.tenant_id = reservation.supplier_tenant_id").Joins("JOIN orders ON orders.id = reservation.order_id AND orders.tenant_id = reservation.sales_tenant_id").Where("reservation.supplier_tenant_id = ? AND reservation.deleted_at IS NULL", request.TenantID)
	if args.HotelName != "" {
		query = query.Where("hotel.name ILIKE ?", "%"+strings.TrimSpace(args.HotelName)+"%")
	}
	if args.ReservationNo != "" {
		query = query.Where("reservation.reservation_no = ?", strings.TrimSpace(args.ReservationNo))
	}
	if args.OrderNo != "" {
		query = query.Where("orders.order_no ILIKE ?", "%"+strings.TrimSpace(args.OrderNo)+"%")
	}
	if args.Status != "" {
		query = query.Where("reservation.status = ?", strings.TrimSpace(args.Status))
	}
	var total int64
	if err := query.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return agentToolExecution{}, err
	}
	var raw []struct {
		ReservationNo, OrderNo, HotelName, RoomTypeName, Status string
		CheckInDate, CheckOutDate                               time.Time
	}
	if err := query.Select("reservation.reservation_no, orders.order_no, hotel.name AS hotel_name, room.name AS room_type_name, reservation.status, reservation.check_in_date, reservation.check_out_date").Order("reservation.check_in_date ASC, reservation.id ASC").Limit(limit).Scan(&raw).Error; err != nil {
		return agentToolExecution{}, err
	}
	data := make([]map[string]interface{}, 0, len(raw))
	for _, row := range raw {
		data = append(data, map[string]interface{}{"reservation_no": row.ReservationNo, "order_no": row.OrderNo, "hotel_name": row.HotelName, "room_type_name": row.RoomTypeName, "check_in_date": row.CheckInDate.Format("2006-01-02"), "check_out_date": row.CheckOutDate.Format("2006-01-02"), "status": row.Status})
	}
	return agentQueryExecution(agentModuleHotel, "query_hotel_reservations", map[string]interface{}{"hotel_name": args.HotelName, "reservation_no": args.ReservationNo, "order_no": args.OrderNo, "status": args.Status}, data, len(data), total, limit)
}

func executeAgentHotelEntitlementQuery(_ *AgentTaskService, request agentToolRequest) (agentToolExecution, error) {
	var args agentHotelEntitlementQueryArgs
	if err := decodeAgentToolArguments(request.RawArgs, &args); err != nil {
		return agentToolExecution{}, err
	}
	limit := agentQueryLimit(args.Limit)
	query := model.DB.Table("scenic_hotel_package_entitlements AS entitlement").Joins("JOIN scenic_hotel_packages AS package ON package.id = entitlement.package_id AND package.tenant_id = entitlement.supplier_tenant_id").Joins("JOIN hotel_properties AS hotel ON hotel.id = package.hotel_id AND hotel.tenant_id = package.tenant_id").Joins("JOIN hotel_room_types AS room ON room.id = package.room_type_id AND room.tenant_id = package.tenant_id").Where("entitlement.supplier_tenant_id = ? AND entitlement.deleted_at IS NULL", request.TenantID)
	if args.HotelName != "" {
		query = query.Where("hotel.name ILIKE ?", "%"+strings.TrimSpace(args.HotelName)+"%")
	}
	if args.Status != "" {
		query = query.Where("entitlement.status = ?", strings.TrimSpace(args.Status))
	}
	var total int64
	if err := query.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return agentToolExecution{}, err
	}
	var raw []struct {
		EntitlementNo, HotelName, RoomTypeName, Status string
		ValidFrom, ValidUntil                          time.Time
	}
	if err := query.Select("entitlement.entitlement_no, hotel.name AS hotel_name, room.name AS room_type_name, entitlement.status, entitlement.valid_from, entitlement.valid_until").Order("entitlement.valid_until ASC").Limit(limit).Scan(&raw).Error; err != nil {
		return agentToolExecution{}, err
	}
	data := make([]map[string]interface{}, 0, len(raw))
	for _, row := range raw {
		data = append(data, map[string]interface{}{"entitlement_no": row.EntitlementNo, "hotel_name": row.HotelName, "room_type_name": row.RoomTypeName, "status": row.Status, "valid_from": row.ValidFrom.Format(time.RFC3339), "valid_until": row.ValidUntil.Format(time.RFC3339)})
	}
	return agentQueryExecution(agentModuleHotel, "query_hotel_booking_entitlements", map[string]interface{}{"hotel_name": args.HotelName, "status": args.Status}, data, len(data), total, limit)
}

func executeAgentHotelBusinessSummaryQuery(_ *AgentTaskService, request agentToolRequest) (agentToolExecution, error) {
	var args agentHotelSummaryQueryArgs
	if err := decodeAgentToolArguments(request.RawArgs, &args); err != nil {
		return agentToolExecution{}, err
	}
	hotelID := uint(0)
	if strings.TrimSpace(args.HotelName) != "" {
		hotel, err := hotelAgentExactProperty(model.DB, request.TenantID, args.HotelName)
		if err != nil {
			return agentToolExecution{}, err
		}
		hotelID = hotel.ID
	}
	summary, err := (&ScenicHotelPackageService{}).BusinessSummary(request.TenantID, hotelID, args.StartDate, args.EndDate)
	if err != nil {
		return agentToolExecution{}, err
	}
	return agentQueryExecution(agentModuleHotel, "query_hotel_business_summary", map[string]interface{}{"hotel_name": args.HotelName, "start_date": args.StartDate, "end_date": args.EndDate}, summary, 1, 1, 1)
}

// --- Preview adapters ---------------------------------------------------

func hotelPreviewExecution(s *AgentTaskService, request agentToolRequest, operation string, envelope *agentAIEnvelope) (agentToolExecution, error) {
	if err := validateAgentPlannerEnvelopeForTask(request.Input, request.Task, envelope); err != nil {
		return agentToolExecution{}, err
	}
	planning, err := s.planFromEnvelope(request.TenantID, request.ActorID, request.ActorRole, request.Task, request.Input, request.Task.ContextJSON, request.Config, s.aiService(), envelope)
	if err != nil {
		return agentToolExecution{}, err
	}
	encoded, _ := json.Marshal(planning)
	return agentToolExecution{ResultJSON: string(encoded), Planning: planning}, nil
}

func executeAgentHotelInventoryPreview(s *AgentTaskService, request agentToolRequest) (agentToolExecution, error) {
	var candidate agentHotelInventoryCandidate
	if err := decodeAgentToolArguments(request.RawArgs, &candidate); err != nil {
		return agentToolExecution{}, err
	}
	return hotelPreviewExecution(s, request, AgentOperationHotelInventoryChange, &agentAIEnvelope{OperationType: AgentOperationHotelInventoryChange, HotelInventory: &candidate})
}
func executeAgentHotelRateCalendarPreview(s *AgentTaskService, request agentToolRequest) (agentToolExecution, error) {
	var candidate agentHotelRateCalendarCandidate
	if err := decodeAgentToolArguments(request.RawArgs, &candidate); err != nil {
		return agentToolExecution{}, err
	}
	return hotelPreviewExecution(s, request, AgentOperationHotelRateCalendarChange, &agentAIEnvelope{OperationType: AgentOperationHotelRateCalendarChange, HotelRateCalendar: &candidate})
}
func executeAgentHotelProductCalendarPreview(s *AgentTaskService, request agentToolRequest) (agentToolExecution, error) {
	var candidate agentHotelProductCalendarCandidate
	if err := decodeAgentToolArguments(request.RawArgs, &candidate); err != nil {
		return agentToolExecution{}, err
	}
	return hotelPreviewExecution(s, request, AgentOperationHotelProductCalendarChange, &agentAIEnvelope{OperationType: AgentOperationHotelProductCalendarChange, HotelProductCalendar: &candidate})
}
func executeAgentHotelReservationStatusPreview(s *AgentTaskService, request agentToolRequest) (agentToolExecution, error) {
	var candidate agentHotelReservationStatusCandidate
	if err := decodeAgentToolArguments(request.RawArgs, &candidate); err != nil {
		return agentToolExecution{}, err
	}
	return hotelPreviewExecution(s, request, AgentOperationHotelReservationStatusChange, &agentAIEnvelope{OperationType: AgentOperationHotelReservationStatusChange, HotelReservationStatus: &candidate})
}

func mergeHotelInventoryCandidate(previous *agentHotelInventoryPlan, next *agentHotelInventoryCandidate) agentHotelInventoryCandidate {
	var result agentHotelInventoryCandidate
	if previous != nil {
		result = previous.Candidate
	}
	if next == nil {
		return result
	}
	if strings.TrimSpace(next.HotelName) != "" {
		result.HotelName = next.HotelName
	}
	if strings.TrimSpace(next.RoomTypeName) != "" {
		result.RoomTypeName = next.RoomTypeName
	}
	if strings.TrimSpace(next.StartDate) != "" {
		result.StartDate = next.StartDate
	}
	if strings.TrimSpace(next.EndDate) != "" {
		result.EndDate = next.EndDate
	}
	if next.Capacity != nil {
		result.Capacity = next.Capacity
	}
	if next.Closed != nil {
		result.Closed = next.Closed
	}
	return result
}
func mergeHotelRateCalendarCandidate(previous *agentHotelRateCalendarPlan, next *agentHotelRateCalendarCandidate) agentHotelRateCalendarCandidate {
	var result agentHotelRateCalendarCandidate
	if previous != nil {
		result = previous.Candidate
	}
	if next == nil {
		return result
	}
	if strings.TrimSpace(next.HotelName) != "" {
		result.HotelName = next.HotelName
	}
	if strings.TrimSpace(next.RoomTypeName) != "" {
		result.RoomTypeName = next.RoomTypeName
	}
	if strings.TrimSpace(next.RatePlanName) != "" {
		result.RatePlanName = next.RatePlanName
	}
	if strings.TrimSpace(next.StartDate) != "" {
		result.StartDate = next.StartDate
	}
	if strings.TrimSpace(next.EndDate) != "" {
		result.EndDate = next.EndDate
	}
	if next.RetailPrice != nil {
		result.RetailPrice = next.RetailPrice
	}
	if next.SettlementPrice != nil {
		result.SettlementPrice = next.SettlementPrice
	}
	if next.ClearOverride != nil {
		result.ClearOverride = next.ClearOverride
	}
	return result
}
func mergeHotelProductCalendarCandidate(previous *agentHotelProductCalendarPlan, next *agentHotelProductCalendarCandidate) agentHotelProductCalendarCandidate {
	var result agentHotelProductCalendarCandidate
	if previous != nil {
		result = previous.Candidate
	}
	if next == nil {
		return result
	}
	if strings.TrimSpace(next.HotelName) != "" {
		result.HotelName = next.HotelName
	}
	if strings.TrimSpace(next.ProductName) != "" {
		result.ProductName = next.ProductName
	}
	if strings.TrimSpace(next.StartDate) != "" {
		result.StartDate = next.StartDate
	}
	if strings.TrimSpace(next.EndDate) != "" {
		result.EndDate = next.EndDate
	}
	if next.RetailPrice != nil {
		result.RetailPrice = next.RetailPrice
	}
	if next.SettlementPrice != nil {
		result.SettlementPrice = next.SettlementPrice
	}
	if next.ClearOverride != nil {
		result.ClearOverride = next.ClearOverride
	}
	return result
}
func mergeHotelReservationCandidate(previous *agentHotelReservationStatusPlan, next *agentHotelReservationStatusCandidate) agentHotelReservationStatusCandidate {
	var result agentHotelReservationStatusCandidate
	if previous != nil {
		result = previous.Candidate
	}
	if next == nil {
		return result
	}
	if strings.TrimSpace(next.ReservationNo) != "" {
		result.ReservationNo = next.ReservationNo
	}
	if strings.TrimSpace(next.TargetStatus) != "" {
		result.TargetStatus = next.TargetStatus
	}
	if strings.TrimSpace(next.Reason) != "" {
		result.Reason = next.Reason
	}
	return result
}

func hotelMissing(field, label, question string) []AgentMissingField {
	return []AgentMissingField{{Field: field, Label: label, Question: question}}
}

func planHotelInventoryChange(tenantID uint, task model.AgentTask, input string, candidate *agentHotelInventoryCandidate, pack agentKnowledgePack) (*agentPlanningResult, error) {
	var previous agentTaskContext
	_ = json.Unmarshal([]byte(task.ContextJSON), &previous)
	merged := mergeHotelInventoryCandidate(previous.HotelInventory, candidate)
	result := &agentPlanningResult{OperationType: AgentOperationHotelInventoryChange, Provider: "", Model: "", Context: agentTaskContext{OperationType: AgentOperationHotelInventoryChange, KnowledgePackID: pack.ID, SkillVersion: pack.Version, SkillHash: pack.Hash}}
	if strings.TrimSpace(merged.HotelName) == "" {
		result.Context.HotelInventory = &agentHotelInventoryPlan{Candidate: merged}
		result.Missing = hotelMissing("hotel_name", "酒店", "请提供要调整房量的酒店名称。")
		return result, nil
	}
	if strings.TrimSpace(merged.RoomTypeName) == "" {
		result.Context.HotelInventory = &agentHotelInventoryPlan{Candidate: merged}
		result.Missing = hotelMissing("room_type_name", "房型", "请提供要调整房量的房型名称。")
		return result, nil
	}
	if strings.TrimSpace(merged.StartDate) == "" || strings.TrimSpace(merged.EndDate) == "" {
		result.Context.HotelInventory = &agentHotelInventoryPlan{Candidate: merged}
		result.Missing = hotelMissing("date_range", "入住日期", "请提供房量调整的开始日期和结束日期（最多 93 天）。")
		return result, nil
	}
	if merged.Capacity == nil && merged.Closed == nil {
		result.Context.HotelInventory = &agentHotelInventoryPlan{Candidate: merged}
		result.Missing = hotelMissing("change", "房量变更", "请提供新的房量，或说明是否关房/开房。")
		return result, nil
	}
	dates, err := hotelAgentDateRange(merged.StartDate, merged.EndDate)
	if err != nil {
		return nil, agentInvalid(err.Error())
	}
	hotel, err := hotelAgentExactProperty(model.DB, tenantID, merged.HotelName)
	if err != nil {
		return nil, err
	}
	room, err := hotelAgentExactRoomType(model.DB, tenantID, hotel.ID, merged.RoomTypeName)
	if err != nil {
		return nil, err
	}
	snapshots := make([]agentHotelInventorySnapshot, 0, len(dates))
	for _, date := range dates {
		var row model.HotelRoomInventory
		queryErr := model.DB.Where("tenant_id = ? AND hotel_id = ? AND room_type_id = ? AND stay_date = ?", tenantID, hotel.ID, room.ID, date).First(&row).Error
		exists := queryErr == nil
		if queryErr != nil && !errors.Is(queryErr, gorm.ErrRecordNotFound) {
			return nil, queryErr
		}
		snapshot := agentHotelInventorySnapshot{StayDate: date.Format("2006-01-02"), Exists: exists}
		if exists {
			snapshot.InventoryID, snapshot.Capacity, snapshot.Reserved, snapshot.Sold, snapshot.Closed = row.ID, row.Capacity, row.Reserved, row.Sold, row.Closed
		}
		snapshot.Hash = hotelAgentSnapshotHash(snapshot)
		afterCapacity, afterClosed := snapshot.Capacity, snapshot.Closed
		if merged.Capacity != nil {
			afterCapacity = *merged.Capacity
		}
		if merged.Closed != nil {
			afterClosed = *merged.Closed
		}
		if afterCapacity < snapshot.Reserved+snapshot.Sold {
			return nil, agentInvalid(fmt.Sprintf("酒店房量 %s 不能低于已预留加已售数量", snapshot.StayDate))
		}
		_ = afterClosed
		snapshots = append(snapshots, snapshot)
	}
	plan := agentHotelInventoryPlan{Candidate: merged, HotelID: hotel.ID, RoomTypeID: room.ID, HotelName: hotel.Name, RoomTypeName: room.Name, Snapshots: snapshots}
	result.Context.HotelInventory = &plan
	preview, err := agentHotelInventoryPreviewJSON(plan)
	if err != nil {
		return nil, err
	}
	result.PreviewJSON = string(preview)
	result.PlanHash = hotelAgentSnapshotHash(plan)
	return result, nil
}

func planHotelRateCalendarChange(tenantID uint, task model.AgentTask, candidate *agentHotelRateCalendarCandidate, pack agentKnowledgePack) (*agentPlanningResult, error) {
	var previous agentTaskContext
	_ = json.Unmarshal([]byte(task.ContextJSON), &previous)
	merged := mergeHotelRateCalendarCandidate(previous.HotelRateCalendar, candidate)
	result := &agentPlanningResult{OperationType: AgentOperationHotelRateCalendarChange, Context: agentTaskContext{OperationType: AgentOperationHotelRateCalendarChange, KnowledgePackID: pack.ID, SkillVersion: pack.Version, SkillHash: pack.Hash}}
	if merged.HotelName == "" {
		result.Context.HotelRateCalendar = &agentHotelRateCalendarPlan{Candidate: merged}
		result.Missing = hotelMissing("hotel_name", "酒店", "请提供酒店名称。")
		return result, nil
	}
	if merged.RoomTypeName == "" {
		result.Context.HotelRateCalendar = &agentHotelRateCalendarPlan{Candidate: merged}
		result.Missing = hotelMissing("room_type_name", "房型", "请提供房型名称。")
		return result, nil
	}
	if merged.RatePlanName == "" {
		result.Context.HotelRateCalendar = &agentHotelRateCalendarPlan{Candidate: merged}
		result.Missing = hotelMissing("rate_plan_name", "价格计划", "请提供价格计划名称。")
		return result, nil
	}
	if merged.StartDate == "" || merged.EndDate == "" {
		result.Context.HotelRateCalendar = &agentHotelRateCalendarPlan{Candidate: merged}
		result.Missing = hotelMissing("date_range", "入住日期", "请提供开始日期和结束日期。")
		return result, nil
	}
	if merged.ClearOverride == nil || !*merged.ClearOverride {
		if merged.RetailPrice == nil || merged.SettlementPrice == nil {
			result.Context.HotelRateCalendar = &agentHotelRateCalendarPlan{Candidate: merged}
			result.Missing = hotelMissing("prices", "价格", "请同时提供零售价和结算价，或明确清除日期覆盖价。")
			return result, nil
		}
	}
	dates, err := hotelAgentDateRange(merged.StartDate, merged.EndDate)
	if err != nil {
		return nil, agentInvalid(err.Error())
	}
	hotel, err := hotelAgentExactProperty(model.DB, tenantID, merged.HotelName)
	if err != nil {
		return nil, err
	}
	room, err := hotelAgentExactRoomType(model.DB, tenantID, hotel.ID, merged.RoomTypeName)
	if err != nil {
		return nil, err
	}
	rate, err := hotelAgentExactRatePlan(model.DB, tenantID, hotel.ID, room.ID, merged.RatePlanName)
	if err != nil {
		return nil, err
	}
	var overrides []model.HotelRatePlanPrice
	if err := model.DB.Where("tenant_id = ? AND hotel_id = ? AND room_type_id = ? AND rate_plan_id = ? AND stay_date BETWEEN ? AND ?", tenantID, hotel.ID, room.ID, rate.ID, dates[0], dates[len(dates)-1]).Find(&overrides).Error; err != nil {
		return nil, err
	}
	byDate := make(map[string]model.HotelRatePlanPrice, len(overrides))
	for _, row := range overrides {
		byDate[row.StayDate.Format("2006-01-02")] = row
	}
	snapshots := make([]agentHotelCalendarSnapshot, 0, len(dates))
	for _, date := range dates {
		row, ok := byDate[date.Format("2006-01-02")]
		snapshot := agentHotelCalendarSnapshot{StayDate: date.Format("2006-01-02"), Exists: ok, RetailPrice: rate.RetailPriceCents, Settlement: rate.SettlementPriceCents}
		if ok {
			snapshot.RowID, snapshot.RetailPrice, snapshot.Settlement = row.ID, row.RetailPriceCents, row.SettlementPriceCents
		}
		snapshot.Hash = hotelAgentSnapshotHash(snapshot)
		snapshots = append(snapshots, snapshot)
	}
	plan := agentHotelRateCalendarPlan{Candidate: merged, HotelID: hotel.ID, RoomTypeID: room.ID, RatePlanID: rate.ID, HotelName: hotel.Name, RoomTypeName: room.Name, RatePlanName: rate.Name, BaseRetail: rate.RetailPriceCents, BaseSettlement: rate.SettlementPriceCents, Snapshots: snapshots}
	if merged.ClearOverride == nil || !*merged.ClearOverride {
		retail, err1 := hotelAgentRetailMoney(merged.RetailPrice)
		settlement, err2 := hotelAgentMoney(merged.SettlementPrice)
		if err1 != nil || err2 != nil || settlement > retail {
			return nil, agentInvalid("价格计划覆盖价无效，结算价不能高于零售价")
		}
		_ = retail
		_ = settlement
	}
	result.Context.HotelRateCalendar = &plan
	preview, err := agentHotelCalendarPreviewJSON(AgentOperationHotelRateCalendarChange, hotel.Name, rate.Name, snapshots, merged.RetailPrice, merged.SettlementPrice, merged.ClearOverride, rate.RetailPriceCents, rate.SettlementPriceCents, []string{"确认前不会写入价格计划日历。", "清除覆盖价后该入住日恢复价格计划基础价。", "已售事实和历史预约价格快照不会被回写。"})
	if err != nil {
		return nil, err
	}
	result.PreviewJSON = string(preview)
	result.PlanHash = hotelAgentSnapshotHash(plan)
	return result, nil
}

func planHotelProductCalendarChange(tenantID uint, task model.AgentTask, candidate *agentHotelProductCalendarCandidate, pack agentKnowledgePack) (*agentPlanningResult, error) {
	var previous agentTaskContext
	_ = json.Unmarshal([]byte(task.ContextJSON), &previous)
	merged := mergeHotelProductCalendarCandidate(previous.HotelProductCalendar, candidate)
	result := &agentPlanningResult{OperationType: AgentOperationHotelProductCalendarChange, Context: agentTaskContext{OperationType: AgentOperationHotelProductCalendarChange, KnowledgePackID: pack.ID, SkillVersion: pack.Version, SkillHash: pack.Hash}}
	if merged.HotelName == "" {
		result.Context.HotelProductCalendar = &agentHotelProductCalendarPlan{Candidate: merged}
		result.Missing = hotelMissing("hotel_name", "酒店", "请提供酒店名称。")
		return result, nil
	}
	if merged.ProductName == "" {
		result.Context.HotelProductCalendar = &agentHotelProductCalendarPlan{Candidate: merged}
		result.Missing = hotelMissing("product_name", "酒店产品", "请提供独立酒店产品名称。")
		return result, nil
	}
	if merged.StartDate == "" || merged.EndDate == "" {
		result.Context.HotelProductCalendar = &agentHotelProductCalendarPlan{Candidate: merged}
		result.Missing = hotelMissing("date_range", "入住日期", "请提供开始日期和结束日期。")
		return result, nil
	}
	if merged.ClearOverride == nil || !*merged.ClearOverride {
		if merged.RetailPrice == nil || merged.SettlementPrice == nil {
			result.Context.HotelProductCalendar = &agentHotelProductCalendarPlan{Candidate: merged}
			result.Missing = hotelMissing("prices", "价格", "请同时提供销售零售价和结算价，或明确清除日期覆盖价。")
			return result, nil
		}
	}
	dates, err := hotelAgentDateRange(merged.StartDate, merged.EndDate)
	if err != nil {
		return nil, agentInvalid(err.Error())
	}
	hotel, err := hotelAgentExactProperty(model.DB, tenantID, merged.HotelName)
	if err != nil {
		return nil, err
	}
	hp, product, err := hotelAgentExactProduct(model.DB, tenantID, hotel.ID, merged.ProductName)
	if err != nil {
		return nil, err
	}
	if hp.SaleMode != "calendar_room" {
		return nil, agentInvalid("预售房产品不支持销售价格日历")
	}
	var revision model.HotelProductRevision
	if err := model.DB.Where("id = ? AND hotel_product_id = ? AND tenant_id = ?", hp.CurrentRevisionID, hp.ID, tenantID).First(&revision).Error; err != nil {
		return nil, err
	}
	var overrides []model.HotelProductCalendarPrice
	if err := model.DB.Where("tenant_id = ? AND hotel_product_id = ? AND hotel_product_revision_id = ? AND stay_date BETWEEN ? AND ?", tenantID, hp.ID, revision.ID, dates[0], dates[len(dates)-1]).Find(&overrides).Error; err != nil {
		return nil, err
	}
	byDate := make(map[string]model.HotelProductCalendarPrice, len(overrides))
	for _, row := range overrides {
		byDate[row.StayDate.Format("2006-01-02")] = row
	}
	snapshots := make([]agentHotelCalendarSnapshot, 0, len(dates))
	for _, date := range dates {
		row, ok := byDate[date.Format("2006-01-02")]
		snapshot := agentHotelCalendarSnapshot{StayDate: date.Format("2006-01-02"), Exists: ok, RetailPrice: revision.BaseRetailPriceCents, Settlement: revision.BaseSettlementPriceCents}
		if ok {
			snapshot.RowID, snapshot.RetailPrice, snapshot.Settlement = row.ID, row.RetailPriceCents, row.SettlementPriceCents
		}
		snapshot.Hash = hotelAgentSnapshotHash(snapshot)
		snapshots = append(snapshots, snapshot)
	}
	if merged.ClearOverride == nil || !*merged.ClearOverride {
		retail, err1 := hotelAgentRetailMoney(merged.RetailPrice)
		settlement, err2 := hotelAgentMoney(merged.SettlementPrice)
		if err1 != nil || err2 != nil || settlement > retail {
			return nil, agentInvalid("酒店产品覆盖价无效，结算价不能高于零售价")
		}
		_ = retail
		_ = settlement
	}
	plan := agentHotelProductCalendarPlan{Candidate: merged, HotelID: hotel.ID, HotelProductID: hp.ID, RevisionID: revision.ID, HotelName: hotel.Name, ProductName: product.Name, BaseRetail: revision.BaseRetailPriceCents, BaseSettlement: revision.BaseSettlementPriceCents, Snapshots: snapshots}
	result.Context.HotelProductCalendar = &plan
	preview, err := agentHotelCalendarPreviewJSON(AgentOperationHotelProductCalendarChange, hotel.Name, product.Name, snapshots, merged.RetailPrice, merged.SettlementPrice, merged.ClearOverride, revision.BaseRetailPriceCents, revision.BaseSettlementPriceCents, []string{"确认前不会写入酒店产品价格日历。", "仅日历房允许设置销售价日历，预售房会拒绝。", "确认时会锁定当前产品 revision，revision 变化则要求重新预览。"})
	if err != nil {
		return nil, err
	}
	result.PreviewJSON = string(preview)
	result.PlanHash = hotelAgentSnapshotHash(plan)
	return result, nil
}

func planHotelReservationStatusChange(tenantID uint, task model.AgentTask, candidate *agentHotelReservationStatusCandidate, pack agentKnowledgePack) (*agentPlanningResult, error) {
	var previous agentTaskContext
	_ = json.Unmarshal([]byte(task.ContextJSON), &previous)
	merged := mergeHotelReservationCandidate(previous.HotelReservationStatus, candidate)
	result := &agentPlanningResult{OperationType: AgentOperationHotelReservationStatusChange, Context: agentTaskContext{OperationType: AgentOperationHotelReservationStatusChange, KnowledgePackID: pack.ID, SkillVersion: pack.Version, SkillHash: pack.Hash}}
	if merged.ReservationNo == "" {
		result.Context.HotelReservationStatus = &agentHotelReservationStatusPlan{Candidate: merged}
		result.Missing = hotelMissing("reservation_no", "住宿预订号", "请提供要登记的精确住宿预订号。")
		return result, nil
	}
	if merged.TargetStatus == "" {
		result.Context.HotelReservationStatus = &agentHotelReservationStatusPlan{Candidate: merged}
		result.Missing = hotelMissing("target_status", "目标状态", "请明确登记为已入住、已离店或未到店。")
		return result, nil
	}
	if merged.TargetStatus != "checked_in" && merged.TargetStatus != "checked_out" && merged.TargetStatus != "no_show" {
		return nil, agentInvalid("AI 仅支持登记已入住、已离店或未到店")
	}
	var row struct {
		model.HotelReservation
		HotelName    string `gorm:"column:hotel_name"`
		RoomTypeName string `gorm:"column:room_type_name"`
	}
	if err := model.DB.Table("hotel_reservations AS reservation").Select("reservation.*, hotel.name AS hotel_name, room.name AS room_type_name").Joins("JOIN hotel_properties AS hotel ON hotel.id = reservation.hotel_id AND hotel.tenant_id = reservation.supplier_tenant_id").Joins("JOIN hotel_room_types AS room ON room.id = reservation.room_type_id AND room.tenant_id = reservation.supplier_tenant_id").Where("reservation.deleted_at IS NULL AND reservation.id IN (SELECT id FROM hotel_reservations WHERE supplier_tenant_id = ? AND reservation_no = ? AND deleted_at IS NULL)", tenantID, strings.TrimSpace(merged.ReservationNo)).First(&row).Error; err != nil {
		return nil, agentInvalid("未找到当前租户内匹配的住宿预订")
	}
	allowed := map[string]map[string]bool{"confirmed": {"checked_in": true, "no_show": true}, "checked_in": {"checked_out": true}}
	if !allowed[row.Status][merged.TargetStatus] {
		return nil, agentInvalid(fmt.Sprintf("住宿预订当前状态为 %s，不能登记为 %s", row.Status, merged.TargetStatus))
	}
	if merged.TargetStatus == "no_show" && strings.TrimSpace(merged.Reason) == "" {
		result.Context.HotelReservationStatus = &agentHotelReservationStatusPlan{Candidate: merged}
		result.Missing = hotelMissing("reason", "未到店原因", "登记未到店必须填写原因。")
		return result, nil
	}
	snapshot := hotelAgentSnapshotHash(map[string]interface{}{"id": row.ID, "status": row.Status, "updated_at": row.UpdatedAt.UTC().Format(time.RFC3339Nano)})
	plan := agentHotelReservationStatusPlan{Candidate: merged, ReservationID: row.ID, ReservationNo: row.ReservationNo, HotelName: row.HotelName, RoomTypeName: row.RoomTypeName, CheckInDate: row.CheckInDate.Format("2006-01-02"), CheckOutDate: row.CheckOutDate.Format("2006-01-02"), CurrentStatus: row.Status, SnapshotHash: snapshot}
	result.Context.HotelReservationStatus = &plan
	preview, _ := json.Marshal(map[string]interface{}{"operation_type": AgentOperationHotelReservationStatusChange, "reservation_no": row.ReservationNo, "hotel_name": row.HotelName, "room_type_name": row.RoomTypeName, "check_in_date": plan.CheckInDate, "check_out_date": plan.CheckOutDate, "before_status": row.Status, "after_status": merged.TargetStatus, "reason": merged.Reason, "safety": []string{"只登记现有酒景套餐住宿履约状态，不创建、取消、改期或退款。", "预约 Saga 进行中或待退款时服务端会拒绝。"}})
	result.PreviewJSON = string(preview)
	result.PlanHash = hotelAgentSnapshotHash(plan)
	return result, nil
}

func (s *AgentTaskService) planHotelFromEnvelope(tenantID uint, task model.AgentTask, input string, config model.PlatformAIConfig, envelope *agentAIEnvelope) (*agentPlanningResult, error) {
	pack, err := agentKnowledgePackForContext(envelope.OperationType, task.ContextJSON)
	if err != nil {
		return nil, err
	}
	var result *agentPlanningResult
	switch envelope.OperationType {
	case AgentOperationHotelInventoryChange:
		result, err = planHotelInventoryChange(tenantID, task, input, envelope.HotelInventory, pack)
	case AgentOperationHotelRateCalendarChange:
		result, err = planHotelRateCalendarChange(tenantID, task, envelope.HotelRateCalendar, pack)
	case AgentOperationHotelProductCalendarChange:
		result, err = planHotelProductCalendarChange(tenantID, task, envelope.HotelProductCalendar, pack)
	case AgentOperationHotelReservationStatusChange:
		result, err = planHotelReservationStatusChange(tenantID, task, envelope.HotelReservationStatus, pack)
	default:
		return nil, agentInvalid("酒店 AI 操作类型不受支持")
	}
	if result != nil {
		result.Provider, result.Model = config.Provider, config.Model
	}
	return result, err
}

// --- Confirmation -------------------------------------------------------

func hotelAgentContextHash(plan interface{}) string { return hotelAgentSnapshotHash(plan) }

func hotelAgentInventoryInputs(plan agentHotelInventoryPlan) []HotelInventoryInput {
	inputs := make([]HotelInventoryInput, 0, len(plan.Snapshots))
	for _, snapshot := range plan.Snapshots {
		capacity, closed := snapshot.Capacity, snapshot.Closed
		if plan.Candidate.Capacity != nil {
			capacity = *plan.Candidate.Capacity
		}
		if plan.Candidate.Closed != nil {
			closed = *plan.Candidate.Closed
		}
		inputs = append(inputs, HotelInventoryInput{StayDate: snapshot.StayDate, Capacity: capacity, Closed: closed})
	}
	return inputs
}
func hotelAgentRateInputs(plan agentHotelRateCalendarPlan) []HotelRatePlanPriceInput {
	inputs := make([]HotelRatePlanPriceInput, 0, len(plan.Snapshots))
	for _, snapshot := range plan.Snapshots {
		input := HotelRatePlanPriceInput{StayDate: snapshot.StayDate}
		if plan.Candidate.ClearOverride != nil && *plan.Candidate.ClearOverride {
			input.ClearOverride = true
		} else {
			input.RetailPriceCents, _ = hotelAgentRetailMoney(plan.Candidate.RetailPrice)
			input.SettlementPriceCents, _ = hotelAgentMoney(plan.Candidate.SettlementPrice)
		}
		inputs = append(inputs, input)
	}
	return inputs
}
func hotelAgentProductCalendarInputs(plan agentHotelProductCalendarPlan) []HotelProductCalendarPriceInput {
	inputs := make([]HotelProductCalendarPriceInput, 0, len(plan.Snapshots))
	for _, snapshot := range plan.Snapshots {
		input := HotelProductCalendarPriceInput{StayDate: snapshot.StayDate}
		if plan.Candidate.ClearOverride != nil && *plan.Candidate.ClearOverride {
			input.ClearOverride = true
		} else {
			input.RetailPriceCents, _ = hotelAgentRetailMoney(plan.Candidate.RetailPrice)
			input.SettlementPriceCents, _ = hotelAgentMoney(plan.Candidate.SettlementPrice)
		}
		inputs = append(inputs, input)
	}
	return inputs
}

func hotelAgentInventorySnapshotCurrent(tx *gorm.DB, tenantID uint, plan agentHotelInventoryPlan) error {
	var roomType model.HotelRoomType
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND hotel_id = ?", plan.RoomTypeID, tenantID, plan.HotelID).First(&roomType).Error; err != nil {
		return err
	}
	for _, snapshot := range plan.Snapshots {
		var row model.HotelRoomInventory
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND hotel_id = ? AND room_type_id = ? AND stay_date = ?", tenantID, plan.HotelID, plan.RoomTypeID, snapshot.StayDate).First(&row).Error
		current := snapshot
		current.InventoryID, current.Exists, current.Capacity, current.Reserved, current.Sold, current.Closed = 0, false, 0, 0, 0, false
		current.Hash = ""
		if err == nil {
			current.InventoryID, current.Exists, current.Capacity, current.Reserved, current.Sold, current.Closed = row.ID, true, row.Capacity, row.Reserved, row.Sold, row.Closed
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		current.Hash = hotelAgentSnapshotHash(current)
		if current.Hash != snapshot.Hash {
			return agentConflict("酒店房量在预览后已变化，请重新生成预览")
		}
	}
	return nil
}
func hotelAgentCalendarSnapshotCurrent(tx *gorm.DB, tenantID uint, hotelID, roomTypeID, ratePlanID uint, plan agentHotelRateCalendarPlan) error {
	var rate model.HotelRatePlan
	if err := tx.Where("id = ? AND tenant_id = ? AND hotel_id = ? AND room_type_id = ?", ratePlanID, tenantID, hotelID, roomTypeID).First(&rate).Error; err != nil {
		return err
	}
	if rate.RetailPriceCents != plan.BaseRetail || rate.SettlementPriceCents != plan.BaseSettlement {
		return agentConflict("价格计划基础价在预览后已变化，请重新生成预览")
	}
	for _, snapshot := range plan.Snapshots {
		var row model.HotelRatePlanPrice
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND hotel_id = ? AND room_type_id = ? AND rate_plan_id = ? AND stay_date = ?", tenantID, hotelID, roomTypeID, ratePlanID, snapshot.StayDate).First(&row).Error
		current := snapshot
		current.RowID, current.Exists, current.RetailPrice, current.Settlement = 0, false, rate.RetailPriceCents, rate.SettlementPriceCents
		current.Hash = ""
		if err == nil {
			current.RowID, current.Exists, current.RetailPrice, current.Settlement = row.ID, true, row.RetailPriceCents, row.SettlementPriceCents
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		current.Hash = hotelAgentSnapshotHash(current)
		if current.Hash != snapshot.Hash {
			return agentConflict("价格计划日历在预览后已变化，请重新生成预览")
		}
	}
	return nil
}
func hotelAgentProductCalendarSnapshotCurrent(tx *gorm.DB, tenantID uint, plan agentHotelProductCalendarPlan) error {
	var hp model.HotelProduct
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", plan.HotelProductID, tenantID).First(&hp).Error; err != nil {
		return err
	}
	if hp.CurrentRevisionID != plan.RevisionID || hp.SaleMode != "calendar_room" {
		return agentConflict("酒店产品 revision 或销售模式在预览后已变化，请重新生成预览")
	}
	var revision model.HotelProductRevision
	if err := tx.Where("id = ? AND hotel_product_id = ? AND tenant_id = ?", plan.RevisionID, hp.ID, tenantID).First(&revision).Error; err != nil {
		return err
	}
	if revision.BaseRetailPriceCents != plan.BaseRetail || revision.BaseSettlementPriceCents != plan.BaseSettlement {
		return agentConflict("酒店产品基础价在预览后已变化，请重新生成预览")
	}
	for _, snapshot := range plan.Snapshots {
		var row model.HotelProductCalendarPrice
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND hotel_product_id = ? AND hotel_product_revision_id = ? AND stay_date = ?", tenantID, hp.ID, plan.RevisionID, snapshot.StayDate).First(&row).Error
		current := snapshot
		current.RowID, current.Exists, current.RetailPrice, current.Settlement = 0, false, revision.BaseRetailPriceCents, revision.BaseSettlementPriceCents
		current.Hash = ""
		if err == nil {
			current.RowID, current.Exists, current.RetailPrice, current.Settlement = row.ID, true, row.RetailPriceCents, row.SettlementPriceCents
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		current.Hash = hotelAgentSnapshotHash(current)
		if current.Hash != snapshot.Hash {
			return agentConflict("酒店产品价格日历在预览后已变化，请重新生成预览")
		}
	}
	return nil
}

func (s *AgentTaskService) confirmHotelTask(tenantID, actorUserID uint, actorRole string, task model.AgentTask, operation string) (*AgentTaskView, error) {
	var response *AgentTaskView
	err := model.Write(func(tx *gorm.DB) error {
		var locked model.AgentTask
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND actor_user_id = ?", task.ID, tenantID, actorUserID).First(&locked).Error; err != nil {
			return err
		}
		if locked.State == AgentTaskCompleted {
			response = agentTaskViewFromModel(locked)
			return nil
		}
		if locked.State != AgentTaskAwaitingConfirmation || locked.OperationType != operation {
			return agentConflict("agent task is no longer executable")
		}
		var context agentTaskContext
		if err := json.Unmarshal([]byte(locked.ContextJSON), &context); err != nil {
			return agentConflict("酒店 AI 预览无法恢复，请重新生成预览")
		}
		if strings.TrimSpace(locked.PlanHash) == "" {
			return agentConflict("酒店 AI 预览缺少版本校验，请重新生成预览")
		}
		var result interface{}
		switch operation {
		case AgentOperationHotelInventoryChange:
			if context.HotelInventory == nil || hotelAgentContextHash(*context.HotelInventory) != locked.PlanHash {
				return agentConflict("酒店房量预览已失效，请重新生成预览")
			}
			plan := *context.HotelInventory
			if err := hotelAgentInventorySnapshotCurrent(tx, tenantID, plan); err != nil {
				return err
			}
			if err := (&HotelService{}).setInventoryTx(tx, tenantID, plan.HotelID, plan.RoomTypeID, actorUserID, hotelAgentInventoryInputs(plan)); err != nil {
				return err
			}
			result = map[string]interface{}{"operation_type": operation, "hotel_name": plan.HotelName, "room_type_name": plan.RoomTypeName, "dates": len(plan.Snapshots), "status": "completed"}
		case AgentOperationHotelRateCalendarChange:
			if context.HotelRateCalendar == nil || hotelAgentContextHash(*context.HotelRateCalendar) != locked.PlanHash {
				return agentConflict("价格计划日历预览已失效，请重新生成预览")
			}
			plan := *context.HotelRateCalendar
			rate := model.HotelRatePlan{}
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND hotel_id = ? AND room_type_id = ?", plan.RatePlanID, tenantID, plan.HotelID, plan.RoomTypeID).First(&rate).Error; err != nil {
				return err
			}
			if err := hotelAgentCalendarSnapshotCurrent(tx, tenantID, plan.HotelID, plan.RoomTypeID, plan.RatePlanID, plan); err != nil {
				return err
			}
			if err := (&HotelService{}).setRatePlanCalendarTx(tx, tenantID, plan.HotelID, plan.RoomTypeID, plan.RatePlanID, actorUserID, hotelAgentRateInputs(plan)); err != nil {
				return err
			}
			result = map[string]interface{}{"operation_type": operation, "hotel_name": plan.HotelName, "rate_plan_name": plan.RatePlanName, "dates": len(plan.Snapshots), "status": "completed"}
		case AgentOperationHotelProductCalendarChange:
			if context.HotelProductCalendar == nil || hotelAgentContextHash(*context.HotelProductCalendar) != locked.PlanHash {
				return agentConflict("酒店产品价格日历预览已失效，请重新生成预览")
			}
			plan := *context.HotelProductCalendar
			if err := hotelAgentProductCalendarSnapshotCurrent(tx, tenantID, plan); err != nil {
				return err
			}
			if err := (&HotelProductService{}).setCalendarTx(tx, tenantID, plan.HotelProductID, actorUserID, hotelAgentProductCalendarInputs(plan)); err != nil {
				return err
			}
			result = map[string]interface{}{"operation_type": operation, "hotel_name": plan.HotelName, "product_name": plan.ProductName, "dates": len(plan.Snapshots), "status": "completed"}
		case AgentOperationHotelReservationStatusChange:
			if context.HotelReservationStatus == nil || hotelAgentContextHash(*context.HotelReservationStatus) != locked.PlanHash {
				return agentConflict("住宿履约预览已失效，请重新生成预览")
			}
			plan := *context.HotelReservationStatus
			var reservation model.HotelReservation
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND supplier_tenant_id = ?", plan.ReservationID, tenantID).First(&reservation).Error; err != nil {
				return err
			}
			currentHash := hotelAgentSnapshotHash(map[string]interface{}{"id": reservation.ID, "status": reservation.Status, "updated_at": reservation.UpdatedAt.UTC().Format(time.RFC3339Nano)})
			if currentHash != plan.SnapshotHash {
				return agentConflict("住宿预订在预览后已变化，请重新生成预览")
			}
			if err := setReservationStatusTx(tx, tenantID, plan.ReservationID, actorUserID, plan.Candidate.TargetStatus, plan.Candidate.Reason); err != nil {
				return err
			}
			result = map[string]interface{}{"operation_type": operation, "reservation_no": plan.ReservationNo, "before_status": plan.CurrentStatus, "after_status": plan.Candidate.TargetStatus, "status": "completed"}
		default:
			return agentInvalid("酒店 AI 操作类型不受支持")
		}
		confirmedAt := time.Now()
		locked.State, locked.ConfirmedAt = AgentTaskExecuting, &confirmedAt
		locked.Version++
		if err := tx.Save(&locked).Error; err != nil {
			return err
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			return err
		}
		if err := recordAuditTx(tx, actorUserID, tenantID, actorRole, "tenant", "agent.task.confirm", "agent_task", locked.ID, "confirm AI planned hotel operation", locked.PreviewJSON, string(encoded)); err != nil {
			return err
		}
		now := time.Now()
		locked.State, locked.ResultJSON, locked.CompletedAt, locked.ErrorMessage = AgentTaskCompleted, string(encoded), &now, ""
		locked.Version++
		if err := tx.Save(&locked).Error; err != nil {
			return err
		}
		response = agentTaskViewFromModel(locked)
		stored, _ := json.Marshal(response)
		return tx.Model(&locked).Update("last_response_json", string(stored)).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, agentNotFound("agent task not found")
		}
		return nil, err
	}
	return response, nil
}

func (s *AgentTaskService) confirmHotelInventoryTask(tenantID, actorUserID uint, actorRole string, task model.AgentTask) (*AgentTaskView, error) {
	return s.confirmHotelTask(tenantID, actorUserID, actorRole, task, AgentOperationHotelInventoryChange)
}
func (s *AgentTaskService) confirmHotelRateCalendarTask(tenantID, actorUserID uint, actorRole string, task model.AgentTask) (*AgentTaskView, error) {
	return s.confirmHotelTask(tenantID, actorUserID, actorRole, task, AgentOperationHotelRateCalendarChange)
}
func (s *AgentTaskService) confirmHotelProductCalendarTask(tenantID, actorUserID uint, actorRole string, task model.AgentTask) (*AgentTaskView, error) {
	return s.confirmHotelTask(tenantID, actorUserID, actorRole, task, AgentOperationHotelProductCalendarChange)
}
func (s *AgentTaskService) confirmHotelReservationStatusTask(tenantID, actorUserID uint, actorRole string, task model.AgentTask) (*AgentTaskView, error) {
	return s.confirmHotelTask(tenantID, actorUserID, actorRole, task, AgentOperationHotelReservationStatusChange)
}
