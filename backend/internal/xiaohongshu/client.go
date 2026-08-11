package xiaohongshu

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	DefaultBaseURL = "https://miniapp.xiaohongshu.com"
	SandboxBaseURL = "https://miniapp-sandbox.xiaohongshu.com"
)

const (
	ProductTypeGroupVoucher   = 1
	ProductTypePresaleVoucher = 2
	ProductTypeCalendar       = 3

	SettleAtHeadOffice = 1
	SettleAtPOI        = 2
	SettleByRegion     = 3
)

type Client struct {
	AppID   string
	Secret  string
	BaseURL string
	HTTP    *http.Client
	Now     func() time.Time

	mu             sync.Mutex
	accessToken    string
	tokenExpiresAt time.Time
}

func NewClient(appID, secret, environment string) *Client {
	return &Client{
		AppID:   strings.TrimSpace(appID),
		Secret:  strings.TrimSpace(secret),
		BaseURL: BaseURLForEnvironment(environment),
	}
}

func BaseURLForEnvironment(environment string) string {
	if strings.EqualFold(strings.TrimSpace(environment), "sandbox") {
		return SandboxBaseURL
	}
	return DefaultBaseURL
}

type APIError struct {
	Code    int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("xiaohongshu API rejected request (%d): %s", e.Code, e.Message)
}

type envelope[T any] struct {
	Data    T      `json:"data"`
	Success bool   `json:"success"`
	Message string `json:"msg"`
	Code    int    `json:"code"`
}

type tokenData struct {
	AccessToken string `json:"access_token"`
	ExpireIn    int    `json:"expire_in"`
}

type Session struct {
	OpenID     string `json:"openid"`
	SessionKey string `json:"session_key"`
}

type Category struct {
	ID                string          `json:"category_id"`
	Name              string          `json:"name"`
	RequireClaimStore bool            `json:"require_claim_store"`
	SupportTrade      bool            `json:"support_trade"`
	TradeAbility      string          `json:"trade_ability,omitempty"`
	Path              json.RawMessage `json:"path,omitempty"`
}

type CategoryListResponse struct {
	Categories []Category `json:"category_info"`
}

type POI struct {
	ID         string  `json:"poi_id"`
	Name       string  `json:"name"`
	Address    string  `json:"address"`
	Longitude  float64 `json:"longitude"`
	Latitude   float64 `json:"latitude"`
	SourceID   string  `json:"source_id,omitempty"`
	SourceType int     `json:"source_type,omitempty"`
	SourceName string  `json:"source_name,omitempty"`
}

type POIListResponse struct {
	List  []POI `json:"list"`
	Total int   `json:"total"`
}

type PackageEntry struct {
	Name  string `json:"name"`
	Count int    `json:"count,omitempty"`
	Unit  string `json:"unit,omitempty"`
}

type PackageDetail struct {
	Rooms          []PackageEntry `json:"package_rooms,omitempty"`
	Foods          []PackageEntry `json:"package_foods,omitempty"`
	Entertainments []PackageEntry `json:"package_entertainments,omitempty"`
	Others         []PackageEntry `json:"package_others,omitempty"`
}

type ProductSKU struct {
	ExternalSKUID string `json:"out_sku_id"`
	Name          string `json:"name"`
	Image         string `json:"sku_image"`
	OriginPrice   int64  `json:"origin_price"`
	SalePrice     int64  `json:"sale_price"`
	Status        int    `json:"status"`
}

type LocalLifeProductRequest struct {
	ExternalProductID string         `json:"out_product_id"`
	Name              string         `json:"name"`
	ShortTitle        string         `json:"short_title"`
	Description       string         `json:"desc"`
	Path              string         `json:"path"`
	TopImage          string         `json:"top_image"`
	CategoryID        string         `json:"category_id"`
	CreatedAt         int64          `json:"biz_create_time"`
	UpdatedAt         int64          `json:"biz_update_time"`
	POIIDs            []string       `json:"poi_id_list,omitempty"`
	Package           *PackageDetail `json:"package_detail,omitempty"`
	SKUs              []ProductSKU   `json:"skus"`
	Ext               map[string]any `json:"ext,omitempty"`
	ProductType       int            `json:"product_type,omitempty"`
	SettleType        int            `json:"settle_type,omitempty"`
}

type Discount struct {
	Name  string `json:"name"`
	Price int64  `json:"price"`
	Count int    `json:"num"`
}

