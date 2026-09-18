package service

import (
	"ticket-backend/internal/model"

	"gorm.io/gorm"
)

// recordCommerceAuditTx keeps isolated commercial-domain fixtures that only
// migrate the commerce tables usable, while production databases still get the
// normal append-only audit row. The platform migration always creates
// audit_logs before commerce writes are served.
func recordCommerceAuditTx(tx *gorm.DB, actorUserID, tenantID uint, actorRole, action, targetType string, targetID uint, reason, beforeJSON, afterJSON string) error {
	if tx == nil || !tx.Migrator().HasTable(&model.AuditLog{}) {
		return nil
	}
	return recordAuditTx(tx, actorUserID, tenantID, actorRole, "tenant", action, targetType, targetID, reason, beforeJSON, afterJSON)
}
