package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"ticket-backend/internal/authz"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"ticket-backend/internal/xiaohongshu"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const xiaohongshuVoucherVerificationLease = 60 * time.Second

var errXiaohongshuVoucherVerificationUnknown = errors.New("小红书券核销结果未知，请人工确认")

var (
	ErrXiaohongshuVoucherResolutionInvalid       = errors.New("小红书券人工处理参数无效")
	ErrXiaohongshuVoucherResolutionNotResolvable = errors.New("小红书券核销记录当前不可人工处理")
	ErrXiaohongshuVoucherResolutionPermission    = errors.New("无权人工处理小红书券核销记录")
)

const (
	xiaohongshuVoucherResolutionConfirm = "confirm_external"
	xiaohongshuVoucherResolutionRelease = "release_external"
)

// XiaohongshuVoucherVerificationResolutionRequest deliberately contains no
// ticket, order, device, checkpoint, tenant, or amount selected by a client.
// The coordinator row is the sole source of those protected facts.
type XiaohongshuVoucherVerificationResolutionRequest struct {
	TenantID         uint
	SagaID           uint
	ActorUserID      uint
	ActorRole        string
	Decision         string
	Reason           string
	Evidence         string
	ExternalVerifyID string
}

func normalizeXiaohongshuVoucherResolution(request *XiaohongshuVoucherVerificationResolutionRequest) error {
	if request == nil || request.TenantID == 0 || request.SagaID == 0 || request.ActorUserID == 0 {
		return ErrXiaohongshuVoucherResolutionInvalid
	}
	request.Decision = strings.TrimSpace(request.Decision)
	request.Reason = strings.TrimSpace(request.Reason)
	request.Evidence = strings.TrimSpace(request.Evidence)
	request.ExternalVerifyID = strings.TrimSpace(request.ExternalVerifyID)
	if request.Decision != xiaohongshuVoucherResolutionConfirm && request.Decision != xiaohongshuVoucherResolutionRelease {
		return ErrXiaohongshuVoucherResolutionInvalid
	}
	if request.Reason == "" || request.Evidence == "" || len(request.Reason) > 500 || len(request.Evidence) > 2000 {
		return ErrXiaohongshuVoucherResolutionInvalid
	}
	if request.Decision == xiaohongshuVoucherResolutionConfirm && (request.ExternalVerifyID == "" || len(request.ExternalVerifyID) > 100) {
		return ErrXiaohongshuVoucherResolutionInvalid
	}
	if request.Decision == xiaohongshuVoucherResolutionRelease && request.ExternalVerifyID != "" {
		return ErrXiaohongshuVoucherResolutionInvalid
	}
	return nil
}

