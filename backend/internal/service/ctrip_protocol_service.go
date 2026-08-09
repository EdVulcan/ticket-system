package service

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"sort"
	"strings"
	"sync"
	"ticket-backend/internal/config"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"ticket-backend/pkg/logger"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type CtripProtocolService struct {
	OrderService OrderService
}

type ctripHeader struct {
	AccountID   string `json:"accountId"`
	ServiceName string `json:"serviceName"`
	RequestTime string `json:"requestTime"`
	Version     string `json:"version"`
	Sign        string `json:"sign"`
}

type ctripEnvelope struct {
	Header ctripHeader `json:"header"`
	Body   string      `json:"body"`
}

type ctripResponseHeader struct {
	ResultCode    string `json:"resultCode"`
	ResultMessage string `json:"resultMessage"`
}

type ctripResponse struct {
	Header ctripResponseHeader `json:"header"`
	Body   string              `json:"body,omitempty"`
}

type ctripBusinessError struct {
	Code    string
	Message string
}

func (e *ctripBusinessError) Error() string { return e.Message }

type ctripSequence struct {
	SequenceID string `json:"sequenceId"`
}

type ctripContact struct {
	Name   string `json:"name"`
	Mobile string `json:"mobile"`
}

type ctripPassenger struct {
	PassengerID string `json:"passengerId"`
	Name        string `json:"name"`
	Mobile      string `json:"mobile"`
	CardNo      string `json:"cardNo"`
}

type ctripPreOrderItem struct {
	ItemID            string           `json:"itemId"`
	PLU               string           `json:"PLU"`
	Locale            string           `json:"locale"`
	Quantity          int              `json:"quantity"`
	Price             float64          `json:"price"`
	PriceCurrency     string           `json:"priceCurrency"`
	UseStartDate      string           `json:"useStartDate"`
	UseEndDate        string           `json:"useEndDate"`
	SalePrice         float64          `json:"salePrice"`
	SalePriceCurrency string           `json:"salePriceCurrency"`
	Cost              float64          `json:"cost"`
	CostCurrency      string           `json:"costCurrency"`
	Passengers        []ctripPassenger `json:"passengers"`
}

type ctripCreatePreOrderRequest struct {
	SequenceID string              `json:"sequenceId"`
	OTAOrderID string              `json:"otaOrderId"`
	Contacts   []ctripContact      `json:"contacts"`
	Items      []ctripPreOrderItem `json:"items"`
}

type ctripPayItem struct {
	ItemID string `json:"itemId"`
	PLU    string `json:"PLU"`
}

type ctripPayPreOrderRequest struct {
	SequenceID      string         `json:"sequenceId"`
	OTAOrderID      string         `json:"otaOrderId"`
	SupplierOrderID string         `json:"supplierOrderId"`
	ConfirmType     int            `json:"confirmType"`
	Items           []ctripPayItem `json:"items"`
}

type ctripCancelPreOrderRequest struct {
	SequenceID string `json:"sequenceId"`
	OTAOrderID string `json:"otaOrderId"`
}

type ctripCancelItem struct {
	ItemID     string `json:"itemId"`
	PLU        string `json:"PLU"`
	CancelType int    `json:"cancelType"`
	Quantity   int    `json:"quantity"`
}

type ctripCancelOrderRequest struct {
	SequenceID      string            `json:"sequenceId"`
	OTAOrderID      string            `json:"otaOrderId"`
	SupplierOrderID string            `json:"supplierOrderId"`
	ConfirmType     int               `json:"confirmType"`
	Items           []ctripCancelItem `json:"items"`
}

type ctripQueryOrderRequest struct {
	SequenceID      string `json:"sequenceId"`
	OTAOrderID      string `json:"otaOrderId"`
	SupplierOrderID string `json:"supplierOrderId"`
}

var ctripRequestStripes [64]sync.Mutex

func ctripRequestLock(accountID uint, sequenceID string) *sync.Mutex {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", accountID, sequenceID)))
	return &ctripRequestStripes[int(sum[0])%len(ctripRequestStripes)]
}

