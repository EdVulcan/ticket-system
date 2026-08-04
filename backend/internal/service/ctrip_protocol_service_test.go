package service

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"testing"
	"ticket-backend/internal/config"
	"ticket-backend/internal/model"
	"time"
)

func TestCtripBodyCodecMatchesPublishedNibbleFormatAndAcceptsBase64(t *testing.T) {
	key, iv := "1234567890abcdef", "abcdef1234567890"
	plain := []byte(`{"sequenceId":"20260804abcdef"}`)
	encoded, err := encryptCtripBody(plain, key, iv)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range encoded {
		if value < 'a' || value > 'p' {
			t.Fatalf("published a-p encoding contains %q", value)
		}
	}
	decoded, err := decryptCtripBody(encoded, key, iv)
	if err != nil || string(decoded) != string(plain) {
		t.Fatalf("a-p round trip=%q err=%v", decoded, err)
	}

	block, _ := aes.NewCipher([]byte(key))
	padding := aes.BlockSize - len(plain)%aes.BlockSize
	padded := append(append([]byte(nil), plain...), make([]byte, padding)...)
	for index := len(padded) - padding; index < len(padded); index++ {
		padded[index] = byte(padding)
	}
	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, []byte(iv)).CryptBlocks(ciphertext, padded)
	decoded, err = decryptCtripBody(base64.StdEncoding.EncodeToString(ciphertext), key, iv)
	if err != nil || string(decoded) != string(plain) {
		t.Fatalf("base64 compatibility=%q err=%v", decoded, err)
	}
}

func TestCtripQueryStatusIsCalculatedPerOrderItem(t *testing.T) {
	order := &model.Order{Status: "paid"}
	unused := &model.OrderItem{Tickets: []model.Ticket{{Status: "unused"}}}
	used := &model.OrderItem{Tickets: []model.Ticket{{Status: "used"}}}
	if status := ctripOrderItemStatus(order, "paid", unused); status != 13 {
		t.Fatalf("unused item status=%d", status)
	}
	if status := ctripOrderItemStatus(order, "paid", used); status != 8 {
		t.Fatalf("used item status=%d", status)
	}
}

