package service

import (
	"errors"
	"strings"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"time"

	"gorm.io/gorm"
)

func normalizeMiniappContactIDCard(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}

// validateMiniappContactIDCard checks the resident ID format and checksum.
// It does not claim an authoritative verification of the name/ID pairing.
func validateMiniappContactIDCard(value string, now time.Time) error {
	invalid := errors.New("请填写有效的联系人身份证号码")
	if len(value) != 18 || value[0] == '0' {
		return invalid
	}
	weights := [...]int{7, 9, 10, 5, 8, 4, 2, 1, 6, 3, 7, 9, 10, 5, 8, 4, 2}
	checks := "10X98765432"
	sum := 0
	for i := 0; i < 17; i++ {
		if value[i] < '0' || value[i] > '9' {
			return invalid
		}
		sum += int(value[i]-'0') * weights[i]
	}
	if value[17] != checks[sum%11] || value[14:17] == "000" {
		return invalid
	}
	birthday, err := time.ParseInLocation("20060102", value[6:14], now.Location())
	if err != nil || birthday.Year() < 1900 || birthday.After(startOfDay(now)) {
		return invalid
	}
	return nil
}

// snapshotMiniappOrderContactTx runs after the order service has resolved the
// supplier and revision. A distributor's mutable sales projection cannot turn
// off the supplier's contact policy. Contact identity never enters visitors,
// tickets, member data or the platform's order request.
func snapshotMiniappOrderContactTx(tx *gorm.DB, order *model.Order, input *MiniappOrderCreateInput, now time.Time) error {
	order.ContactRealNameRequired = false
	order.ContactIDCardCiphertext = ""
	for _, item := range order.Items {
		var revision model.ProductRevision
		if err := tx.Where("id = ? AND product_id = ? AND tenant_id = ?", item.ProductRevisionID, item.FulfillmentProductID, item.FulfillmentTenantID).First(&revision).Error; err != nil {
			return errors.New("订单联系人规则暂不可用，请稍后重试")
		}
		order.ContactRealNameRequired = order.ContactRealNameRequired || revision.ContactRealNameRequired
	}
	if !order.ContactRealNameRequired {
		// Do not collect an unnecessary identity, even from an outdated client.
		input.ContactIDCard = ""
		return nil
	}
	input.ContactIDCard = normalizeMiniappContactIDCard(input.ContactIDCard)
	if err := validateMiniappOrderContact(input.GuestName, input.ContactPhone); err != nil {
		return newMiniappOrderCreateError(miniappOrderNotCreatedCode, "", err.Error())
	}
	if err := validateMiniappContactIDCard(input.ContactIDCard, now); err != nil {
		return newMiniappOrderCreateError(miniappOrderNotCreatedCode, "", err.Error())
	}
	ciphertext, err := utils.EncryptAES(input.ContactIDCard)
	if err != nil {
		return errors.New("订单联系人资料暂无法保存，请稍后重试")
	}
	order.ContactIDCardCiphertext = ciphertext
	return nil
}

func maskedMiniappContactIDCard(ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}
	value, err := utils.DecryptAES(ciphertext)
	if err != nil || len(value) != 18 {
		return "", errors.New("订单联系人资料暂不可用")
	}
	return value[:6] + "********" + value[14:], nil
}