func (s *CtripProtocolService) Handle(raw []byte, remoteIP string) ([]byte, error) {
	var envelope ctripEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return marshalCtripHeaderOnly("0001", "报文解析失败"), nil
	}
	account, signKey, config, err := loadCtripAccount(envelope.Header.AccountID)
	if err != nil {
		return marshalCtripHeaderOnly("0003", "供应商账户信息不正确"), nil
	}
	if !ctripIPAllowed(account.AllowedIPsJSON, remoteIP) {
		return marshalCtripHeaderOnly("0003", "来源网络地址未获授权"), nil
	}
	if !ctripRequestTimeValid(envelope.Header.RequestTime, time.Now()) {
		return marshalCtripHeaderOnly("0001", "请求时间无效或已过期"), nil
	}
	if !ctripPermissionAllows(account.PermissionsJSON, envelope.Header.ServiceName) {
		return marshalCtripHeaderOnly("0003", "接口权限未授权"), nil
	}
	plainBody, err := decryptCtripBody(envelope.Body, config.AESKey, config.AESIV)
	if err != nil {
		return marshalCtripHeaderOnly("0001", "报文解密失败"), nil
	}
	var sequence ctripSequence
	if err := json.Unmarshal(plainBody, &sequence); err != nil || strings.TrimSpace(sequence.SequenceID) == "" {
		return marshalCtripHeaderOnly("0001", "缺少处理批次流水号"), nil
	}
	// Ctrip signs the encrypted body exactly as it appears in the envelope.
	provided := strings.ToLower(strings.TrimSpace(envelope.Header.Sign))
	expected := ctripSignature(envelope.Header, envelope.Body, signKey)
	if len(expected) != len(provided) || subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) != 1 {
		if logger.Log != nil {
			logger.Log.Warn("ctrip request signature mismatch",
				zap.String("account_id", envelope.Header.AccountID),
				zap.String("service_name", envelope.Header.ServiceName),
				zap.Int("encrypted_body_length", len(envelope.Body)),
				zap.Int("plain_body_length", len(plainBody)),
				zap.Int("signature_length", len(envelope.Header.Sign)),
			)
		}
		return marshalCtripHeaderOnly("0002", "签名错误"), nil
	}

	lock := ctripRequestLock(account.ID, sequence.SequenceID)
	lock.Lock()
	defer lock.Unlock()

	requestHash := sha256.Sum256([]byte(envelope.Header.ServiceName + "\n" + envelope.Body))
	hashText := hex.EncodeToString(requestHash[:])
	endpoint := "ctrip:" + envelope.Header.ServiceName
	var request model.ChannelRequest
	retryAttempt := false
	err = model.DB.Where("channel_account_id = ? AND request_id = ?", account.ID, sequence.SequenceID).First(&request).Error
	if err == nil {
		if request.BodyHash != hashText || request.Endpoint != endpoint {
			return s.encryptResponse(config, "0001", "同一流水号的报文内容不一致", nil), nil
		}
		if request.Status == "completed" && request.ResponseJSON != "" {
			return []byte(request.ResponseJSON), nil
		}
		retryAttempt = true
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	} else {
		now := time.Now()
		if account.RateLimitPerMin > 0 {
			var recent int64
			if err := model.DB.Model(&model.ChannelRequest{}).Where("channel_account_id = ? AND created_at >= ?", account.ID, now.Add(-time.Minute)).Count(&recent).Error; err != nil {
				return nil, err
			}
			if recent >= int64(account.RateLimitPerMin) {
				return s.encryptResponse(config, "1100", "请求过于频繁，请稍后重试", nil), nil
			}
		}
		request = model.ChannelRequest{
			ChannelAccountID: account.ID, RequestID: sequence.SequenceID, Endpoint: endpoint,
			BodyHash: hashText, Status: "processing", ResponseStatus: 200, RemoteIP: remoteIP,
			AttemptCount: 1, LastAttemptAt: &now, LockedAt: &now,
		}
		if err := model.Write(func(tx *gorm.DB) error { return tx.Create(&request).Error }); err != nil {
			return nil, err
		}
	}

	code, message, responseBody := s.dispatch(account, envelope.Header.ServiceName, plainBody)
	response := s.encryptResponse(config, code, message, responseBody)
	now := time.Now()
	status := "completed"
	var completedAt interface{} = &now
	if code == "1100" && message == "系统处理失败，请稍后重试" {
		status = "failed"
		completedAt = nil
	}
	attemptCount := request.AttemptCount
	if retryAttempt {
		attemptCount++
	}
	if err := model.Write(func(tx *gorm.DB) error {
		return tx.Model(&model.ChannelRequest{}).Where("id = ?", request.ID).Updates(map[string]interface{}{
			"status": status, "response_json": string(response), "response_status": 200, "attempt_count": attemptCount,
			"completed_at": completedAt, "last_attempt_at": &now, "locked_at": nil,
		}).Error
	}); err != nil {
		return nil, err
	}
	_ = model.DB.Model(&model.ChannelAccount{}).Where("id = ?", account.ID).Update("last_used_at", &now).Error
	return response, nil
}

func loadCtripAccount(accountID string) (*model.ChannelAccount, string, CtripChannelConfig, error) {
	var account model.ChannelAccount
	query := model.DB.Where("type = ? AND app_id = ? AND status IN ?", "ctrip", strings.TrimSpace(accountID), []string{"active", "sandbox"})
	var count int64
	if err := query.Model(&model.ChannelAccount{}).Count(&count).Error; err != nil || count != 1 {
		return nil, "", CtripChannelConfig{}, errors.New("ctrip account unavailable")
	}
	if err := query.First(&account).Error; err != nil {
		return nil, "", CtripChannelConfig{}, err
	}
	if err := requireAnyActiveTenantCapability(model.DB, account.TenantID, "supplier"); err != nil {
		return nil, "", CtripChannelConfig{}, err
	}
	signKey, err := utils.DecryptAES(account.SecretCiphertext)
	if err != nil {
		return nil, "", CtripChannelConfig{}, err
	}
	configText, err := utils.DecryptAES(account.ProtocolConfigCiphertext)
	if err != nil {
		return nil, "", CtripChannelConfig{}, err
	}
	var config CtripChannelConfig
	if err := json.Unmarshal([]byte(configText), &config); err != nil || len([]byte(config.AESKey)) != 16 || len([]byte(config.AESIV)) != 16 {
		return nil, "", CtripChannelConfig{}, errors.New("invalid ctrip crypto config")
	}
	return &account, signKey, config, nil
}

