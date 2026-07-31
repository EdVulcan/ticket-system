package model

import (
	"time"

	"gorm.io/gorm"
)

// Base model with common fields
type Base struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

// Tenant 租户/主体
type Tenant struct {
	Base
	Name         string             `gorm:"size:100;not null" json:"name"`
	SystemCode   string             `gorm:"size:50;uniqueIndex;not null" json:"system_code"`       // 唯一系统编号
	SecretKey    string             `gorm:"size:50;not null" json:"-"`                             // API签名密钥
	Status       string             `gorm:"size:20;not null;default:'active';index" json:"status"` // active, frozen, closed
	Contact      string             `gorm:"size:50" json:"contact"`
	Phone        string             `gorm:"size:20" json:"phone"`
	Address      string             `gorm:"size:255" json:"address"`
	Users        []User             `json:"users,omitempty"`
	Capabilities []TenantCapability `json:"capabilities,omitempty"`
}

// User 用户
type User struct {
	Base
	Username string `gorm:"size:50;uniqueIndex:idx_tenant_username;not null" json:"username"`
	Password string `gorm:"size:100;not null" json:"-"`
	Role     string `gorm:"size:20;default:'staff'" json:"role"` // admin, staff
	TenantID     uint   `gorm:"uniqueIndex:idx_tenant_username" json:"tenant_id"`
	TokenVersion int    `gorm:"not null;default:1" json:"-"`
	Tenant   Tenant `json:"tenant,omitempty"`
}

// Staff 员工 (一线作业人员)
type Staff struct {
	Base
	Name           string               `gorm:"size:50;not null" json:"name"`
	JobNumber      string               `gorm:"size:50;uniqueIndex:idx_tenant_job;not null" json:"job_number"` // 工号
	Password       string               `gorm:"size:100;not null" json:"-"`
	Roles          string               `gorm:"size:100;default:'seller'" json:"roles"` // seller, checker (comma separated)
	Status         string               `gorm:"size:20;default:'active'" json:"status"` // active, frozen
	TenantID       uint                 `gorm:"uniqueIndex:idx_tenant_job" json:"tenant_id"`
	TokenVersion   int                  `gorm:"not null;default:1" json:"-"`
	ResourceScopes []StaffResourceScope `gorm:"foreignKey:StaffID" json:"resource_scopes,omitempty"`
}

// CheckPoint 检票点
type CheckPoint struct {
	Base
	Name         string   `gorm:"size:100;not null" json:"name"`
	Location     string   `gorm:"size:100" json:"location"`
	TenantID     uint     `json:"tenant_id"`
	ScenicAreaID uint     `gorm:"index" json:"scenic_area_id"`
	Devices      []Device `json:"devices,omitempty"`
}

// Device 终端设备
type Device struct {
	Base
	Name          string      `gorm:"size:100;not null" json:"name"`
	SerialNumber  string      `gorm:"size:100;uniqueIndex;not null" json:"serial_number"` // 设备序列号
	Type          string      `gorm:"size:20;not null" json:"type"`                       // gate, handheld, pos
	Status        string      `gorm:"size:20;default:'offline'" json:"status"`            // online, offline, fault
	LastHeartbeat *time.Time  `json:"last_heartbeat"`
	IPAddress     string      `gorm:"size:50" json:"ip_address"`
	MACAddress    string      `gorm:"size:50" json:"mac_address"`
	TenantID      uint        `json:"tenant_id"`
	ScenicAreaID  uint        `gorm:"index" json:"scenic_area_id"`
	CheckPointID  *uint       `json:"check_point_id"` // 绑定检票点 (可选)
	CheckPoint    *CheckPoint `json:"check_point,omitempty"`
	AuthKeyHash   string      `gorm:"size:64" json:"-"`
	AuthKey       string      `gorm:"-" json:"auth_key,omitempty"`
}