type OrderProduct struct {
	ExternalProductID string     `json:"out_product_id"`
	ExternalSKUID     string     `json:"out_sku_id"`
	Count             int        `json:"num"`
	SalePrice         int64      `json:"sale_price"`
	RealPrice         int64      `json:"real_price"`
	Image             string     `json:"image,omitempty"`
	Discounts         []Discount `json:"discount_infos,omitempty"`
}

type ExtraPrice struct {
	Name  string `json:"name"`
	Price int64  `json:"price"`
	Count int    `json:"num"`
}

type OrderPrice struct {
	OrderPrice  int64        `json:"order_price"`
	Freight     int64        `json:"freight_price,omitempty"`
	ExtraPrices []ExtraPrice `json:"extra_price_infos,omitempty"`
}

type OrderUpsertRequest struct {
	ExternalOrderID string         `json:"out_order_id"`
	OpenID          string         `json:"open_id"`
	Path            string         `json:"path"`
	CreatedAt       int64          `json:"biz_create_time"`
	UpdatedAt       int64          `json:"biz_update_time,omitempty"`
	ExpiresAt       int64          `json:"order_expired_time,omitempty"`
	Products        []OrderProduct `json:"product_infos"`
	Price           OrderPrice     `json:"price_info"`
}

type OrderUpsertResponse struct {
	ExternalOrderID string `json:"out_order_id"`
	OrderID         string `json:"order_id"`
	FinalPrice      int64  `json:"final_price"`
	PayToken        string `json:"pay_token"`
	ExpiresAt       int64  `json:"expired_time"`
	OpenPayType     string `json:"open_pay_type"`
}

type GuaranteeOrderRequest struct {
	ExternalOrderID string `json:"out_order_id"`
	OpenID          string `json:"open_id"`
	OrderType       int    `json:"order_type"`
	ExtInfo         string `json:"ext_info,omitempty"`
}

type VoucherInfo struct {
	Code      string `json:"voucher_code"`
	Status    int    `json:"voucher_status"`
	PayAmount int64  `json:"pay_amount,omitempty"`
}

type BookDetail struct {
	BookID  string `json:"book_id"`
	Voucher string `json:"voucher_code,omitempty"`
	Status  int    `json:"book_status"`
}

type GuaranteeOrderResponse struct {
	OrderID     string         `json:"order_id"`
	PayAmount   int64          `json:"pay_amount"`
	OrderStatus int            `json:"order_status"`
	Vouchers    []VoucherInfo  `json:"voucher_infos,omitempty"`
	Bookings    []BookDetail   `json:"book_details,omitempty"`
	Products    []OrderProduct `json:"product_infos,omitempty"`
	TradeNo     string         `json:"third_trade_no,omitempty"`
	PayChannel  int            `json:"pay_channel,omitempty"`
}

type VoucherCode struct {
	Code string `json:"voucher_code"`
}

type VoucherVerifyRequest struct {
	ExternalOrderID string        `json:"out_order_id"`
	POIID           string        `json:"poi_id,omitempty"`
	Vouchers        []VoucherCode `json:"voucher_infos"`
}

type VoucherVerifyResponse struct {
	VerifyID string `json:"verify_id"`
}

func (c *Client) Code2Session(ctx context.Context, code string) (*Session, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, errors.New("xiaohongshu temporary login code is required")
	}
	token, err := c.getAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	endpoint, err := url.Parse(c.baseURL() + "/api/rmp/session")
	if err != nil {
		return nil, fmt.Errorf("create xiaohongshu session endpoint: %w", err)
	}
	query := endpoint.Query()
	query.Set("app_id", strings.TrimSpace(c.AppID))
	query.Set("access_token", token)
	query.Set("code", code)
	endpoint.RawQuery = query.Encode()
	var session Session
	if err := c.get(ctx, endpoint.String(), &session); err != nil {
		return nil, err
	}
	if strings.TrimSpace(session.OpenID) == "" || strings.TrimSpace(session.SessionKey) == "" {
		return nil, errors.New("xiaohongshu session response is incomplete")
	}
	return &session, nil
}

func (c *Client) ListCategories(ctx context.Context) ([]Category, error) {
	var response CategoryListResponse
	if err := c.authenticatedGet(ctx, "/api/rmp/apps/category", nil, &response); err != nil {
		return nil, err
	}
	return response.Categories, nil
}