func ctripSignature(header ctripHeader, body, signKey string) string {
	value := header.AccountID + header.ServiceName + header.RequestTime + body + header.Version + signKey
	sum := md5.Sum([]byte(value)) // Ctrip's published protocol requires MD5 for compatibility.
	return hex.EncodeToString(sum[:])
}

func ctripRequestTimeValid(value string, now time.Time) bool {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		location = time.Local
	}
	parsed, err := time.ParseInLocation("2006-01-02 15:04:05", strings.TrimSpace(value), location)
	if err != nil {
		return false
	}
	maxSkew := time.Duration(config.GlobalConfig.Security.OTAMaxClockSkewSeconds) * time.Second
	if maxSkew <= 0 {
		maxSkew = 5 * time.Minute
	}
	delta := now.In(location).Sub(parsed)
	return delta <= maxSkew && delta >= -maxSkew
}

func encryptCtripBody(plain []byte, key, iv string) (string, error) {
	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		return "", err
	}
	padding := block.BlockSize() - len(plain)%block.BlockSize()
	padded := append(append([]byte(nil), plain...), make([]byte, padding)...)
	for i := len(padded) - padding; i < len(padded); i++ {
		padded[i] = byte(padding)
	}
	encrypted := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, []byte(iv)).CryptBlocks(encrypted, padded)
	var encoded strings.Builder
	encoded.Grow(len(encrypted) * 2)
	for _, value := range encrypted {
		encoded.WriteByte('a' + (value >> 4))
		encoded.WriteByte('a' + (value & 0x0f))
	}
	return encoded.String(), nil
}

func decryptCtripBody(encoded, key, iv string) ([]byte, error) {
	var encrypted []byte
	if len(encoded)%2 == 0 && strings.Trim(encoded, "abcdefghijklmnop") == "" {
		encrypted = make([]byte, len(encoded)/2)
		for i := 0; i < len(encoded); i += 2 {
			encrypted[i/2] = (encoded[i]-'a')<<4 | (encoded[i+1] - 'a')
		}
	} else {
		var err error
		encrypted, err = base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, err
		}
	}
	block, err := aes.NewCipher([]byte(key))
	if err != nil || len(encrypted) == 0 || len(encrypted)%aes.BlockSize != 0 {
		return nil, errors.New("invalid encrypted body")
	}
	plain := make([]byte, len(encrypted))
	cipher.NewCBCDecrypter(block, []byte(iv)).CryptBlocks(plain, encrypted)
	padding := int(plain[len(plain)-1])
	if padding < 1 || padding > aes.BlockSize || padding > len(plain) {
		return nil, errors.New("invalid body padding")
	}
	for _, value := range plain[len(plain)-padding:] {
		if int(value) != padding {
			return nil, errors.New("invalid body padding")
		}
	}
	return plain[:len(plain)-padding], nil
}

