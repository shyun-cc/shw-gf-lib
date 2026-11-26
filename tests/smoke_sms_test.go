//go:build smoke

// sms 组件阿里云短信冒烟测试：真发送闭环（TestSmokeSmsRealSend，costly 档）
// + 发送明细查询（TestSmokeSmsQueryDetails，read 档零计费）
// （change: rdev-testing-infra task 3.10 / sms-query-details task 1.2）。
//
// 【真金计费警示】TestSmokeSmsRealSend 每次执行都向阿里云真实发送一条短信
// 并产生计费，必须同时满足两个条件才会真正发送：
//  1. SMOKE_LEVEL=costly（默认 read 档自动 Skip，防误触发付费操作）；
//  2. 五个环境变量凭证齐备（任一缺失即 Skip）。
//
// TestSmokeSmsQueryDetails 为 read 档零计费用例：查询接口本身不计费，
// 默认 read 档即执行；凭证与发送用例同一套五变量，任一缺失即 Skip。
//
// SMS_TEST_PHONE 必须是测试专用手机号，禁止指向真实客户号码。
//
// TestSmokeSmsRealSend 验证目标（契约层）：经组件 Provider 机制组装
// （aliyun.NewClient 凭证注入 → sms.NewAliyunProvider 签名与场景模板 →
// sms.NewWithProvider）后，SendSms 的 ACS3 签名、报文组装、模板配置被
// 阿里云接受——断言发送接口返回接受且携带 BizId；不校验送达状态
// （厂商侧异步回执，属业务测试范围，冒烟只验「厂商接受请求形态」）。
// 事件派发注入独立空分发器隔离：不触碰全局 events.Default()，无订阅者
// 派发即 no-op（sms 构造不强制 Dispatcher）。
// 发送成功后追加闭环段：以 BizId+phone+当日日期轮询 QuerySendDetails
// （间隔 3s、10 轮上限约 30s，每轮重取当日日期防跨日界），直至明细
// SendStatus 出终态（非等待回执）；超时未出终态不判失败——回执延迟属
// 厂商侧，以 BizId 查得该条发送记录即已验证闭环链路。
//
// TestSmokeSmsQueryDetails 验证目标（契约层）：直连 aliyun 层调用
// QuerySendDetails（查询是厂商特有能力，不经 sms.Provider 发送抽象层，
// 见 change sms-query-details 设计 D1），sendDate 取昨日（近 7 天内且
// 整天已过，请求格式 yyyyMMdd，bizID 空 = 不限流水），断言厂商接受
// 请求形态：无 Go error 且 Success=true、TotalCount≥0、Details 非 nil
// ——记录存在性不校验，查询号当日无发送是合法空态。
//
// 环境变量（连接信息全部经环境变量注入，不硬编码凭证；两用例共用）：
//   - SMS_AK / SMS_SK：阿里云 AccessKey（建议测试专用子账号，仅短信权限）；
//   - SMS_SIGN：短信签名；
//   - SMS_TEMPLATE_CODE：模板 Code，须为无变量模板（通知类）；带 ${xxx}
//     变量的验证码类模板会因缺参被厂商拒绝，不适用本用例；
//   - SMS_TEST_PHONE：接收短信的测试手机号（真金计费落在此号码上）。
//
// 运行方式（见 doc.go 包注释与 smoke_helper_test.go 契约说明）：
//
//	cd tests
//	SMOKE_LEVEL=costly \
//	  SMS_AK=... SMS_SK=... SMS_SIGN=... SMS_TEMPLATE_CODE=... SMS_TEST_PHONE=... \
//	  GOWORK=off go test -tags smoke -run TestSmokeSmsRealSend -v
//
// 查询用例零计费，read 档即可：
//
//	cd tests
//	SMOKE_LEVEL=read \
//	  SMS_AK=... SMS_SK=... SMS_SIGN=... SMS_TEMPLATE_CODE=... SMS_TEST_PHONE=... \
//	  GOWORK=off go test -tags smoke -run TestSmokeSmsQueryDetails -v
package tests

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/shyun-cc/shw-gf-lib/aliyun"
	"github.com/shyun-cc/shw-gf-lib/events"
	"github.com/shyun-cc/shw-gf-lib/sms"
)

