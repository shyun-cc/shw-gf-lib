//go:build smoke

// tencent 组件（腾讯地图 WebService API client）真 API 冒烟用例
// （change: rdev-testing-infra，task 3.2，档位 read）。
//
// 覆盖两类端点的契约层——厂商接受请求形态（HTTP 成功 + status==0 +
// 结构可解析），不断言业务数据（地址文案、POI 列表内容等会变化的值）：
//   - 逆地址解析 geocoder（真坐标换地址）；
//   - 关键词搜索 place search（城市内检索）。
//
// 组件导出面为通用 Get(ctx, path, query)（status==0 判定成功，非 0 内部
// 重试 3 次后报错），用例经其发起真实请求；瞬时错误（限频/超时）由
// retryOnceOnTransient 再兜底一次，与契约失败分类判定。
//
// 凭证：环境变量 TENCENT_MAP_KEY（与 region 组件共用同一腾讯位置服务
// key，变量清单见 tests/doc.go），缺失时 Skip。SK 签名路径不依赖外部
// 凭证（组件契约：SK 为空不签名，签名逻辑已由 tencent 包单测覆盖）。

package tests

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/shyun-cc/shw-gf-lib/tencent"
)

// smokeTencentTimeout 单次用例的 context 预算：组件 Get 内部最多 3 次
// 尝试 + 2 次 2 秒重试间隔，预算须覆盖整轮内部重试。
const smokeTencentTimeout = 30 * time.Second

// smokeTencentClient 凭证检查并构造 client；缺 TENCENT_MAP_KEY 时 Skip。
func smokeTencentClient(t *testing.T) *tencent.Client {
	t.Helper()
	key := os.Getenv("TENCENT_MAP_KEY")
	if key == "" {
		t.Skip("缺少 TENCENT_MAP_KEY：跳过腾讯地图真 API 冒烟（凭证清单见 tests/doc.go）")
	}
	return tencent.New(key, "")
}

// tencentStatus 从响应 payload 提取厂商业务码 status。组件 Get 成功路径
// status 恒为 0；payload 非 nil 时提取类型断言失败即结构不可解析，返回
// -1 交给断言层拒绝；payload 为 nil（请求已失败）返回 0，由 err 分支报错。
func tencentStatus(resp map[string]any) int {
	if v, ok := resp["status"].(float64); ok {
		return int(v)
	}
	if resp != nil {
		return -1
	}
	return 0
}

// smokeTencentGet 经 retryOnceOnTransient 发起真 API 调用，assertAccepted
// 断言契约层后返回成功 payload；失败路径不会返回（断言层已 Fatal）。
func smokeTencentGet(t *testing.T, name, path string, query map[string]string) map[string]any {
	t.Helper()
	client := smokeTencentClient(t)
	var resp map[string]any
	err, retried := retryOnceOnTransient(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), smokeTencentTimeout)
		defer cancel()
		var e error
		resp, e = client.Get(ctx, path, query)
		return e
	})
	if retried {
		t.Logf("%s: 首次请求遇瞬时错误（限频/超时类），休整 %s 后重试一次判定", name, transientRetryBackoff)
	}
	assertAccepted(t, name, err, tencentStatus(resp))
	return resp
}

// TestSmokeTencentGeocoder 逆地址解析（geocoder）：真坐标换地址，纯读端点。
// 契约断言之外仅校验顶层 result 字段存在（结构可解析），不断言地址文案。
func TestSmokeTencentGeocoder(t *testing.T) {
	smokeWant(t, "read")
	resp := smokeTencentGet(t, "逆地址解析 geocoder", "/ws/geocoder/v1/", map[string]string{
		"location": "39.984154,116.307490", // 腾讯文档经典示例坐标（北京）
		"get_poi":  "0",
	})
	if _, ok := resp["result"]; !ok {
		t.Fatalf("逆地址解析 geocoder: 响应缺少 result 字段，结构不可解析: %v", resp)
	}
}

// TestSmokeTencentPlaceSearch 关键词搜索（place search）：城市内检索，纯读端点。
// 契约断言之外仅校验顶层 data 字段存在（结构可解析），不断言 POI 内容。
func TestSmokeTencentPlaceSearch(t *testing.T) {
	smokeWant(t, "read")
	resp := smokeTencentGet(t, "关键词搜索 place search", "/ws/place/v1/search", map[string]string{
		"keyword":  "公园",
		"boundary": "region(北京,0)",
	})
	if _, ok := resp["data"]; !ok {
		t.Fatalf("关键词搜索 place search: 响应缺少 data 字段，结构不可解析: %v", resp)
	}
}
