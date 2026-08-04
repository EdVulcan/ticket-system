package ctrip

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// This client implements the protocol used by Trip.com's official Go SDK.
// Protocol reference: trip_sdk_golang.zip, SHA256
// 3EB45B41A3F367481B0A4F536193629EFF9EB4AF80CE2EFD23DCBE88BE9DF9E5.
// It intentionally omits the SDK demos, embedded sample credentials and
// process-wide logging behavior.
type Client struct {
	AccountID string
	SignKey   string
	AESKey    string
	AESIV     string
	HTTP      *http.Client
	Now       func() time.Time
}

type Response struct {
	Code    string
	Message string
	Raw     string
}

type Price struct {
	Date      string  `json:"date,omitempty"`
	SalePrice float64 `json:"salePrice,omitempty"`
	CostPrice float64 `json:"costPrice,omitempty"`
}

type PriceRequest struct {
	SequenceID       string  `json:"sequenceId"`
	SupplierOptionID string  `json:"supplierOptionId"`
	DateType         string  `json:"dateType"`
	Prices           []Price `json:"prices"`
}

type Inventory struct {
	Date     string `json:"date,omitempty"`
	Quantity int    `json:"quantity"`
}

type InventoryRequest struct {
	SequenceID       string      `json:"sequenceId"`
	SupplierOptionID string      `json:"supplierOptionId"`
	DateType         string      `json:"dateType"`
	Inventories      []Inventory `json:"inventorys"`
}

func (c *Client) SyncPrice(ctx context.Context, endpoint string, payload PriceRequest) (*Response, error) {
	return c.post(ctx, endpoint, "DatePriceModify", payload)
}

func (c *Client) SyncInventory(ctx context.Context, endpoint string, payload InventoryRequest) (*Response, error) {
	return c.post(ctx, endpoint, "DateInventoryModify", payload)
}

func (c *Client) post(ctx context.Context, endpoint, serviceName string, payload interface{}) (*Response, error) {
	if strings.TrimSpace(endpoint) == "" || c.AccountID == "" || c.SignKey == "" {
		return nil, errors.New("ctrip endpoint and credentials are required")
	}
	if len([]byte(c.AESKey)) != aes.BlockSize || len([]byte(c.AESIV)) != aes.BlockSize {
		return nil, errors.New("ctrip AES key and IV must each be 16 bytes")
	}
	plain, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal ctrip payload: %w", err)
	}
	encrypted, err := Encrypt(plain, c.AESKey, c.AESIV)
	if err != nil {
		return nil, err
	}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	requestTime := now().Format("2006-01-02 15:04:05")
	version := "1.0"
	signature := Signature(c.AccountID, serviceName, requestTime, encrypted, version, c.SignKey)
	body, err := json.Marshal(map[string]interface{}{
		"header": map[string]string{"accountId": c.AccountID, "serviceName": serviceName, "requestTime": requestTime, "version": version, "sign": signature},
		"body":   encrypted,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal ctrip envelope: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create ctrip request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json;charset=UTF-8")
	req.Header.Set("User-Agent", "ticket-system-ctrip/1.0")
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send ctrip request: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read ctrip response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("ctrip HTTP %d", resp.StatusCode)
	}
	result := &Response{Raw: string(raw)}
	var envelope struct {
		Header struct {
			ResultCode    string `json:"resultCode"`
			ResultMessage string `json:"resultMessage"`
		} `json:"header"`
		ResultCode    string `json:"resultCode"`
		ResultMessage string `json:"resultMessage"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("decode ctrip response: %w", err)
	}
	result.Code, result.Message = envelope.Header.ResultCode, envelope.Header.ResultMessage
	if result.Code == "" {
		result.Code, result.Message = envelope.ResultCode, envelope.ResultMessage
	}
	if result.Code == "" {
		return nil, errors.New("ctrip response did not contain a result code")
	}
	return result, nil
}

func Signature(accountID, serviceName, requestTime, body, version, signKey string) string {
	sum := md5.Sum([]byte(accountID + serviceName + requestTime + body + version + signKey))
	return hex.EncodeToString(sum[:])
}

func Encrypt(plain []byte, key, iv string) (string, error) {
	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		return "", fmt.Errorf("create ctrip AES cipher: %w", err)
	}
	if len([]byte(iv)) != aes.BlockSize {
		return "", errors.New("ctrip AES IV must be 16 bytes")
	}
	padding := aes.BlockSize - len(plain)%aes.BlockSize
	padded := append(append([]byte(nil), plain...), bytes.Repeat([]byte{byte(padding)}, padding)...)
	encrypted := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, []byte(iv)).CryptBlocks(encrypted, padded)
	encoded := make([]byte, len(encrypted)*2)
	for i, value := range encrypted {
		encoded[i*2] = 'a' + (value >> 4)
		encoded[i*2+1] = 'a' + (value & 0x0f)
	}
	return string(encoded), nil
}
