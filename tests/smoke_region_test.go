//go:build smoke

// region 组件冒烟用例（change: rdev-testing-infra，task 3.1）。
//
// 覆盖两件事（均为 read 档纯读，真腾讯位置服务 API）：
//   - 全量行政区划采集走通：Collector.Fetch 全链路（list 拉平 + 按市分型补街
//     getchildren + 直辖市 level 修正 + 补零 + path 补全），并批量断言采集结果
//     坐标非零——v0.8.0 审计缺陷「region 坐标落 0」（location 嵌套对象被按平铺
//     key 解析导致全量落零值）的暴露用例；
//   - 真坐标批量逆地理查询：geocoder（坐标→地址）对多个真实城市中心坐标逐一
//     查询，断言请求形态被接受、响应结构可解析、返回坐标非零。
//
// 断言纪律：只断言「API 接受请求形态 + 结构可解析 + 坐标非零」契约层，不断言
// 具体行政区名称、地址文本等业务数据。网络调用一律包 retryOnceOnTransient。
//
// 凭证经环境变量注入构造参数（region/tencent 组件无 SetConfig，tencent.New
// 构造参数即注入形态）：TENCENT_MAP_KEY 必需，缺失 Skip；TENCENT_MAP_SK 可选
// （设置走 SK 签名路径，空走无签名路径——两者都是组件契约）。
package tests

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/shyun-cc/shw-gf-lib/region"
	"github.com/shyun-cc/shw-gf-lib/tencent"
)

// tencentMapClient 从环境变量构造腾讯地图客户端：TENCENT_MAP_KEY 缺失时
// Skip（经环境变量提供，缺失不阻塞）。
func tencentMapClient(t *testing.T) *tencent.Client {
	t.Helper()
	key := os.Getenv("TENCENT_MAP_KEY")
	if key == "" {
		t.Skip("missing TENCENT_MAP_KEY: 腾讯位置服务 key 未注入，region 冒烟跳过")
	}
	return tencent.New(key, os.Getenv("TENCENT_MAP_SK"))
}

// TestSmokeRegionCollectAll 全量行政区划采集走通（真腾讯位置服务 API）。
//
// 采集保持默认 200ms getchildren 限速（QPS=5 腾讯平台硬限，SetInterval(0)
// 仅包内单测加速用，真 API 不得注入加速）；全量约 117 次补拉为半分钟量级，
// 超时预算放宽到 10 分钟容纳网络抖动与客户端内置重试。
func TestSmokeRegionCollectAll(t *testing.T) {
	smokeWant(t, "read")
	client := tencentMapClient(t)

	src := region.NewTencentSource(client)
	var regions []region.Region
	err, retried := retryOnceOnTransient(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		var e error
		regions, e = region.NewCollector(src).Fetch(ctx)
		return e
	})
	if retried {
		t.Log("全量采集首试遇瞬时错误（限流/超时），休整后重试")
	}
	// 契约层：Client.Get 已保证 status==0（非 0 即报错），err==nil 即请求
	// 形态（list/getchildren + key/签名）被腾讯接受、完整性 fail-fast 未触发
	assertAccepted(t, "region 全量行政区划采集", err, 0)

	if len(regions) < 3000 {
		t.Fatalf("全量采集节点数异常：仅 %d 个，全国省/市/区县/街道应远大于 3000", len(regions))
	}

	// 结构契约：三级层级各有节点、adcode 统一 9 位（右侧补零）、path 为
	// /祖先…/自身/ 物化路径形态（Collector 补全）
	levelCount := map[int]int{}
	for _, r := range regions {
		levelCount[r.Level]++
		if len(r.Adcode) != 9 {
			t.Fatalf("adcode 应统一 9 位（6 位右侧补零），实际 %q", r.Adcode)
		}
		if !strings.HasPrefix(r.Path, "/") || !strings.HasSuffix(r.Path, "/") || !strings.Contains(r.Path, r.Adcode) {
			t.Fatalf("path 应为 /祖先…/自身/ 形态且含自身 9 位码，实际 adcode=%s path=%q", r.Adcode, r.Path)
		}
	}
	for _, lv := range []int{1, 2, 3} {
		if levelCount[lv] == 0 {
			t.Fatalf("采集结果缺 level=%d 层级节点（层级分布: %v）", lv, levelCount)
		}
	}

	// 坐标非零（v0.8.0 审计缺陷「region 坐标落 0」暴露用例）：location 嵌套
	// 对象若被按平铺 key 解析会全量落零。容忍个别数据源固有零值节点，双非零
	// 占比应压倒性多数（阈值 90%）；省级节点（一级行政区均有中心点）逐一断言。
	nonZero, provinceTotal := 0, 0
	for _, r := range regions {
		if r.Lat != 0 && r.Lng != 0 {
			nonZero++
		}
		if r.Level == 1 {
			provinceTotal++
			if r.Lat == 0 || r.Lng == 0 {
				t.Fatalf("省级节点坐标落零（adcode=%s fullname=%s）：location 嵌套解析疑似回退为平铺 key", r.Adcode, r.Fullname)
			}
		}
	}
	ratio := float64(nonZero) / float64(len(regions))
	if ratio < 0.9 {
		t.Fatalf("坐标非零占比 %.1f%%（%d/%d）异常偏低：「region 坐标落 0」缺陷疑似回退", ratio*100, nonZero, len(regions))
	}
	t.Logf("全量采集 %d 节点：层级分布 %v，坐标非零 %d 个（%.1f%%，省级 %d 个全非零）",
		len(regions), levelCount, nonZero, ratio*100, provinceTotal)
}