func ctripIPAllowed(raw, remote string) bool {
	if strings.TrimSpace(raw) == "" {
		return true
	}
	var allowed []string
	if err := json.Unmarshal([]byte(raw), &allowed); err != nil {
		return false
	}
	ip := net.ParseIP(strings.TrimSpace(remote))
	if ip == nil {
		return false
	}
	for _, entry := range allowed {
		entry = strings.TrimSpace(entry)
		if candidate := net.ParseIP(entry); candidate != nil && candidate.Equal(ip) {
			return true
		}
		if _, network, err := net.ParseCIDR(entry); err == nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

func ctripPermissionAllows(raw, serviceName string) bool {
	required := map[string]string{
		"CreatePreOrder": "orders:create",
		"PayPreOrder":    "orders:create",
		"CancelPreOrder": "orders:cancel",
		"CancelOrder":    "orders:cancel",
		"QueryOrder":     "orders:query",
	}[serviceName]
	if required == "" {
		return false
	}
	var permissions []string
	if json.Unmarshal([]byte(raw), &permissions) != nil {
		return false
	}
	for _, permission := range permissions {
		if permission == "*" || permission == required {
			return true
		}
	}
	return false
}

func marshalCtripHeaderOnly(code, message string) []byte {
	data, _ := json.Marshal(ctripResponse{Header: ctripResponseHeader{ResultCode: code, ResultMessage: message}})
	return data
}

func (s *CtripProtocolService) encryptResponse(config CtripChannelConfig, code, message string, body interface{}) []byte {
	response := ctripResponse{Header: ctripResponseHeader{ResultCode: code, ResultMessage: message}}
	if code == "0000" && body != nil {
		plain, err := json.Marshal(body)
		if err != nil {
			return marshalCtripHeaderOnly("0001", "响应报文生成失败")
		}
		response.Body, err = encryptCtripBody(plain, config.AESKey, config.AESIV)
		if err != nil {
			return marshalCtripHeaderOnly("0001", "响应报文加密失败")
		}
	}
	data, _ := json.Marshal(response)
	return data
}

func (s *CtripProtocolService) dispatch(account *model.ChannelAccount, serviceName string, body []byte) (string, string, interface{}) {
	var response interface{}
	var err error
	switch serviceName {
	case "CreatePreOrder":
		response, err = s.createPreOrder(account, body)
	case "PayPreOrder":
		response, err = s.payPreOrder(account, body)
	case "CancelPreOrder":
		response, err = s.cancelPreOrder(account, body)
	case "CancelOrder":
		response, err = s.cancelOrder(account, body)
	case "QueryOrder":
		response, err = s.queryOrder(account, body)
	default:
		return "0001", "暂不支持该接口方法", nil
	}
	if err == nil {
		return "0000", "操作成功", response
	}
	var businessErr *ctripBusinessError
	if errors.As(err, &businessErr) {
		return businessErr.Code, businessErr.Message, nil
	}
	return "1100", "系统处理失败，请稍后重试", nil
}

func (s *CtripProtocolService) createPreOrder(account *model.ChannelAccount, raw []byte) (interface{}, error) {
	var input ctripCreatePreOrderRequest
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, &ctripBusinessError{Code: "1006", Message: "预下单信息格式错误"}
	}
	if strings.TrimSpace(input.OTAOrderID) == "" || len(input.Items) == 0 {
		return nil, &ctripBusinessError{Code: "1005", Message: "缺少携程订单号或订单项"}
	}
	if len(input.OTAOrderID) > 100 {
		return nil, &ctripBusinessError{Code: "1006", Message: "携程订单号过长"}
	}
	contactName, contactPhone := "", ""
	if len(input.Contacts) > 0 {
		contactName, contactPhone = strings.TrimSpace(input.Contacts[0].Name), strings.TrimSpace(input.Contacts[0].Mobile)
	}
	order := model.Order{TenantID: account.TenantID, ContactName: contactName, ContactPhone: contactPhone, Channel: fmt.Sprintf("ctrip:%d", account.ID), ChannelAccountID: account.ID}
	externalNo := strings.TrimSpace(input.OTAOrderID)
	order.ExternalNo = &externalNo
	links := make([]model.CtripOrderItem, 0, len(input.Items))
	seenItems := make(map[string]struct{}, len(input.Items))
	for _, source := range input.Items {
		itemID, plu := strings.TrimSpace(source.ItemID), strings.TrimSpace(source.PLU)
		if itemID == "" || plu == "" || source.Quantity <= 0 {
			return nil, &ctripBusinessError{Code: "1005", Message: "订单项编号、PLU 和数量必填"}
		}
		if len(itemID) > 100 || len(plu) > 120 {
			return nil, &ctripBusinessError{Code: "1006", Message: "订单项编号或 PLU 过长"}
		}
		if _, exists := seenItems[itemID]; exists {
			return nil, &ctripBusinessError{Code: "1006", Message: "订单项编号重复"}
		}
		seenItems[itemID] = struct{}{}
		if !isCNY(source.PriceCurrency) || !isCNY(source.SalePriceCurrency) || (source.CostCurrency != "" && !isCNY(source.CostCurrency)) {
			return nil, &ctripBusinessError{Code: "1006", Message: "当前仅支持人民币订单"}
		}
		useDate, err := parseCtripDate(source.UseStartDate)
		useEndDate, endDateErr := parseCtripDate(source.UseEndDate)
		if err != nil || endDateErr != nil || useEndDate.Before(*useDate) {
			return nil, &ctripBusinessError{Code: "1009", Message: "使用日期错误"}
		}
		if source.Price < 0 || source.SalePrice < 0 || source.Cost < 0 {
			return nil, &ctripBusinessError{Code: "1006", Message: "订单金额不能为负数"}
		}
		var mapping model.ChannelProductMapping
		if err := model.DB.Where("channel_account_id = ? AND external_code = ? AND status = ?", account.ID, plu, "active").First(&mapping).Error; err != nil {
			return nil, &ctripBusinessError{Code: "1001", Message: "产品 PLU 不存在"}
		}
		if mapping.ChannelSaleCents <= 0 || mapping.ChannelCostCents < 0 || mapping.ChannelCostCents > mapping.ChannelSaleCents {
			return nil, &ctripBusinessError{Code: "1002", Message: "产品价格尚未配置"}
		}
		costProvided := strings.TrimSpace(source.CostCurrency) != "" || source.Cost != 0
		if moneyToCents(source.SalePrice) != mapping.ChannelSaleCents || costProvided && moneyToCents(source.Cost) != mapping.ChannelCostCents {
			return nil, &ctripBusinessError{Code: "1008", Message: "订单价格与已同步价格不一致"}
		}
		var product model.Product
		if err := model.DB.Where("id = ? AND tenant_id = ?", mapping.ProductID, account.TenantID).First(&product).Error; err != nil {
			return nil, &ctripBusinessError{Code: "1001", Message: "产品 PLU 不存在"}
		}
		if product.Status != "online" {
			return nil, &ctripBusinessError{Code: "1002", Message: "产品已经下架"}
		}
		passengerIDs := make([]string, 0, len(source.Passengers))
		visitors := make([]model.VisitorInput, 0, len(source.Passengers))
		seenPassengers := make(map[string]struct{}, len(source.Passengers))
		for _, passenger := range source.Passengers {
			passengerID := strings.TrimSpace(passenger.PassengerID)
			if passengerID == "" {
				return nil, &ctripBusinessError{Code: "1005", Message: "缺少出行人编号"}
			}
			if _, exists := seenPassengers[passengerID]; exists {
				return nil, &ctripBusinessError{Code: "1006", Message: "出行人编号重复"}
			}
			seenPassengers[passengerID] = struct{}{}
			passengerIDs = append(passengerIDs, passengerID)
			visitors = append(visitors, model.VisitorInput{Name: passenger.Name, Phone: passenger.Mobile, IdentityNo: passenger.CardNo})
		}
		if product.CodeMode == "ticket" && len(visitors) > 0 && len(visitors) != source.Quantity {
			return nil, &ctripBusinessError{Code: "1006", Message: "出行人数量与购票数量不一致"}
		}
		item := model.OrderItem{ProductID: mapping.ProductID, Quantity: source.Quantity, UseDate: useDate}
		if product.CodeMode == "ticket" {
			item.Visitors = visitors
		} else if len(visitors) > 0 {
			item.VisitorName, item.VisitorPhone, item.VisitorID = visitors[0].Name, visitors[0].Phone, visitors[0].IdentityNo
		}
		order.Items = append(order.Items, item)
		passengerJSON, _ := json.Marshal(passengerIDs)
		links = append(links, model.CtripOrderItem{
			ExternalItemID: itemID, PLU: plu, PassengerIDsJSON: string(passengerJSON),
			GuestPriceCents: moneyToCents(source.Price), SalePriceCents: moneyToCents(source.SalePrice), CostCents: moneyToCents(source.Cost),
		})
	}
	if err := s.OrderService.Create(&order); err != nil {
		if !errors.Is(err, ErrDuplicateExternalOrder) {
			return nil, mapCtripCreateError(err)
		}
		if existingLink, existingOrder, loadErr := loadCtripOrder(account, externalNo, ""); loadErr == nil {
			if !ctripPreOrderMatches(existingLink, existingOrder, order.Items, links) {
				return nil, &ctripBusinessError{Code: "1100", Message: "携程订单号已存在且订单内容不一致"}
			}
			return ctripCreatePreOrderResponse(existingLink), nil
		}
		existingOrder, loadErr := s.OrderService.GetByExternalNo(externalNo, order.Channel, account.TenantID)
		if loadErr != nil || !ctripLocalOrderMatches(existingOrder, order.Items) {
			return nil, &ctripBusinessError{Code: "1100", Message: "携程订单号已存在且订单内容不一致"}
		}
		order = *existingOrder
	}
	sort.Slice(order.Items, func(left, right int) bool { return order.Items[left].ID < order.Items[right].ID })
	for index := range links {
		links[index].OrderItemID = order.Items[index].ID
	}
	link := model.CtripOrderLink{
		TenantID: account.TenantID, ChannelAccountID: account.ID, OrderID: order.ID,
		OTAOrderID: externalNo, SupplierOrderID: order.OrderNo, State: "preordered", Items: links,
	}
	if err := model.Write(func(tx *gorm.DB) error { return tx.Create(&link).Error }); err != nil {
		if existingLink, existingOrder, loadErr := loadCtripOrder(account, externalNo, ""); loadErr == nil && ctripPreOrderMatches(existingLink, existingOrder, order.Items, links) {
			return ctripCreatePreOrderResponse(existingLink), nil
		}
		return nil, err
	}
	return ctripCreatePreOrderResponse(&link), nil
}

func ctripCreatePreOrderResponse(link *model.CtripOrderLink) map[string]interface{} {
	responseItems := make([]map[string]interface{}, 0, len(link.Items))
	for _, item := range link.Items {
		responseItems = append(responseItems, map[string]interface{}{"PLU": item.PLU})
	}
	return map[string]interface{}{"otaOrderId": link.OTAOrderID, "supplierOrderId": link.SupplierOrderID, "items": responseItems}
}

func ctripLocalOrderMatches(existing *model.Order, requested []model.OrderItem) bool {
	if existing == nil || len(existing.Items) != len(requested) {
		return false
	}
	items := append([]model.OrderItem(nil), existing.Items...)
	sort.Slice(items, func(left, right int) bool { return items[left].ID < items[right].ID })
	for index := range requested {
		if items[index].ProductID != requested[index].ProductID || items[index].Quantity != requested[index].Quantity || !sameOptionalDate(items[index].UseDate, requested[index].UseDate) {
			return false
		}
	}
	return true
}

func ctripPreOrderMatches(link *model.CtripOrderLink, existing *model.Order, requestedItems []model.OrderItem, requestedLinks []model.CtripOrderItem) bool {
	if link == nil || existing == nil || len(link.Items) != len(requestedLinks) || len(existing.Items) != len(requestedItems) {
		return false
	}
	orderItems := make(map[uint]model.OrderItem, len(existing.Items))
	for _, item := range existing.Items {
		orderItems[item.ID] = item
	}
	existingLinks := make(map[string]model.CtripOrderItem, len(link.Items))
	for _, item := range link.Items {
		existingLinks[item.ExternalItemID] = item
	}
	for index, requestedLink := range requestedLinks {
		existingLink, ok := existingLinks[requestedLink.ExternalItemID]
		if !ok || existingLink.PLU != requestedLink.PLU || existingLink.PassengerIDsJSON != requestedLink.PassengerIDsJSON ||
			existingLink.GuestPriceCents != requestedLink.GuestPriceCents || existingLink.SalePriceCents != requestedLink.SalePriceCents || existingLink.CostCents != requestedLink.CostCents {
			return false
		}
		orderItem, ok := orderItems[existingLink.OrderItemID]
		if !ok || orderItem.ProductID != requestedItems[index].ProductID || orderItem.Quantity != requestedItems[index].Quantity || !sameOptionalDate(orderItem.UseDate, requestedItems[index].UseDate) {
			return false
		}
	}
	return true
}

func mapCtripCreateError(err error) error {
	message := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, ErrDuplicateExternalOrder):
		return &ctripBusinessError{Code: "1100", Message: "携程订单号已存在"}
	case strings.Contains(message, "stock") || strings.Contains(message, "inventory") || strings.Contains(message, "库存"):
		return &ctripBusinessError{Code: "1003", Message: "库存不足"}
	case strings.Contains(message, "date") || strings.Contains(message, "validity") || strings.Contains(message, "日期"):
		return &ctripBusinessError{Code: "1009", Message: "使用日期错误"}
	case strings.Contains(message, "visitor") || strings.Contains(message, "contact"):
		return &ctripBusinessError{Code: "1006", Message: "游客或联系人信息错误"}
	case strings.Contains(message, "unavailable") || strings.Contains(message, "offline"):
		return &ctripBusinessError{Code: "1002", Message: "产品已经下架或不可售"}
	default:
		return err
	}
}

