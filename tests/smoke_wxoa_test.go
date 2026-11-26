//go:build smoke

// wxoa（微信公众号）真 API 冒烟测试（change: rdev-testing-infra，task 3.7）。
//
// 凭证经环境变量注入（缺失则 Skip，不阻塞）：WXOA_APPID / WXOA_SECRET。
// Redis 支撑（token 缓存与并发去重）：REDIS_ADDR 指向真实 Redis，未设置时
// 降级进程内 miniredis（token 缓存机制的正确性已由主库单测覆盖，冒烟里
// Redis 只是 token 管理器的支撑件，两种形态均可）。
//
// 用例分档（经 smoke_helper_test.go 档位体系）：
//   - read  TestSmokeWxoaToken：access_token 经组件 token 管理器
//     GetAccessToken 获取（缓存 + redislock 并发去重本身是被验证契约），
//     并以 GetMaterialCount 纯读端点验证 token 实际可用；
//   - write TestSmokeWxoaMaterialUploadDelete：素材上传 + 删除成对。
//     v0.8.0 审计债主暴露用例——「素材双死路」（临时 /cgi-bin/media/upload
//     与永久 /cgi-bin/material/add_material 两条上传路）与「multipart 中文
//     文件名转义」（Content-Disposition filename 含中文字节时的报文组装）
//     两缺陷任一存在时本用例 FAIL，即冒烟有效性证据；首跑暴露后按
//     task 4.2 以 knownDefect skip 收敛。
//
// 注意：测试号需在微信公众平台后台将出口 IP 加入白名单，否则微信侧拒绝
// 属环境问题而非组件缺陷（断言报错信息可分辨 errcode 40164 类来源 IP）。
package tests

import (
	"context"
	"encoding/base64"
	"os"
	"sync"
	"testing"
	"time"

	// 注册 gredis 的 go-redis adapter：缺少它 g.Redis() 因无 adapter 返回
	// nil（gins 在首次 g.Redis() 时固化 adapter 进包级单例，必须提前 import；
	// 同主库 internal/testredis 与 tests/seqnum_recovery_test.go 的处理方式，
	// integration 与 smoke 构建标签正交，各自文件需自行注册）。
	"github.com/alicebob/miniredis/v2"
	_ "github.com/gogf/gf/contrib/nosql/redis/v2"
	"github.com/gogf/gf/v2/database/gredis"

	"github.com/shyun-cc/shw-gf-lib/wxoa"
)

// smokeWxoaTinyPNG 最小合法 1x1 PNG（67 字节）：素材上传对内容仅要求合法
// 图片字节，1x1 足矣——最小化对测试号素材额度与流量的占用。
const smokeWxoaTinyPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhK+mKAAAABJRU5ErkJggg=="

// smokeWxoaFilename 上传素材文件名，含中文字符：正是 v0.8.0 债主
// 「multipart 中文文件名转义」的暴露面——filename 进入 Content-Disposition
// 报文头，转义处理有缺陷时微信侧会以 errcode 拒绝（如 44002/40005 类）。
const smokeWxoaFilename = "测试素材.jpg"

// miniredis 进程级单例：只启动一次、常驻不关闭。
// gins.Redis() 按分组缓存已创建的 *gredis.Redis 实例且包外无法清除，
// 多个用例各自换新实例会使缓存指向已关闭的 miniredis（同主库
// internal/testredis 的教训，详见 tests/seqnum_recovery_test.go 注释）。
var (
	smokeWxoaMrOnce sync.Once
	smokeWxoaMr     *miniredis.Miniredis
	smokeWxoaMrErr  error
)

// setupWxoaRedis 为 wxoa token 管理器准备 g.Redis() 底座：
// REDIS_ADDR 已设置则用真实 Redis；否则降级进程内 miniredis。
// SetConfig 幂等且进程内各用例指向同一目标，重复调用无副作用。
func setupWxoaRedis(t *testing.T) {
	t.Helper()
	if addr := os.Getenv("REDIS_ADDR"); addr != "" {
		gredis.SetConfig(&gredis.Config{Address: addr})
		return
	}
	smokeWxoaMrOnce.Do(func() {
		smokeWxoaMr, smokeWxoaMrErr = miniredis.Run()
	})
	if smokeWxoaMrErr != nil {
		t.Fatalf("启动 miniredis 失败（REDIS_ADDR 未设置时的兜底方案）: %v", smokeWxoaMrErr)
	}
	gredis.SetConfig(&gredis.Config{Address: smokeWxoaMr.Addr()})
}

// smokeWxoaService 凭证检查 + 构造服务：WXOA_APPID / WXOA_SECRET 任一缺失即
// Skip（缺凭证资产对应用例保持 Skip，不阻塞——change 任务 0.3/4.1 的约定）。
func smokeWxoaService(t *testing.T) *wxoa.WxOAService {
	t.Helper()
	appID, secret := os.Getenv("WXOA_APPID"), os.Getenv("WXOA_SECRET")
	if appID == "" || secret == "" {
		t.Skip("未设置 WXOA_APPID / WXOA_SECRET，跳过 wxoa 冒烟测试")
	}
	setupWxoaRedis(t)
	return wxoa.NewWxOAService(appID, secret)
}

// smokeWxoaErrcode 从微信应答 map 提取 errcode（无 errcode 字段视为 0）。
// invoke/invokeMultipart 已把 errcode≠0 转为 BizError 随 err 返回，此处
// 二次提取交给 assertAccepted，双保险维持契约层断言语义完整。
func smokeWxoaErrcode(resp map[string]any) int {
	if v, ok := resp["errcode"].(float64); ok {
		return int(v)
	}
	return 0
}

