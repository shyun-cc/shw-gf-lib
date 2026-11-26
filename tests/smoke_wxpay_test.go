//go:build smoke

// wxpay 真 API 冒烟用例（change: rdev-testing-infra，task 3.9）。
//
// 覆盖三个 v0.8.0 审计债主缺陷的暴露用例（首跑 FAIL 即缺陷暴露证据，
// knownDefect 收敛策略见 change task 4.2）：
//   - needCert panic：各用例 defer wxpayDeferNoPanic 兜底——组件在任何
//     凭证/证书缺位场景下 panic 都被捕获并转为契约失败报告，不静默吞掉；
//   - 账单端点成功即错：申请交易账单断言「成功路径不返回错误」——组件把
//     厂商成功响应当错误处理时直接 FAIL（「无账单」类业务码除外，那属于
//     厂商侧业务现实而非契约失败）；
//   - 虚构 V2 验签：V2 验签以本地自算 MD5 闭环覆盖（design 裁决：不做
//     回调真联调）——按官方规范（非空参数按 key ASCII 排序、k=v 以 &
//     拼接、尾接 &key=商户密钥后 MD5、hex 大写）自算签名并验证；组件
//     未提供 V2 回调验签导出入口（docs 自述），故闭环另以查单真请求双
//     锚定：请求被微信接受（组件 V2 签名实现与官方规范一致）+ 微信真
//     应答签名经本地官方规范验签通过（文档声称的验签形态与微信真实
//     签名一致），任一不符即 FAIL。
//
// 凭证经环境变量注入（清单见 tests/doc.go）：WXPAY_MCHID /
// WXPAY_APPID / WXPAY_APIKEY_V3 / WXPAY_SERIAL_NO / WXPAY_PRIVATE_KEY_PATH
// （apiclient_key.pem 文件路径，env 只放路径）/ WXPAY_APIKEY_V2（V2 验签
// 闭环用）。缺凭证或私钥文件未落位则 Skip，不 FAIL。
package tests

import (
	"context"
	"crypto/md5"
	"crypto/rsa"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/shyun-cc/shw-gf-lib/wxpay"
)

// wxpaySmokeTimeout 单次冒烟请求的上下文超时（厂商接口偶发慢响应留足余量）。
const wxpaySmokeTimeout = 60 * time.Second

// wxpayDeferNoPanic needCert panic 债主缺陷防御：调用方以 defer 调用本函数，
// 用例期间组件的任何 panic 都转为契约失败报告，不静默吞掉。
func wxpayDeferNoPanic(t *testing.T) {
	if r := recover(); r != nil {
		t.Fatalf("panic: %v（needCert panic 债主缺陷暴露：凭证/证书缺位不得以 panic 报告）", r)
	}
}