func (s *CtripProtocolService) payPreOrder(account *model.ChannelAccount, raw []byte) (interface{}, error) {
	var input ctripPayPreOrderRequest
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, &ctripBusinessError{Code: "1006", Message: "支付信息格式错误"}
	}
	link, order, err := loadCtripOrder(account, input.OTAOrderID, input.SupplierOrderID)
	if err != nil {
		return nil, &ctripBusinessError{Code: "1100", Message: "预下单不存在"}
	}
	if link.State == "pre_cancelled" || link.State == "cancelled" {
		return nil, &ctripBusinessError{Code: "1100", Message: "订单已经取消"}
	}
	if err := validateCtripPayItems(link.Items, input.Items); err != nil {
		return nil, err
	}
	if err := model.Write(func(tx *gorm.DB) error {
		var lockedLink model.CtripOrderLink
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND channel_account_id = ?", link.ID, account.ID).First(&lockedLink).Error; err != nil {
			return err
		}
		if lockedLink.State == "pre_cancelled" || lockedLink.State == "cancelled" {
			return errors.New("ctrip order is cancelled")
		}
		var lockedOrder model.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND channel_account_id = ?", order.ID, account.TenantID, account.ID).First(&lockedOrder).Error; err != nil {
			return err
		}
		if err := markOrderAsPaidTx(tx, &lockedOrder); err != nil {
			return err
		}
		return tx.Model(&lockedLink).Update("state", "paid").Error
	}); err != nil {
		return nil, err
	}
	link.State = "paid"
	return buildCtripPayResponse(link)
}

