//go:build smoke

// aliyun 组件（通用 OpenAPI client，ACS3-HMAC-SHA256 V3 签名）真 API 冒烟
// （change: rdev-testing-infra，task 3.5）。
//
// 端点选择：sts.aliyuncs.com 的 GetCallerIdentity（2015-04-01）——零参数、
// 零成本、纯读，且与组件 Sts 链同域同签名形态（V3 签名 + sts 域已被
// hyperf-lib PHP 侧生产验证），是「签名被真 API 接受」的最小契约探针。
//
// 契约层口径：响应非 SignatureDoesNotMatch / InvalidAccessKeyId 类认证
// 错误即契约通过（请求形态已被签名接受，进入业务层）；业务空结果或
// 非认证类业务错误可接受。凭证经 ALIYUN_AK / ALIYUN_SK 注入，任一缺失
// Skip。
package tests

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/shyun-cc/shw-gf-lib/aliyun"
)

// aliyunAuthErrRe 认证契约类错误码（前缀匹配，涵盖 InvalidAccessKeyId.NotFound
// 等子码）：命中意味着请求未被阿里云认证层接受——签名计算或凭证形态错误，
// 即组件 V3 签名实现的契约未通过。
var aliyunAuthErrRe = regexp.MustCompile(`^(SignatureDoesNotMatch|InvalidAccessKeyId|IncompleteSignature|SignatureExpireException|SignatureNonceUsed|IllegalAccessKeySecret)`)

// aliyunSmokeCreds 读取冒烟凭证：ALIYUN_AK / ALIYUN_SK 任一缺失即 Skip。
func aliyunSmokeCreds(t *testing.T) aliyun.Credentials {
	t.Helper()
	ak, sk := os.Getenv("ALIYUN_AK"), os.Getenv("ALIYUN_SK")
	if ak == "" || sk == "" {
		t.Skip("需要环境变量 ALIYUN_AK / ALIYUN_SK（缺失则跳过 aliyun 冒烟）")
	}
	return aliyun.Credentials{AccessKeyID: ak, AccessKeySecret: sk}
}

// TestSmokeAliyunClientGetCallerIdentity 真 API 冒烟：经通用 Client.Do 调
// sts GetCallerIdentity，断言 V3 签名被真服务端认证层接受（read 档纯读）。
func TestSmokeAliyunClientGetCallerIdentity(t *testing.T) {
	smokeWant(t, "read")
	client := aliyun.NewClient(aliyunSmokeCreds(t))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var resp *aliyun.Response
	call := func() error {
		var err error
		resp, err = client.Do(ctx, aliyun.Request{
			Method:  "GET",
			Host:    "sts.aliyuncs.com",
			Path:    "/",
			Action:  "GetCallerIdentity",
			Version: "2015-04-01",
		})
		if err != nil {
			return err
		}
		// 5xx 视为服务端瞬时异常（非契约拒绝），转错误后交由
		// retryOnceOnTransient 休整重试一次；仍失败才交给断言层。
		if resp.StatusCode >= 500 {
			return fmt.Errorf("阿里云服务端暂时不可用 status=%d（temporarily unavailable）", resp.StatusCode)
		}
		return nil
	}
	err, retried := retryOnceOnTransient(call)
	if err != nil {
		t.Fatalf("aliyun GetCallerIdentity: 契约层未通过，请求失败: %v", err)
	}
	if retried {
		t.Logf("aliyun GetCallerIdentity: 首次调用为瞬时错误，休整重试一次后收敛")
	}
	if resp == nil {
		t.Fatalf("aliyun GetCallerIdentity: 契约层未通过，响应为空")
	}

	// 认证契约判定：认证类错误码 = 签名/凭证被真 API 拒绝。
	if code := resp.ErrorCode; code != "" && code != "OK" {
		if aliyunAuthErrRe.MatchString(code) {
			t.Fatalf("aliyun GetCallerIdentity: 契约层被拒，认证类错误 code=%s message=%s requestId=%s",
				code, resp.ErrorMessage, resp.RequestID)
		}
		// 非认证类业务错误：请求形态已被签名接受，契约通过（业务空结果可接受）。
		t.Logf("aliyun GetCallerIdentity: 契约通过（请求已被签名接受），业务层返回 code=%s message=%s requestId=%s",
			code, resp.ErrorMessage, resp.RequestID)
		return
	}
	if !resp.Success {
		// 非 2xx 且无错误码：响应未被正常解析（报文/路由形态存疑），
		// 冒烟宁可误报失败也不误报通过，打出原始 Body 取证。
		t.Fatalf("aliyun GetCallerIdentity: 契约层未通过，非 2xx 且无错误码 status=%d body=%v requestId=%s",
			resp.StatusCode, resp.Body, resp.RequestID)
	}
	// 2xx 且无业务错误码：完全成功，记录调用者身份取证（业务数据不参与断言）。
	t.Logf("aliyun GetCallerIdentity: 契约通过，AccountId=%v Arn=%v UserId=%v requestId=%s",
		resp.Body["AccountId"], resp.Body["Arn"], resp.Body["UserId"], resp.RequestID)
}
