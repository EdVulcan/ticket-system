package zyb

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const (
	pageGetTransaction    = "QUERY_IMG_URL_PAGE_GET"
	pageSearchTransaction = "QUERY_IMG_URL_PAGE_SEARCH"
	pageImageTransaction  = "QUERY_IMG_URL_IMAGE_GET"
	maxTicketPageURL      = 8 << 10
	maxTicketPageField    = 4 << 10
	maxTicketPageRows     = 100
)

// ResolveTicketArtifacts turns provider QR-page links into downloaded image
// artifacts. Direct image artifacts are returned unchanged. All requests in
// one call use the same cookie jar because the QR page and its AJAX endpoint
// are a single provider session.
func (c Client) ResolveTicketArtifacts(ctx context.Context, artifacts []*Artifact) ([]*Artifact, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(artifacts) == 0 {
		return nil, errors.New("智游宝票码响应为空")
	}
	hc, err := c.pageHTTPClient()
	if err != nil {
		return nil, err
	}
	resolved := make([]*Artifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		if artifact == nil {
			return nil, errors.New("智游宝票码响应包含空凭证")
		}
		switch artifact.Kind {
		case "image":
			resolved = append(resolved, artifact)
		case "url":
			values, err := c.resolveTicketURL(ctx, hc, artifact.Value)
			if err != nil {
				return nil, err
			}
			resolved = append(resolved, values...)
		default:
			return nil, fmt.Errorf("智游宝票码凭证类型不支持: %s", artifact.Kind)
		}
	}
	if len(resolved) == 0 {
		return nil, errors.New("智游宝未返回二维码图片")
	}
	return resolved, nil
}

func (c Client) pageHTTPClient() (*http.Client, error) {
	var hc http.Client
	if c.HTTP != nil {
		hc = *c.HTTP
	}
	if hc.Jar == nil {
		jar, err := cookiejar.New(nil)
		if err != nil {
			return nil, fmt.Errorf("创建智游宝会话失败: %w", err)
		}
		hc.Jar = jar
	}
	// Do not follow redirects. A redirect is a new provider request and must
	// never silently cross the configured upstream host.
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if hc.Timeout <= 0 && c.Config.Timeout > 0 {
		hc.Timeout = c.Config.Timeout
	}
	return &hc, nil
}

func (c Client) resolveTicketURL(ctx context.Context, hc *http.Client, rawURL string) ([]*Artifact, error) {
	pageURL, err := c.validateProviderURL(rawURL)
	if err != nil {
		return nil, err
	}
	body, header, err := c.pageRequest(ctx, hc, http.MethodGet, pageURL, nil, pageGetTransaction, "")
	if err != nil {
		return nil, err
	}
	mediaType := responseMediaType(header.Get("Content-Type"))
	if strings.HasPrefix(mediaType, "image/") {
		artifact, err := imageArtifact(body, mediaType)
		if err != nil {
			return nil, err
		}
		return []*Artifact{artifact}, nil
	}
	if mediaType != "" && mediaType != "text/html" && mediaType != "application/xhtml+xml" {
		return nil, fmt.Errorf("智游宝票码链接返回了不支持的内容类型: %s", mediaType)
	}
	orderDetailID, gmCode, err := parseTicketPage(body)
	if err != nil {
		return nil, err
	}
	searchURL := *pageURL
	searchURL.Path = "/boss/gm/code/searchData.htm"
	searchURL.RawPath = ""
	searchURL.RawQuery = ""
	form := url.Values{"orderDetailId": {orderDetailID}, "gmCode": {gmCode}}
	searchBody, searchHeader, err := c.pageRequest(ctx, hc, http.MethodPost, &searchURL, strings.NewReader(form.Encode()), pageSearchTransaction, form.Encode())
	if err != nil {
		return nil, err
	}
	if media := responseMediaType(searchHeader.Get("Content-Type")); media != "" && media != "application/json" && media != "text/json" {
		return nil, fmt.Errorf("智游宝二维码明细返回了不支持的内容类型: %s", media)
	}
	rows, err := parseTicketPageSearch(searchBody)
	if err != nil {
		return nil, err
	}
	resolved := make([]*Artifact, 0, len(rows))
	for _, row := range rows {
		imageURL, err := ticketPageImageURL(pageURL, row)
		if err != nil {
			return nil, err
		}
		imageBody, imageHeader, err := c.pageRequest(ctx, hc, http.MethodGet, imageURL, nil, pageImageTransaction, "")
		if err != nil {
			return nil, err
		}
		media := responseMediaType(imageHeader.Get("Content-Type"))
		if media != "" && !strings.HasPrefix(media, "image/") {
			return nil, fmt.Errorf("智游宝二维码图片返回了不支持的内容类型: %s", media)
		}
		artifact, err := imageArtifact(imageBody, media)
		if err != nil {
			return nil, err
		}
		resolved = append(resolved, artifact)
	}
	return resolved, nil
}