func validateCtripPayItems(expected []model.CtripOrderItem, actual []ctripPayItem) error {
	if len(expected) != len(actual) {
		return &ctripBusinessError{Code: "1006", Message: "支付订单项与预下单不一致"}
	}
	values := make(map[string]string, len(actual))
	for _, item := range actual {
		values[strings.TrimSpace(item.ItemID)] = strings.TrimSpace(item.PLU)
	}
	for _, item := range expected {
		if values[item.ExternalItemID] != item.PLU {
			return &ctripBusinessError{Code: "1006", Message: "支付订单项与预下单不一致"}
		}
	}
	return nil
}

func buildCtripPayResponse(link *model.CtripOrderLink) (interface{}, error) {
	var items []model.OrderItem
	if err := model.DB.Preload("Tickets").Where("order_id = ?", link.OrderID).Order("id").Find(&items).Error; err != nil {
		return nil, err
	}
	byID := make(map[uint]model.OrderItem, len(items))
	for _, item := range items {
		byID[item.ID] = item
	}
	vouchers := make([]map[string]interface{}, 0)
	responseItems := make([]map[string]interface{}, 0, len(link.Items))
	for _, protocolItem := range link.Items {
		item, ok := byID[protocolItem.OrderItemID]
		if !ok {
			return nil, errors.New("ctrip order item link is inconsistent")
		}
		for _, ticket := range item.Tickets {
			vouchers = append(vouchers, map[string]interface{}{
				"itemId": protocolItem.ExternalItemID, "voucherId": ticket.TicketCode,
				"voucherType": 3, "voucherCode": ticket.TicketCode, "voucherData": ticket.TicketCode,
			})
		}
		responseItems = append(responseItems, map[string]interface{}{"itemId": protocolItem.ExternalItemID, "isCredentialVouchers": 0})
	}
	return map[string]interface{}{
		"otaOrderId": link.OTAOrderID, "supplierOrderId": link.SupplierOrderID,
		"supplierConfirmType": 1, "voucherSender": 1, "vouchers": vouchers, "items": responseItems,
	}, nil
}