// ResolveXiaohongshuVoucherVerification closes an external_unknown state only
// after an authorized operator has supplied a reason and auditable evidence.
// It never invokes Xiaohongshu. confirm_external records the externally
// confirmed verify ID and resumes only the local, already-reserved admission;
// release_external clears that reservation while leaving the coordinator in a
// terminal rejected state, so the unknown provider request cannot be retried.
func (s *DeviceService) ResolveXiaohongshuVoucherVerification(request XiaohongshuVoucherVerificationResolutionRequest) (*model.XiaohongshuVoucherVerification, error) {
	if s == nil || s.DB == nil || s.TicketService == nil {
		return nil, errors.New("小红书券人工处理服务不可用")
	}
	if err := normalizeXiaohongshuVoucherResolution(&request); err != nil {
		return nil, err
	}
	if !authz.HasTenantPermission(request.ActorRole, authz.PermissionXiaohongshuVoucherResolve) {
		return nil, ErrXiaohongshuVoucherResolutionPermission
	}

	var resolved model.XiaohongshuVoucherVerification
	shouldFinishLocal := false
	now := s.now()
	err := model.Write(func(tx *gorm.DB) error {
		var saga model.XiaohongshuVoucherVerification
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", request.SagaID, request.TenantID).First(&saga).Error; err != nil {
			return err
		}
		if err := validateXiaohongshuVoucherResolutionOwnershipTx(tx, &saga); err != nil {
			return err
		}

		beforeJSON := xiaohongshuVoucherResolutionAuditJSON(&saga, "", "", "")
		switch request.Decision {
		case xiaohongshuVoucherResolutionConfirm:
			if saga.State == "external_unknown" || saga.State == "manual_review" {
				var link model.XiaohongshuVoucherLink
				if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", saga.VoucherLinkID, saga.TenantID).First(&link).Error; err != nil {
					return err
				}
				if (saga.VerifyID != "" && saga.VerifyID != request.ExternalVerifyID) || (link.VerifyID != "" && link.VerifyID != request.ExternalVerifyID) {
					return ErrXiaohongshuVoucherResolutionNotResolvable
				}
				if err := tx.Model(&link).Update("verify_id", request.ExternalVerifyID).Error; err != nil {
					return err
				}
				updates := map[string]interface{}{
					"state": "external_confirmed", "verify_id": request.ExternalVerifyID,
					"external_confirmed_at": now, "last_error": "",
				}
				if err := tx.Model(&saga).Updates(updates).Error; err != nil {
					return err
				}
				saga.State, saga.VerifyID, saga.ExternalConfirmedAt, saga.LastError = "external_confirmed", request.ExternalVerifyID, &now, ""
				shouldFinishLocal = true
			} else if (saga.State == "external_confirmed" || saga.State == "local_pending" || saga.State == "local_completed") && saga.VerifyID == request.ExternalVerifyID {
				// Retrying the same operator action is safe. It cannot write a
				// second audit row or create another external verification.
				shouldFinishLocal = saga.State != "local_completed"
				resolved = saga
				return nil
			} else {
				return ErrXiaohongshuVoucherResolutionNotResolvable
			}
		case xiaohongshuVoucherResolutionRelease:
			if saga.State == "external_rejected" && saga.VerifyID == "" {
				// An exact retry after a release is idempotent. The ticket was
				// already unlocked and the upstream request remains permanently
				// non-retryable.
				resolved = saga
				return nil
			}
			if saga.State != "external_unknown" {
				return ErrXiaohongshuVoucherResolutionNotResolvable
			}
			if err := tx.Model(&model.Ticket{}).
				Where("id = ? AND tenant_id = ? AND pending_xiaohongshu_verification_id = ?", saga.TicketID, saga.TenantID, saga.ID).
				Update("pending_xiaohongshu_verification_id", 0).Error; err != nil {
				return err
			}
			if err := tx.Model(&saga).Updates(map[string]interface{}{
				"state": "external_rejected", "last_error": "人工确认渠道未核销，已释放本地票权",
			}).Error; err != nil {
				return err
			}
			saga.State, saga.LastError = "external_rejected", "人工确认渠道未核销，已释放本地票权"
		}

		afterJSON := xiaohongshuVoucherResolutionAuditJSON(&saga, request.Decision, request.Evidence, request.ExternalVerifyID)
		if err := recordAuditTx(tx, request.ActorUserID, request.TenantID, request.ActorRole, "tenant", "xiaohongshu.voucher_verification.resolve", "xiaohongshu_voucher_verification", saga.ID, request.Reason, beforeJSON, afterJSON); err != nil {
			return err
		}
		resolved = saga
		return nil
	})
	if err != nil {
		return nil, err
	}

	if shouldFinishLocal && resolved.State != "local_completed" {
		// This resumes only the local transaction. It deliberately ignores a
		// transient local error because the durable external_confirmed state is
		// recoverable by ProcessPendingXiaohongshuVoucherVerifications.
		var ticket model.Ticket
		if err := s.DB.Where("id = ? AND tenant_id = ?", resolved.TicketID, resolved.TenantID).First(&ticket).Error; err == nil {
			_, _ = s.finishXiaohongshuVoucherLocal(DirectVerifyRequest{
				TenantID: resolved.TenantID, DeviceID: resolved.DeviceID, CheckPointID: resolved.CheckPointID,
				RequestID: resolved.RequestID, RequestHash: resolved.RequestHash, TicketCode: ticket.TicketCode,
			}, &resolved, resolved.DeviceVerificationID)
		}
	}
	if err := s.DB.Where("id = ? AND tenant_id = ?", request.SagaID, request.TenantID).First(&resolved).Error; err != nil {
		return nil, err
	}
	return &resolved, nil
}

