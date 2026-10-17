package service

import (
	"errors"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"time"
)

var (
	errUpstreamContactUnavailable     = errors.New("订单联系人实名资料不可用，请人工核对原订单")
	errUpstreamContactSnapshotMissing = errors.New("原上游请求缺少联系人实名，禁止修改原请求，请人工核对；普通重试不能补齐")
)

// Contact identity is an immutable sales-order fact, never a current product,
// visitor or ticket field. Only the bound supplier request receives it.
func upstreamOrderContactCertificate(order *model.Order) (string, error) {
	if !order.ContactRealNameRequired {
		return "", nil
	}
	invalid := errUpstreamContactUnavailable
	if order.Channel != "xiaohongshu" || order.ContactIDCardCiphertext == "" {
		return "", invalid
	}
	value, err := utils.DecryptAES(order.ContactIDCardCiphertext)
	if err != nil || value != normalizeMiniappContactIDCard(value) {
		return "", invalid
	}
	if validateMiniappContactIDCard(value, time.Now()) != nil || validateMiniappOrderContact(order.ContactName, order.ContactPhone) != nil {
		return "", invalid
	}
	return value, nil
}

// A provider can echo the request in any error field. Keep descriptions out of
// persisted tasks and API results while retaining typed retry classification.
type upstreamContactPrivateError struct{ cause error }

func (e *upstreamContactPrivateError) Error() string {
	return "智游宝实名订单处理失败，请核对供应商原订单和实名配置后重试"
}
func (e *upstreamContactPrivateError) Unwrap() error { return e.cause }

func protectUpstreamContactError(order *model.Order, err error) error {
	if err == nil || !order.ContactRealNameRequired {
		return err
	}
	// These local sentinel messages contain no provider text or personal data.
	if err == errUpstreamContactUnavailable || err == errUpstreamContactSnapshotMissing {
		return err
	}
	return &upstreamContactPrivateError{cause: err}
}
