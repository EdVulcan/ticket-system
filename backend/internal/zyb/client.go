package zyb

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxResponseBytes = 4 << 20

var (
	ErrOrderNotFound  = errors.New("智游宝订单不存在")
	ErrRefundPending  = errors.New("智游宝退票审核中")
	ErrRefundRejected = errors.New("智游宝退票审核未通过")
)

type Config struct {
	Endpoint   string
	CorpCode   string
	Username   string
	PrivateKey string
	Timeout    time.Duration
	Now        func() time.Time
}

type Client struct {
	Config Config
	HTTP   *http.Client
}

// CreateSignature implements the documented MD5("xmlMsg=" + xmlMsg + privateKey).
func CreateSignature(xmlMsg, privateKey string) string {
	sum := md5.Sum([]byte("xmlMsg=" + xmlMsg + privateKey))
	return hex.EncodeToString(sum[:])
}

// BuildRequestEnvelope builds the exact request envelope used for signing.
func BuildRequestEnvelope(transaction, corpCode, username, body string, now time.Time) string {
	return buildEnvelope(transaction, corpCode, username, body, now)
}

type responseMeta struct {
	TransactionName string `xml:"transactionName"`
	Code            string `xml:"code"`
	Description     string `xml:"description"`
}

func (c Client) request(ctx context.Context, transaction, body string, out any, allowPending bool) ([]byte, responseMeta, error) {
	if strings.TrimSpace(c.Config.Endpoint) == "" || strings.TrimSpace(c.Config.CorpCode) == "" || strings.TrimSpace(c.Config.Username) == "" || strings.TrimSpace(c.Config.PrivateKey) == "" {
		return nil, responseMeta{}, errors.New("智游宝连接配置不完整")
	}
	if c.Config.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Config.Timeout)
		defer cancel()
	}
	now := time.Now().In(time.FixedZone("CST", 8*3600))
	if c.Config.Now != nil {
		now = c.Config.Now()
	}
	xmlMsg := buildEnvelope(transaction, c.Config.CorpCode, c.Config.Username, body, now)
	sign := CreateSignature(xmlMsg, c.Config.PrivateKey)
	form := url.Values{"xmlMsg": {xmlMsg}, "sign": {sign}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Config.Endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, responseMeta{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=UTF-8")
	hc := c.HTTP
	if hc == nil {
		timeout := c.Config.Timeout
		if timeout <= 0 {
			timeout = 15 * time.Second
		}
		hc = &http.Client{Timeout: timeout}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, responseMeta{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, responseMeta{}, err
	}
	if len(raw) > maxResponseBytes {
		return raw[:maxResponseBytes], responseMeta{}, errors.New("智游宝响应过大")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return raw, responseMeta{}, fmt.Errorf("智游宝 HTTP %d", resp.StatusCode)
	}
	// Keep XMLName out of the anonymously embedded responseMeta: decoding an
	// XMLName promoted through an unexported embedded field panics on Go 1.26.
	var header struct {
		XMLName         xml.Name
		TransactionName string `xml:"transactionName"`
		Code            string `xml:"code"`
		Description     string `xml:"description"`
	}
	if err := xml.Unmarshal(raw, &header); err != nil {
		return raw, responseMeta{}, fmt.Errorf("解析智游宝响应: %w", err)
	}
	meta := responseMeta{TransactionName: header.TransactionName, Code: header.Code, Description: header.Description}
	if out != nil {
		if err := xml.Unmarshal(raw, out); err != nil {
			return raw, meta, fmt.Errorf("解析智游宝响应数据: %w", err)
		}
	}
	if strings.TrimSpace(meta.Code) != "0" && !(allowPending && strings.TrimSpace(meta.Code) == "6") {
		queryResponse := strings.TrimSpace(meta.TransactionName)
		if transaction == "QUERY_ORDER_NEW_REQ" && header.XMLName.Local == "PWBResponse" && (queryResponse == "" || queryResponse == "QUERY_ORDER_NEW_RES") && (strings.Contains(meta.Description, "订单不存在") || strings.Contains(meta.Description, "订单号不存在")) {
			return raw, meta, fmt.Errorf("%w: %s", ErrOrderNotFound, strings.TrimSpace(meta.Description))
		}
		return raw, meta, fmt.Errorf("智游宝请求失败(code=%s): %s", strings.TrimSpace(meta.Code), strings.TrimSpace(meta.Description))
	}
	return raw, meta, nil
}

func buildEnvelope(transaction, corp, user, body string, now time.Time) string {
	return fmt.Sprintf("<PWBRequest>\n  <transactionName>%s</transactionName>\n  <header>\n    <application>SendCode</application>\n    <requestTime>%s</requestTime>\n  </header>\n  <identityInfo>\n    <corpCode>%s</corpCode>\n    <userName>%s</userName>\n  </identityInfo>\n%s\n</PWBRequest>", escapeXML(transaction), now.Format("2006-01-02 15:04:05"), escapeXML(corp), escapeXML(user), body)
}

func escapeXML(value string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(value))
	return b.String()
}
func text(value string) string { return strings.TrimSpace(value) }

type SendCodeRequest struct {
	ThirdPartyOrderCode string
	ChildOrderCode      string
	ContactName         string
	ContactMobile       string
	PayMethod           string
	GoodsCode           string
	GoodsName           string
	VisitDate           string
	Price               string
	PriceCents          int64
	TotalPrice          string
	TotalPriceCents     int64
	Quantity            int
	CertificateNo       string
	Source              string
	FSStartTime         string
	FSEndTime           string
	Remark              string
	Credentials         []Credential
}

type Credential struct {
	Name string
	ID   string
}

type SendCodeResult struct {
	TransactionName string
	Code            string
	Description     string
	Order           SendCodeOrder
}
type SendCodeOrder struct {
	ThirdPartyOrderCode string
	ProviderOrderCode   string
	AssistCheckNo       string
	ContactName         string
	ContactMobile       string
	OrderPrice          string
	PayMethod           string
	Source              string
	Tickets             []SendCodeTicket
}
type SendCodeTicket struct {
	ProviderSubOrderCode string
	GoodsCode            string
	GoodsName            string
	Quantity             string
	Price                string
	TotalPrice           string
	VisitDate            string
	ValidFrom            string
	ValidTo              string
	ScenicThirdCode      string
	ReturnedQuantity     string
	CheckedQuantity      string
	SeatInfo             string
}

func (c Client) SendCode(ctx context.Context, req SendCodeRequest) (*SendCodeResult, []byte, error) {
	if strings.TrimSpace(req.ThirdPartyOrderCode) == "" || strings.TrimSpace(req.ChildOrderCode) == "" || strings.TrimSpace(req.ContactName) == "" || strings.TrimSpace(req.ContactMobile) == "" || strings.TrimSpace(req.GoodsCode) == "" || strings.TrimSpace(req.GoodsName) == "" || strings.TrimSpace(req.VisitDate) == "" || req.Quantity <= 0 {
		return nil, nil, errors.New("智游宝出票请求字段不完整")
	}
	unit, err := amountCents(req.Price, req.PriceCents)
	if strings.TrimSpace(req.Price) == "" && req.PriceCents == 0 {
		return nil, nil, errors.New("智游宝出票金额不能为空")
	}
	if err != nil {
		return nil, nil, fmt.Errorf("智游宝出票金额无效: %w", err)
	}
	if unit > (int64(^uint64(0)>>1))/int64(req.Quantity) {
		return nil, nil, errors.New("智游宝出票金额超出范围")
	}
	total := unit * int64(req.Quantity)
	if req.TotalPrice != "" || req.TotalPriceCents != 0 {
		provided, e := amountCents(req.TotalPrice, req.TotalPriceCents)
		if e != nil || provided != total {
			return nil, nil, errors.New("智游宝出票总价与单价数量不一致")
		}
	}
	pay := req.PayMethod
	if pay == "" {
		pay = "vm"
	}
	credentials := ""
	if len(req.Credentials) > 0 {
		var b strings.Builder
		b.WriteString("<credentials>")
		for _, credential := range req.Credentials {
			if strings.TrimSpace(credential.Name) == "" || strings.TrimSpace(credential.ID) == "" {
				return nil, nil, errors.New("智游宝实名凭证字段不完整")
			}
			b.WriteString("<credential><name>")
			b.WriteString(escapeXML(credential.Name))
			b.WriteString("</name><id>")
			b.WriteString(escapeXML(credential.ID))
			b.WriteString("</id></credential>")
		}
		b.WriteString("</credentials>")
		credentials = b.String()
	}
	certificate, source, startTime, endTime, remark := escapeXML(req.CertificateNo), escapeXML(req.Source), escapeXML(req.FSStartTime), escapeXML(req.FSEndTime), escapeXML(req.Remark)
	body := fmt.Sprintf("  <orderRequest><order><certificateNo>%s</certificateNo><linkName>%s</linkName><linkMobile>%s</linkMobile><orderCode>%s</orderCode><orderPrice>%s</orderPrice><groupNo></groupNo><payMethod>%s</payMethod><src>%s</src><ticketOrders><ticketOrder><orderCode>%s</orderCode>%s<price>%s</price><quantity>%d</quantity><totalPrice>%s</totalPrice><occDate>%s</occDate><goodsCode>%s</goodsCode><goodsName>%s</goodsName><fsStartTime>%s</fsStartTime><fsEndTime>%s</fsEndTime><remark>%s</remark></ticketOrder></ticketOrders></order></orderRequest>", certificate, escapeXML(req.ContactName), escapeXML(req.ContactMobile), escapeXML(req.ThirdPartyOrderCode), formatCents(total), escapeXML(pay), source, escapeXML(req.ChildOrderCode), credentials, formatCents(unit), req.Quantity, formatCents(total), escapeXML(req.VisitDate), escapeXML(req.GoodsCode), escapeXML(req.GoodsName), startTime, endTime, remark)
	var env sendCodeEnvelope
	raw, meta, err := c.request(ctx, "SEND_CODE_REQ", body, &env, false)
	if err != nil {
		return nil, raw, err
	}
	if text(meta.TransactionName) != "SEND_CODE_RES" {
		return nil, raw, fmt.Errorf("智游宝出票响应交易类型不正确: %s", text(meta.TransactionName))
	}
	order := env.OrderResponse.Order
	if text(order.ProviderOrderCode) == "" || len(order.TicketOrders.Tickets) == 0 {
		return nil, raw, errors.New("智游宝出票响应缺少订单或票项")
	}
	result := &SendCodeResult{TransactionName: text(meta.TransactionName), Code: text(meta.Code), Description: text(meta.Description), Order: SendCodeOrder{ThirdPartyOrderCode: strings.TrimSpace(req.ThirdPartyOrderCode), ProviderOrderCode: text(order.ProviderOrderCode), AssistCheckNo: text(order.AssistCheckNo), ContactName: text(order.ContactName), ContactMobile: text(order.ContactMobile), OrderPrice: text(order.OrderPrice), PayMethod: text(order.PayMethod), Source: text(order.Source)}}
	for _, t := range order.TicketOrders.Tickets {
		if text(t.ProviderSubOrderCode) == "" || text(t.GoodsCode) == "" || text(t.Quantity) == "" {
			return nil, raw, errors.New("智游宝出票响应票项身份不完整")
		}
		result.Order.Tickets = append(result.Order.Tickets, SendCodeTicket{ProviderSubOrderCode: text(t.ProviderSubOrderCode), GoodsCode: text(t.GoodsCode), GoodsName: text(t.GoodsName), Quantity: text(t.Quantity), Price: text(t.Price), TotalPrice: text(t.TotalPrice), VisitDate: text(t.VisitDate), ValidFrom: text(t.ValidFrom), ValidTo: text(t.ValidTo), ScenicThirdCode: text(t.ScenicThirdCode), ReturnedQuantity: text(t.ReturnedQuantity), CheckedQuantity: text(t.CheckedQuantity), SeatInfo: text(t.SeatInfo)})
	}
	return result, raw, nil
}

func amountCents(value string, cents int64) (int64, error) {
	if strings.TrimSpace(value) == "" {
		if cents < 0 {
			return 0, errors.New("金额必须为非负数")
		}
		return cents, nil
	}
	p := strings.Split(strings.TrimSpace(value), ".")
	if len(p) > 2 || p[0] == "" {
		return 0, errors.New("金额格式无效")
	}
	if strings.HasPrefix(p[0], "-") || strings.HasPrefix(p[0], "+") {
		return 0, errors.New("金额必须为非负数")
	}
	if len(p) == 2 && (len(p[1]) > 2) {
		return 0, errors.New("金额最多两位小数")
	}
	for _, s := range p {
		for _, r := range s {
			if s != "" && (r < '0' || r > '9') {
				return 0, errors.New("金额格式无效")
			}
		}
	}
	frac := ""
	if len(p) == 2 {
		frac = p[1]
	}
	for len(frac) < 2 {
		frac += "0"
	}
	whole, e := strconv.ParseInt(p[0], 10, 64)
	if e != nil || whole > (int64(^uint64(0)>>1)-0)/100 {
		return 0, errors.New("金额超出范围")
	}
	f, _ := strconv.ParseInt(frac, 10, 64)
	out := whole*100 + f
	if out < 0 {
		return 0, errors.New("金额超出范围")
	}
	return out, nil
}
func formatCents(c int64) string { return fmt.Sprintf("%d.%02d", c/100, c%100) }

type sendCodeEnvelope struct {
	responseMeta
	OrderResponse struct {
		Order responseOrder `xml:"order"`
	} `xml:"orderResponse"`
}

type responseOrder struct {
	ProviderOrderCode string `xml:"orderCode"`
	AssistCheckNo     string `xml:"assistCheckNo"`
	ContactName       string `xml:"linkName"`
	ContactMobile     string `xml:"linkMobile"`
	OrderPrice        string `xml:"orderPrice"`
	PayMethod         string `xml:"payMethod"`
	Source            string `xml:"src"`
	Quantity          string `xml:"quantity"`
	TicketOrders      struct {
		Tickets []responseTicket `xml:"ticketOrder"`
	} `xml:"ticketOrders"`
}
type responseTicket struct {
	ProviderSubOrderCode string `xml:"orderCode"`
	GoodsCode            string `xml:"goodsCode"`
	GoodsName            string `xml:"goodsName"`
	Quantity             string `xml:"quantity"`
	Price                string `xml:"price"`
	TotalPrice           string `xml:"totalPrice"`
	VisitDate            string `xml:"occDate"`
	ValidFrom            string `xml:"startDate"`
	ValidTo              string `xml:"endDate"`
	ScenicThirdCode      string `xml:"scenicThirdCode"`
	ReturnedQuantity     string `xml:"returnNum"`
	CheckedQuantity      string `xml:"alreadyCheckNum"`
	SeatInfo             string `xml:"seatInfo"`
}

type QueryOrderResult struct {
	TransactionName   string
	Code              string
	Description       string
	ProviderOrderCode string
	AssistCheckNo     string
	ContactName       string
	ContactMobile     string
	OrderPrice        string
	PayMethod         string
	Source            string
	Tickets           []QueryOrderTicket
}
type QueryOrderTicket struct {
	ProviderSubOrderCode string
	GoodsCode            string
	GoodsName            string
	Quantity             string
	ReturnedQuantity     string
	CheckedQuantity      string
	VisitDate            string
	Price                string
	TotalPrice           string
	ValidFrom            string
	ValidTo              string
	ScenicThirdCode      string
	SeatInfo             string
}

func (c Client) QueryOrder(ctx context.Context, orderCode string) (*QueryOrderResult, []byte, error) {
	orderCode = strings.TrimSpace(orderCode)
	if orderCode == "" {
		return nil, nil, errors.New("智游宝订单号不能为空")
	}
	body := fmt.Sprintf("  <orderRequest><order><orderCode>%s</orderCode></order></orderRequest>", escapeXML(orderCode))
	var env struct {
		responseMeta
		Order         responseOrder `xml:"order"`
		OrderResponse struct {
			Order responseOrder `xml:"order"`
		} `xml:"orderResponse"`
	}
	raw, meta, err := c.request(ctx, "QUERY_ORDER_NEW_REQ", body, &env, false)
	if err != nil {
		return nil, raw, err
	}
	if text(meta.TransactionName) != "QUERY_ORDER_NEW_RES" {
		return nil, raw, fmt.Errorf("智游宝订单查询响应交易类型不正确: %s", text(meta.TransactionName))
	}
	o := env.Order
	if text(o.ProviderOrderCode) == "" {
		o = env.OrderResponse.Order
	}
	if text(o.ProviderOrderCode) == "" {
		return nil, raw, errors.New("智游宝订单查询缺少订单号")
	}
	r := &QueryOrderResult{TransactionName: text(meta.TransactionName), Code: text(meta.Code), Description: text(meta.Description), ProviderOrderCode: text(o.ProviderOrderCode), AssistCheckNo: text(o.AssistCheckNo), ContactName: text(o.ContactName), ContactMobile: text(o.ContactMobile), OrderPrice: text(o.OrderPrice), PayMethod: text(o.PayMethod), Source: text(o.Source)}
	for _, t := range o.TicketOrders.Tickets {
		if (text(t.ProviderSubOrderCode) == "" && text(t.ScenicThirdCode) == "") || text(t.GoodsCode) == "" || text(t.Quantity) == "" {
			return nil, raw, errors.New("智游宝订单查询票项身份不完整")
		}
		r.Tickets = append(r.Tickets, QueryOrderTicket{ProviderSubOrderCode: text(t.ProviderSubOrderCode), GoodsCode: text(t.GoodsCode), GoodsName: text(t.GoodsName), Quantity: text(t.Quantity), ReturnedQuantity: text(t.ReturnedQuantity), CheckedQuantity: text(t.CheckedQuantity), VisitDate: text(t.VisitDate), Price: text(t.Price), TotalPrice: text(t.TotalPrice), ValidFrom: text(t.ValidFrom), ValidTo: text(t.ValidTo), ScenicThirdCode: text(t.ScenicThirdCode), SeatInfo: text(t.SeatInfo)})
	}
	if len(r.Tickets) == 0 {
		return nil, raw, errors.New("智游宝订单查询缺少票项")
	}
	return r, raw, nil
}

func ParseAmountCents(value string) (int64, error) {
	if strings.TrimSpace(value) == "" {
		return 0, errors.New("金额缺失")
	}
	return amountCents(value, 0)
}

type Artifact struct {
	Kind     string
	Value    string
	MimeType string
}

func (c Client) TicketImage(ctx context.Context, orderCode string) (*Artifact, []byte, error) {
	artifacts, raw, err := c.TicketImages(ctx, orderCode)
	if err != nil {
		return nil, raw, err
	}
	if len(artifacts) != 1 {
		return nil, raw, errors.New("智游宝返回多张票码图片，请按多码模式处理")
	}
	return artifacts[0], raw, nil
}

// TicketImages accepts the existing image fields, including repeated elements.
// It does not follow QR-page URLs or infer codes from order/assist numbers.
func (c Client) TicketImages(ctx context.Context, orderCode string) ([]*Artifact, []byte, error) {
	orderCode = strings.TrimSpace(orderCode)
	if orderCode == "" {
		return nil, nil, errors.New("智游宝订单号不能为空")
	}
	body := fmt.Sprintf("  <orderRequest><order><orderCode>%s</orderCode></order></orderRequest>", escapeXML(orderCode))
	var env struct {
		responseMeta
		Img   []string `xml:"img"`
		Image []string `xml:"image"`
		URL   []string `xml:"url"`
	}
	raw, meta, err := c.request(ctx, "SEND_CODE_IMG_REQ", body, &env, false)
	if err != nil {
		return nil, raw, err
	}
	if text(meta.TransactionName) != "SEND_CODE_IMG_RES" {
		return nil, raw, fmt.Errorf("智游宝票码响应交易类型不正确: %s", text(meta.TransactionName))
	}
	values := env.Img
	if len(values) == 0 || (len(values) == 1 && text(values[0]) == "") {
		values = env.Image
	}
	if len(values) == 0 || (len(values) == 1 && text(values[0]) == "") {
		values = env.URL
	}
	if len(values) == 0 {
		return nil, raw, errors.New("智游宝票码响应为空")
	}
	artifacts := make([]*Artifact, 0, len(values))
	for _, value := range values {
		a, err := ParseArtifact(value)
		if err != nil {
			return nil, raw, err
		}
		artifacts = append(artifacts, a)
	}
	return artifacts, raw, nil
}

type CancelOrderResult struct {
	TransactionName string
	Code            string
	Description     string
	RetreatBatchNo  string
}

func (c Client) CancelOrder(ctx context.Context, orderCode string) (*CancelOrderResult, []byte, error) {
	return c.cancel(ctx, orderCode, "SEND_CODE_CANCEL_NEW_REQ", "SEND_CODE_CANCEL_RES")
}
func (c Client) cancel(ctx context.Context, orderCode, reqName, resName string) (*CancelOrderResult, []byte, error) {
	orderCode = strings.TrimSpace(orderCode)
	if orderCode == "" {
		return nil, nil, errors.New("智游宝订单号不能为空")
	}
	body := fmt.Sprintf("  <orderRequest><order><orderCode>%s</orderCode></order></orderRequest>", escapeXML(orderCode))
	var env struct {
		responseMeta
		RetreatBatchNo string `xml:"retreatBatchNo"`
	}
	raw, meta, err := c.request(ctx, reqName, body, &env, false)
	if err != nil {
		return nil, raw, err
	}
	if text(meta.TransactionName) != resName {
		return nil, raw, fmt.Errorf("智游宝取消响应交易类型不正确: %s", text(meta.TransactionName))
	}
	return &CancelOrderResult{TransactionName: text(meta.TransactionName), Code: text(meta.Code), Description: text(meta.Description), RetreatBatchNo: text(env.RetreatBatchNo)}, raw, nil
}

type CheckStatusSubOrder struct {
	OrderCode       string
	NeedCheckNum    string
	AlreadyCheckNum string
	ReturnNum       string
	CheckStatus     string
	OrderType       string
	LastCheckTime   string
}
type CheckStatusResult struct {
	TransactionName string
	Code            string
	Description     string
	SubOrders       []CheckStatusSubOrder
}
type CheckRecord struct {
	CheckNum  string
	CheckTime string
}
type CheckRecordSubOrder struct {
	CheckStatusSubOrder
	CheckRecords []CheckRecord
}
type CheckRecordsResult struct {
	TransactionName string
	Code            string
	Description     string
	SubOrders       []CheckRecordSubOrder
}

func (c Client) QueryCheckStatus(ctx context.Context, orderCode string, _ ...string) (*CheckStatusResult, error) {
	body := orderRequestCode(orderCode)
	var env struct {
		responseMeta
		SubOrders []subOrderXML `xml:"subOrders>subOrder"`
		Data      struct {
			SubOrders []subOrderXML `xml:"subOrders>subOrder"`
		} `xml:"data"`
	}
	_, meta, err := c.request(ctx, "CHECK_STATUS_QUERY_REQ", body, &env, false)
	if err != nil {
		return nil, err
	}
	if text(meta.TransactionName) != "CHECK_STATUS_QUERY_RES" {
		return nil, fmt.Errorf("智游宝核销状态响应交易类型不正确: %s", text(meta.TransactionName))
	}
	ss := env.SubOrders
	if len(ss) == 0 {
		ss = env.Data.SubOrders
	}
	if len(ss) == 0 {
		return nil, errors.New("智游宝核销查询缺少票项")
	}
	r := &CheckStatusResult{TransactionName: text(meta.TransactionName), Code: text(meta.Code), Description: text(meta.Description)}
	for _, s := range ss {
		r.SubOrders = append(r.SubOrders, CheckStatusSubOrder{OrderCode: text(s.OrderCode), NeedCheckNum: text(s.NeedCheckNum), AlreadyCheckNum: text(s.AlreadyCheckNum), ReturnNum: text(s.ReturnNum), CheckStatus: text(s.CheckStatus), OrderType: text(s.OrderType), LastCheckTime: text(s.LastCheckTime)})
	}
	return r, nil
}
func (c Client) QueryCheckRecords(ctx context.Context, orderCode string, _ ...string) (*CheckRecordsResult, error) {
	body := orderRequestCode(orderCode)
	var env struct {
		responseMeta
		SubOrders []subOrderXML `xml:"subOrders>subOrder"`
		Data      struct {
			SubOrders []subOrderXML `xml:"subOrders>subOrder"`
		} `xml:"data"`
	}
	_, meta, err := c.request(ctx, "QUERY_SUB_ORDER_CHECK_RECORD_REQ", body, &env, false)
	if err != nil {
		return nil, err
	}
	if text(meta.TransactionName) != "QUERY_SUB_ORDER_CHECK_RECORD_RES" {
		return nil, fmt.Errorf("智游宝核销明细响应交易类型不正确: %s", text(meta.TransactionName))
	}
	ss := env.SubOrders
	if len(ss) == 0 {
		ss = env.Data.SubOrders
	}
	if len(ss) == 0 {
		return nil, errors.New("智游宝核销明细缺少票项")
	}
	r := &CheckRecordsResult{TransactionName: text(meta.TransactionName), Code: text(meta.Code), Description: text(meta.Description)}
	for _, s := range ss {
		x := CheckRecordSubOrder{CheckStatusSubOrder: CheckStatusSubOrder{OrderCode: text(s.OrderCode), NeedCheckNum: text(s.NeedCheckNum), AlreadyCheckNum: text(s.AlreadyCheckNum), ReturnNum: text(s.ReturnNum), CheckStatus: text(s.CheckStatus), OrderType: text(s.OrderType), LastCheckTime: text(s.LastCheckTime)}}
		for _, cr := range s.CheckRecords.Records {
			x.CheckRecords = append(x.CheckRecords, CheckRecord{CheckNum: text(cr.CheckNum), CheckTime: text(cr.CheckTime)})
		}
		r.SubOrders = append(r.SubOrders, x)
	}
	return r, nil
}
func orderRequestCode(code string) string {
	return fmt.Sprintf("  <orderRequest><order><orderCode>%s</orderCode></order></orderRequest>", escapeXML(strings.TrimSpace(code)))
}

type subOrderXML struct {
	OrderCode       string `xml:"orderCode"`
	NeedCheckNum    string `xml:"needCheckNum"`
	AlreadyCheckNum string `xml:"alreadyCheckNum"`
	ReturnNum       string `xml:"returnNum"`
	CheckStatus     string `xml:"checkStatus"`
	OrderType       string `xml:"orderType"`
	LastCheckTime   string `xml:"lastCheckTime"`
	CheckRecords    struct {
		Records []checkRecordXML `xml:"subOrderCheckRecord"`
	} `xml:"checkRecords"`
}
type checkRecordXML struct {
	CheckNum  string `xml:"checkNum"`
	CheckTime string `xml:"checkTime"`
}

type RefundState string

const (
	RefundCompleted RefundState = "completed"
	RefundPending   RefundState = "pending"
	RefundRejected  RefundState = "rejected"
	RefundError     RefundState = "error"
)

type RefundResult struct {
	TransactionName string
	Code            string
	Description     string
	RetreatBatchNo  string
	State           RefundState
	Pending         bool
	Completed       bool
	Rejected        bool
}

func (c Client) QueryRefund(ctx context.Context, batchNo string) (*RefundResult, []byte, error) {
	batchNo = strings.TrimSpace(batchNo)
	if batchNo == "" {
		return nil, nil, errors.New("智游宝退票批次号不能为空")
	}
	body := fmt.Sprintf("  <orderRequest><order><retreatBatchNo>%s</retreatBatchNo></order></orderRequest>", escapeXML(batchNo))
	var env struct {
		responseMeta
		RetreatBatchNo string `xml:"retreatBatchNo"`
	}
	raw, meta, err := c.request(ctx, "QUERY_RETREAT_STATUS_REQ", body, &env, true)
	if err != nil {
		if strings.Contains(meta.Description, "未通过") || strings.Contains(strings.ToLower(meta.Description), "failure") {
			return &RefundResult{TransactionName: text(meta.TransactionName), Code: text(meta.Code), Description: text(meta.Description), RetreatBatchNo: batchNo, State: RefundRejected, Rejected: true}, raw, nil
		}
		return &RefundResult{TransactionName: text(meta.TransactionName), Code: text(meta.Code), Description: text(meta.Description), RetreatBatchNo: batchNo, State: RefundError}, raw, err
	}
	if text(meta.TransactionName) != "QUERY_RETREAT_STATUS_RES" {
		return nil, raw, fmt.Errorf("智游宝退票查询响应交易类型不正确: %s", text(meta.TransactionName))
	}
	r := &RefundResult{TransactionName: text(meta.TransactionName), Code: text(meta.Code), Description: text(meta.Description), RetreatBatchNo: batchNo, State: RefundCompleted, Completed: text(meta.Code) == "0"}
	if text(meta.Code) == "6" {
		r.State = RefundPending
		r.Pending = true
		return r, raw, nil
	}
	if strings.Contains(meta.Description, "未通过") || strings.Contains(strings.ToLower(meta.Description), "failure") {
		r.State = RefundRejected
		r.Rejected = true
		return r, raw, nil
	}
	return r, raw, nil
}