func (s *CtripProtocolService) cancelPreOrder(account *model.ChannelAccount, raw []byte) (interface{}, error) {
	var input ctripCancelPreOrderRequest
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, &ctripBusinessError{Code: "2001", Message: "预下单取消信息错误"}
	}
	link, order, err := loadCtripOrder(account, input.OTAOrderID, "")
	if err != nil {
		return nil, &ctripBusinessError{Code: "2001", Message: "该订单号不存在"}
	}
	if link.State == "pre_cancelled" {
		return nil, nil
	}
	if order.Status != "unpaid" {
		return nil, &ctripBusinessError{Code: "2100", Message: "该订单已支付，不能按预下单取消"}
	}
	if err := model.Write(func(tx *gorm.DB) error {
		var lockedLink model.CtripOrderLink
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND channel_account_id = ?", link.ID, account.ID).First(&lockedLink).Error; err != nil {
			return err
		}
		if lockedLink.State == "pre_cancelled" {
			return nil
		}
		var lockedOrder model.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Preload("Items.Tickets").Where("id = ? AND tenant_id = ? AND channel_account_id = ?", order.ID, account.TenantID, account.ID).First(&lockedOrder).Error; err != nil {
			return err
		}
		if lockedOrder.Status != "unpaid" && lockedOrder.Status != "cancelled" {
			return errors.New("paid order cannot be cancelled as a preorder")
		}
		if err := cancelOrderTx(tx, &lockedOrder); err != nil {
			return err
		}
		return tx.Model(&lockedLink).Update("state", "pre_cancelled").Error
	}); err != nil {
		return nil, err
	}
	return nil, nil
}

func (s *CtripProtocolService) cancelOrder(account *model.ChannelAccount, raw []byte) (interface{}, error) {
	var input ctripCancelOrderRequest
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, &ctripBusinessError{Code: "2004", Message: "取消信息格式错误"}
	}
	link, order, err := loadCtripOrder(account, input.OTAOrderID, input.SupplierOrderID)
	if err != nil {
		return nil, &ctripBusinessError{Code: "2001", Message: "该订单号不存在"}
	}
	if link.State == "cancelled" {
		return buildCtripCancelResponse(link)
	}
	if order.Status != "paid" {
		return nil, &ctripBusinessError{Code: "2007", Message: "该订单尚未支付"}
	}
	if err := validateFullCtripCancellation(link.Items, order.Items, input.Items); err != nil {
		return nil, err
	}
	if err := cancelCtripOrderAtomic(account, link.ID, order.ID); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "used") || strings.Contains(err.Error(), "核销") {
			return nil, &ctripBusinessError{Code: "2002", Message: "该订单已经使用"}
		}
		return nil, err
	}
	link.State = "cancelled"
	return buildCtripCancelResponse(link)
}

func validateFullCtripCancellation(protocolItems []model.CtripOrderItem, orderItems []model.OrderItem, requested []ctripCancelItem) error {
	if len(protocolItems) != len(orderItems) || len(requested) != len(protocolItems) {
		return &ctripBusinessError{Code: "2004", Message: "当前仅支持整单退订"}
	}
	quantities := make(map[uint]int, len(orderItems))
	for _, item := range orderItems {
		quantities[item.ID] = item.Quantity
	}
	requestByID := make(map[string]ctripCancelItem, len(requested))
	for _, item := range requested {
		requestByID[strings.TrimSpace(item.ItemID)] = item
	}
	for _, item := range protocolItems {
		request, ok := requestByID[item.ExternalItemID]
		if !ok || strings.TrimSpace(request.PLU) != item.PLU || request.CancelType != 0 || request.Quantity != quantities[item.OrderItemID] {
			return &ctripBusinessError{Code: "2004", Message: "当前仅支持整单退订"}
		}
	}
	return nil
}

func cancelCtripOrderAtomic(account *model.ChannelAccount, linkID, orderID uint) error {
	return model.Write(func(tx *gorm.DB) error {
		var link model.CtripOrderLink
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND channel_account_id = ?", linkID, account.ID).First(&link).Error; err != nil {
			return err
		}
		if link.State == "cancelled" {
			return nil
		}
		var order model.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Preload("Items.Tickets").Where("id = ? AND tenant_id = ? AND channel_account_id = ?", orderID, account.TenantID, account.ID).First(&order).Error; err != nil {
			return err
		}
		if order.Status != "paid" && order.Status != "cancelled" {
			return errors.New("channel order is not paid")
		}
		if err := cancelOrderTxMode(tx, &order, true); err != nil {
			return err
		}
		return tx.Model(&link).Update("state", "cancelled").Error
	})
}