// TicketRule 检票规则 (M选N核心)
type TicketRule struct {
	Base
	Name         string      `gorm:"size:100;not null" json:"name"`
	TenantID     uint        `json:"tenant_id"`
	ValidityType string      `gorm:"size:20;default:'date'" json:"validity_type"` // date, days, period
	Groups       []RuleGroup `gorm:"foreignKey:RuleID" json:"groups,omitempty"`
}

// RuleGroup 规则分组 (如: A/B一组, C/D一组)
type RuleGroup struct {
	Base
	RuleID          uint       `json:"rule_id"`
	GroupName       string     `gorm:"size:50" json:"group_name"`
	MaxTotalCheckIn int        `gorm:"default:0" json:"max_total_check_in"` // 组内总核销次数 (0=不限, M值)
	Items           []RuleItem `gorm:"foreignKey:GroupID" json:"items,omitempty"`
}

// RuleItem 规则明细 (单点限制)
type RuleItem struct {
	Base
	GroupID       uint       `json:"group_id"`
	CheckPointID  uint       `json:"check_point_id"`
	CheckPoint    CheckPoint `gorm:"foreignKey:CheckPointID" json:"check_point,omitempty"`
	MaxPerCheckIn int        `gorm:"default:1" json:"max_per_check_in"` // 单点允许核销次数
}

// Product 票务产品
type Product struct {
	Base
	Name         string     `gorm:"size:100;not null" json:"name"`
	Price        float64    `gorm:"type:decimal(10,2)" json:"price"`
	TenantID     uint       `json:"tenant_id"`
	ScenicAreaID uint       `gorm:"index" json:"scenic_area_id"`
	RuleID       uint       `json:"rule_id"`
	Rule         TicketRule `gorm:"foreignKey:RuleID" json:"rule,omitempty"`
	Type         string     `gorm:"size:20;default:'online'" json:"type"`   // online, offline
	Status       string     `gorm:"size:20;default:'online'" json:"status"` // online, offline (下架)

	// --- B2B Distribution Fields ---
	IsDistributable bool `json:"is_distributable" gorm:"default:false"` // 是否允许分销(供应商设置)
	SourceProductID uint `json:"source_product_id" gorm:"index"`        // 来源产品ID (分销商端)
	SourceTenantID  uint `json:"source_tenant_id" gorm:"index"`         // 来源供应商ID (分销商端)

	// Fulfillment ownership is server-controlled. Source* is retained for
	// backward-compatible reads while existing listings are migrated.
	FulfillmentProductID    uint  `json:"fulfillment_product_id" gorm:"index"`
	FulfillmentTenantID     uint  `json:"fulfillment_tenant_id" gorm:"index"`
	FulfillmentScenicAreaID uint  `json:"fulfillment_scenic_area_id" gorm:"index"`
	ProductOfferID          uint  `json:"product_offer_id" gorm:"index"`
	CurrentRevisionID       uint  `gorm:"index" json:"current_revision_id"`
	ResolvedCommissionBPS   int64 `gorm:"-" json:"-"`

	// --- New Fields for Ticket Management ---
	// Basic
	SettlementPrice float64 `gorm:"type:decimal(10,2)" json:"settlement_price"`
	Tags            string  `gorm:"size:255" json:"tags"` // JSON array

	// Validity
	ValidityType      string     `gorm:"size:20;default:'date'" json:"validity_type"` // fixed_date, days
	ValidityStartDate *time.Time `json:"validity_start_date"`
	ValidityEndDate   *time.Time `json:"validity_end_date"`
	ValidityDays      int        `json:"validity_days"`

	// Stock & TimeSlot
	StockType      string `gorm:"size:20;default:'unlimited'" json:"stock_type"` // unlimited, daily, total
	DailyStock     int    `json:"daily_stock"`
	TimeSlotConfig string `gorm:"type:text" json:"time_slot_config"` // JSON

	// RealName
	RealNameRequired bool   `json:"real_name_required"`
	RegionLimit      string `gorm:"size:255" json:"region_limit"` // JSON: ["3301"]
	LimitPerPhone    int    `json:"limit_per_phone"`
	LimitPerID       int    `json:"limit_per_id"`

	// Refund
	RefundType string `gorm:"size:20;default:'no_refund'" json:"refund_type"` // no_refund, free, ladder
	RefundRule string `gorm:"type:text" json:"refund_rule"`                   // JSON

	// Code Mode
	CodeMode string `gorm:"size:20;default:'order'" json:"code_mode"` // order, ticket
}