func validateXiaohongshuVoucherResolutionOwnershipTx(tx *gorm.DB, saga *model.XiaohongshuVoucherVerification) error {
	if saga == nil || saga.TenantID == 0 || saga.TicketID == 0 || saga.VoucherLinkID == 0 || saga.DeviceID == 0 || saga.CheckPointID == 0 {
		return ErrXiaohongshuVoucherResolutionNotResolvable
	}
	var link model.XiaohongshuVoucherLink
	if err := tx.Where("id = ? AND tenant_id = ? AND channel_account_id = ? AND ticket_id = ?", saga.VoucherLinkID, saga.TenantID, saga.ChannelAccountID, saga.TicketID).First(&link).Error; err != nil {
		return ErrXiaohongshuVoucherResolutionNotResolvable
	}
	if saga.VerifyID != "" && link.VerifyID != "" && saga.VerifyID != link.VerifyID {
		return ErrXiaohongshuVoucherResolutionNotResolvable
	}
	var orderLink model.XiaohongshuOrderLink
	if err := tx.Where("id = ? AND tenant_id = ? AND channel_account_id = ?", link.XiaohongshuOrderLinkID, saga.TenantID, saga.ChannelAccountID).First(&orderLink).Error; err != nil {
		return ErrXiaohongshuVoucherResolutionNotResolvable
	}
	var ticket model.Ticket
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND order_id = ?", saga.TicketID, saga.TenantID, orderLink.OrderID).First(&ticket).Error; err != nil {
		return ErrXiaohongshuVoucherResolutionNotResolvable
	}
	if saga.State == "external_unknown" && ticket.PendingXiaohongshuVerificationID != saga.ID {
		return ErrXiaohongshuVoucherResolutionNotResolvable
	}
	var checkpoint model.CheckPoint
	if err := tx.Where("id = ? AND tenant_id = ?", saga.CheckPointID, saga.TenantID).First(&checkpoint).Error; err != nil {
		return ErrXiaohongshuVoucherResolutionNotResolvable
	}
	var device model.Device
	if err := tx.Where("id = ? AND tenant_id = ? AND scenic_area_id = ? AND check_point_id = ?", saga.DeviceID, saga.TenantID, checkpoint.ScenicAreaID, checkpoint.ID).First(&device).Error; err != nil {
		return ErrXiaohongshuVoucherResolutionNotResolvable
	}
	var verification model.DeviceVerification
	if err := tx.Where("id = ? AND tenant_id = ? AND device_id = ? AND scenic_area_id = ? AND request_id = ?", saga.DeviceVerificationID, saga.TenantID, saga.DeviceID, checkpoint.ScenicAreaID, saga.RequestID).First(&verification).Error; err != nil {
		return ErrXiaohongshuVoucherResolutionNotResolvable
	}
	return nil
}

func xiaohongshuVoucherResolutionAuditJSON(saga *model.XiaohongshuVoucherVerification, decision, evidence, externalVerifyID string) string {
	if saga == nil {
		return "{}"
	}
	payload, err := json.Marshal(map[string]string{
		"state": saga.State, "verify_id": saga.VerifyID, "decision": decision,
		"evidence": evidence, "external_verify_id": externalVerifyID,
	})
	if err != nil {
		return "{}"
	}
	return string(payload)
}