func (c *Client) ListPOIs(ctx context.Context, page, pageSize int) (*POIListResponse, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}
	query := url.Values{}
	query.Set("page_no", fmt.Sprintf("%d", page))
	query.Set("page_size", fmt.Sprintf("%d", pageSize))
	var response POIListResponse
	if err := c.authenticatedGet(ctx, "/api/rmp/mp/deal/poi/list", query, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) UpsertLocalLifeProduct(ctx context.Context, request LocalLifeProductRequest) error {
	if err := validateProduct(request); err != nil {
		return err
	}
	var response struct{}
	return c.authenticatedPost(ctx, "/api/rmp/mp/deal/poi/product/upsert", request, &response)
}

func (c *Client) UpsertOrder(ctx context.Context, request OrderUpsertRequest) (*OrderUpsertResponse, error) {
	if err := validateOrder(request); err != nil {
		return nil, err
	}
	var response OrderUpsertResponse
	if err := c.authenticatedPost(ctx, "/api/rmp/mp/deal/order/upsert", request, &response); err != nil {
		return nil, err
	}
	if response.OrderID == "" || response.PayToken == "" {
		return nil, errors.New("xiaohongshu order response is missing order_id or pay_token")
	}
	return &response, nil
}

func (c *Client) GetGuaranteeOrder(ctx context.Context, request GuaranteeOrderRequest) (*GuaranteeOrderResponse, error) {
	if strings.TrimSpace(request.ExternalOrderID) == "" || strings.TrimSpace(request.OpenID) == "" || (request.OrderType != 1 && request.OrderType != 2) {
		return nil, errors.New("xiaohongshu external order, open_id and valid order type are required")
	}
	var response GuaranteeOrderResponse
	if err := c.authenticatedPost(ctx, "/api/rmp/mp/deal/gpay_order/get", request, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) VerifyVouchers(ctx context.Context, request VoucherVerifyRequest) (*VoucherVerifyResponse, error) {
	if strings.TrimSpace(request.ExternalOrderID) == "" || len(request.Vouchers) == 0 || len(request.Vouchers) > 10 {
		return nil, errors.New("xiaohongshu external order and 1 to 10 vouchers are required")
	}
	for _, voucher := range request.Vouchers {
		if strings.TrimSpace(voucher.Code) == "" {
			return nil, errors.New("xiaohongshu voucher code is required")
		}
	}
	var response VoucherVerifyResponse
	if err := c.authenticatedPost(ctx, "/api/rmp/mp/deal/voucher/verify", request, &response); err != nil {
		return nil, err
	}
	if response.VerifyID == "" {
		return nil, errors.New("xiaohongshu voucher response is missing verify_id")
	}
	return &response, nil
}

func validateProduct(request LocalLifeProductRequest) error {
	if strings.TrimSpace(request.ExternalProductID) == "" || strings.TrimSpace(request.Name) == "" || strings.TrimSpace(request.ShortTitle) == "" ||
		strings.TrimSpace(request.Description) == "" || !strings.HasPrefix(request.Path, "/") || strings.TrimSpace(request.TopImage) == "" || strings.TrimSpace(request.CategoryID) == "" {
		return errors.New("xiaohongshu product identifiers, text, image, category and miniapp path are required")
	}
	if request.CreatedAt <= 0 || request.UpdatedAt <= 0 || len(request.SKUs) == 0 {
		return errors.New("xiaohongshu product timestamps and at least one SKU are required")
	}
	if request.ProductType < ProductTypeGroupVoucher || request.ProductType > ProductTypeCalendar {
		return errors.New("xiaohongshu product type must be group voucher, presale voucher or calendar")
	}
	if request.SettleType < SettleAtHeadOffice || request.SettleType > SettleByRegion {
		return errors.New("xiaohongshu local-life guarantee payment requires a settlement type")
	}
	for _, sku := range request.SKUs {
		if strings.TrimSpace(sku.ExternalSKUID) == "" || strings.TrimSpace(sku.Name) == "" || strings.TrimSpace(sku.Image) == "" || sku.OriginPrice <= 0 || sku.SalePrice <= 0 {
			return errors.New("xiaohongshu SKU identifier, name, image and positive prices are required")
		}
	}
	return nil
}

func validateOrder(request OrderUpsertRequest) error {
	if strings.TrimSpace(request.ExternalOrderID) == "" || strings.TrimSpace(request.OpenID) == "" || !strings.HasPrefix(request.Path, "/") || request.CreatedAt <= 0 || len(request.Products) == 0 {
		return errors.New("xiaohongshu external order, open_id, miniapp path, timestamp and products are required")
	}
	var total int64
	for _, product := range request.Products {
		if strings.TrimSpace(product.ExternalProductID) == "" || strings.TrimSpace(product.ExternalSKUID) == "" || product.Count <= 0 || product.SalePrice < 0 || product.RealPrice < 0 {
			return errors.New("xiaohongshu order product identifiers, quantity and prices are invalid")
		}
		total += product.RealPrice
	}
	total += request.Price.Freight
	for _, extra := range request.Price.ExtraPrices {
		if strings.TrimSpace(extra.Name) == "" || extra.Count <= 0 || extra.Price < 0 {
			return errors.New("xiaohongshu order extra price is invalid")
		}
		total += extra.Price
	}
	if total != request.Price.OrderPrice {
		return fmt.Errorf("xiaohongshu order price mismatch: calculated %d, declared %d", total, request.Price.OrderPrice)
	}
	return nil
}

func (c *Client) authenticatedPost(ctx context.Context, path string, payload, response any) error {
	token, err := c.getAccessToken(ctx)
	if err != nil {
		return err
	}
	endpoint, err := url.Parse(c.baseURL() + path)
	if err != nil {
		return fmt.Errorf("create xiaohongshu endpoint: %w", err)
	}
	query := endpoint.Query()
	query.Set("app_id", strings.TrimSpace(c.AppID))
	query.Set("access_token", token)
	endpoint.RawQuery = query.Encode()
	return c.post(ctx, endpoint.String(), payload, response)
}

func (c *Client) authenticatedGet(ctx context.Context, path string, params url.Values, response any) error {
	token, err := c.getAccessToken(ctx)
	if err != nil {
		return err
	}
	endpoint, err := url.Parse(c.baseURL() + path)
	if err != nil {
		return fmt.Errorf("create xiaohongshu endpoint: %w", err)
	}
	query := endpoint.Query()
	for key, values := range params {
		for _, value := range values {
			query.Add(key, value)
		}
	}
	query.Set("app_id", strings.TrimSpace(c.AppID))
	query.Set("access_token", token)
	endpoint.RawQuery = query.Encode()
	return c.get(ctx, endpoint.String(), response)
}

func (c *Client) getAccessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	if c.accessToken != "" && now.Add(time.Minute).Before(c.tokenExpiresAt) {
		return c.accessToken, nil
	}
	if strings.TrimSpace(c.AppID) == "" || strings.TrimSpace(c.Secret) == "" {
		return "", errors.New("xiaohongshu miniapp appid and secret are required")
	}
	var data tokenData
	if err := c.post(ctx, c.baseURL()+"/api/rmp/token", map[string]string{"appid": strings.TrimSpace(c.AppID), "secret": strings.TrimSpace(c.Secret)}, &data); err != nil {
		return "", err
	}
	if data.AccessToken == "" || data.ExpireIn <= 0 {
		return "", errors.New("xiaohongshu token response is incomplete")
	}
	c.accessToken = data.AccessToken
	c.tokenExpiresAt = now.Add(time.Duration(data.ExpireIn) * time.Second)
	return c.accessToken, nil
}