// wxpayLoadPrivateKey 从 PEM 文件加载商户 API 证书私钥（apiclient_key.pem）。
// 文件读取失败视为未落位 Skip 提示；文件存在但内容不是合法 RSA 私钥属落位
// 事故，Fatalf 当场暴露。
func wxpayLoadPrivateKey(t *testing.T, path string) *rsa.PrivateKey {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("商户私钥文件读取失败（WXPAY_PRIVATE_KEY_PATH=%s，证书文件是否已落位）: %v", path, err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatalf("商户私钥文件非 PEM 格式（WXPAY_PRIVATE_KEY_PATH=%s），落位文件有误", path)
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatalf("商户私钥解析失败（WXPAY_PRIVATE_KEY_PATH=%s，应为 apiclient_key.pem 的 RSA 私钥）: %v", path, err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		t.Fatalf("商户私钥非 RSA 私钥（WXPAY_PRIVATE_KEY_PATH=%s）", path)
	}
	return key
}

// wxpayV3FromEnv 从环境变量组装 V3 客户端（证书链：私钥文件 + 序列号注入）。
// V3 五凭证缺一即 Skip。
func wxpayV3FromEnv(t *testing.T) *wxpay.Client {
	t.Helper()
	mchID := os.Getenv("WXPAY_MCHID")
	appID := os.Getenv("WXPAY_APPID")
	apiKeyV3 := os.Getenv("WXPAY_APIKEY_V3")
	serialNo := os.Getenv("WXPAY_SERIAL_NO")
	keyPath := os.Getenv("WXPAY_PRIVATE_KEY_PATH")
	if mchID == "" || appID == "" || apiKeyV3 == "" || serialNo == "" || keyPath == "" {
		t.Skipf("缺 V3 凭证（需 WXPAY_MCHID/WXPAY_APPID/WXPAY_APIKEY_V3/WXPAY_SERIAL_NO/WXPAY_PRIVATE_KEY_PATH 全部注入），跳过")
	}
	return wxpay.NewClient(wxpay.Credentials{
		MchID:      mchID,
		AppID:      appID,
		SerialNo:   serialNo,
		PrivateKey: wxpayLoadPrivateKey(t, keyPath),
		APIv3Key:   []byte(apiKeyV3), // 回调解密用，本冒烟注入以完整模拟证书链
	})
}

// md5SignV2Local 微信支付 V2 官方签名规范本地实现（与组件 signV2 形态对齐）：
// 非空参数（排除 sign）按 key ASCII 升序、以 k=v 形式 & 拼接，尾接
// &key=商户密钥后取 MD5，hex 大写。此即 V2 回调/应答验签所校验的签名形态。
func md5SignV2Local(params map[string]string, key string) string {
	keys := make([]string, 0, len(params))
	for k, v := range params {
		if v == "" || k == "sign" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, k+"="+params[k])
	}
	raw := strings.Join(pairs, "&") + "&key=" + key
	sum := md5.Sum([]byte(raw))
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

// verifyV2SignMD5Local V2 验签本地实现：提取报文 sign 字段，其余非空参数
// 按官方规范自算签名比对。sign 缺失或比对不一致均验签失败。
func verifyV2SignMD5Local(params map[string]string, key string) bool {
	sign := params["sign"]
	return sign != "" && md5SignV2Local(params, key) == sign
}

// TestSmokeWxpayV3CertChainSign V3 商户证书链加载 + 签名请求（read 档）。
//
// needCert panic 债主缺陷暴露用例：先本地构造 Authorization 签名头断言形态，
// 再以无参纯读端点（投诉回调地址查询）发真请求，端到端验证「私钥文件加载 →
// SHA256-RSA 签名 → 微信接受」契约链。组件若在证书链任何环节 panic，
// wxpayDeferNoPanic 兜底转为契约失败报告。
func TestSmokeWxpayV3CertChainSign(t *testing.T) {
	smokeWant(t, "read")
	defer wxpayDeferNoPanic(t)

	client := wxpayV3FromEnv(t)

	// 段一：本地签名构造——私钥可加载、可签出非空 WECHATPAY2-SHA256-RSA2048 头。
	const probePath = "/v3/merchant-service/complaint-notifications"
	auth, err := client.AuthorizationHeader(http.MethodGet, probePath, "")
	if err != nil {
		t.Fatalf("V3 请求签名构造失败: %v", err)
	}
	if !strings.HasPrefix(auth, "WECHATPAY2-SHA256-RSA2048 ") ||
		!strings.Contains(auth, `mchid="`+client.Creds.MchID) ||
		!strings.Contains(auth, `serial_no="`+client.Creds.SerialNo) {
		t.Fatalf("V3 Authorization 头形态不符: %s", auth)
	}
	sigIdx := strings.Index(auth, `signature="`)
	if sigIdx < 0 || len(auth) <= sigIdx+len(`signature="`)+1 {
		t.Fatalf("V3 Authorization 头签名为空: %s", auth)
	}

	// 段二：真 API 签名请求——契约层（签名/证书链被微信接受）断言。
	ctx, cancel := context.WithTimeout(context.Background(), wxpaySmokeTimeout)
	defer cancel()
	var fetchErr error
	var retried bool
	fetchErr, retried = retryOnceOnTransient(func() error {
		_, e := client.GetComplaintNotificationURL(ctx)
		return e
	})
	if retried {
		t.Logf("首次请求遇瞬时错误，休整后重试一次")
	}
	if fetchErr != nil {
		t.Fatalf("V3 证书链+签名请求真 API 验证（投诉回调地址查询）: 契约层未通过: %v", fetchErr)
	}
	t.Logf("V3 证书链加载与签名请求端到端通过")
}

// TestSmokeWxpayV3BillDownload V3 账单申请与下载（read 档）。
//
// 「账单端点成功即错」债主缺陷暴露用例：申请昨日交易账单断言成功路径不
// 返回错误；拿到 download_url 后继续下载断言无错且字节非空。组件若把厂商
// 成功响应当错误处理（err 非 nil），直接 FAIL 暴露。「昨日无账单」属厂商
// 侧业务现实（测试商户可能无交易），不算契约失败，Skip 下载段。
func TestSmokeWxpayV3BillDownload(t *testing.T) {
	smokeWant(t, "read")
	defer wxpayDeferNoPanic(t)

	client := wxpayV3FromEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), wxpaySmokeTimeout)
	defer cancel()

	// 每日 10 点后生成昨日账单，账单日期取昨日（标准冒烟语义）。
	billDate := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	var res map[string]any
	err, retried := retryOnceOnTransient(func() error {
		var err error
		res, err = client.ApplyTradeBill(ctx, billDate, "ALL", "")
		return err
	})
	if retried {
		t.Logf("首次请求遇瞬时错误，休整后重试一次")
	}
	if err == nil {
		downloadURL, _ := res["download_url"].(string)
		if downloadURL == "" {
			t.Logf("账单申请成功但应答未含 download_url（%v），跳过下载段断言", res)
			return
		}
		var bill []byte
		derr, dretried := retryOnceOnTransient(func() error {
			var err error
			bill, err = client.DownloadBill(ctx, downloadURL)
			return err
		})
		if dretried {
			t.Logf("下载账单首次遇瞬时错误，休整后重试一次")
		}
		if derr != nil {
			t.Fatalf("下载账单: 成功路径不应返回错误（「账单端点成功即错」缺陷暴露点）: %v", derr)
		}
		if len(bill) == 0 {
			t.Fatalf("下载账单: 应答字节为空（download_url=%s）", downloadURL)
		}
		t.Logf("账单申请与下载成功，账单字节 %d", len(bill))
		return
	}
	var biz *wxpay.BizError
	if errors.As(err, &biz) && (biz.Code == "NO_BILL_EXIST" || biz.Code == "BILL_NOT_EXIST") {
		t.Logf("昨日（%s）无账单（%s）：请求契约层已通过（签名与报文被微信接受），跳过下载断言", billDate, biz.Code)
		return
	}
	t.Fatalf("申请交易账单: 成功路径不应返回错误（「账单端点成功即错」缺陷暴露点）: %v", err)
}

