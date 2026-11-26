package wxpay

import (
	"bytes"
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// 夹具密钥（测试内生成，包内零硬编码密钥）
func fixtureKeys(t *testing.T) (*rsa.PrivateKey, *rsa.PublicKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key, &key.PublicKey
}

func fixtureClient(t *testing.T) (*Client, *rsa.PublicKey) {
	t.Helper()
	priv, pub := fixtureKeys(t)
	c := NewClient(Credentials{
		MchID: "1900000001", AppID: "wx123", SerialNo: "SERIAL1",
		PrivateKey: priv, APIv3Key: []byte("0123456789abcdef0123456789abcdef"),
		VerifyKeys: map[string]*rsa.PublicKey{"PLAT1": pub},
	})
	return c, pub
}

// TestSign_MessageStringAndHeader 签名串 5 行公式 + Authorization 头格式 + 公钥验真
func TestSign_MessageStringAndHeader(t *testing.T) {
	c, pub := fixtureClient(t)
	body := `{"mchid":"1900000001"}`
	header, err := c.AuthorizationHeader("POST", "/v3/pay/transactions/jsapi", body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(header, `WECHATPAY2-SHA256-RSA2048 mchid="1900000001",nonce_str="`) {
		t.Fatalf("Authorization 前缀/格式不符: %s", header)
	}
	for _, k := range []string{`nonce_str="`, `timestamp="`, `serial_no="SERIAL1"`, `signature="`} {
		if !strings.Contains(header, k) {
			t.Fatalf("Authorization 缺 %s: %s", k, header)
		}
	}
	// 抽出要素重放签名串验真（5 行公式）
	extract := func(key string) string {
		i := strings.Index(header, key+`="`)
		rest := header[i+len(key)+2:]
		return rest[:strings.Index(rest, `"`)]
	}
	nonce, ts, sigB64 := extract("nonce_str"), extract("timestamp"), extract("signature")
	msg := "POST\n/v3/pay/transactions/jsapi\n" + ts + "\n" + nonce + "\n" + body + "\n"
	digest := sha256.Sum256([]byte(msg))
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		t.Fatal(err)
	}
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig); err != nil {
		t.Fatalf("签名应可由公钥验真: %v", err)
	}
}

