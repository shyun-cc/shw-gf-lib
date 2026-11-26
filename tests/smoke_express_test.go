//go:build smoke

// 快递100（express/kd100）真 API 冒烟用例（change: rdev-testing-infra，task 3.3）。
//
// 凭证与查询资产（全经环境变量注入，缺即 Skip）：
//   - KD100_CUSTOMER / KD100_KEY：企业客户 ID 与授权码（查询类签名
//     sign=UPPER(MD5(param+key+customer)) 只用这两项；secret 仅下单域签名
//     用，本用例不注入）；
//   - KD100_TRACK_NO：真实运单号（可选）——组件 Provider 9 方法中无
//     「公司列表」类免单号读端点，Track 必须提供 com+num，故未注入时
//     Skip 而非降级查别的端点（以组件实际导出为准）；
//   - KD100_COM：运单号所属快递公司编码（可选）——未注入按 jd（京东）
//     查询，单号非京东时会以厂商业务码拒绝，届时注入与单号匹配的编码
//     （如 zhongtong / sf / yunda，全量表见快递100文档）。
//
// 档位 read：实时查询吃少量查询配额，属读路径，无写入副作用。
package tests

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/shyun-cc/shw-gf-lib/express/kd100"
)

// kd100TrackCode 从快递100实时查询响应中提取厂商业务码：0 表示契约层
// 通过（查询成功）。失败响应有两种形态，returnCode 优先（result=false
// 授权/参数错误），其次 status 非 200（如 201 查询无结果）；成功响应
// status=200 且无 returnCode，提取为 0。
func kd100TrackCode(resp map[string]any) int {
	if rc, ok := resp["returnCode"].(string); ok {
		if n, err := strconv.Atoi(rc); err == nil {
			return n
		}
	}
	if st, ok := resp["status"].(string); ok {
		if n, err := strconv.Atoi(st); err == nil && n != 200 {
			return n
		}
	}
	return 0
}

// TestSmokeExpressTrack 快递100 实时查询真 API：真实运单号走通 Track
// （签名/报文/凭证形态被厂商接受），断言止于契约层，轨迹内容仅记录不断言。
func TestSmokeExpressTrack(t *testing.T) {
	smokeWant(t, "read")

	customer := os.Getenv("KD100_CUSTOMER")
	key := os.Getenv("KD100_KEY")
	if customer == "" || key == "" {
		t.Skip("missing KD100_CUSTOMER / KD100_KEY")
	}
	trackNo := os.Getenv("KD100_TRACK_NO")
	if trackNo == "" {
		t.Skip("missing KD100_TRACK_NO（组件无免单号读端点，注入真实运单号后执行真单查询）")
	}
	com := os.Getenv("KD100_COM")
	if com == "" {
		com = "jd"
		t.Logf("KD100_COM 未注入，按默认 jd（京东）查询；单号非京东被拒时请注入与单号匹配的快递公司编码")
	}

	// secret 仅下单域签名使用，查询域传空。
	client := kd100.New(key, "", customer)
	// 组件内部 HTTP 异常重试至多 3 次（间隔 2s），helper 层再叠一次瞬时
	// 重试，预算放宽到 60s 避免长尾超时误判。
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var resp map[string]any
	err, retried := retryOnceOnTransient(func() error {
		var e error
		resp, e = client.Track(ctx, com, trackNo)
		return e
	})
	if retried {
		t.Logf("首次请求遇瞬时错误（限流/超时），休整后重试一次")
	}
	assertAccepted(t, "快递100实时查询", err, kd100TrackCode(resp))

	// 契约层已通过：签名与凭证形态被厂商接受。以下仅记录响应形态供取证，
	// 不断言业务数据（轨迹条数受单号在途状态影响，空数组不算失败）。
	msg, _ := resp["message"].(string)
	nu, _ := resp["nu"].(string)
	tracks := 0
	if data, ok := resp["data"].([]any); ok {
		tracks = len(data)
	}
	t.Logf("查询成功：com=%s nu=%s message=%q 轨迹条数=%d", com, nu, msg, tracks)
}