// TestSmokeSmsRealSend 真短信发送冒烟（costly 档，真金计费）：
// 验证阿里云短信链路契约——签名、报文、模板被厂商接受并返回 BizId。
func TestSmokeSmsRealSend(t *testing.T) {
	// costly 档（真金写：短信计费）：默认 read 档 Skip，须显式升档才执行
	smokeWant(t, "costly")

	ak := os.Getenv("SMS_AK")
	sk := os.Getenv("SMS_SK")
	sign := os.Getenv("SMS_SIGN")
	templateCode := os.Getenv("SMS_TEMPLATE_CODE")
	phone := os.Getenv("SMS_TEST_PHONE")
	if ak == "" || sk == "" || sign == "" || templateCode == "" || phone == "" {
		t.Skip("缺少阿里云短信凭证：需同时注入 SMS_AK / SMS_SK / SMS_SIGN / SMS_TEMPLATE_CODE / SMS_TEST_PHONE")
	}

	// 经组件 Provider 机制注入阿里云 provider：
	// 凭证在 aliyun.Client 注入，签名与场景模板在 AliyunConfig 注入，
	// 事件经独立空分发器隔离（sms 构造不强制 Dispatcher，空分发器派发为 no-op）。
	client := aliyun.NewClient(aliyun.Credentials{
		AccessKeyID:     ak,
		AccessKeySecret: sk,
	})
	provider := sms.NewAliyunProvider(sms.AliyunConfig{
		Client:    client,
		SignName:  sign,
		Templates: map[string]string{string(sms.SceneGeneric): templateCode},
	})
	svc := sms.NewWithProvider(provider)
	svc.SetDispatcher(events.New())

	msg := sms.Message{
		Phone: phone,
		Scene: sms.SceneGeneric,
		// 无变量模板不传 Params（组件对 nil 不携带 TemplateParam 字段）
	}

	// 真实发送：瞬时错误（超时/限流类）休整后重试一次，仍失败才判失败
	var res *sms.Result
	send := func() error {
		r, err := svc.Send(context.Background(), msg)
		res = r
		if err != nil {
			return err
		}
		if r != nil && !r.Success {
			// 厂商业务拒绝折叠为错误，交由瞬时错误识别（限流类消息可重试）
			return fmt.Errorf("阿里云拒绝发送: %s", r.ErrorMessage)
		}
		return nil
	}
	err, retried := retryOnceOnTransient(send)
	if retried {
		t.Logf("首次发送为瞬时错误，休整 2 秒后已重试一次")
	}
	if err != nil {
		t.Fatalf("阿里云短信真发送失败（真金计费档，排查方向：签名/模板变量/手机号/流控）: %v", err)
	}
	if res == nil || res.MessageID == "" {
		t.Fatalf("阿里云短信请求已通过但未返回 BizId: %+v", res)
	}
	t.Logf("发送被接受（真金计费已产生）: BizId=%s provider=%s", res.MessageID, res.Provider)

	// —— 发送闭环段（change: sms-query-details task 1.2）——
	// 以 BizId+phone+当日日期轮询 QuerySendDetails，直到某条明细 SendStatus
	// 出终态（非等待回执）。查询直连 aliyun 层（设计 D1：厂商特有能力不经
	// sms.Provider 发送抽象层），复用本用例顶部已构造的 client。
	as := aliyun.NewSms(client)

	// 轮询纪律：间隔 3s、10 轮（含查询耗时上限约 30s）；每轮重取当日日期
	// （time.Now().Format("20060102")）防跨日界——跨日后按旧日期查询会查
	// 不到记录。查询瞬时失败（err 或 Success=false）不终止轮询，记日志后
	// 继续下一轮。
	foundRecord := false  // 至少一轮以 BizId 精确查得该条发送记录（TotalCount≥1）
	reachedFinal := false // 明细已出终态（SendStatus 非等待回执）
	for round := 1; round <= 10; round++ {
		if round > 1 {
			time.Sleep(3 * time.Second)
		}
		qr, qerr := as.QuerySendDetails(context.Background(), phone, time.Now().Format("20060102"), res.MessageID, 10, 1)
		if qerr != nil {
			t.Logf("闭环第 %d 轮查询网络层失败，继续轮询: %v", round, qerr)
			continue
		}
		if qr == nil || !qr.Success {
			t.Logf("闭环第 %d 轮查询被厂商拒绝，继续轮询: %+v", round, qr)
			continue
		}
		if qr.TotalCount >= 1 {
			foundRecord = true
		}
		for _, d := range qr.Details {
			if d.SendStatus == aliyun.SmsStatusWaiting {
				continue
			}
			reachedFinal = true
			switch d.SendStatus {
			case aliyun.SmsStatusFailed:
				t.Logf("闭环第 %d 轮明细出终态: 发送失败（ErrCode=%q），停止轮询", round, d.ErrCode)
			case aliyun.SmsStatusSuccess:
				t.Logf("闭环第 %d 轮明细出终态: 发送成功（ErrCode=%q ReceiveDate=%s），停止轮询", round, d.ErrCode, d.ReceiveDate)
			default:
				t.Logf("闭环第 %d 轮明细出非等待状态 SendStatus=%d，停止轮询", round, d.SendStatus)
			}
			break
		}
		if reachedFinal {
			break
		}
	}
	// 形态级断言：以 BizId 精确查询可得该条发送记录（TotalCount≥1）。
	// 终态不构成断言：超时不判失败——回执延迟属厂商侧，查得记录即已
	// 验证发送→查询的闭环链路（本用例目标）。
	if !foundRecord {
		t.Fatalf("闭环轮询约 30s 内未查到该发送记录（BizId=%s 查询 TotalCount 恒为 0），查询链路异常", res.MessageID)
	}
	if !reachedFinal {
		t.Logf("闭环约 30s 未出回执终态：回执延迟属厂商侧，不判失败；已查得该 BizId 发送记录，闭环链路验证完成")
	}
}