// TestSmokeWxpayV2VerifySelfSignLoop V2 验签自算 MD5 闭环（read 档）。
//
// 「虚构 V2 验签」债主缺陷暴露用例（design 裁决：不做回调真联调，本地自算
// 闭环覆盖）：
//   - 步骤 A（纯本地，不依赖真回调）：构造典型支付成功回调报文，按官方
//     MD5 规范自算签名后本地验签断言通过；再篡改字段断言验签失败，防闭环
//     自证恒真；
//   - 步骤 B（真请求双锚定）：以查单端点发真请求——组件 V2 签名链路被微信
//     接受（return_code=SUCCESS，签名实现与规范一致），微信真应答签名经
//     本地官方规范验签通过（验签形态与微信真实签名一致）。组件签名实现
//     虚构或文档声称的验签形态虚构，均在此 FAIL。
//
// 订单不存在（result_code=FAIL）是查虚构单号的预期业务码，不算契约失败。
func TestSmokeWxpayV2VerifySelfSignLoop(t *testing.T) {
	smokeWant(t, "read")
	defer wxpayDeferNoPanic(t)

	mchID := os.Getenv("WXPAY_MCHID")
	appID := os.Getenv("WXPAY_APPID")
	apiKeyV2 := os.Getenv("WXPAY_APIKEY_V2")
	if mchID == "" || appID == "" || apiKeyV2 == "" {
		t.Skipf("缺 V2 凭证（需 WXPAY_MCHID/WXPAY_APPID/WXPAY_APIKEY_V2），跳过")
	}

	// 步骤 A：本地构造回调报文自算 MD5 闭环（attach 留空，覆盖「空值不
	// 参与签名」的官方规则；字段形态取自微信支付 V2 支付结果通知）。
	notify := map[string]string{
		"appid":          appID,
		"mch_id":         mchID,
		"openid":         "oUpF8uMuAJO_M2pxb1Q9zNjWeS6o",
		"is_subscribe":   "N",
		"trade_type":     "NATIVE",
		"bank_type":      "CFT",
		"total_fee":      "1",
		"fee_type":       "CNY",
		"transaction_id": "1000010203720160420000000001",
		"out_trade_no":   "gf-lib-smoke-selfsign-0001",
		"attach":         "", // 空值字段：不参与签名
		"time_end":       "20260826120000",
		"trade_state":    "SUCCESS",
		"cash_fee":       "1",
		"cash_fee_type":  "CNY",
		"result_code":    "SUCCESS",
		"return_code":    "SUCCESS",
	}
	notify["sign"] = md5SignV2Local(notify, apiKeyV2)
	if !verifyV2SignMD5Local(notify, apiKeyV2) {
		t.Fatalf("V2 自算闭环: 自算签名本地验签未通过，签名算法与官方规范不自洽")
	}
	// 篡改负例：报文被篡改后验签必须失败，否则闭环恒真无效。
	notify["total_fee"] = "999"
	if verifyV2SignMD5Local(notify, apiKeyV2) {
		t.Fatalf("V2 自算闭环: 报文篡改后验签仍通过，闭环自证恒真")
	}

	// 步骤 B：真请求双锚定——组件 V2 签名被微信接受 + 微信真应答签名本地验签。
	v2Client := wxpay.NewV2Client(wxpay.V2Credentials{MchID: mchID, AppID: appID, APIKey: apiKeyV2})
	ctx, cancel := context.WithTimeout(context.Background(), wxpaySmokeTimeout)
	defer cancel()
	var res map[string]string
	err, retried := retryOnceOnTransient(func() error {
		var err error
		res, err = v2Client.V2OrderQuery(ctx, map[string]string{
			"out_trade_no": "gf-lib-smoke-notexist-0001", // 虚构单号：订单不存在为预期业务码
		})
		return err
	})
	if retried {
		t.Logf("首次请求遇瞬时错误，休整后重试一次")
	}
	if res == nil || res["return_code"] == "" {
		t.Fatalf("V2 查单: 未取得微信应答: %v", err)
	}
	if res["return_code"] != "SUCCESS" {
		t.Fatalf("V2 查单: 契约层未通过（return_code=%s return_msg=%s err_code=%s）——组件 V2 签名实现与官方规范不符即在此暴露",
			res["return_code"], res["return_msg"], res["err_code"])
	}
	if res["sign"] == "" {
		t.Fatalf("V2 查单: return_code=SUCCESS 应答缺少 sign 字段，无法做验签闭环")
	}
	if !verifyV2SignMD5Local(res, apiKeyV2) {
		t.Fatalf("V2 验签闭环: 微信真应答签名与官方规范自算不一致（「虚构 V2 验签」缺陷暴露点：文档声称的验签形态与微信真实签名不符）")
	}
	t.Logf("V2 自算 MD5 闭环通过（本地构造报文 + 组件签名链路 + 微信真应答签名三重锚定）")
}

