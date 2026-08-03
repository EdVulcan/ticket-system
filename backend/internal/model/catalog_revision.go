package model

import "time"

type ProductRevision struct {
	Base
	ProductID       uint      `gorm:"uniqueIndex:idx_product_revision,priority:1;not null"`
	TenantID        uint      `gorm:"index;not null"`
	ScenicAreaID    uint      `gorm:"index;not null"`
	Version         int       `gorm:"uniqueIndex:idx_product_revision,priority:2;not null"`
	Status          string    `gorm:"size:20;not null"`
	PriceCents      int64     `gorm:"not null"`
	SettlementCents int64     `gorm:"not null"`
	GateVoiceCode   string    `gorm:"size:100"`
	SnapshotJSON    string    `gorm:"type:text;not null"`
	EffectiveFrom   time.Time `gorm:"not null"`
	EffectiveTo     *time.Time
}
