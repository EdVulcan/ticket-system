package model

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

type SchemaMigration struct {
	Version   int       `gorm:"primaryKey"`
	Name      string    `gorm:"size:100;not null"`
	AppliedAt time.Time `gorm:"not null"`
}

type migration struct {
	version int
	name    string
	apply   func(*gorm.DB) error
}

func runMigrations(db *gorm.DB) error {
	if err := db.AutoMigrate(&SchemaMigration{}); err != nil {
		return err
	}
	migrations := []migration{
		{version: 1, name: "initial embedded schema", apply: migrateInitialSchema},
		{version: 2, name: "distribution uniqueness", apply: migrateDistributionUniqueness},
	}
	for _, item := range migrations {
		var count int64
		if err := db.Model(&SchemaMigration{}).Where("version = ?", item.version).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			continue
		}
		if err := db.Transaction(func(tx *gorm.DB) error {
			if err := item.apply(tx); err != nil {
				return err
			}
			return tx.Create(&SchemaMigration{Version: item.version, Name: item.name, AppliedAt: time.Now()}).Error
		}); err != nil {
			return fmt.Errorf("migration %d (%s): %w", item.version, item.name, err)
		}
	}
	return nil
}

func migrateDistributionUniqueness(db *gorm.DB) error {
	if err := db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_distribution_pair ON distributor_relationships(agent_tenant_id, supplier_tenant_id)").Error; err != nil {
		return err
	}
	return db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_capital_account_pair ON capital_accounts(owner_tenant_id, manager_tenant_id)").Error
}

func migrateInitialSchema(db *gorm.DB) error {
	return db.AutoMigrate(
		&Tenant{}, &User{}, &Staff{}, &CheckPoint{}, &Device{},
		&TicketRule{}, &RuleGroup{}, &RuleItem{}, &Product{}, &ProductInventory{},
		&Order{}, &OrderItem{}, &Ticket{}, &CheckInRecord{},
		&DistributorRelationship{}, &CapitalAccount{}, &TransactionRecord{},
		&Policy{}, &PaymentConfig{}, &Payment{}, &OTANonce{},
	)
}