func TestCtripPreOrderPaymentQueryAndCancellation(t *testing.T) {
	resetBusinessData(t)
	previousKey := config.GlobalConfig.Security.EncryptionKey
	config.GlobalConfig.Security.EncryptionKey = "0123456789abcdef0123456789abcdef"
	t.Cleanup(func() { config.GlobalConfig.Security.EncryptionKey = previousKey })

	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	channel := model.ChannelAccount{
		Code: "ctrip-sandbox", Type: "ctrip", Status: "sandbox",
		PermissionsJSON: `["orders:create","orders:query","orders:cancel"]`, RateLimitPerMin: 600,
	}
	channelService := &ChannelService{}
	const accountID = "ctrip-test-account"
	const signKey = "ctrip-sign-key"
	const aesKey = "1234567890abcdef"
	const aesIV = "abcdef1234567890"
	if err := channelService.CreateCtrip(tenantID, &channel, accountID, signKey, aesKey, aesIV); err != nil {
		t.Fatal(err)
	}
	if err := channelService.AddMapping(tenantID, &model.ChannelProductMapping{ChannelAccountID: channel.ID, ProductID: productID, ExternalCode: "PLU-ADULT"}); err != nil {
		t.Fatal(err)
	}
	service := &CtripProtocolService{OrderService: OrderService{}}
	visitDate := time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	createBody := map[string]interface{}{
		"sequenceId": "20260804-create-001", "otaOrderId": "CTRIP-ORDER-001",
		"contacts": []map[string]string{{"name": "测试游客", "mobile": "13800138000"}},
		"items": []map[string]interface{}{{
			"itemId": "ITEM-001", "PLU": "PLU-ADULT", "locale": "zh-CN", "quantity": 1,
			"price": 99.5, "priceCurrency": "CNY", "salePrice": 99.5, "salePriceCurrency": "CNY",
			"useStartDate": visitDate, "useEndDate": visitDate,
			"passengers": []map[string]string{{"passengerId": "PASSENGER-001", "name": "测试游客", "mobile": "13800138000", "cardNo": "110101199001010011"}},
		}},
	}
	createRequest := buildCtripTestRequest(t, accountID, signKey, aesKey, aesIV, "CreatePreOrder", createBody)
	createResponse, err := service.Handle(createRequest, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	createResult := decodeCtripTestResponse(t, createResponse, aesKey, aesIV)
	if createResult.Code != "0000" || createResult.Body["supplierOrderId"] == "" {
		t.Fatalf("create response=%+v", createResult)
	}
	supplierOrderID := createResult.Body["supplierOrderId"].(string)

	repeatedResponse, err := service.Handle(createRequest, "127.0.0.1")
	if err != nil || string(repeatedResponse) != string(createResponse) {
		t.Fatalf("idempotent response changed: err=%v\nfirst=%s\nsecond=%s", err, createResponse, repeatedResponse)
	}
	newBatchCreateBody := make(map[string]interface{}, len(createBody))
	for key, value := range createBody {
		newBatchCreateBody[key] = value
	}
	newBatchCreateBody["sequenceId"] = "20260804-create-002"
	newBatchResponse, err := service.Handle(buildCtripTestRequest(t, accountID, signKey, aesKey, aesIV, "CreatePreOrder", newBatchCreateBody), "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if result := decodeCtripTestResponse(t, newBatchResponse, aesKey, aesIV); result.Code != "0000" || result.Body["supplierOrderId"] != supplierOrderID {
		t.Fatalf("same order in a new batch was not recovered: %+v", result)
	}
	changedCreateBody := make(map[string]interface{}, len(createBody))
	for key, value := range createBody {
		changedCreateBody[key] = value
	}
	changedCreateBody["otaOrderId"] = "CTRIP-ORDER-CHANGED"
	changedResponse, err := service.Handle(buildCtripTestRequest(t, accountID, signKey, aesKey, aesIV, "CreatePreOrder", changedCreateBody), "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if result := decodeCtripTestResponse(t, changedResponse, aesKey, aesIV); result.Code != "0001" {
		t.Fatalf("changed body with reused sequence was accepted: %+v", result)
	}
	var orderCount int64
	if err := model.DB.Model(&model.Order{}).Where("tenant_id = ? AND external_no = ?", tenantID, "CTRIP-ORDER-001").Count(&orderCount).Error; err != nil || orderCount != 1 {
		t.Fatalf("order count=%d err=%v", orderCount, err)
	}

	payBody := map[string]interface{}{
		"sequenceId": "20260804-pay-001", "otaOrderId": "CTRIP-ORDER-001", "supplierOrderId": supplierOrderID,
		"confirmType": 2, "orderLastConfirmTime": time.Now().Add(time.Hour).Format("2006-01-02 15:04:05"),
		"items": []map[string]string{{"itemId": "ITEM-001", "PLU": "PLU-ADULT"}},
	}
	payResponse, err := service.Handle(buildCtripTestRequest(t, accountID, signKey, aesKey, aesIV, "PayPreOrder", payBody), "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	payResult := decodeCtripTestResponse(t, payResponse, aesKey, aesIV)
	if payResult.Code != "0000" || payResult.Body["supplierConfirmType"].(float64) != 1 {
		t.Fatalf("pay response=%+v", payResult)
	}
	var vouchers []interface{}
	if values, ok := payResult.Body["vouchers"].([]interface{}); ok {
		vouchers = values
	}
	if len(vouchers) != 1 {
		t.Fatalf("vouchers=%+v", payResult.Body["vouchers"])
	}

	queryBody := map[string]interface{}{"sequenceId": "20260804-query-001", "otaOrderId": "CTRIP-ORDER-001", "supplierOrderId": supplierOrderID}
	queryResponse, err := service.Handle(buildCtripTestRequest(t, accountID, signKey, aesKey, aesIV, "QueryOrder", queryBody), "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	queryResult := decodeCtripTestResponse(t, queryResponse, aesKey, aesIV)
	assertCtripItemStatus(t, queryResult, 13)

	cancelBody := map[string]interface{}{
		"sequenceId": "20260804-cancel-001", "otaOrderId": "CTRIP-ORDER-001", "supplierOrderId": supplierOrderID, "confirmType": 2,
		"items": []map[string]interface{}{{"itemId": "ITEM-001", "PLU": "PLU-ADULT", "cancelType": 0, "quantity": 1}},
	}
	cancelResponse, err := service.Handle(buildCtripTestRequest(t, accountID, signKey, aesKey, aesIV, "CancelOrder", cancelBody), "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	cancelResult := decodeCtripTestResponse(t, cancelResponse, aesKey, aesIV)
	if cancelResult.Code != "0000" {
		t.Fatalf("cancel response=%+v", cancelResult)
	}
	queryBody["sequenceId"] = "20260804-query-002"
	queryResponse, err = service.Handle(buildCtripTestRequest(t, accountID, signKey, aesKey, aesIV, "QueryOrder", queryBody), "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	assertCtripItemStatus(t, decodeCtripTestResponse(t, queryResponse, aesKey, aesIV), 5)
}

func TestSameOptionalDateIgnoresDatabaseTimezone(t *testing.T) {
	shanghai := time.FixedZone("Asia/Shanghai", 8*60*60)
	requestDate := time.Date(2026, 8, 5, 0, 0, 0, 0, shanghai)
	databaseDate := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	differentDate := time.Date(2026, 8, 6, 0, 0, 0, 0, time.UTC)

	if !sameOptionalDate(&requestDate, &databaseDate) {
		t.Fatal("the same calendar date in different timezones must match")
	}
	if sameOptionalDate(&requestDate, &differentDate) {
		t.Fatal("different calendar dates must not match")
	}
}

type ctripTestResponse struct {
	Code    string
	Message string
	Body    map[string]interface{}
}

func buildCtripTestRequest(t *testing.T, accountID, signKey, aesKey, aesIV, serviceName string, body interface{}) []byte {
	t.Helper()
	plain, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := encryptCtripBody(plain, aesKey, aesIV)
	if err != nil {
		t.Fatal(err)
	}
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		location = time.Local
	}
	header := ctripHeader{AccountID: accountID, ServiceName: serviceName, RequestTime: time.Now().In(location).Format("2006-01-02 15:04:05"), Version: "1.0"}
	header.Sign = ctripSignature(header, encrypted, signKey)
	data, err := json.Marshal(ctripEnvelope{Header: header, Body: encrypted})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func decodeCtripTestResponse(t *testing.T, raw []byte, aesKey, aesIV string) ctripTestResponse {
	t.Helper()
	var response ctripResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("decode response %s: %v", raw, err)
	}
	result := ctripTestResponse{Code: response.Header.ResultCode, Message: response.Header.ResultMessage}
	if response.Body != "" {
		plain, err := decryptCtripBody(response.Body, aesKey, aesIV)
		if err != nil {
			t.Fatalf("decrypt response: %v", err)
		}
		if err := json.Unmarshal(plain, &result.Body); err != nil {
			t.Fatalf("decode response body %s: %v", plain, err)
		}
	}
	return result
}

func assertCtripItemStatus(t *testing.T, result ctripTestResponse, expected int) {
	t.Helper()
	if result.Code != "0000" {
		t.Fatalf("query failed: %+v", result)
	}
	items, ok := result.Body["items"].([]interface{})
	if !ok || len(items) != 1 {
		t.Fatalf("query items=%+v", result.Body["items"])
	}
	item, ok := items[0].(map[string]interface{})
	if !ok || int(item["orderStatus"].(float64)) != expected {
		t.Fatalf("query status=%v want=%d", item["orderStatus"], expected)
	}
}