// Order 订单
type Order struct {
	Base
	OrderNo              string      `gorm:"size:50;uniqueIndex;not null" json:"order_no"`
	TenantID             uint        `gorm:"uniqueIndex:idx_order_external,priority:1" json:"tenant_id"`
	TotalAmount          float64     `gorm:"type:decimal(10,2)" json:"total_amount"`
	Status               string      `gorm:"size:20;default:'unpaid'" json:"status"` // unpaid, paid, cancelled, refunded, partial_refunded, completed
	ExpiresAt            *time.Time  `json:"expires_at,omitempty"`
	ContactName          string      `gorm:"size:50" json:"contact_name"`
	ContactPhone         string      `gorm:"size:20" json:"contact_phone"`
	VisitorID            string      `gorm:"size:50" json:"visitor_id,omitempty"`
	VisitorRegion        string      `gorm:"size:50" json:"visitor_region,omitempty"`
	Channel              string      `gorm:"size:50;default:'online';uniqueIndex:idx_order_external,priority:2" json:"channel"` // online, ota, window
	ChannelAccountID     uint        `gorm:"index" json:"channel_account_id"`
	ChannelReservationID uint        `gorm:"index" json:"channel_reservation_id,omitempty"`
	ExternalNo           *string     `gorm:"size:100;uniqueIndex:idx_order_external,priority:3" json:"external_no,omitempty"`
	Items                []OrderItem `gorm:"foreignKey:OrderID" json:"items,omitempty"`
}

// OrderItem 订单明细 (按产品聚合)
type OrderItem struct {
	Base
	OrderID                 uint       `json:"order_id"`
	ProductID               uint       `json:"product_id"`
	Product                 Product    `gorm:"foreignKey:ProductID" json:"product,omitempty"` // Added relation
	ProductName             string     `gorm:"size:100" json:"product_name"`
	Price                   float64    `gorm:"type:decimal(10,2)" json:"price"`
	SettlementPrice         float64    `gorm:"type:decimal(10,2)" json:"settlement_price"`
	Quantity                int        `json:"quantity"`
	UseDate                 *time.Time `gorm:"type:date" json:"use_date"`
	StockSlot               string     `gorm:"size:50" json:"stock_slot,omitempty"`
	VisitorName             string     `gorm:"size:50" json:"visitor_name,omitempty"`
	VisitorPhone            string     `gorm:"size:20" json:"visitor_phone,omitempty"`
	VisitorID               string     `gorm:"size:50" json:"visitor_id,omitempty"`
	VisitorRegion           string     `gorm:"size:50" json:"visitor_region,omitempty"`
	ValidityType            string     `gorm:"size:20" json:"validity_type"`
	ValidityStart           *time.Time `json:"validity_start"`
	ValidityEnd             *time.Time `json:"validity_end"`
	FulfillmentProductID    uint       `json:"fulfillment_product_id" gorm:"index"`
	FulfillmentTenantID     uint       `json:"fulfillment_tenant_id" gorm:"index"`
	FulfillmentScenicAreaID uint       `json:"fulfillment_scenic_area_id" gorm:"index"`
	ProductOfferID          uint       `json:"product_offer_id" gorm:"index"`
	FulfillmentOrderID      uint       `json:"fulfillment_order_id" gorm:"index"`
	ProductRevisionID       uint       `gorm:"index" json:"product_revision_id"`
	CashCostCents           int64      `json:"cash_cost_cents"`
	CreditCostCents         int64      `json:"credit_cost_cents"`
	CommissionBPS           int64      `gorm:"not null;default:0" json:"commission_bps"`
	Tickets                 []Ticket   `gorm:"foreignKey:OrderItemID" json:"tickets,omitempty"`
}

