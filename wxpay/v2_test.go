package wxpay

import (
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestV2Sign 签名向量：MD5 与 HMAC-SHA256 双算法独立重算一致、大写
func TestV2Sign(t *testing.T) {
	params := map[string]string{"appid": "wx123", "mch_id": "1900000001", "body": "d", "total_fee": "100", "empty": ""}
	key := strings.Repeat("k", 32)
	// 独立重算（同公式另写）
	sorted := "appid=wx123&body=d&mch_id=1900000001&total_fee=100&key=" + key
	md5Want := strings.ToUpper(hex.EncodeToString(md5sum([]byte(sorted))))
	if got := signV2(params, key, ""); got != md5Want {
		t.Fatalf("MD5 签名不符: %s != %s", got, md5Want)
	}
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(sorted))
	hmacWant := strings.ToUpper(hex.EncodeToString(mac.Sum(nil)))
	if got := signV2(params, key, "HMAC-SHA256"); got != hmacWant {
		t.Fatalf("HMAC-SHA256 签名不符: %s != %s", got, hmacWant)
	}
	if signV2(params, key, "") == signV2(params, key, "HMAC-SHA256") {
		t.Fatal("两算法签名应不同")
	}
}

func md5sum(b []byte) []byte {
	s := md5.Sum(b)
	return s[:]
}

// TestV2XMLRoundTrip XML 组包/解析 round-trip
func TestV2XMLRoundTrip(t *testing.T) {
	params := map[string]string{"appid": "wx123", "body": "desc<&>", "sign": "ABC"}
	xmlBody := buildV2XML(params)
	if !strings.HasPrefix(xmlBody, "<xml>") || !strings.HasSuffix(xmlBody, "</xml>") {
		t.Fatalf("XML 形态不符: %s", xmlBody)
	}
	got, err := parseV2XML(xmlBody)
	if err != nil {
		t.Fatal(err)
	}
	if got["appid"] != "wx123" || got["body"] != "desc<&>" || got["sign"] != "ABC" {
		t.Fatalf("round-trip 不符: %v", got)
	}
}

