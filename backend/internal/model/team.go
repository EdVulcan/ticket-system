package model

import "time"

type TravelContract struct {
	Base
	TravelTenantID   uint       `gorm:"index;not null" json:"travel_tenant_id"`
	SupplierTenantID uint       `gorm:"index;not null" json:"supplier_tenant_id"`
	ContractNo       string     `gorm:"size:80;uniqueIndex;not null" json:"contract_no"`
	Status           string     `gorm:"size:20;not null;default:'active'" json:"status"`
	SettlementDays   int        `gorm:"not null;default:0" json:"settlement_days"`
	CreditLimitCents int64      `gorm:"not null;default:0" json:"credit_limit_cents"`
	PriceRulesJSON   string     `gorm:"type:text" json:"price_rules_json"`
	StartsAt         *time.Time `json:"starts_at,omitempty"`
	EndsAt           *time.Time `json:"ends_at,omitempty"`
}

type TravelAgent struct {
	Base
	TenantID  uint   `gorm:"uniqueIndex:idx_travel_agent_no,priority:1;not null" json:"tenant_id"`
	Name      string `gorm:"size:80;not null" json:"name"`
	Phone     string `gorm:"size:30" json:"phone"`
	JobNumber string `gorm:"size:50;uniqueIndex:idx_travel_agent_no,priority:2;not null" json:"job_number"`
	Status    string `gorm:"size:20;not null;default:'active'" json:"status"`
}

type TourGuide struct {
	Base
	TenantID  uint   `gorm:"index;not null" json:"tenant_id"`
	Name      string `gorm:"size:80;not null" json:"name"`
	Phone     string `gorm:"size:30" json:"phone"`
	LicenseNo string `gorm:"size:80" json:"license_no"`
	Status    string `gorm:"size:20;not null;default:'active'" json:"status"`
}

type TravelVehicle struct {
	Base
	TenantID    uint   `gorm:"index;not null" json:"tenant_id"`
	PlateNumber string `gorm:"size:30;not null" json:"plate_number"`
	DriverName  string `gorm:"size:80" json:"driver_name"`
	DriverPhone string `gorm:"size:30" json:"driver_phone"`
	Capacity    int    `gorm:"not null;default:0" json:"capacity"`
	Status      string `gorm:"size:20;not null;default:'active'" json:"status"`
}

// TourGroup is a team booking/fulfillment unit. It can reference a normal
// sales order but has its own lifecycle, roster and batch admission records.
type TourGroup struct {
	Base
	TenantID            uint      `gorm:"index;not null" json:"tenant_id"`
	SupplierTenantID    uint      `gorm:"index;not null" json:"supplier_tenant_id"`
	ScenicAreaID        uint      `gorm:"index;not null" json:"scenic_area_id"`
	ContractID          uint      `gorm:"index" json:"contract_id"`
	SalesOrderID        uint      `gorm:"index" json:"sales_order_id"`
	GroupNo             string    `gorm:"size:80;uniqueIndex;not null" json:"group_no"`
	Name                string    `gorm:"size:120;not null" json:"name"`
	VisitDate           time.Time `gorm:"type:date;not null" json:"visit_date"`
	ExpectedCount       int       `gorm:"not null;default:0" json:"expected_count"`
	Status              string    `gorm:"size:20;not null;default:'draft';index" json:"status"` // draft, confirmed, partial_entry, entered, cancelled
	GuideID             uint      `gorm:"index" json:"guide_id"`
	VehicleID           uint      `gorm:"index" json:"vehicle_id"`
	AgentID             uint      `gorm:"index" json:"agent_id"`
	ContractAmountCents int64     `gorm:"not null;default:0" json:"contract_amount_cents"`
	DepositCents        int64     `gorm:"not null;default:0" json:"deposit_cents"`
	CreditUsedCents     int64     `gorm:"not null;default:0" json:"credit_used_cents"`
	SettlementStatus    string    `gorm:"size:20;not null;default:'open'" json:"settlement_status"` // open, statement, settled
}

type TourGroupMember struct {
	Base
	GroupID      uint       `gorm:"index;not null" json:"group_id"`
	Name         string     `gorm:"size:80;not null" json:"name"`
	Phone        string     `gorm:"size:30" json:"phone"`
	IdentityNo   string     `gorm:"size:80" json:"identity_no"`
	TicketCode   string     `gorm:"size:80;index" json:"ticket_code"`
	Status       string     `gorm:"size:20;not null;default:'planned'" json:"status"` // planned, ticketed, entered, cancelled
	EnteredAt    *time.Time `json:"entered_at,omitempty"`
	EntryBatchNo string     `gorm:"size:80;index" json:"entry_batch_no"`
}

type TourEntryBatch struct {
	Base
	GroupID      uint      `gorm:"index;not null" json:"group_id"`
	BatchNo      string    `gorm:"size:80;uniqueIndex;not null" json:"batch_no"`
	DeviceID     uint      `gorm:"index" json:"device_id"`
	EnteredCount int       `gorm:"not null;default:0" json:"entered_count"`
	OperatorID   uint      `gorm:"index" json:"operator_id"`
	EnteredAt    time.Time `gorm:"not null" json:"entered_at"`
}

// TeamSettlementStatement is the travel-agency settlement projection for one
// team. It is separate from supplier/distributor settlement because a team
// contract may use deposits, credit and an agency-specific price list.
type TeamSettlementStatement struct {
	Base
	TravelTenantID   uint       `gorm:"index;not null" json:"travel_tenant_id"`
	SupplierTenantID uint       `gorm:"index;not null" json:"supplier_tenant_id"`
	GroupID          uint       `gorm:"uniqueIndex;not null" json:"group_id"`
	StatementNo      string     `gorm:"size:80;uniqueIndex;not null" json:"statement_no"`
	IdempotencyKey   string     `gorm:"size:120;uniqueIndex;not null" json:"idempotency_key"`
	GrossCents       int64      `gorm:"not null" json:"gross_cents"`
	RefundCents      int64      `gorm:"not null" json:"refund_cents"`
	DepositCents     int64      `gorm:"not null" json:"deposit_cents"`
	NetCents         int64      `gorm:"not null" json:"net_cents"`
	Status           string     `gorm:"size:30;not null;index" json:"status"` // draft, supplier_confirmed, confirmed, disputed, paid
	DisputeReason    string     `gorm:"size:255" json:"dispute_reason,omitempty"`
	PaymentProof     string     `gorm:"size:255" json:"payment_proof,omitempty"`
	ConfirmedAt      *time.Time `json:"confirmed_at,omitempty"`
	PaidAt           *time.Time `json:"paid_at,omitempty"`
}