// smokeRealCoords 批量逆地理查询输入：真实城市中心坐标（地理公共事实，
// 非项目参数），覆盖不同经纬度区间。
var smokeRealCoords = [][2]float64{
	{39.904, 116.407}, // 北京
	{31.230, 121.474}, // 上海
	{23.129, 113.264}, // 广州
	{43.792, 87.617},  // 乌鲁木齐
}

// TestSmokeRegionGeocoderByRealCoord 真坐标批量逆地理查询（geocoder，
// 坐标→地址）：断言请求形态被接受、result.location 结构可解析、返回坐标
// 非零（规范化坐标契约）。不校验具体地址文本——业务数据不断言。
func TestSmokeRegionGeocoderByRealCoord(t *testing.T) {
	smokeWant(t, "read")
	client := tencentMapClient(t)

	for _, c := range smokeRealCoords {
		loc := fmt.Sprintf("%f,%f", c[0], c[1])
		t.Run(loc, func(t *testing.T) {
			var payload map[string]any
			err, retried := retryOnceOnTransient(func() error {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				data, e := client.Get(ctx, "/ws/geocoder/v1", map[string]string{"location": loc})
				if e != nil {
					return e
				}
				payload = data
				return nil
			})
			if retried {
				t.Log("逆地理查询首试遇瞬时错误（限流/超时），休整后重试")
			}
			// 契约层：从原始响应提取厂商业务码走统一断言（err 非 nil 时
			// code 无意义，由 assertAccepted 先行报错）
			var code int
			if err == nil {
				if s, ok := payload["status"].(float64); ok {
					code = int(s)
				}
			}
			assertAccepted(t, "逆地理编码（location="+loc+"）", err, code)

			result, _ := payload["result"].(map[string]any)
			if len(result) == 0 {
				t.Fatalf("逆地理响应缺 result 结构: %v", payload)
			}
			location, _ := result["location"].(map[string]any)
			if len(location) == 0 {
				t.Fatalf("逆地理响应 result 缺 location 结构: %v", result)
			}
			lat, _ := location["lat"].(float64)
			lng, _ := location["lng"].(float64)
			if lat == 0 || lng == 0 {
				t.Fatalf("逆地理返回坐标落零（输入 %s，返回 (%v, %v)）：坐标契约不成立", loc, lat, lng)
			}
			t.Logf("逆地理 %s → 规范化坐标 (%.6f, %.6f)", loc, lat, lng)
		})
	}
}