func (c Client) validateProviderURL(rawURL string) (*url.URL, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" || len(rawURL) > maxTicketPageURL {
		return nil, errors.New("智游宝票码链接无效")
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return nil, errors.New("智游宝票码链接无效")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("智游宝票码链接必须使用 HTTP 或 HTTPS")
	}
	endpoint, err := url.Parse(strings.TrimSpace(c.Config.Endpoint))
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return nil, errors.New("智游宝连接地址无效")
	}
	if isPrivateOrLocalIP(u.Hostname()) && !isPrivateOrLocalIP(endpoint.Hostname()) {
		return nil, errors.New("智游宝票码链接禁止访问内网地址")
	}
	if !sameProviderHost(endpoint, u) {
		return nil, errors.New("智游宝票码链接必须指向当前供应商主机")
	}
	return u, nil
}

func sameProviderHost(a, b *url.URL) bool {
	if a == nil || b == nil || !strings.EqualFold(a.Hostname(), b.Hostname()) {
		return false
	}
	return effectiveURLPort(a) == effectiveURLPort(b)
}

func effectiveURLPort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	if strings.EqualFold(u.Scheme, "https") {
		return "443"
	}
	return "80"
}

func isPrivateOrLocalIP(host string) bool {
	ip := net.ParseIP(strings.TrimSpace(host))
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsLinkLocalMulticast()
}

func (c Client) pageRequest(ctx context.Context, hc *http.Client, method string, target *url.URL, body io.Reader, transaction, gateBody string) ([]byte, http.Header, error) {
	if target == nil {
		return nil, nil, errors.New("智游宝请求地址为空")
	}
	if _, err := c.validateProviderURL(target.String()); err != nil {
		return nil, nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return nil, nil, err
	}
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=UTF-8")
		req.Header.Set("Accept", "application/json")
	} else {
		req.Header.Set("Accept", "text/html,image/*;q=0.9,*/*;q=0.1")
	}
	gate := c.Config.Gate
	var permit RequestPermit
	if gate != nil {
		permit, err = gate.Acquire(ctx, transaction, gateBody)
		if err != nil {
			return nil, nil, err
		}
		if permit == nil {
			return nil, nil, &DeferredError{RetryAt: time.Now().Add(defaultGateLease), Cause: ErrRequestGateUnavailable}
		}
	}
	networkCtx := ctx
	var cancel context.CancelFunc
	if c.Config.Timeout > 0 {
		networkCtx, cancel = context.WithTimeout(ctx, c.Config.Timeout)
		defer cancel()
	}
	if permit != nil {
		if err = permit.Start(networkCtx); err != nil {
			return nil, nil, err
		}
	}
	resp, err := hc.Do(req.WithContext(networkCtx))
	if err != nil {
		if permit != nil {
			if finishErr := permit.Finish(context.WithoutCancel(networkCtx), nil); finishErr != nil {
				return nil, nil, errors.Join(err, fmt.Errorf("智游宝请求已开始但释放调度租约失败: %w", finishErr))
			}
		}
		return nil, nil, err
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if readErr != nil {
		if permit != nil {
			_ = permit.Finish(context.WithoutCancel(networkCtx), nil)
		}
		return nil, resp.Header, readErr
	}
	if len(raw) > maxResponseBytes {
		if permit != nil {
			_ = permit.Finish(context.WithoutCancel(networkCtx), nil)
		}
		return raw[:maxResponseBytes], resp.Header, errors.New("智游宝票码链接响应过大")
	}
	if permit != nil {
		var limited *RateLimitError
		if resp.StatusCode == http.StatusTooManyRequests {
			limited = &RateLimitError{RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()), StatusCode: resp.StatusCode, ResponseStatus: resp.Status, HeaderValue: resp.Header.Get("Retry-After")}
		}
		if finishErr := permit.Finish(context.WithoutCancel(networkCtx), limited); finishErr != nil {
			if limited != nil {
				return raw, resp.Header, errors.Join(limited, fmt.Errorf("智游宝限流后释放调度租约失败: %w", finishErr))
			}
			return raw, resp.Header, fmt.Errorf("智游宝请求已开始但释放调度租约失败: %w", finishErr)
		}
		if limited != nil {
			return raw, resp.Header, limited
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			if location := strings.TrimSpace(resp.Header.Get("Location")); location != "" {
				redirect, parseErr := target.Parse(location)
				if parseErr == nil && !sameProviderHost(target, redirect) {
					return raw, resp.Header, errors.New("智游宝票码链接禁止跨主机重定向")
				}
			}
		}
		return raw, resp.Header, fmt.Errorf("智游宝票码链接 HTTP %d", resp.StatusCode)
	}
	return raw, resp.Header, nil
}