// TestSmokeSmsQueryDetails 发送明细查询冒烟（read 档，零计费）：
// 验证 QuerySendDetails 契约——查询参数形态（手机号、sendDate 请求格式
// yyyyMMdd、分页）被厂商接受。查询是厂商特有能力，直连 aliyun 层而不经
// sms.Provider 发送抽象层（change sms-query-details 设计 D1）。
func TestSmokeSmsQueryDetails(t *testing.T) {
	// read 档（查询接口不计费，纯读）：默认档即执行
	smokeWant(t, "read")

	// 五凭证与 TestSmokeSmsRealSend 同一套（经环境变量整组注入，
	// 保持 Skip 条件一致），任一缺失即 Skip
	ak := os.Getenv("SMS_AK")
	sk := os.Getenv("SMS_SK")
	sign := os.Getenv("SMS_SIGN")
	templateCode := os.Getenv("SMS_TEMPLATE_CODE")
	phone := os.Getenv("SMS_TEST_PHONE")
	if ak == "" || sk == "" || sign == "" || templateCode == "" || phone == "" {
		t.Skip("缺少阿里云短信凭证：需同时注入 SMS_AK / SMS_SK / SMS_SIGN / SMS_TEMPLATE_CODE / SMS_TEST_PHONE")
	}

	client := aliyun.NewClient(aliyun.Credentials{
		AccessKeyID:     ak,
		AccessKeySecret: sk,
	})
	as := aliyun.NewSms(client)

	// sendDate 取昨日：近 7 天内（厂商支持近 30 天）且整天已过，
	// 请求格式 yyyyMMdd（与响应明细 SendDate 的 yyyy-MM-dd HH:mm:ss 不对称）
	sendDate := time.Now().AddDate(0, 0, -1).Format("20060102")

	// bizID 空 = 不限流水，查该手机号 sendDate 当日全量明细
	res, err := as.QuerySendDetails(context.Background(), phone, sendDate, "", 10, 1)
	if err != nil {
		t.Fatalf("QuerySendDetails 请求失败（网络/本地层）: %v", err)
	}
	if res == nil {
		t.Fatalf("QuerySendDetails 无错误但返回 nil 结果")
	}
	if !res.Success {
		t.Fatalf("QuerySendDetails 被厂商拒绝: code=%s msg=%s requestID=%s", res.ErrorCode, res.ErrorMessage, res.RequestID)
	}
	// 形态级断言：TotalCount≥0、Details 非 nil（空态为空切片是合法形态）；
	// 记录存在性不校验——查询号当日无发送是合法空态
	if res.TotalCount < 0 {
		t.Fatalf("QuerySendDetails TotalCount 为负: %d", res.TotalCount)
	}
	if res.Details == nil {
		t.Fatalf("QuerySendDetails Details 为 nil（契约要求空态为空切片）")
	}
	t.Logf("查询被接受: sendDate=%s TotalCount=%d Details=%d 条（记录存在性不校验，当日无发送是合法空态）",
		sendDate, res.TotalCount, len(res.Details))
}