// TestSmokeWxpayPrepayNativeCostly Native 预下单（costly 档，默认 Skip）。
//
// 下单类真金写操作：SMOKE_LEVEL=costly 才执行。1 分钱 Native 预下单取
// code_url，断言契约层通过（err 为 nil 且应答含 code_url）。
func TestSmokeWxpayPrepayNativeCostly(t *testing.T) {
	smokeWant(t, "costly") // 真金预下单，read/write 档自动 Skip
	defer wxpayDeferNoPanic(t)

	client := wxpayV3FromEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), wxpaySmokeTimeout)
	defer cancel()

	body := map[string]any{
		"appid":        client.Creds.AppID,
		"mchid":        client.Creds.MchID,
		"description":  "gf-lib 冒烟测试预下单",
		"out_trade_no": fmt.Sprintf("gfsmokewxpay%d", time.Now().UnixNano()),
		"notify_url":   "https://smoke.gf-lib.invalid/wxpay/notify", // 保留域占位，仅过格式校验
		"amount":       map[string]any{"total": 1},                  // 1 分钱
	}
	var res map[string]any
	err, retried := retryOnceOnTransient(func() error {
		var err error
		res, err = client.PrepayNative(ctx, body)
		return err
	})
	if retried {
		t.Logf("首次请求遇瞬时错误，休整后重试一次")
	}
	if err != nil {
		t.Fatalf("Native 预下单: 契约层未通过: %v", err)
	}
	codeURL, _ := res["code_url"].(string)
	if codeURL == "" {
		t.Fatalf("Native 预下单: 应答缺少 code_url: %v", res)
	}
	t.Logf("Native 预下单成功（prepay_id=%v）", res["prepay_id"])
}
