package model

import "time"

type TravelContract struct {
	Base
	TravelTenantID   uint       `gorm:"index;not null" json:"travel_tenant_id"`
	SupplierTenantID uint       `gorm:"index;not null" json:"supplier_tenant_id"`
	ContractNo       string     `gorm:"size:80;not null;index" json:"contract_no"`
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
	SalesOrderNo        string    `gorm:"-" json:"sales_order_no,omitempty"`
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
	GroupID          uint      `gorm:"index;not null" json:"group_id"`
	SupplierTenantID uint      `gorm:"index;not null;default:0" json:"supplier_tenant_id"`
	ScenicAreaID     uint      `gorm:"index;not null;default:0" json:"scenic_area_id"`
	BatchNo          string    `gorm:"size:80;uniqueIndex;not null" json:"batch_no"`
	IdempotencyKey   string    `gorm:"size:120;not null;index" json:"idempotency_key"`
	MemberIDsJSON    string    `gorm:"type:text;not null" json:"member_ids_json"`
	DeviceID         uint      `gorm:"index" json:"device_id"`
	EnteredCount     int       `gorm:"not null;default:0" json:"entered_count"`
	OperatorID       uint      `gorm:"index" json:"operator_id"`
	EnteredAt        time.Time `gorm:"not null" json:"entered_at"`
}

// TourGroupConfirmation is an append-only operational confirmation snapshot.
// Later revisions do not overwrite what either party previously confirmed.
type TourGroupConfirmation struct {
	Base
	GroupID                uint       `gorm:"uniqueIndex:idx_team_confirmation_sequence,priority:1;not null" json:"group_id"`
	Sequence               int        `gorm:"uniqueIndex:idx_team_confirmation_sequence,priority:2;not null" json:"sequence"`
	TravelTenantID         uint       `gorm:"index;not null" json:"travel_tenant_id"`
	SupplierTenantID       uint       `gorm:"index;not null" json:"supplier_tenant_id"`
	ScenicAreaID           uint       `gorm:"index;not null" json:"scenic_area_id"`
	ConfirmedCount         int        `gorm:"not null" json:"confirmed_count"`
	GuideID                uint       `gorm:"index" json:"guide_id"`
	GuideName              string     `gorm:"size:80" json:"guide_name"`
	GuidePhone             string     `gorm:"size:30" json:"guide_phone"`
	VehicleID              uint       `gorm:"index" json:"vehicle_id"`
	PlateNumber            string     `gorm:"size:30" json:"plate_number"`
	Notes                  string     `gorm:"size:500" json:"notes"`
	SubmittedBy            uint       `gorm:"index" json:"submitted_by"`
	SubmittedAt            time.Time  `gorm:"not null" json:"submitted_at"`
	SupplierAcknowledgedBy uint       `gorm:"index" json:"supplier_acknowledged_by"`
	SupplierAcknowledgedAt *time.Time `json:"supplier_acknowledged_at,omitempty"`
}

// TourGroupMemberChange records post-confirmation additions and removals.
type TourGroupMemberChange struct {
	Base
	GroupID             uint   `gorm:"uniqueIndex:idx_team_member_change_sequence,priority:1;not null" json:"group_id"`
	Sequence            int    `gorm:"uniqueIndex:idx_team_member_change_sequence,priority:2;not null" json:"sequence"`
	TravelTenantID      uint   `gorm:"index;not null" json:"travel_tenant_id"`
	SupplierTenantID    uint   `gorm:"index;not null" json:"supplier_tenant_id"`
	ChangeType          string `gorm:"size:20;not null" json:"change_type"`
	MemberID            uint   `gorm:"index;not null" json:"member_id"`
	MemberName          string `gorm:"size:80;not null" json:"member_name"`
	BeforeExpectedCount int    `gorm:"not null" json:"before_expected_count"`
	AfterExpectedCount  int    `gorm:"not null" json:"after_expected_count"`
	Reason              string `gorm:"size:255;not null" json:"reason"`
	ActorUserID         uint   `gorm:"index" json:"actor_user_id"`
}

// TeamSettlementStatement is the travel-agency settlement projection for one
// team. It is separate from supplier/distributor settlement because a team
// contract may use deposits, credit and an agency-specific price list.
type TeamSettlementStatement struct {
	Base
	TravelTenantID     uint                       `gorm:"index;not null" json:"travel_tenant_id"`
	TravelTenantName   string                     `gorm:"-" json:"travel_tenant_name,omitempty"`
	SupplierTenantID   uint                       `gorm:"index;not null" json:"supplier_tenant_id"`
	SupplierTenantName string                     `gorm:"-" json:"supplier_tenant_name,omitempty"`
	GroupID            uint                       `gorm:"uniqueIndex:idx_team_settlement_group_sequence,priority:1;not null" json:"group_id"`
	GroupNo            string                     `gorm:"-" json:"group_no,omitempty"`
	GroupName          string                     `gorm:"-" json:"group_name,omitempty"`
	Sequence           int                        `gorm:"uniqueIndex:idx_team_settlement_group_sequence,priority:2;not null;default:1" json:"sequence"`
	Kind               string                     `gorm:"size:30;not null;default:'original'" json:"kind"` // original, refund_correction
	StatementNo        string                     `gorm:"size:80;uniqueIndex;not null" json:"statement_no"`
	IdempotencyKey     string                     `gorm:"size:120;uniqueIndex;not null" json:"idempotency_key"`
	GrossCents         int64                      `gorm:"not null" json:"gross_cents"`
	RefundCents        int64                      `gorm:"not null" json:"refund_cents"`
	DepositCents       int64                      `gorm:"not null" json:"deposit_cents"`
	NetCents           int64                      `gorm:"not null" json:"net_cents"`
	AdjustmentCents    int64                      `gorm:"not null;default:0" json:"adjustment_cents"`
	Status             string                     `gorm:"size:30;not null;index" json:"status"` // draft, supplier_confirmed, confirmed, payment_submitted, disputed, paid
	DisputeReason      string                     `gorm:"size:255" json:"dispute_reason,omitempty"`
	PaymentProof       string                     `gorm:"size:255" json:"payment_proof,omitempty"`
	DueAt              *time.Time                 `gorm:"index" json:"due_at,omitempty"`
	ConfirmedAt        *time.Time                 `json:"confirmed_at,omitempty"`
	PaidAt             *time.Time                 `json:"paid_at,omitempty"`
	Adjustments        []TeamSettlementAdjustment `gorm:"foreignKey:StatementID" json:"adjustments,omitempty"`
}

// TeamSettlementAdjustment preserves each negotiated correction while keeping
// the original order and refund snapshot immutable.
type TeamSettlementAdjustment struct {
	Base
	StatementID             uint   `gorm:"uniqueIndex:idx_team_settlement_adjustment_sequence,priority:1;not null" json:"statement_id"`
	Sequence                int    `gorm:"uniqueIndex:idx_team_settlement_adjustment_sequence,priority:2;not null" json:"sequence"`
	ActorTenantID           uint   `gorm:"index;not null" json:"actor_tenant_id"`
	ActorUserID             uint   `gorm:"index" json:"actor_user_id"`
	AmountCents             int64  `gorm:"not null" json:"amount_cents"`
	PreviousAdjustmentCents int64  `gorm:"not null" json:"previous_adjustment_cents"`
	NewAdjustmentCents      int64  `gorm:"not null" json:"new_adjustment_cents"`
	Reason                  string `gorm:"size:255;not null" json:"reason"`
}