func buildCtripCancelResponse(link *model.CtripOrderLink) (interface{}, error) {
	items := make([]map[string]interface{}, 0, len(link.Items))
	for _, protocolItem := range link.Items {
		var tickets []model.Ticket
		if err := model.DB.Where("order_item_id = ?", protocolItem.OrderItemID).Order("id").Find(&tickets).Error; err != nil {
			return nil, err
		}
		vouchers := make([]map[string]string, 0, len(tickets))
		for _, ticket := range tickets {
			vouchers = append(vouchers, map[string]string{"voucherId": ticket.TicketCode})
		}
		items = append(items, map[string]interface{}{"itemId": protocolItem.ExternalItemID, "vouchers": vouchers})
	}
	return map[string]interface{}{"supplierConfirmType": 1, "items": items}, nil
}

func (s *CtripProtocolService) queryOrder(account *model.ChannelAccount, raw []byte) (interface{}, error) {
	var input ctripQueryOrderRequest
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, &ctripBusinessError{Code: "4001", Message: "订单查询信息错误"}
	}
	link, order, err := loadCtripOrder(account, input.OTAOrderID, input.SupplierOrderID)
	if err != nil {
		return nil, &ctripBusinessError{Code: "4001", Message: "该订单号不存在"}
	}
	byID := make(map[uint]model.OrderItem, len(order.Items))
	for _, item := range order.Items {
		byID[item.ID] = item
	}
	responseItems := make([]map[string]interface{}, 0, len(link.Items))
	for _, protocolItem := range link.Items {
		item, ok := byID[protocolItem.OrderItemID]
		if !ok {
			return nil, errors.New("ctrip order item link is inconsistent")
		}
		used, cancelled := 0, 0
		vouchers := make([]map[string]interface{}, 0, len(item.Tickets))
		for _, ticket := range item.Tickets {
			status := 0
			switch ticket.Status {
			case "used":
				status, used = 1, used+1
			case "refunded", "void":
				status, cancelled = 2, cancelled+1
			}
			vouchers = append(vouchers, map[string]interface{}{"voucherId": ticket.TicketCode, "voucherStatus": status})
		}
		quantityUsed := used
		quantityCancelled := cancelled
		if len(item.Tickets) == 1 && item.Tickets[0].CodeMode == "order" {
			if used > 0 {
				quantityUsed = item.Quantity
			}
			if cancelled > 0 {
				quantityCancelled = item.Quantity
			}
		}
		responseItem := map[string]interface{}{
			"itemId": protocolItem.ExternalItemID, "orderStatus": ctripOrderItemStatus(order, link.State, &item),
			"quantity": item.Quantity, "useQuantity": quantityUsed, "cancelQuantity": quantityCancelled,
			"vouchers": vouchers,
		}
		if item.UseDate != nil {
			date := item.UseDate.Format("2006-01-02")
			responseItem["useStartDate"], responseItem["useEndDate"] = date, date
		}
		responseItems = append(responseItems, responseItem)
	}
	return map[string]interface{}{"otaOrderId": link.OTAOrderID, "supplierOrderId": link.SupplierOrderID, "items": responseItems}, nil
}

func loadCtripOrder(account *model.ChannelAccount, otaOrderID, supplierOrderID string) (*model.CtripOrderLink, *model.Order, error) {
	otaOrderID, supplierOrderID = strings.TrimSpace(otaOrderID), strings.TrimSpace(supplierOrderID)
	if otaOrderID == "" {
		return nil, nil, gorm.ErrRecordNotFound
	}
	var link model.CtripOrderLink
	if err := model.DB.Preload("Items").Where("tenant_id = ? AND channel_account_id = ? AND ota_order_id = ?", account.TenantID, account.ID, otaOrderID).First(&link).Error; err != nil {
		return nil, nil, err
	}
	if supplierOrderID != "" && link.SupplierOrderID != supplierOrderID {
		return nil, nil, gorm.ErrRecordNotFound
	}
	var order model.Order
	if err := model.DB.Preload("Items.Tickets").Where("id = ? AND tenant_id = ? AND channel_account_id = ?", link.OrderID, account.TenantID, account.ID).First(&order).Error; err != nil {
		return nil, nil, err
	}
	return &link, &order, nil
}

func ctripOrderItemStatus(order *model.Order, linkState string, item *model.OrderItem) int {
	if linkState == "pre_cancelled" {
		return 14
	}
	if order.Status == "unpaid" {
		return 11
	}
	if order.Status == "cancelled" || order.Status == "refunded" {
		return 5
	}
	total, used, cancelled, expired := 0, 0, 0, 0
	for _, ticket := range item.Tickets {
		total++
		switch ticket.Status {
		case "used":
			used++
		case "refunded", "void":
			cancelled++
		case "expired":
			expired++
		}
	}
	switch {
	case total > 0 && used == total:
		return 8
	case used > 0:
		return 7
	case total > 0 && cancelled == total:
		return 5
	case cancelled > 0:
		return 4
	case total > 0 && expired == total:
		return 10
	default:
		return 13
	}
}

func parseCtripDate(value string) (*time.Time, error) {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		location = time.Local
	}
	parsed, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(value), location)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func isCNY(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), "CNY")
}

func moneyToCents(value float64) int64 {
	return int64(math.Round(value * 100))
}
