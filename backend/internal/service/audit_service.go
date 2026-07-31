package service

import (
	"ticket-backend/internal/model"

	"gorm.io/gorm"
)

// RecordAudit appends a sensitive-operation record. Callers should invoke it
// after the business transaction succeeds and surface failures to operators;
// audit rows are never updated or deleted by the application.
func RecordAudit(actorUserID, tenantID uint, actorRole, scope, action, targetType string, targetID uint, reason, beforeJSON, afterJSON string) error {
	return model.Write(func(tx *gorm.DB) error {
		return recordAuditTx(tx, actorUserID, tenantID, actorRole, scope, action, targetType, targetID, reason, beforeJSON, afterJSON)
	})
}

func recordAuditTx(tx *gorm.DB, actorUserID, tenantID uint, actorRole, scope, action, targetType string, targetID uint, reason, beforeJSON, afterJSON string) error {
	return tx.Create(&model.AuditLog{
		ActorUserID: actorUserID, ActorRole: actorRole, Scope: scope, TenantID: tenantID,
		Action: action, TargetType: targetType, TargetID: targetID, Reason: reason,
		BeforeJSON: beforeJSON, AfterJSON: afterJSON,
	}).Error
}
