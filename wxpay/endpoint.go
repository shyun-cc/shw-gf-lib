package wxpay

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Endpoint V3 端点声明式表项（表驱动薄委托）。Path 可含 {占位}（路径参数由方法层替换）。
type Endpoint struct {
	Name   string
	Method string
	Path   string // 可含 {transaction_id} / {out_trade_no} 占位
	Query  []string
	Body   bool
}

// V3 基础支付族表项（官方清点见 change wxpay-v3-basic proposal；合单 jsapi/app/h5 变体与
// out-trade-no 查单挂起下一 change 取证）
var (
	epJSAPITx       = Endpoint{"v3.pay.transactions.jsapi", http.MethodPost, "/v3/pay/transactions/jsapi", nil, true}
	epAPPTx         = Endpoint{"v3.pay.transactions.app", http.MethodPost, "/v3/pay/transactions/app", nil, true}
	epH5Tx          = Endpoint{"v3.pay.transactions.h5", http.MethodPost, "/v3/pay/transactions/h5", nil, true}
	epNativeTx      = Endpoint{"v3.pay.transactions.native", http.MethodPost, "/v3/pay/transactions/native", nil, true}
	epCombineNative = Endpoint{"v3.combine.transactions.native", http.MethodPost, "/v3/combine-transactions/native", nil, true}
	epQueryTxByID   = Endpoint{"v3.pay.transactions.id", http.MethodGet, "/v3/pay/transactions/id/{transaction_id}", []string{"mchid"}, false}
	epCloseTx       = Endpoint{"v3.pay.transactions.close", http.MethodPost, "/v3/pay/transactions/out-trade-no/{out_trade_no}/close", nil, true}
)

var allEndpoints = []Endpoint{
	epJSAPITx, epAPPTx, epH5Tx, epNativeTx, epCombineNative, epQueryTxByID, epCloseTx,
	epCombineJSAPI, epCombineAPP, epCombineH5, epQueryTxByNo,
	epCreateRefund, epQueryRefund, epAbnormalRefund,
	epTradeBill, epProfitsharingBill, epReceiverAdd, epProfitsharingNew, epProfitsharingGet, epProfitsharingUnfreeze,
	epTransferCreate, epTransferPreAuth, epTransferAuthed, epTransferQueryNo, epTransferQueryWx,
	epTransferElecSign, epTransferReceipt,
	epFundFlowBill, epComplaintList, epComplaintGet, epComplaintHist, epComplaintReply, epComplaintNotif,
	epPayScoreCreate, epPayScoreQuery, epPayScoreCancel, epPayScoreComplete,
	epCouponStockCreate, epCouponStockStart, epCouponSend, epCouponGet,
	epCouponStockList, epCouponStockGet, epCouponStockPause, epCouponStockRestart,
	epCouponUseFlow, epUserCouponsList,
	epFavorNotify, epBusiFavorNotify,
	epBusiFavorGet, epBusiFavorPatch, epBusiFavorUse,
	epBusiFavorCreate, epBusiFavorBudget, epBusiFavorCodes, epBusiFavorUserGet,
	epProductCouponSend, epUserCouponsByFilter, epBusiFavorCallbackSet, epBusiFavorCallbackGet,
	epBusiFavorStockPatch, epBusiFavorReturn, epBusiFavorDeactivate, epFavorImageUpload,
}

// invoke V3 统一分发：自动构造 Authorization 头（WECHATPAY2-SHA256-RSA2048）、
// HTTP≥400 或应答体含 code → BizError{Code,Message,HTTPStatus}、网络/解码 → InfraError。
// 路径占位 {k} 由 pathParams 替换。
func (c *Client) invoke(ctx context.Context, ep Endpoint, pathParams, query map[string]string, body any) (map[string]any, error) {
	path := ep.Path
	for k, v := range pathParams {
		path = strings.ReplaceAll(path, "{"+k+"}", v)
	}
	qs := ""
	if len(query) > 0 {
		for k, v := range query {
			qs += k + "=" + v + "&"
		}
		qs = "?" + strings.TrimSuffix(qs, "&")
	}
	pathWithQuery := path + qs

	var raw []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, &InfraError{Op: "encode", Err: err}
		}
		raw = b
	}
	auth, err := c.AuthorizationHeader(ep.Method, pathWithQuery, string(raw))
	if err != nil {
		return nil, err
	}

	contentType := ""
	if raw != nil {
		contentType = "application/json"
	}
	return c.doRequest(ctx, ep.Method, pathWithQuery, contentType, raw, auth)
}

// doRequest 发送已签名请求并解析应答（invoke / invokeMultipart 公共尾段）：
// HTTP≥400 或应答体含 code → BizError{Code,Message,HTTPStatus}、网络/解码 → InfraError。
func (c *Client) doRequest(ctx context.Context, method, pathWithQuery, contentType string, raw []byte, auth string) (map[string]any, error) {
	base := c.BaseURL
	if base == "" {
		base = defaultMchAPI
	}
	var reader io.Reader
	if raw != nil {
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+pathWithQuery, reader)
	if err != nil {
		return nil, &InfraError{Op: "http", Err: err}
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, &InfraError{Op: "http", Err: err}
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &InfraError{Op: "http", Err: err}
	}
	var decoded map[string]any
	if len(respBody) > 0 {
		if err := json.Unmarshal(respBody, &decoded); err != nil {
			// 关单等成功为 204 无包体；非 JSON 非空应答按基础设施异常
			return nil, &InfraError{Op: "decode", Err: fmt.Errorf("响应非 JSON: %w", err)}
		}
	}
	if resp.StatusCode >= 400 {
		code, _ := decoded["code"].(string)
		message, _ := decoded["message"].(string)
		return decoded, &BizError{Code: code, Message: message, HTTPStatus: resp.StatusCode}
	}
	if code, ok := decoded["code"].(string); ok && code != "" {
		message, _ := decoded["message"].(string)
		return decoded, &BizError{Code: code, Message: message, HTTPStatus: resp.StatusCode}
	}
	return decoded, nil
}

// invokeMultipart 营销图片上传等 multipart 端点分发（官方 4015717515）：
// Authorization 签名主体是 meta JSON 串而非整个 body；HTTP body 为 multipart/form-data
// （meta part JSON + file part 二进制）。
func (c *Client) invokeMultipart(ctx context.Context, ep Endpoint, filename string, content []byte) (map[string]any, error) {
	sum := sha256.Sum256(content)
	meta := fmt.Sprintf(`{"filename":%q,"sha256":%q}`, filename, hex.EncodeToString(sum[:]))
	auth, err := c.AuthorizationHeader(ep.Method, ep.Path, meta)
	if err != nil {
		return nil, err
	}
	boundary := "gf-lib-" + hex.EncodeToString(sum[:8])
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "--%s\r\nContent-Disposition: form-data; name=%q\r\n\r\n%s\r\n", boundary, "meta", meta)
	fmt.Fprintf(&buf, "--%s\r\nContent-Disposition: form-data; name=%q; filename=%q\r\nContent-Type: application/octet-stream\r\n\r\n", boundary, "file", filename)
	buf.Write(content)
	fmt.Fprintf(&buf, "\r\n--%s--\r\n", boundary)
	return c.doRequest(ctx, ep.Method, ep.Path, "multipart/form-data; boundary="+boundary, buf.Bytes(), auth)
}