func (c *Client) post(ctx context.Context, endpoint string, payload, response any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal xiaohongshu request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create xiaohongshu request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json;charset=UTF-8")
	req.Header.Set("User-Agent", "ticket-system-xiaohongshu/1.0")
	return c.do(req, response)
}

func (c *Client) get(ctx context.Context, endpoint string, response any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("create xiaohongshu request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "ticket-system-xiaohongshu/1.0")
	return c.do(req, response)
}

func (c *Client) do(req *http.Request, response any) error {
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send xiaohongshu request: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return fmt.Errorf("read xiaohongshu response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("xiaohongshu HTTP %d", resp.StatusCode)
	}
	result := envelope[json.RawMessage]{}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("decode xiaohongshu response: %w", err)
	}
	if !result.Success || result.Code != 0 {
		return &APIError{Code: result.Code, Message: strings.TrimSpace(result.Message)}
	}
	if response == nil || len(result.Data) == 0 || string(result.Data) == "null" {
		return nil
	}
	if err := json.Unmarshal(result.Data, response); err != nil {
		return fmt.Errorf("decode xiaohongshu response data: %w", err)
	}
	return nil
}

func (c *Client) baseURL() string {
	if value := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/"); value != "" {
		return value
	}
	return DefaultBaseURL
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}