// resolveXiaohongshuVoucher accepts either the provider voucher scanned by a
// gate or the local ticket code shown by the existing ticketing workflow. The
// raw provider code is only compared by hash here; it is never written to a
// device or check-in record.
func (s *DeviceService) resolveXiaohongshuVoucher(tenantID uint, scannedCode string) (*model.XiaohongshuVoucherLink, string, bool, error) {
	scannedCode = strings.TrimSpace(scannedCode)
	if scannedCode == "" {
		return nil, "", false, nil
	}
	var links []model.XiaohongshuVoucherLink
	if err := s.DB.Where("tenant_id = ? AND voucher_code_hash = ?", tenantID, hashMiniappValue(scannedCode)).Find(&links).Error; err != nil {
		return nil, "", false, err
	}
	if len(links) == 0 {
		var ticket model.Ticket
		err := s.DB.Where("ticket_code = ? AND (fulfillment_tenant_id = ? OR (fulfillment_tenant_id = 0 AND tenant_id = ?))", scannedCode, tenantID, tenantID).First(&ticket).Error
		if err == nil {
			if err = s.DB.Where("tenant_id = ? AND ticket_id = ?", tenantID, ticket.ID).Find(&links).Error; err != nil {
				return nil, "", false, err
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, "", false, err
		}
	}
	if len(links) == 0 {
		return nil, "", false, nil
	}
	if len(links) != 1 {
		return nil, "", false, errors.New("小红书券码归属不明确")
	}
	link := &links[0]
	var ticket model.Ticket
	if err := s.DB.Where("id = ? AND (fulfillment_tenant_id = ? OR (fulfillment_tenant_id = 0 AND tenant_id = ?))", link.TicketID, tenantID, tenantID).First(&ticket).Error; err != nil {
		return nil, "", false, errors.New("小红书券码未绑定有效票权")
	}
	return link, ticket.TicketCode, true, nil
}

type xiaohongshuVoucherVerificationInput struct {
	Account       model.ChannelAccount
	OrderLink     model.XiaohongshuOrderLink
	VoucherCode   string
	POIID         string
	ExternalOrder string
}

func (s *DeviceService) loadXiaohongshuVoucherVerificationInput(link *model.XiaohongshuVoucherLink) (*xiaohongshuVoucherVerificationInput, error) {
	if link == nil || link.ID == 0 {
		return nil, errors.New("小红书券关联不存在")
	}
	var account model.ChannelAccount
	if err := s.DB.Where("id = ? AND tenant_id = ? AND type = ? AND status IN ?", link.ChannelAccountID, link.TenantID, "xiaohongshu", []string{"active", "sandbox"}).First(&account).Error; err != nil {
		return nil, ErrMiniappUnavailable
	}
	var orderLink model.XiaohongshuOrderLink
	if err := s.DB.Where("id = ? AND tenant_id = ? AND channel_account_id = ?", link.XiaohongshuOrderLinkID, link.TenantID, link.ChannelAccountID).First(&orderLink).Error; err != nil {
		return nil, errors.New("小红书订单关联不存在")
	}
	voucherCode, err := utils.DecryptAES(link.VoucherCodeCiphertext)
	if err != nil || strings.TrimSpace(voucherCode) == "" {
		return nil, errors.New("小红书券码解密失败")
	}
	input := &xiaohongshuVoucherVerificationInput{Account: account, OrderLink: orderLink, VoucherCode: voucherCode, ExternalOrder: orderLink.ExternalOrderID}
	var ticket model.Ticket
	if err := s.DB.Where("id = ? AND tenant_id = ?", link.TicketID, link.TenantID).First(&ticket).Error; err != nil {
		return nil, errors.New("小红书券码未绑定有效票权")
	}
	var item model.OrderItem
	if err := s.DB.Where("id = ? AND order_id = ?", ticket.OrderItemID, ticket.OrderID).First(&item).Error; err == nil {
		var config struct{ POIIDsJSON string }
		if cfgErr := s.DB.Table("xiaohongshu_product_configs AS config").
			Joins("JOIN channel_product_mappings AS mapping ON mapping.id = config.channel_product_mapping_id AND mapping.product_id = ? AND mapping.channel_account_id = config.channel_account_id", item.ProductID).
			Where("config.tenant_id = ? AND config.channel_account_id = ?", link.TenantID, link.ChannelAccountID).
			Select("config.poi_ids_json").First(&config).Error; cfgErr == nil {
			pois := parseXiaohongshuPOIIDs(config.POIIDsJSON)
			if len(pois) > 0 {
				input.POIID = pois[0]
			}
		}
	}
	return input, nil
}

func (s *DeviceService) ensureXiaohongshuVoucherVerification(req DirectVerifyRequest, link *model.XiaohongshuVoucherLink, verificationID uint) (*model.XiaohongshuVoucherVerification, error) {
	var saga model.XiaohongshuVoucherVerification
	err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("voucher_link_id = ?", link.ID).First(&saga).Error; err == nil {
			if saga.TenantID != req.TenantID || saga.TicketID != link.TicketID || saga.ChannelAccountID != link.ChannelAccountID {
				return errors.New("小红书券核销协调归属不一致")
			}
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		saga = model.XiaohongshuVoucherVerification{
			TenantID: req.TenantID, ChannelAccountID: link.ChannelAccountID, VoucherLinkID: link.ID,
			TicketID: link.TicketID, DeviceVerificationID: verificationID, DeviceID: req.DeviceID,
			CheckPointID: req.CheckPointID, RequestID: req.RequestID, RequestHash: req.RequestHash, State: "prepared",
		}
		return tx.Create(&saga).Error
	})
	if err == nil {
		return &saga, nil
	}
	// A concurrent scanner may have won the unique voucher-link insert. Read
	// the winner instead of creating a second external request.
	if loadErr := s.DB.Where("voucher_link_id = ?", link.ID).First(&saga).Error; loadErr == nil {
		return &saga, nil
	}
	return nil, err
}

func (s *DeviceService) claimXiaohongshuVoucherExternal(sagaID uint, now time.Time) (bool, error) {
	claimed := false
	err := model.Write(func(tx *gorm.DB) error {
		var saga model.XiaohongshuVoucherVerification
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", sagaID).First(&saga).Error; err != nil {
			return err
		}
		switch saga.State {
		case "prepared":
			if err := tx.Model(&saga).Updates(map[string]interface{}{
				"state": "external_in_flight", "attempt_count": gorm.Expr("attempt_count + 1"), "external_started_at": now, "last_error": "",
			}).Error; err != nil {
				return err
			}
			claimed = true
		case "external_in_flight":
			if saga.ExternalStartedAt != nil && now.Sub(*saga.ExternalStartedAt) >= xiaohongshuVoucherVerificationLease {
				return tx.Model(&saga).Updates(map[string]interface{}{"state": "external_unknown", "last_error": errXiaohongshuVoucherVerificationUnknown.Error()}).Error
			}
		}
		return nil
	})
	return claimed, err
}