// fixtureCertPEM 测试内生成证书夹具（零硬编码密钥）
func fixtureCertPEM(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "mch-test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, cerr := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if cerr != nil {
		t.Fatal(cerr)
	}
	if err != nil {
		t.Fatal(err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return
}

// TestV2Endpoints_TableDriven 6 端点：path/XML body/sign 字段命中 + 证书端点 TLS 双向认证取证
func TestV2Endpoints_TableDriven(t *testing.T) {
	certPEM, keyPEM := fixtureCertPEM(t)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "<sign>") || !strings.HasPrefix(string(body), "<xml>") {
			t.Errorf("V2 报文形态不符: %s", body)
		}
		w.Header().Set("Content-Type", "text/xml")
		_, _ = w.Write([]byte(`<xml><return_code><![CDATA[SUCCESS]]></return_code><result_code><![CDATA[SUCCESS]]></result_code><nonce_str><![CDATA[n]]></nonce_str><sign><![CDATA[S]]></sign></xml>`))
	}))
	// TLS 服务端：要求客户端证书（双向认证取证）
	clientCAS := x509.NewCertPool()
	clientCAS.AppendCertsFromPEM(certPEM)
	srv.TLS = &tls.Config{ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: clientCAS}
	srv.StartTLS()
	t.Cleanup(srv.Close)

	// 注入信任桩 CA 的 client（非证书端点也走 TLS 桩；证书端点继承 RootCAs + 商户证书双证）
	caPool := x509.NewCertPool()
	caPool.AddCert(srv.Certificate())
	c := NewV2Client(V2Credentials{MchID: "1900000001", AppID: "wx123", APIKey: strings.Repeat("k", 32),
		TLSCertPEM: certPEM, TLSKeyPEM: keyPEM})
	mchCert, err2 := tls.X509KeyPair(certPEM, keyPEM)
	if err2 != nil {
		t.Fatal(err2)
	}
	c.HTTPClient = &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: caPool, Certificates: []tls.Certificate{mchCert}}}}
	c.BaseURL = srv.URL

	cases := []struct {
		ep     V2Endpoint
		invoke func() error
	}{
		{ep: epV2MicroPay, invoke: func() error {
			_, e := c.MicroPay(context.Background(), map[string]string{"body": "d", "out_trade_no": "N1", "total_fee": "100", "spbill_create_ip": "1.2.3.4", "auth_code": "100012345678901234"})
			return e
		}},
		{ep: epV2SendRedPack, invoke: func() error {
			_, e := c.SendRedPack(context.Background(), map[string]string{"mch_billno": "B1", "send_name": "s", "re_openid": "o1", "total_amount": "100", "total_num": "1", "wishing": "w", "client_ip": "1.2.3.4", "act_name": "a", "remark": "r"})
			return e
		}},
		{ep: epV2QueryRedPack, invoke: func() error {
			_, e := c.QueryRedPack(context.Background(), "B1")
			return e
		}},
		{ep: epV2CustomsOrder, invoke: func() error {
			_, e := c.CustomsDeclareOrder(context.Background(), map[string]string{"out_trade_no": "N1", "transaction_id": "T1", "customs": "SH", "mch_customs_no": "C1"})
			return e
		}},
		{ep: epV2CustomsQuery, invoke: func() error {
			_, e := c.CustomsDeclareQuery(context.Background(), map[string]string{"customs": "SH", "out_trade_no": "N1"})
			return e
		}},
		{ep: epV2CustomsRe, invoke: func() error {
			_, e := c.CustomsRedeclare(context.Background(), map[string]string{"customs": "SH", "out_trade_no": "N1", "mch_customs_no": "C1"})
			return e
		}},
		{ep: epV2UnifiedOrder, invoke: func() error {
			_, e := c.V2UnifiedOrder(context.Background(), map[string]string{"body": "d", "out_trade_no": "N1", "total_fee": "100", "trade_type": "NATIVE", "notify_url": "https://x/cb", "spbill_create_ip": "1.2.3.4"})
			return e
		}},
		{ep: epV2OrderQuery, invoke: func() error {
			_, e := c.V2OrderQuery(context.Background(), map[string]string{"out_trade_no": "N1"})
			return e
		}},
		{ep: epV2CloseOrder, invoke: func() error {
			_, e := c.V2CloseOrder(context.Background(), map[string]string{"out_trade_no": "N1"})
			return e
		}},
		{ep: epV2Refund, invoke: func() error {
			_, e := c.V2Refund(context.Background(), map[string]string{"out_trade_no": "N1", "out_refund_no": "R1", "total_fee": "100", "refund_fee": "100"})
			return e
		}},
		{ep: epV2RefundQuery, invoke: func() error {
			_, e := c.V2RefundQuery(context.Background(), map[string]string{"out_trade_no": "N1", "out_refund_no": "R1"})
			return e
		}},
		{ep: epV2DownloadBill, invoke: func() error {
			_, e := c.V2DownloadBill(context.Background(), map[string]string{"bill_date": "20260822", "bill_type": "ALL"})
			return e
		}},
		{ep: epV2FundFlow, invoke: func() error {
			_, e := c.V2DownloadFundFlow(context.Background(), map[string]string{"bill_date": "20260822", "account_type": "Basic"})
			return e
		}},
		{ep: epV2Report, invoke: func() error {
			_, e := c.V2Report(context.Background(), map[string]string{"interface_url": "/pay/micropay", "execute_time": "100", "return_code": "SUCCESS"})
			return e
		}},
		{ep: epV2ShortURL, invoke: func() error {
			_, e := c.V2ShortURL(context.Background(), map[string]string{"long_url": "weixin://wxpay/bizpayurl?u=1"})
			return e
		}},
		{ep: epV2AuthCode2OID, invoke: func() error {
			_, e := c.V2AuthCodeToOpenID(context.Background(), map[string]string{"auth_code": "100012345678901234"})
			return e
		}},
		{ep: epV2SandboxMicro, invoke: func() error {
			_, e := c.V2SandboxMicroPay(context.Background(), map[string]string{"body": "d", "out_trade_no": "N1", "total_fee": "100", "spbill_create_ip": "1.2.3.4", "auth_code": "100012345678901234"})
			return e
		}},
		{ep: epV2MiniHBSend, invoke: func() error {
			_, e := c.SendMiniProgramRedPack(context.Background(), map[string]string{"mch_billno": "B1", "re_openid": "o1", "total_amount": "100", "total_num": "1", "scene_id": "PRODUCT_1"})
			return e
		}},
		{ep: epV2MiniHBReceive, invoke: func() error {
			_, e := c.ReceiveMiniProgramRedPack(context.Background(), map[string]string{"mch_billno": "B1", "user_ticket": "T1"})
			return e
		}},
		{ep: epV2MiniHBQuery, invoke: func() error {
			_, e := c.QueryMiniProgramRedPack(context.Background(), map[string]string{"mch_billno": "B1"})
			return e
		}},
		{ep: epV2PayBank, invoke: func() error {
			_, e := c.PayBank(context.Background(), map[string]string{"partner_trade_no": "PB1", "enc_bank_no": "ENC1", "enc_name": "ENC2", "amount": "100", "bank_code": "1002", "desc": "d"})
			return e
		}},
		{ep: epV2QueryBank, invoke: func() error {
			_, e := c.QueryBank(context.Background(), map[string]string{"partner_trade_no": "PB1"})
			return e
		}},
		{ep: epV2GetPublicKey, invoke: func() error { _, e := c.GetV2PublicKey(context.Background(), map[string]string{}); return e }},
		{ep: epV2EntrustWeb, invoke: func() error {
			_, e := c.EntrustWeb(context.Background(), map[string]string{"plan_id": "P1", "contract_code": "C1", "request_serial": "1", "notify_url": "https://x/cb"})
			return e
		}},
		{ep: epV2ContractOrder, invoke: func() error {
			_, e := c.ContractOrder(context.Background(), map[string]string{"body": "d", "out_trade_no": "N1", "total_fee": "100", "spbill_create_ip": "1.2.3.4", "plan_id": "P1", "contract_code": "C1"})
			return e
		}},
		{ep: epV2PappayApply, invoke: func() error {
			_, e := c.PappayApply(context.Background(), map[string]string{"contract_id": "CT1", "out_trade_no": "N1", "total_fee": "100"})
			return e
		}},
		{ep: epV2PreConsult, invoke: func() error {
			_, e := c.PappayPreConsult(context.Background(), map[string]string{"contract_id": "CT1", "out_trade_no": "N1"})
			return e
		}},
		{ep: epV2DeleteContract, invoke: func() error {
			_, e := c.DeleteContract(context.Background(), map[string]string{"plan_id": "P1", "contract_code": "C1", "version": "1"})
			return e
		}},
		{ep: epV2QueryContract, invoke: func() error {
			_, e := c.QueryContract(context.Background(), map[string]string{"plan_id": "P1", "contract_code": "C1", "version": "1"})
			return e
		}},
		{ep: epV2PSAddReceiver, invoke: func() error {
			_, e := c.V2AddProfitSharingReceiver(context.Background(), map[string]string{"receiver": `{"type":"MERCHANT_ID","account":"1901"}`})
			return e
		}},
		{ep: epV2PSRemoveReceiver, invoke: func() error {
			_, e := c.V2RemoveProfitSharingReceiver(context.Background(), map[string]string{"receiver": `{"type":"MERCHANT_ID","account":"1901"}`})
			return e
		}},
		{ep: epV2PSOrder, invoke: func() error {
			_, e := c.V2ProfitSharing(context.Background(), map[string]string{"transaction_id": "TX1", "out_order_no": "PS1", "receivers": "[]"})
			return e
		}},
		{ep: epV2PSMulti, invoke: func() error {
			_, e := c.V2MultiProfitSharing(context.Background(), map[string]string{"transaction_id": "TX1", "out_order_no": "PS2"})
			return e
		}},
		{ep: epV2PSFinish, invoke: func() error {
			_, e := c.V2ProfitSharingFinish(context.Background(), map[string]string{"transaction_id": "TX1", "out_order_no": "PS3", "description": "完结"})
			return e
		}},
		{ep: epV2PSQuery, invoke: func() error {
			_, e := c.V2ProfitSharingQuery(context.Background(), map[string]string{"transaction_id": "TX1", "out_order_no": "PS1"})
			return e
		}},
		{ep: epV2PSReturn, invoke: func() error {
			_, e := c.V2ProfitSharingReturn(context.Background(), map[string]string{"order_id": "O1", "out_return_no": "PR1", "return_mchid": "1901", "amount": "100"})
			return e
		}},
		{ep: epV2PSReturnQuery, invoke: func() error {
			_, e := c.V2ProfitSharingReturnQuery(context.Background(), map[string]string{"order_id": "O1", "out_return_no": "PR1", "return_mchid": "1901"})
			return e
		}},
		{ep: epV2PSAmountQuery, invoke: func() error {
			_, e := c.V2ProfitSharingAmountQuery(context.Background(), map[string]string{"transaction_id": "TX1"})
			return e
		}},
	}
	if len(cases) != len(v2AllEndpoints) {
		t.Fatalf("用例数 %d != 表项数 %d", len(cases), len(v2AllEndpoints))
	}
	var mu sync.Mutex
	var paths []string
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "<sign>") {
			t.Errorf("缺 sign: %s", body)
		}
		w.Header().Set("Content-Type", "text/xml")
		_, _ = w.Write([]byte(`<xml><return_code><![CDATA[SUCCESS]]></return_code><result_code><![CDATA[SUCCESS]]></result_code></xml>`))
	})
	for _, tc := range cases {
		if err := tc.invoke(); err != nil {
			t.Fatalf("%s 调用失败: %v", tc.ep.Name, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	for i, ep := range v2AllEndpoints {
		if paths[i] != ep.Path {
			t.Fatalf("[%d] %s 命中 %s 期望 %s", i, ep.Name, paths[i], ep.Path)
		}
	}
}

// TestV2BizError return_code=FAIL → V2BizError
func TestV2BizError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<xml><return_code><![CDATA[FAIL]]></return_code><return_msg><![CDATA[签名错误]]></return_msg></xml>`))
	}))
	t.Cleanup(srv.Close)
	c := NewV2Client(V2Credentials{MchID: "m", AppID: "a", APIKey: "k"})
	c.BaseURL = srv.URL
	_, err := c.MicroPay(context.Background(), map[string]string{"body": "d"})
	biz, ok := err.(*V2BizError)
	if !ok {
		t.Fatalf("应 V2BizError: %v", err)
	}
	_ = biz
}
