package model

import "time"

type SettlementStatement struct {
	Base
	SupplierTenantID       uint      `gorm:"index;not null"`
	DistributorTenantID    uint      `gorm:"index;not null"`
	StatementNo            string    `gorm:"size:80;uniqueIndex;not null"`
	IdempotencyKey         string    `gorm:"size:160;index"`
	PeriodStart            time.Time `gorm:"index;not null"`
	PeriodEnd              time.Time `gorm:"index;not null"`
	GrossCents             int64     `gorm:"not null"`
	RefundCents            int64     `gorm:"not null"`
	CommissionCents        int64     `gorm:"not null"`
	NetCents               int64     `gorm:"not null"`
	Status                 string    `gorm:"size:30;not null;index"`
	DueAt                  *time.Time
	PaidAt                 *time.Time
	ConfirmedAt            *time.Time
	SupplierConfirmedAt    *time.Time
	DistributorConfirmedAt *time.Time
	PaymentProof           string `gorm:"size:255"`
	DisputeReason          string `gorm:"size:255"`
}

type SettlementLine struct {
	Base
	StatementID        uint   `gorm:"index;not null"`
	FulfillmentOrderID uint   `gorm:"uniqueIndex;not null"`
	GrossCents         int64  `gorm:"not null"`
	RefundCents        int64  `gorm:"not null"`
	CommissionCents    int64  `gorm:"not null"`
	NetCents           int64  `gorm:"not null"`
	Status             string `gorm:"size:20;not null"`
}