func (s *DeviceService) persistXiaohongshuVoucherExternalSuccess(sagaID uint, verifyID string, now time.Time) error {
	verifyID = strings.TrimSpace(verifyID)
	if verifyID == "" {
		return errors.New("小红书核销响应缺少 verify_id")
	}
	return model.Write(func(tx *gorm.DB) error {
		var saga model.XiaohongshuVoucherVerification
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", sagaID).First(&saga).Error; err != nil {
			return err
		}
		if saga.State != "external_in_flight" && saga.State != "external_confirmed" && saga.State != "local_pending" {
			return fmt.Errorf("小红书核销协调状态不可确认: %s", saga.State)
		}
		if saga.VerifyID != "" && saga.VerifyID != verifyID {
			return errors.New("小红书核销编号不一致")
		}
		var link model.XiaohongshuVoucherLink
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", saga.VoucherLinkID, saga.TenantID).First(&link).Error; err != nil {
			return err
		}
		if link.VerifyID != "" && link.VerifyID != verifyID {
			return errors.New("小红书券关联已有不同核销编号")
		}
		if err := tx.Model(&link).Update("verify_id", verifyID).Error; err != nil {
			return err
		}
		updates := map[string]interface{}{"state": "local_pending", "verify_id": verifyID, "external_confirmed_at": now, "last_error": ""}
		if saga.ExternalConfirmedAt != nil {
			delete(updates, "external_confirmed_at")
		}
		return tx.Model(&saga).Updates(updates).Error
	})
}

func (s *DeviceService) setXiaohongshuVoucherState(sagaID uint, state, message string, releaseReservation bool, now time.Time) error {
	return model.Write(func(tx *gorm.DB) error {
		var saga model.XiaohongshuVoucherVerification
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", sagaID).First(&saga).Error; err != nil {
			return err
		}
		updates := map[string]interface{}{"state": state, "last_error": truncateChannelError(message)}
		if state == "manual_review" {
			updates["manual_review_at"] = now
		}
		if err := tx.Model(&saga).Updates(updates).Error; err != nil {
			return err
		}
		if releaseReservation {
			return tx.Model(&model.Ticket{}).Where("id = ? AND pending_xiaohongshu_verification_id = ?", saga.TicketID, saga.ID).Update("pending_xiaohongshu_verification_id", 0).Error
		}
		return nil
	})
}

func (s *DeviceService) completeXiaohongshuDeviceVerification(verificationID uint, response *VerifyResponse, checkInRecordID uint) error {
	if response == nil {
		return errors.New("核销响应不能为空")
	}
	openStatus := ""
	if response.Result == "allow" {
		openStatus = "pending"
	}
	return model.Write(func(tx *gorm.DB) error {
		return tx.Model(&model.DeviceVerification{}).Where("id = ? AND status = ?", verificationID, "processing").Updates(map[string]interface{}{
			"status": "completed", "response_code": response.Code, "result": response.Result,
			"display_text": response.DisplayText, "voice_file": response.VoiceFile, "voice_code": response.VoiceCode,
			"open_duration": response.OpenDuration, "check_in_record_id": checkInRecordID, "open_status": openStatus,
		}).Error
	})
}

