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
	Name       string `gorm:"size:100;not null" json:"name"`
	SystemCode string `gorm:"size:50;uniqueIndex;not null" json:"system_code"` // 唯一系统编号
	Contact    string `gorm:"size:50" json:"contact"`
	Phone      string `gorm:"size:20" json:"phone"`
	Address    string `gorm:"size:255" json:"address"`
	Users      []User `json:"users,omitempty"`
}

// User 用户
type User struct {
	Base
	Username string `gorm:"size:50;uniqueIndex;not null" json:"username"`
	Password string `gorm:"size:100;not null" json:"-"`
	Role     string `gorm:"size:20;default:'staff'" json:"role"` // admin, staff
	TenantID uint   `json:"tenant_id"`
	Tenant   Tenant `json:"tenant,omitempty"`
}

// CheckPoint 检票点
type CheckPoint struct {
	Base
	Name     string   `gorm:"size:100;not null" json:"name"`
	Location string   `gorm:"size:100" json:"location"`
	TenantID uint     `json:"tenant_id"`
	Devices  []Device `json:"devices,omitempty"`
}

// Device 终端设备
type Device struct {
	Base
	Name         string      `gorm:"size:100;not null" json:"name"`
	SerialNumber string      `gorm:"size:100;uniqueIndex;not null" json:"serial_number"` // 设备序列号
	Type         string      `gorm:"size:20;not null" json:"type"`                       // gate, handheld, pos
	Status       string      `gorm:"size:20;default:'offline'" json:"status"`            // online, offline, fault
	IPAddress    string      `gorm:"size:50" json:"ip_address"`
	MACAddress   string      `gorm:"size:50" json:"mac_address"`
	TenantID     uint        `json:"tenant_id"`
	CheckPointID *uint       `json:"check_point_id"` // 绑定检票点 (可选)
	CheckPoint   *CheckPoint `json:"check_point,omitempty"`
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
	Name     string     `gorm:"size:100;not null" json:"name"`
	Price    float64    `gorm:"type:decimal(10,2)" json:"price"`
	TenantID uint       `json:"tenant_id"`
	RuleID   uint       `json:"rule_id"`
	Rule     TicketRule `gorm:"foreignKey:RuleID" json:"rule,omitempty"`
	Type     string     `gorm:"size:20;default:'online'" json:"type"`   // online, offline
	Status   string     `gorm:"size:20;default:'online'" json:"status"` // online, offline (下架)

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
	OrderNo      string      `gorm:"size:50;uniqueIndex;not null" json:"order_no"`
	TenantID     uint        `json:"tenant_id"`
	TotalAmount  float64     `gorm:"type:decimal(10,2)" json:"total_amount"`
	Status       string      `gorm:"size:20;default:'unpaid'" json:"status"` // unpaid, paid, cancelled, refunded, partial_refunded, completed
	ContactName  string      `gorm:"size:50" json:"contact_name"`
	ContactPhone string      `gorm:"size:20" json:"contact_phone"`
	Channel      string      `gorm:"size:50;default:'miniapp'" json:"channel"` // miniapp, ota, window
	Items        []OrderItem `gorm:"foreignKey:OrderID" json:"items,omitempty"`
}

// OrderItem 订单明细 (按产品聚合)
type OrderItem struct {
	Base
	OrderID       uint       `json:"order_id"`
	ProductID     uint       `json:"product_id"`
	ProductName   string     `gorm:"size:100" json:"product_name"`
	Price         float64    `gorm:"type:decimal(10,2)" json:"price"`
	Quantity      int        `json:"quantity"`
	ValidityType  string     `gorm:"size:20" json:"validity_type"`
	ValidityStart *time.Time `json:"validity_start"`
	ValidityEnd   *time.Time `json:"validity_end"`
	Tickets       []Ticket   `gorm:"foreignKey:OrderItemID" json:"tickets,omitempty"`
}

// Ticket 具体票据 (对应一个游客或一个二维码)
type Ticket struct {
	Base
	OrderItemID uint      `json:"order_item_id"`
	OrderItem   OrderItem `gorm:"foreignKey:OrderItemID" json:"order_item,omitempty"` // Added relation
	OrderID     uint      `json:"order_id"`
	TicketCode  string    `gorm:"size:50;uniqueIndex;not null" json:"ticket_code"` // 核销码
	Status      string    `gorm:"size:20;default:'unused'" json:"status"`          // unused, used, refunded, expired

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
	TicketID     uint       `json:"ticket_id"`
	CheckPointID uint       `json:"check_point_id"`
	CheckPoint   CheckPoint `json:"check_point"`
	DeviceID     uint       `json:"device_id"`
	CheckInTime  time.Time  `json:"check_in_time"`
	Result       string     `gorm:"size:20" json:"result"` // success, fail
	Message      string     `gorm:"size:255" json:"message"`
}