// TestVerifyResponse 三行验签串验签 round-trip + 篡改拒绝
func TestVerifyResponse(t *testing.T) {
	c, _ := fixtureClient(t)
	ts, nonce, body := "1723963200", "n1", `{"id":"N1"}`
	msg := ts + "\n" + nonce + "\n" + body + "\n"
	digest := sha256.Sum256([]byte(msg))
	sig, err := rsa.SignPKCS1v15(rand.Reader, c.Creds.PrivateKey, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	sigB64 := base64.StdEncoding.EncodeToString(sig)
	if err := c.VerifyNotify(ts, nonce, body, sigB64, "PLAT1"); err != nil {
		t.Fatalf("正确签名应通过: %v", err)
	}
	if err := c.VerifyNotify(ts, nonce, body, "bad"+sigB64[3:], "PLAT1"); err == nil {
		t.Fatal("篡改签名应拒绝")
	}
	if err := c.VerifyNotify(ts, nonce, body, sigB64, "UNKNOWN"); err == nil {
		t.Fatal("未知序列号应拒绝")
	}
}

// TestDecryptResource GCM round-trip：C‖T 结构 + AAD/密钥错误拒绝
func TestDecryptResource(t *testing.T) {
	c, _ := fixtureClient(t)
	plain := `{"out_trade_no":"N1","trade_state":"SUCCESS"}`
	// 加密：APIv3 密钥 AES-256-GCM，nonce 12 字节，AAD "transaction"
	nonce, aad := "nonce1234567", "transaction"
	ct := gcmSeal(t, c.Creds.APIv3Key, nonce, []byte(plain), []byte(aad))
	res := NotifyResource{
		OriginalType:   "transaction",
		Algorithm:      "AEAD_AES_256_GCM",
		Ciphertext:     base64.StdEncoding.EncodeToString(ct),
		Nonce:          nonce,
		AssociatedData: aad,
	}
	got, err := c.DecryptResource(res)
	if err != nil || string(got) != plain {
		t.Fatalf("round-trip 失败: %v %s", err, got)
	}
	// 错误 AAD
	res.AssociatedData = "other"
	if _, err := c.DecryptResource(res); err == nil {
		t.Fatal("错误 AAD 应拒绝")
	}
	// 错误密钥
	c2 := *c
	c2.Creds.APIv3Key = []byte("ffffffffffffffffffffffffffffffff")
	res.AssociatedData = aad
	var infra *InfraError
	_, err = c2.DecryptResource(res)
	if !errors.As(err, &infra) || infra.Op != "decrypt" {
		t.Fatalf("错误密钥应 InfraError(decrypt): %v", err)
	}
}

// TestEndpoints_TableDriven 7 端点断言：path 命中 + Authorization 头前缀 + 计数相等
func TestEndpoints_TableDriven(t *testing.T) {
	c, _ := fixtureClient(t)
	cases := []struct {
		ep     Endpoint
		invoke func(cl *Client) error
	}{
		{ep: epJSAPITx, invoke: func(cl *Client) error {
			_, e := cl.PrepayJSAPI(context.Background(), map[string]any{"description": "d", "out_trade_no": "N1", "notify_url": "https://x/cb", "amount": map[string]any{"total": 1}, "payer": map[string]any{"openid": "o1"}})
			return e
		}},
		{ep: epAPPTx, invoke: func(cl *Client) error {
			_, e := cl.PrepayAPP(context.Background(), map[string]any{"description": "d", "out_trade_no": "N2", "notify_url": "https://x/cb", "amount": map[string]any{"total": 1}})
			return e
		}},
		{ep: epH5Tx, invoke: func(cl *Client) error {
			_, e := cl.PrepayH5(context.Background(), map[string]any{"description": "d", "out_trade_no": "N3", "notify_url": "https://x/cb", "amount": map[string]any{"total": 1}, "scene_info": map[string]any{"payer_client_ip": "1.2.3.4", "h5_info": map[string]any{"type": "Wap"}}})
			return e
		}},
		{ep: epNativeTx, invoke: func(cl *Client) error {
			_, e := cl.PrepayNative(context.Background(), map[string]any{"description": "d", "out_trade_no": "N4", "notify_url": "https://x/cb", "amount": map[string]any{"total": 1}})
			return e
		}},
		{ep: epCombineNative, invoke: func(cl *Client) error {
			_, e := cl.PrepayCombineNative(context.Background(), map[string]any{"combine_out_trade_no": "CN1", "sub_orders": []any{}, "notify_url": "https://x/cb"})
			return e
		}},
		{ep: epQueryTxByID, invoke: func(cl *Client) error {
			_, e := cl.QueryTransactionByID(context.Background(), "TX1")
			return e
		}},
		{ep: epCloseTx, invoke: func(cl *Client) error {
			_, e := cl.CloseTransaction(context.Background(), "N1")
			return e
		}},
		{ep: epCombineJSAPI, invoke: func(cl *Client) error {
			_, e := cl.PrepayCombineJSAPI(context.Background(), map[string]any{"combine_out_trade_no": "CN2", "sub_orders": []any{}, "notify_url": "https://x/cb"})
			return e
		}},
		{ep: epCombineAPP, invoke: func(cl *Client) error {
			_, e := cl.PrepayCombineAPP(context.Background(), map[string]any{"combine_out_trade_no": "CN3", "sub_orders": []any{}, "notify_url": "https://x/cb"})
			return e
		}},
		{ep: epCombineH5, invoke: func(cl *Client) error {
			_, e := cl.PrepayCombineH5(context.Background(), map[string]any{"combine_out_trade_no": "CN4", "sub_orders": []any{}, "notify_url": "https://x/cb"})
			return e
		}},
		{ep: epQueryTxByNo, invoke: func(cl *Client) error {
			_, e := cl.QueryTransactionByOutTradeNo(context.Background(), "N1")
			return e
		}},
		{ep: epCreateRefund, invoke: func(cl *Client) error {
			_, e := cl.CreateRefund(context.Background(), map[string]any{"out_refund_no": "R1", "amount": map[string]any{"refund": 1, "total": 1, "currency": "CNY"}, "out_trade_no": "N1"})
			return e
		}},
		{ep: epQueryRefund, invoke: func(cl *Client) error {
			_, e := cl.QueryRefund(context.Background(), "R1")
			return e
		}},
		{ep: epAbnormalRefund, invoke: func(cl *Client) error {
			_, e := cl.ApplyAbnormalRefund(context.Background(), "RID1", map[string]any{"out_refund_no": "R2", "type": "USER_BANK_CARD"})
			return e
		}},
		{ep: epTradeBill, invoke: func(cl *Client) error {
			_, e := cl.ApplyTradeBill(context.Background(), "2026-08-21", "ALL", "")
			return e
		}},
		{ep: epProfitsharingBill, invoke: func(cl *Client) error {
			_, e := cl.ApplyProfitsharingBill(context.Background(), "2026-08-21", "")
			return e
		}},
		{ep: epReceiverAdd, invoke: func(cl *Client) error {
			_, e := cl.AddProfitsharingReceiver(context.Background(), map[string]any{"appid": "wx123", "type": "MERCHANT_ID", "account": "1900000001", "relation_type": "SERVICE_PROVIDER"})
			return e
		}},
		{ep: epProfitsharingNew, invoke: func(cl *Client) error {
			_, e := cl.CreateProfitsharingOrder(context.Background(), map[string]any{"transaction_id": "TX1", "out_order_no": "P1", "receivers": []any{}, "unfreeze_unsplit": false})
			return e
		}},
		{ep: epProfitsharingGet, invoke: func(cl *Client) error {
			_, e := cl.QueryProfitsharingOrder(context.Background(), "P1", "TX1")
			return e
		}},
		{ep: epProfitsharingUnfreeze, invoke: func(cl *Client) error {
			_, e := cl.UnfreezeProfitsharingRemainder(context.Background(), map[string]any{"transaction_id": "TX1", "out_order_no": "P2", "description": "d"})
			return e
		}},
		{ep: epTransferCreate, invoke: func(cl *Client) error {
			_, e := cl.CreateTransferBill(context.Background(), map[string]any{"appid": "wx123", "out_bill_no": "B1", "transfer_scene_id": "S1", "openid": "o1", "transfer_amount": 100, "transfer_remark": "r", "transfer_scene_report_infos": []any{}})
			return e
		}},
		{ep: epTransferPreAuth, invoke: func(cl *Client) error {
			_, e := cl.PreTransferWithAuthorization(context.Background(), map[string]any{"appid": "wx123", "out_bill_no": "B2", "transfer_scene_id": "S1", "openid": "o1", "transfer_amount": 100, "transfer_remark": "r"})
			return e
		}},
		{ep: epTransferAuthed, invoke: func(cl *Client) error {
			_, e := cl.TransferWithAuthorization(context.Background(), map[string]any{"appid": "wx123", "out_bill_no": "B3", "transfer_scene_id": "S1", "transfer_amount": 100, "transfer_remark": "r", "transfer_scene_report_infos": []any{}, "authorization_id": "A1"})
			return e
		}},
		{ep: epTransferQueryNo, invoke: func(cl *Client) error {
			_, e := cl.QueryTransferByOutBillNo(context.Background(), "B1")
			return e
		}},
		{ep: epTransferQueryWx, invoke: func(cl *Client) error {
			_, e := cl.QueryTransferByTransferBillNo(context.Background(), "TB1")
			return e
		}},
		{ep: epTransferElecSign, invoke: func(cl *Client) error {
			_, e := cl.QueryTransferElecSign(context.Background(), "B1")
			return e
		}},
		{ep: epTransferReceipt, invoke: func(cl *Client) error {
			_, e := cl.ApplyTransferBillReceipt(context.Background(), "BATCH1")
			return e
		}},
		{ep: epFundFlowBill, invoke: func(cl *Client) error {
			_, e := cl.ApplyFundFlowBill(context.Background(), "2026-08-21", "BASIC", "")
			return e
		}},
		{ep: epComplaintList, invoke: func(cl *Client) error {
			_, e := cl.ListComplaints(context.Background(), map[string]string{"begin_date": "2026-08-01", "end_date": "2026-08-21"})
			return e
		}},
		{ep: epComplaintGet, invoke: func(cl *Client) error {
			_, e := cl.GetComplaint(context.Background(), "CP1")
			return e
		}},
		{ep: epComplaintHist, invoke: func(cl *Client) error {
			_, e := cl.ListComplaintNegotiationHistorys(context.Background(), "CP1", 50, 0)
			return e
		}},
		{ep: epComplaintReply, invoke: func(cl *Client) error {
			_, e := cl.ReplyComplaint(context.Background(), "CP1", map[string]any{"complainted_mchid": "1900000001", "response_content": "处理中"})
			return e
		}},
		{ep: epComplaintNotif, invoke: func(cl *Client) error {
			_, e := cl.GetComplaintNotificationURL(context.Background())
			return e
		}},
		{ep: epPayScoreCreate, invoke: func(cl *Client) error {
			_, e := cl.CreatePayScoreOrder(context.Background(), map[string]any{"out_order_no": "P1", "service_id": "SVC1", "service_introduction": "svc", "time_range": map[string]any{}, "risk_fund": map[string]any{}, "notify_url": "https://x/cb"})
			return e
		}},
		{ep: epPayScoreQuery, invoke: func(cl *Client) error {
			_, e := cl.QueryPayScoreOrder(context.Background(), map[string]string{"service_id": "SVC1", "appid": "wx123", "out_order_no": "P1"})
			return e
		}},
		{ep: epPayScoreCancel, invoke: func(cl *Client) error {
			_, e := cl.CancelPayScoreOrder(context.Background(), "P1", map[string]any{"appid": "wx123", "reason": "r"})
			return e
		}},
		{ep: epPayScoreComplete, invoke: func(cl *Client) error {
			_, e := cl.CompletePayScoreOrder(context.Background(), "P1", map[string]any{"appid": "wx123", "service_id": "SVC1", "post_payments": []any{}})
			return e
		}},
		{ep: epCouponStockCreate, invoke: func(cl *Client) error {
			_, e := cl.CreateCouponStock(context.Background(), map[string]any{"stock_name": "n", "belong_merchant": "1900000001", "stock_type": "NORMAL", "out_request_no": "REQ1"})
			return e
		}},
		{ep: epCouponStockStart, invoke: func(cl *Client) error {
			_, e := cl.StartCouponStock(context.Background(), "STK1", "1900000001")
			return e
		}},
		{ep: epCouponSend, invoke: func(cl *Client) error {
			_, e := cl.SendCoupon(context.Background(), "o1", map[string]any{"stock_id": "STK1", "out_request_no": "REQ2", "appid": "wx123", "stock_creator_mchid": "1900000001"})
			return e
		}},
		{ep: epCouponGet, invoke: func(cl *Client) error {
			_, e := cl.GetCoupon(context.Background(), "o1", "CPN1", "wx123")
			return e
		}},
		{ep: epCouponStockList, invoke: func(cl *Client) error {
			_, e := cl.ListCouponStocks(context.Background(), map[string]string{"offset": "0", "limit": "10", "stock_creator_mchid": "1900000001"})
			return e
		}},
		{ep: epCouponStockGet, invoke: func(cl *Client) error {
			_, e := cl.GetCouponStock(context.Background(), "STK1", "1900000001")
			return e
		}},
		{ep: epCouponStockPause, invoke: func(cl *Client) error {
			_, e := cl.PauseCouponStock(context.Background(), "STK1", "1900000001")
			return e
		}},
		{ep: epCouponStockRestart, invoke: func(cl *Client) error {
			_, e := cl.RestartCouponStock(context.Background(), "STK1", "1900000001")
			return e
		}},
		{ep: epCouponUseFlow, invoke: func(cl *Client) error {
			_, e := cl.DownloadCouponUseFlow(context.Background(), "STK1")
			return e
		}},
		{ep: epUserCouponsList, invoke: func(cl *Client) error {
			_, e := cl.ListUserCoupons(context.Background(), "o1", map[string]string{"appid": "wx123"})
			return e
		}},
		{ep: epFavorNotify, invoke: func(cl *Client) error {
			_, e := cl.SetFavorNotifyURL(context.Background(), "1900000001", "https://x/cb", true)
			return e
		}},
		{ep: epBusiFavorNotify, invoke: func(cl *Client) error {
			_, e := cl.SetBusiFavorNotifyURL(context.Background(), "https://x/cb")
			return e
		}},
		{ep: epBusiFavorGet, invoke: func(cl *Client) error {
			_, e := cl.GetBusiFavorStock(context.Background(), "STK1")
			return e
		}},
		{ep: epBusiFavorPatch, invoke: func(cl *Client) error {
			_, e := cl.UpdateBusiFavorStock(context.Background(), "STK1", map[string]any{"out_request_no": "REQ3"})
			return e
		}},
		{ep: epBusiFavorUse, invoke: func(cl *Client) error {
			_, e := cl.UseBusiFavorCoupon(context.Background(), map[string]any{"coupon_code": "CC1", "appid": "wx123", "use_time": "2026-08-22T12:00:00+08:00", "use_request_no": "REQ4"})
			return e
		}},
		{ep: epBusiFavorCreate, invoke: func(cl *Client) error {
			_, e := cl.CreateBusiFavorStock(context.Background(), map[string]any{"stock_name": "n", "belong_merchant": "1900000001", "goods_name": "g", "stock_type": "NORMAL", "out_request_no": "REQ5", "coupon_code_mode": "WECHATPAY_MODE"})
			return e
		}},
		{ep: epBusiFavorBudget, invoke: func(cl *Client) error {
			_, e := cl.UpdateBusiFavorBudget(context.Background(), "STK1", map[string]any{"modify_budget_request_no": "REQ6"})
			return e
		}},
		{ep: epBusiFavorCodes, invoke: func(cl *Client) error {
			_, e := cl.UploadBusiFavorCouponCodes(context.Background(), "STK1", map[string]any{"coupon_code_list": []string{"CC1"}, "upload_request_no": "REQ7"})
			return e
		}},
		{ep: epBusiFavorUserGet, invoke: func(cl *Client) error {
			_, e := cl.GetUserBusiFavorCoupon(context.Background(), "o1", "CC1", "wx123")
			return e
		}},
		{ep: epProductCouponSend, invoke: func(cl *Client) error {
			_, e := cl.SendProductCoupon(context.Background(), map[string]any{"openid": "o1", "appid": "wx123", "stock_id": "STK1", "out_request_no": "REQ8", "coupon_type": "NORMAL"})
			return e
		}},
		{ep: epUserCouponsByFilter, invoke: func(cl *Client) error {
			_, e := cl.ListUserCouponsByFilter(context.Background(), "o1", map[string]string{"appid": "wx123", "coupon_state": "SENDED"})
			return e
		}},
		{ep: epBusiFavorCallbackSet, invoke: func(cl *Client) error {
			_, e := cl.SetBusiFavorCallbacks(context.Background(), map[string]any{"notify_url": "https://x/cb"})
			return e
		}},
		{ep: epBusiFavorCallbackGet, invoke: func(cl *Client) error {
			_, e := cl.GetBusiFavorCallbacks(context.Background(), "")
			return e
		}},
		{ep: epBusiFavorStockPatch, invoke: func(cl *Client) error {
			_, e := cl.PatchBusiFavorStock(context.Background(), "STK1", map[string]any{"out_request_no": "REQ9"})
			return e
		}},
		{ep: epBusiFavorReturn, invoke: func(cl *Client) error {
			_, e := cl.ReturnBusiFavorCoupon(context.Background(), map[string]any{"coupon_code": "CC1", "stock_id": "STK1", "return_request_no": "REQ10"})
			return e
		}},
		{ep: epBusiFavorDeactivate, invoke: func(cl *Client) error {
			_, e := cl.DeactivateBusiFavorCoupon(context.Background(), map[string]any{"coupon_code": "CC1", "stock_id": "STK1", "deactivate_request_no": "REQ11"})
			return e
		}},
		{ep: epFavorImageUpload, invoke: func(cl *Client) error {
			_, e := cl.UploadMarketingImage(context.Background(), "c.jpg", []byte("fakejpgbytes"))
			return e
		}},
	}
	if len(cases) != len(allEndpoints) {
		t.Fatalf("用例数 %d != 表项数 %d", len(cases), len(allEndpoints))
	}
	var mu sync.Mutex
	type hit struct{ method, path, auth string }
	var hits []hit
	var bizErrSeen *BizError
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits = append(hits, hit{r.Method, r.URL.Path, r.Header.Get("Authorization")})
		mu.Unlock()
		if strings.Contains(r.URL.Path, "/close") {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = w.Write([]byte(`{"prepay_id":"PP1","code_url":"weixin://wxpay/bizpayurl?pr=ABC","h5_url":"https://wx.tenpay.com/cgi-bin/mmpayweb-bin/checkmweb"}`))
	}))
	t.Cleanup(srv.Close)
	c.BaseURL = srv.URL
	for _, tc := range cases {
		if err := tc.invoke(c); err != nil {
			t.Fatalf("%s 调用失败: %v", tc.ep.Name, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(hits) != len(allEndpoints) {
		t.Fatalf("桩命中 %d != 表项数 %d", len(hits), len(allEndpoints))
	}
	for i, tc := range cases {
		h := hits[i]
		wantPath := tc.ep.Path
		for k, v := range map[string]string{"{transaction_id}": "TX1", "{out_trade_no}": "N1", "{out_refund_no}": "R1", "{refund_id}": "RID1", "{out_order_no}": "P1", "{out_bill_no}": "B1", "{transfer_bill_no}": "TB1", "{complaint_id}": "CP1", "{stock_id}": "STK1", "{openid}": "o1", "{coupon_id}": "CPN1", "{coupon_code}": "CC1", "{appid}": "wx123"} {
			wantPath = strings.ReplaceAll(wantPath, k, v)
		}
		if h.path != wantPath {
			t.Fatalf("%s 命中 %s，期望 %s", tc.ep.Name, h.path, wantPath)
		}
		if !strings.HasPrefix(h.auth, "WECHATPAY2-SHA256-RSA2048 ") {
			t.Fatalf("%s Authorization 头前缀不符: %s", tc.ep.Name, h.auth)
		}
	}
	_ = bizErrSeen
}

// gcmSeal 测试辅助：AES-256-GCM 加密（Seal 输出 C‖T）
func gcmSeal(t *testing.T, key []byte, nonce string, plain, aad []byte) []byte {
	t.Helper()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	return g.Seal(nil, []byte(nonce), plain, aad)
}

// TestInvoke_BizError V3 错误形态：HTTP≥400 + body code/message → BizError
func TestInvoke_BizError(t *testing.T) {
	c, _ := fixtureClient(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"PARAM_ERROR","message":"参数错误"}`))
	}))
	t.Cleanup(srv.Close)
	c.BaseURL = srv.URL
	_, err := c.PrepayNative(context.Background(), map[string]any{})
	var biz *BizError
	if !errors.As(err, &biz) || biz.HTTPStatus != 400 || biz.Code != "PARAM_ERROR" || biz.Message != "参数错误" {
		t.Fatalf("应 BizError V3 形态: %v", err)
	}
	var infra *InfraError
	if errors.As(err, &infra) {
		t.Fatal("业务错误不应同时是 InfraError")
	}
}

// TestDownloadBill 动态下载分发：签名头在位、字节返回、跳过验签
func TestDownloadBill(t *testing.T) {
	c, _ := fixtureClient(t)
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte("BILL,CSV,DATA"))
	}))
	t.Cleanup(srv.Close)
	data, err := c.DownloadBill(context.Background(), srv.URL+"/v3/billdownload/file?token=TOKEN1")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "BILL,CSV,DATA" {
		t.Fatalf("账单字节不符: %q", data)
	}
	if !strings.HasPrefix(gotAuth, "WECHATPAY2-SHA256-RSA2048 ") {
		t.Fatalf("下载请求应带签名头: %s", gotAuth)
	}
}

// TestUploadMarketingImage_MultipartSignature 营销图片上传 multipart 形态：
// Authorization 签名主体是 meta JSON（非 multipart body）、file 内容与 meta sha256 校验
func TestUploadMarketingImage_MultipartSignature(t *testing.T) {
	c, pub := fixtureClient(t)
	content := []byte("fakejpgbytes-for-upload")
	var gotMeta, gotAuth, gotCT string
	var gotFile []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("multipart 解析失败: %v", err)
			return
		}
		meta := r.FormValue("meta")
		gotMeta = meta
		f, _, err := r.FormFile("file")
		if err != nil {
			t.Errorf("file part 缺失: %v", err)
			return
		}
		defer f.Close()
		gotFile, _ = io.ReadAll(f)
		_, _ = w.Write([]byte(`{"media_url":"https://qpic.cn/xxx"}`))
	}))
	t.Cleanup(srv.Close)
	c.BaseURL = srv.URL

	res, err := c.UploadMarketingImage(context.Background(), "c.jpg", content)
	if err != nil {
		t.Fatal(err)
	}
	if res["media_url"] != "https://qpic.cn/xxx" {
		t.Fatalf("media_url 应答不符: %v", res)
	}
	if !strings.HasPrefix(gotCT, "multipart/form-data; boundary=") {
		t.Fatalf("Content-Type 应为 multipart: %s", gotCT)
	}
	if !bytes.Equal(gotFile, content) {
		t.Fatal("file part 内容应与上传字节一致")
	}
	// meta JSON 校验：filename + sha256
	sum := sha256.Sum256(content)
	wantMeta := fmt.Sprintf(`{"filename":"c.jpg","sha256":%q}`, hex.EncodeToString(sum[:]))
	if gotMeta != wantMeta {
		t.Fatalf("meta 不符: got %s want %s", gotMeta, wantMeta)
	}
	// 签名主体断言：Authorization 签名串第 5 行（body 段）必须是 meta JSON 而非 multipart body
	extract := func(key string) string {
		i := strings.Index(gotAuth, key+`="`)
		rest := gotAuth[i+len(key)+2:]
		return rest[:strings.Index(rest, `"`)]
	}
	nonce, ts, sigB64 := extract("nonce_str"), extract("timestamp"), extract("signature")
	msg := "POST\n/v3/marketing/favor/media/image-upload\n" + ts + "\n" + nonce + "\n" + gotMeta + "\n"
	digest := sha256.Sum256([]byte(msg))
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		t.Fatal(err)
	}
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig); err != nil {
		t.Fatalf("签名主体应为 meta JSON（以 meta 重放签名串验签失败）: %v", err)
	}
}