func responseMediaType(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	media, _, err := mime.ParseMediaType(value)
	if err != nil {
		return strings.ToLower(strings.TrimSpace(strings.SplitN(value, ";", 2)[0]))
	}
	return strings.ToLower(media)
}

func imageArtifact(data []byte, mediaType string) (*Artifact, error) {
	if len(data) == 0 {
		return nil, errors.New("智游宝二维码图片为空")
	}
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	return &Artifact{Kind: "image", Value: base64.StdEncoding.EncodeToString(data), MimeType: mediaType}, nil
}

func parseTicketPage(data []byte) (string, string, error) {
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return "", "", fmt.Errorf("解析智游宝二维码页面失败: %w", err)
	}
	var ids, gmCodes []string
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.ElementNode {
			var id, gm string
			for _, attr := range node.Attr {
				switch strings.ToLower(strings.TrimSpace(attr.Key)) {
				case "data-id":
					id = strings.TrimSpace(attr.Val)
				case "data-gmcode":
					gm = strings.TrimSpace(attr.Val)
				}
			}
			if id != "" {
				ids = appendUniqueString(ids, id)
			}
			if gm != "" {
				gmCodes = appendUniqueString(gmCodes, gm)
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(doc)
	if len(ids) != 1 || len(gmCodes) != 1 {
		return "", "", errors.New("智游宝二维码页面缺少唯一订单明细标识")
	}
	if len(ids[0]) > maxTicketPageField || len(gmCodes[0]) > maxTicketPageField {
		return "", "", errors.New("智游宝二维码页面标识过长")
	}
	return ids[0], gmCodes[0], nil
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

type ticketPageSearchRow struct {
	AssistCheckNo     string `json:"assistCheckNo"`
	GMCode            string `json:"gmCode"`
	ThirdOfflineCheck string `json:"thirdOfflineCheck"`
}

type ticketPageSearchResponse struct {
	IsSuccess bool                  `json:"isSuccess"`
	Result    []ticketPageSearchRow `json:"result"`
}

func parseTicketPageSearch(data []byte) ([]ticketPageSearchRow, error) {
	var response ticketPageSearchResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("解析智游宝二维码明细失败: %w", err)
	}
	if !response.IsSuccess {
		return nil, errors.New("智游宝二维码明细查询失败")
	}
	if len(response.Result) == 0 {
		return nil, errors.New("智游宝二维码明细为空")
	}
	if len(response.Result) > maxTicketPageRows {
		return nil, errors.New("智游宝二维码明细数量超出限制")
	}
	return response.Result, nil
}

func ticketPageImageURL(pageURL *url.URL, row ticketPageSearchRow) (*url.URL, error) {
	if pageURL == nil {
		return nil, errors.New("智游宝二维码页面地址为空")
	}
	if third := strings.TrimSpace(row.ThirdOfflineCheck); third != "" {
		u, err := pageURL.Parse(third)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return nil, errors.New("智游宝第三方二维码链接无效")
		}
		if u.User != nil || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") || !sameProviderHost(pageURL, u) {
			return nil, errors.New("智游宝第三方二维码链接必须指向当前供应商主机")
		}
		return u, nil
	}
	gmCode := strings.TrimSpace(row.GMCode)
	assist := strings.TrimSpace(row.AssistCheckNo)
	if gmCode == "" || assist == "" || len(gmCode) > maxTicketPageField || len(assist) > maxTicketPageField || strings.ContainsAny(gmCode, "&?#\r\n") || strings.ContainsAny(assist, "&?#\r\n") {
		return nil, errors.New("智游宝二维码明细缺少安全的票码标识")
	}
	u := *pageURL
	u.Path = "/boss/gmCheckCode.htm"
	u.RawPath = ""
	u.RawQuery = gmCode + "@@" + assist
	return &u, nil
}