// completedXiaohongshuVoucherResponse reconstructs the durable local result
// instead of relying on the coordinator's original DeviceVerification row.
// A retry may arrive with a different request id after another process has
// completed the local admission, leaving that original row in processing.
func (s *DeviceService) completedXiaohongshuVoucherResponse(saga *model.XiaohongshuVoucherVerification, verificationID uint) (*VerifyResponse, error) {
	if saga == nil {
		return nil, errors.New("小红书核销协调记录不存在")
	}
	var checkIn model.CheckInRecord
	if saga.CheckInRecordID != 0 {
		err := s.DB.Where("id = ? AND tenant_id = ? AND ticket_id = ? AND device_id = ? AND check_point_id = ? AND result = ?", saga.CheckInRecordID, saga.TenantID, saga.TicketID, saga.DeviceID, saga.CheckPointID, "success").First(&checkIn).Error
		if err == nil {
			response := s.allowResponse(checkIn.TicketCode)
			if verificationID != 0 {
				if err := s.completeXiaohongshuDeviceVerification(verificationID, response, checkIn.ID); err != nil {
					return nil, err
				}
			}
			return response, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}

	// A completed saga should always have a successful CheckInRecord. Keep a
	// defensive fallback for historical rows, but never return an empty success.
	var completedVerification model.DeviceVerification
	if err := s.DB.Where("id = ? AND tenant_id = ? AND device_id = ?", saga.DeviceVerificationID, saga.TenantID, saga.DeviceID).First(&completedVerification).Error; err != nil {
		return nil, err
	}
	response := responseFromVerification(&completedVerification)
	if response.Result == "" {
		response = xiaohongshuVoucherUnknownResponse()
	}
	if verificationID != completedVerification.ID {
		if err := s.completeXiaohongshuDeviceVerification(verificationID, response, saga.CheckInRecordID); err != nil {
			return nil, err
		}
	}
	return response, nil
}

func xiaohongshuVoucherUnknownResponse() *VerifyResponse {
	return &VerifyResponse{Code: 409, Result: "deny", DisplayText: "核销确认中，请稍后重试", VoiceFile: "invalid.mp3", VoiceCode: "invalid"}
}

func xiaohongshuVoucherRejectedResponse(message string) *VerifyResponse {
	response := denyResponse(errors.New(strings.TrimSpace(message)))
	if response.DisplayText == "" {
		response.DisplayText = "小红书券不可核销"
	}
	return response
}

// executeXiaohongshuVoucherExternal performs exactly one claimed provider
// request. A non-nil response means the provider step reached a terminal local
// response (rejected or unknown) and the caller must not invoke the provider
// again. A nil response means verify_id was durably stored and local admission
// can continue from local_pending.
func (s *DeviceService) executeXiaohongshuVoucherExternal(ctx context.Context, saga *model.XiaohongshuVoucherVerification, link *model.XiaohongshuVoucherLink) (*VerifyResponse, error) {
	if saga == nil || link == nil {
		return nil, errors.New("小红书核销协调记录不存在")
	}
	input, err := s.loadXiaohongshuVoucherVerificationInput(link)
	if err != nil {
		message := err.Error()
		if stateErr := s.setXiaohongshuVoucherState(saga.ID, "external_unknown", message, false, s.now()); stateErr != nil {
			return nil, errors.Join(err, stateErr)
		}
		response := xiaohongshuVoucherUnknownResponse()
		if completeErr := s.completeXiaohongshuDeviceVerification(saga.DeviceVerificationID, response, 0); completeErr != nil {
			return nil, completeErr
		}
		return response, nil
	}
	secret, err := utils.DecryptAES(input.Account.SecretCiphertext)
	if err != nil || strings.TrimSpace(secret) == "" {
		message := "小红书渠道密钥不可用"
		if stateErr := s.setXiaohongshuVoucherState(saga.ID, "external_unknown", message, false, s.now()); stateErr != nil {
			return nil, stateErr
		}
		response := xiaohongshuVoucherUnknownResponse()
		if completeErr := s.completeXiaohongshuDeviceVerification(saga.DeviceVerificationID, response, 0); completeErr != nil {
			return nil, completeErr
		}
		return response, nil
	}
	newClient := s.NewXiaohongshuClient
	if newClient == nil {
		newClient = xiaohongshu.NewClient
	}
	requestContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	remote, remoteErr := newClient(input.Account.AppID, secret, input.Account.Environment).VerifyVouchers(requestContext, xiaohongshu.VoucherVerifyRequest{
		ExternalOrderID: input.ExternalOrder, POIID: input.POIID, Vouchers: []xiaohongshu.VoucherCode{{Code: input.VoucherCode}},
	})
	if remoteErr != nil {
		state := "external_rejected"
		if !isKnownXiaohongshuVoucherRejection(remoteErr) {
			state = "external_unknown"
		}
		if stateErr := s.setXiaohongshuVoucherState(saga.ID, state, remoteErr.Error(), state == "external_rejected", s.now()); stateErr != nil {
			return nil, errors.Join(remoteErr, stateErr)
		}
		response := xiaohongshuVoucherRejectedResponse(remoteErr.Error())
		if state == "external_unknown" {
			response = xiaohongshuVoucherUnknownResponse()
		}
		if completeErr := s.completeXiaohongshuDeviceVerification(saga.DeviceVerificationID, response, 0); completeErr != nil {
			return nil, completeErr
		}
		return response, nil
	}
	if remote == nil || strings.TrimSpace(remote.VerifyID) == "" {
		message := "小红书核销响应缺少 verify_id"
		if stateErr := s.setXiaohongshuVoucherState(saga.ID, "external_unknown", message, false, s.now()); stateErr != nil {
			return nil, stateErr
		}
		response := xiaohongshuVoucherUnknownResponse()
		if completeErr := s.completeXiaohongshuDeviceVerification(saga.DeviceVerificationID, response, 0); completeErr != nil {
			return nil, completeErr
		}
		return response, nil
	}
	if err := s.persistXiaohongshuVoucherExternalSuccess(saga.ID, remote.VerifyID, s.now()); err != nil {
		return nil, err
	}
	return nil, nil
}

func (s *DeviceService) verifyXiaohongshuVoucher(req DirectVerifyRequest, device model.Device, link *model.XiaohongshuVoucherLink, localTicketCode string) (*VerifyResponse, error) {
	localReq := req
	localReq.TicketCode = localTicketCode
	verification, replay, err := s.beginDeviceVerification(localReq, device.ScenicAreaID)
	if err != nil {
		return nil, err
	}
	if replay != nil {
		return replay, nil
	}
	saga, err := s.ensureXiaohongshuVoucherVerification(localReq, link, verification.ID)
	if err != nil {
		return nil, err
	}
	if saga.DeviceVerificationID != verification.ID && saga.RequestID == req.RequestID && saga.DeviceID == req.DeviceID {
		verification.ID = saga.DeviceVerificationID
	}
	// Only the request that owns a prepared/in-flight saga may advance its
	// external step. A different request id gets a durable replay/processing
	// response and must never take over the first request's lease.
	if saga.RequestID != req.RequestID || saga.DeviceID != req.DeviceID {
		switch saga.State {
		case "local_completed":
			return s.completedXiaohongshuVoucherResponse(saga, verification.ID)
		case "external_rejected", "local_rejected":
			response := xiaohongshuVoucherRejectedResponse(saga.LastError)
			if err := s.completeXiaohongshuDeviceVerification(verification.ID, response, 0); err != nil {
				return nil, err
			}
			return response, nil
		default:
			response := xiaohongshuVoucherUnknownResponse()
			if err := s.completeXiaohongshuDeviceVerification(verification.ID, response, saga.CheckInRecordID); err != nil {
				return nil, err
			}
			return response, nil
		}
	}

	for step := 0; step < 3; step++ {
		if err := s.DB.Where("id = ?", saga.ID).First(saga).Error; err != nil {
			return nil, err
		}
		switch saga.State {
		case "local_completed":
			return s.completedXiaohongshuVoucherResponse(saga, verification.ID)
		case "external_unknown", "manual_review", "external_in_flight":
			response := xiaohongshuVoucherUnknownResponse()
			if saga.State == "manual_review" {
				response = xiaohongshuVoucherRejectedResponse("小红书券已核销但本地履约待人工处理")
			}
			if completeErr := s.completeXiaohongshuDeviceVerification(verification.ID, response, saga.CheckInRecordID); completeErr != nil {
				return nil, completeErr
			}
			return response, nil
		case "external_rejected", "local_rejected":
			response := xiaohongshuVoucherRejectedResponse(saga.LastError)
			if completeErr := s.completeXiaohongshuDeviceVerification(verification.ID, response, 0); completeErr != nil {
				return nil, completeErr
			}
			return response, nil
		case "external_confirmed", "local_pending":
			return s.finishXiaohongshuVoucherLocal(localReq, saga, verification.ID)
		case "prepared":
			if err := s.TicketService.PrepareDeviceRequest(localTicketCode, req.CheckPointID, req.DeviceID, req.TenantID, saga.ID, req.RequestID); err != nil {
				_ = s.setXiaohongshuVoucherState(saga.ID, "local_rejected", err.Error(), true, s.now())
				response := xiaohongshuVoucherRejectedResponse(err.Error())
				if completeErr := s.completeXiaohongshuDeviceVerification(verification.ID, response, 0); completeErr != nil {
					return nil, completeErr
				}
				return response, nil
			}
			claimed, err := s.claimXiaohongshuVoucherExternal(saga.ID, s.now())
			if err != nil {
				return nil, err
			}
			if !claimed {
				continue
			}
			response, err := s.executeXiaohongshuVoucherExternal(context.Background(), saga, link)
			if err != nil {
				return nil, err
			}
			if response != nil {
				return response, nil
			}
			continue
		default:
			return nil, fmt.Errorf("小红书券核销协调状态无效: %s", saga.State)
		}
	}
	return nil, ErrVerificationProcessing
}

func (s *DeviceService) finishXiaohongshuVoucherLocal(req DirectVerifyRequest, saga *model.XiaohongshuVoucherVerification, verificationID uint) (*VerifyResponse, error) {
	err := s.TicketService.VerifyDeviceRequestReserved(req.TicketCode, req.CheckPointID, req.DeviceID, req.TenantID, req.RequestID, saga.ID)
	var checkIn model.CheckInRecord
	_ = s.DB.Where("device_id = ? AND device_request_id = ? AND result = ?", req.DeviceID, req.RequestID, "success").Order("id desc").First(&checkIn).Error
	if err != nil && checkIn.ID == 0 {
		if isDeterministicLocalVerificationError(err) {
			_ = s.setXiaohongshuVoucherState(saga.ID, "manual_review", err.Error(), false, s.now())
			response := xiaohongshuVoucherRejectedResponse("小红书券已确认，但本地核销待人工处理")
			if completeErr := s.completeXiaohongshuDeviceVerification(verificationID, response, 0); completeErr != nil {
				return nil, completeErr
			}
			return response, nil
		}
		return nil, err
	}
	if checkIn.ID == 0 {
		return nil, errors.New("本地核销成功但未找到核销记录")
	}
	now := s.now()
	response := s.allowResponse(req.TicketCode)
	if err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Model(&model.XiaohongshuVoucherVerification{}).Where("id = ? AND state IN ?", saga.ID, []string{"external_confirmed", "local_pending"}).Updates(map[string]interface{}{
			"state": "local_completed", "check_in_record_id": checkIn.ID, "local_completed_at": now, "last_error": "",
		}).Error; err != nil {
			return err
		}
		return tx.Model(&model.DeviceVerification{}).Where("id = ? AND status = ?", verificationID, "processing").Updates(map[string]interface{}{
			"status": "completed", "response_code": response.Code, "result": response.Result, "display_text": response.DisplayText, "voice_file": response.VoiceFile, "voice_code": response.VoiceCode, "open_duration": response.OpenDuration, "check_in_record_id": checkIn.ID, "open_status": "pending",
		}).Error
	}); err != nil {
		return nil, err
	}
	return response, nil
}