// Ticket 具体票据 (对应一个游客或一个二维码)
type Ticket struct {
	Base
	OrderItemID             uint      `json:"order_item_id"`
	OrderItem               OrderItem `gorm:"foreignKey:OrderItemID" json:"order_item,omitempty"` // Added relation
	OrderID                 uint      `json:"order_id"`
	TenantID                uint      `gorm:"index" json:"tenant_id"` // 冗余TenantID方便查询
	ScenicAreaID            uint      `gorm:"index" json:"scenic_area_id"`
	FulfillmentProductID    uint      `json:"fulfillment_product_id" gorm:"index"`
	FulfillmentTenantID     uint      `json:"fulfillment_tenant_id" gorm:"index"`
	FulfillmentScenicAreaID uint      `json:"fulfillment_scenic_area_id" gorm:"index"`
	FulfillmentOrderID      uint      `json:"fulfillment_order_id" gorm:"index"`
	ProductRevisionID       uint      `gorm:"index" json:"product_revision_id"`
	RuleSnapshot            string    `gorm:"type:text" json:"-"`
	CodeMode                string    `gorm:"size:20" json:"code_mode"`
	TicketCode              string    `gorm:"size:50;uniqueIndex;not null" json:"ticket_code"` // 核销码
	Status                  string    `gorm:"size:20;default:'unused'" json:"status"`          // unused, used, refunded, expired

	// Visitor Info
	VisitorName  string `gorm:"size:50" json:"visitor_name"`
	VisitorPhone string `gorm:"size:20" json:"visitor_phone"`
	VisitorID    string `gorm:"size:50" json:"visitor_id"`

	// Usage Tracking
	CheckInCount int `gorm:"default:0" json:"check_in_count"` // 总核销次数
}

// CheckInRecord 核销记录
type CheckInRecord struct {
	Base
	TenantID     uint       `gorm:"index" json:"tenant_id"`           // 租户隔离
	ScenicAreaID uint       `gorm:"index" json:"scenic_area_id"`      // 核销履约景区
	TicketCode   string     `gorm:"size:50;index" json:"ticket_code"` // 冗余存储，便于查询
	TicketID     uint       `json:"ticket_id"`
	CheckPointID uint       `json:"check_point_id"`
	CheckPoint   CheckPoint `json:"check_point"`
	DeviceID     uint       `json:"device_id"`
	CheckInTime  time.Time  `json:"check_in_time"`
	Result       string     `gorm:"size:20" json:"result"` // success, fail
	Message      string     `gorm:"size:255" json:"message"`
}

// ProductInventory tracks daily capacity independently for each visit date.
type ProductInventory struct {
	Base
	TenantID     uint      `gorm:"uniqueIndex:idx_product_stock_slot,priority:1;not null" json:"tenant_id"`
	ProductID    uint      `gorm:"uniqueIndex:idx_product_stock_slot,priority:2;not null" json:"product_id"`
	ScenicAreaID uint      `gorm:"index" json:"scenic_area_id"`
	StockDate    time.Time `gorm:"type:date;uniqueIndex:idx_product_stock_slot,priority:3;not null" json:"stock_date"`
	StockSlot    string    `gorm:"size:50;uniqueIndex:idx_product_stock_slot,priority:4" json:"stock_slot,omitempty"`
	Capacity     int       `gorm:"not null" json:"capacity"`
	Sold         int       `gorm:"not null;default:0" json:"sold"`
}

// OTANonce prevents replay of signed OTA requests within the accepted time window.
type OTANonce struct {
	ID        uint      `gorm:"primarykey"`
	TenantID  uint      `gorm:"uniqueIndex:idx_ota_nonce,priority:1;not null"`
	Nonce     string    `gorm:"size:100;uniqueIndex:idx_ota_nonce,priority:2;not null"`
	ExpiresAt time.Time `gorm:"index;not null"`
}