// TestSmokeWxoaToken read 档：access_token 经组件 token 管理器获取。
//
// 被验证契约：GetAccessToken 的完整链路（真实 fetch /cgi-bin/token →
// g.Redis() 缓存回写 → 缓存命中直返）。获取后再调 GetMaterialCount
// 纯读端点，验证 token 实际可用且第二次调用走缓存路径（不重复 fetch）。
func TestSmokeWxoaToken(t *testing.T) {
	smokeWant(t, "read")
	svc := smokeWxoaService(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// token 获取：瞬时抖动（超时/限流）休整后重试一次
	var token string
	err, retried := retryOnceOnTransient(func() error {
		var e error
		token, e = svc.GetAccessToken(ctx)
		return e
	})
	if retried {
		t.Log("wxoa access_token 获取遇到瞬时错误，休整后重试一次")
	}
	assertAccepted(t, "wxoa access_token 获取", err, 0)
	if token == "" {
		t.Fatalf("wxoa access_token 获取成功但返回空 token，契约层异常")
	}
	t.Logf("access_token 获取成功（长度 %d）", len(token))

	// token 可用性验证：纯读端点 GetMaterialCount（同时覆盖缓存命中路径）
	var count map[string]any
	err, retried = retryOnceOnTransient(func() error {
		var e error
		count, e = svc.GetMaterialCount(ctx)
		return e
	})
	if retried {
		t.Log("wxoa 素材计数查询遇到瞬时错误，休整后重试一次")
	}
	assertAccepted(t, "wxoa 永久素材总数查询", err, smokeWxoaErrcode(count))
	t.Logf("永久素材计数: %v", count)
}

// TestSmokeWxoaMaterialUploadDelete write 档：素材上传 + 删除成对，
// 中文文件名暴露面（v0.8.0 债主：素材双死路、multipart 中文文件名转义）。
//
// 链路覆盖两条上传路 + 删除路：
//  1. 临时素材 UploadTempMedia（/cgi-bin/media/upload）：断言 media_id 获取
//     成功——临时素材无删除端点（微信侧 3 天自然过期），不做删除；
//  2. 永久素材 AddMaterial（/cgi-bin/material/add_material）：断言 media_id
//     获取成功后 DelMaterial 删除并断言删除被接受，传删成对自清理；
//     t.Cleanup 兜底尽力清理（中途断言失败时不留残留素材）。
func TestSmokeWxoaMaterialUploadDelete(t *testing.T) {
	smokeWant(t, "write")
	svc := smokeWxoaService(t)

	pngBytes, err := base64.StdEncoding.DecodeString(smokeWxoaTinyPNG)
	if err != nil {
		t.Fatalf("内嵌 1x1 PNG base64 解码失败（测试自身问题）: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// ① 临时素材上传（中文文件名 multipart）——「素材双死路」其中一路
	var tempResp map[string]any
	err, retried := retryOnceOnTransient(func() error {
		var e error
		tempResp, e = svc.UploadTempMedia(ctx, "image", smokeWxoaFilename, pngBytes)
		return e
	})
	if retried {
		t.Log("wxoa 临时素材上传遇到瞬时错误，休整后重试一次")
	}
	assertAccepted(t, "wxoa 临时素材上传（中文文件名）", err, smokeWxoaErrcode(tempResp))
	tempMediaID, _ := tempResp["media_id"].(string)
	if tempMediaID == "" {
		t.Fatalf("wxoa 临时素材上传应答缺少 media_id: %v", tempResp)
	}
	t.Logf("临时素材上传成功，media_id=%s（3 天自然过期，无删除端点）", tempMediaID)

	// ② 永久素材上传（中文文件名 multipart）——「素材双死路」另一路
	var upResp map[string]any
	err, retried = retryOnceOnTransient(func() error {
		var e error
		upResp, e = svc.AddMaterial(ctx, "image", smokeWxoaFilename, pngBytes, nil)
		return e
	})
	if retried {
		t.Log("wxoa 永久素材上传遇到瞬时错误，休整后重试一次")
	}
	assertAccepted(t, "wxoa 永久素材上传（中文文件名）", err, smokeWxoaErrcode(upResp))
	mediaID, _ := upResp["media_id"].(string)
	if mediaID == "" {
		t.Fatalf("wxoa 永久素材上传应答缺少 media_id: %v", upResp)
	}
	t.Logf("永久素材上传成功，media_id=%s", mediaID)

	// 兜底清理：正常删除（③）未执行时（中途断言失败等）尽力删除已上传素材。
	// Cleanup 里用独立 ctx（原 ctx 可能已超时）；失败只记录不计失败——
	// 尽力而为的清理不是断言面。
	deleted := false
	t.Cleanup(func() {
		if deleted || mediaID == "" {
			return
		}
		if _, e := svc.DelMaterial(context.Background(), mediaID); e != nil {
			t.Logf("永久素材兜底清理失败（测试号素材库可能残留 media_id=%s，请手动清理）: %v", mediaID, e)
		}
	})

	// ③ 永久素材删除——传删成对收口，断言删除被微信侧接受
	var delResp map[string]any
	err, retried = retryOnceOnTransient(func() error {
		var e error
		delResp, e = svc.DelMaterial(ctx, mediaID)
		return e
	})
	if retried {
		t.Log("wxoa 永久素材删除遇到瞬时错误，休整后重试一次")
	}
	assertAccepted(t, "wxoa 永久素材删除", err, smokeWxoaErrcode(delResp))
	deleted = true
	t.Log("永久素材删除成功，传删成对收口，素材无残留")
}