func isKnownXiaohongshuVoucherRejection(err error) bool {
	var apiErr *xiaohongshu.APIError
	if errors.As(err, &apiErr) {
		return apiErr.Code != 429 && apiErr.Code < 500
	}
	return false
}

func isDeterministicLocalVerificationError(err error) bool {
	return errors.Is(err, ErrInvalidTicket) || errors.Is(err, ErrOrderNotPaid) || errors.Is(err, ErrTicketUnavailable) ||
		errors.Is(err, ErrTicketNotStarted) || errors.Is(err, ErrTicketExpired) || errors.Is(err, ErrCheckpointNotFound) ||
		errors.Is(err, ErrAccessDenied) || errors.Is(err, ErrPointLimitReached) || errors.Is(err, ErrGroupLimitReached)
}

// ProcessPendingXiaohongshuVoucherVerifications recovers prepared records and
// local work after an external verify_id has been durably recorded. It never
// retries external_unknown or stale external_in_flight calls.
func (s *DeviceService) ProcessPendingXiaohongshuVoucherVerifications(ctx context.Context, now time.Time, limit int) (int, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	var sagas []model.XiaohongshuVoucherVerification
	if err := s.DB.Where("state IN ?", []string{"prepared", "external_confirmed", "local_pending"}).Order("updated_at ASC, id ASC").Limit(limit).Find(&sagas).Error; err != nil {
		return 0, err
	}
	processed := 0
	for i := range sagas {
		select {
		case <-ctx.Done():
			return processed, ctx.Err()
		default:
		}
		var ticket model.Ticket
		if err := s.DB.Where("id = ? AND tenant_id = ?", sagas[i].TicketID, sagas[i].TenantID).First(&ticket).Error; err != nil {
			continue
		}
		request := DirectVerifyRequest{TenantID: sagas[i].TenantID, DeviceID: sagas[i].DeviceID, CheckPointID: sagas[i].CheckPointID, RequestID: sagas[i].RequestID, RequestHash: sagas[i].RequestHash, TicketCode: ticket.TicketCode}
		if sagas[i].State == "prepared" {
			var link model.XiaohongshuVoucherLink
			if err := s.DB.Where("id = ? AND tenant_id = ? AND ticket_id = ?", sagas[i].VoucherLinkID, sagas[i].TenantID, sagas[i].TicketID).First(&link).Error; err != nil {
				continue
			}
			if err := s.TicketService.PrepareDeviceRequest(ticket.TicketCode, sagas[i].CheckPointID, sagas[i].DeviceID, sagas[i].TenantID, sagas[i].ID, sagas[i].RequestID); err != nil {
				if isDeterministicLocalVerificationError(err) {
					_ = s.setXiaohongshuVoucherState(sagas[i].ID, "local_rejected", err.Error(), true, s.now())
					_ = s.completeXiaohongshuDeviceVerification(sagas[i].DeviceVerificationID, xiaohongshuVoucherRejectedResponse(err.Error()), 0)
				}
				continue
			}
			claimed, err := s.claimXiaohongshuVoucherExternal(sagas[i].ID, now)
			if err != nil || !claimed {
				continue
			}
			response, err := s.executeXiaohongshuVoucherExternal(ctx, &sagas[i], &link)
			if err != nil {
				continue
			}
			if response != nil {
				processed++
				continue
			}
			if err := s.DB.Where("id = ?", sagas[i].ID).First(&sagas[i]).Error; err != nil {
				continue
			}
		}
		if _, err := s.finishXiaohongshuVoucherLocal(request, &sagas[i], sagas[i].DeviceVerificationID); err == nil {
			processed++
		}
	}
	return processed, nil
}
