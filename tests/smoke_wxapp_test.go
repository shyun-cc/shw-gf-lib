//go:build smoke

// wxapp（微信小程序服务端 API）真 API 冒烟测试（change: rdev-testing-infra，task 3.8）。
//
// 凭证全部经环境变量注入（清单见 tests/doc.go）：
//   - WXAPP_APPID / WXAPP_SECRET：小程序测试号（read 档）；测试号后台需把
//     运行机 IP 加入白名单，否则 token 获取被微信以 errcode 拒绝；
//   - WXAPP_LOGIN_CODE：人工门变量——code2session 的 code 只能由真人扫码
//     产生且数分钟失效，无法全自动获取，故仅当 SMOKE_LEVEL 达 costly 且
//     该变量非空时才执行，否则 Skip 注明人工门两个前置条件；
//   - REDIS_ADDR（可选）：token 管理器依赖 g.Redis() 缓存；未设置时降级
//     进程内 miniredis（缓存共享契约与真实 Redis 一致）。
//
// 断言限定契约层（assertAccepted）：微信接受请求形态（凭证有效、IP 白名单
// 放行、报文组装正确），不断言业务数据。token 用例额外验证 token 管理器的
// 缓存共享契约（连续多次获取命中同一缓存值、跨服务实例共享、键已回写 Redis）。
//
// 运行方式（见 doc.go 包注释）：
//
//	cd tests
//	WXAPP_APPID=... WXAPP_SECRET=... \
//	  GOWORK=off go test -tags smoke -run TestSmokeWxApp -v
//	# code2session 人工门（costly 档 + 真人扫码取得 code 后手工注入）：
//	SMOKE_LEVEL=costly WXAPP_LOGIN_CODE=... \
//	  GOWORK=off go test -tags smoke -run TestSmokeWxAppCode2Session -v
package tests

import (
	"context"
	"errors"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	// 注册 gredis 的 go-redis adapter：缺少它 g.Redis() 因无 adapter 返回 nil
	// 且 gins 首次调用即固化单例，必须 blank import 提前注册（同 seqnum 集成测试）。
	_ "github.com/gogf/gf/contrib/nosql/redis/v2"
	"github.com/gogf/gf/v2/database/gredis"
	"github.com/gogf/gf/v2/frame/g"

	"github.com/shyun-cc/shw-gf-lib/wxapp"
)

// wxappTokenCacheKeyPrefix 对应 wxapp 包 accessTokenKeyPrefix（未导出）：
// 清理/核验缓存键时引用同一约定，键前缀变更时需同步改此处。
const wxappTokenCacheKeyPrefix = "wxapp:access_token:"

var (
	wxappRedisOnce sync.Once
	wxappMr        *miniredis.Miniredis
	wxappMrErr     error
)

// setupWxAppRedis 为 wxapp token 管理器注入 GoFrame 默认分组的 Redis 配置：
// 设置了 REDIS_ADDR 则指向真实 Redis（远程现场容器）；未设置则降级进程内
// miniredis。gins 的 Redis 实例按分组缓存且无法从包外清除，miniredis 必须
// 做进程级单例（同 seqnum 集成测试 setupRedis 的教训形态，两文件构建标签
// 正交、各自持有独立单例）。
func setupWxAppRedis(t *testing.T) {
	t.Helper()

	if addr := os.Getenv("REDIS_ADDR"); addr != "" {
		gredis.SetConfig(&gredis.Config{Address: addr})
		return
	}
	wxappRedisOnce.Do(func() {
		wxappMr, wxappMrErr = miniredis.Run()
	})
	if wxappMrErr != nil {
		t.Fatalf("启动 miniredis 失败（REDIS_ADDR 未设置时的兜底方案）: %v", wxappMrErr)
	}
	gredis.SetConfig(&gredis.Config{Address: wxappMr.Addr()})
}

// wxappTokenPrefix 截取 token 前 8 字符用于失败日志（避免完整凭证进日志，且防越界）。
func wxappTokenPrefix(tok string) string {
	if len(tok) > 8 {
		return tok[:8]
	}
	return tok
}

// wxappBizErrCode 从错误中提取微信业务码：*BizError 取 Errcode（err 非 nil
// 时 assertAccepted 直接以 err 判定，此值仅在错误被组件吞掉、errcode 随成功
// 响应返回的形态下生效）；其余错误（*InfraError 等）返回 0。
func wxappBizErrCode(err error) int {
	var biz *wxapp.BizError
	if errors.As(err, &biz) {
		return biz.Errcode
	}
	return 0
}

// TestSmokeWxAppAccessToken token 经组件 token 管理器获取（read 档）：
// 首次获取真打微信 /cgi-bin/token 并回写 Redis 缓存，连续第二次及另一服务
// 实例的获取应命中缓存返回同一 token（缓存共享 + 回写契约本身是被验证对象）。
func TestSmokeWxAppAccessToken(t *testing.T) {
	smokeWant(t, "read")

	appID := os.Getenv("WXAPP_APPID")
	secret := os.Getenv("WXAPP_SECRET")
	if appID == "" || secret == "" {
		t.Skip("缺凭证：WXAPP_APPID / WXAPP_SECRET 未注入，跳过 wxapp token 冒烟")
	}

	setupWxAppRedis(t)
	ctx := context.Background()

	// 清掉历史残留缓存键，确保首次获取走 fetch 路径真打微信 API
	cacheKey := wxappTokenCacheKeyPrefix + appID
	if _, err := g.Redis().Do(ctx, "DEL", cacheKey); err != nil {
		t.Fatalf("清理 wxapp token 缓存键 %s 失败: %v", cacheKey, err)
	}

	svc := wxapp.NewWxAppService(appID, secret)
	svc.HTTPClient = &http.Client{Timeout: 15 * time.Second}

	var first string
	err, retried := retryOnceOnTransient(func() error {
		var e error
		first, e = svc.GetAccessToken(ctx)
		return e
	})
	if retried {
		t.Logf("wxapp token 首试遇瞬时错误，休整重试一次后判定")
	}
	assertAccepted(t, "wxapp.getAccessToken", err, wxappBizErrCode(err))
	if first == "" {
		t.Fatalf("wxapp.getAccessToken: 契约层通过但 access_token 为空，响应形态异常")
	}

	// 连续第二次获取：应命中 Redis 缓存返回同一 token
	second, err := svc.GetAccessToken(ctx)
	assertAccepted(t, "wxapp.getAccessToken 第二次（缓存命中）", err, wxappBizErrCode(err))
	if first != second {
		t.Fatalf("wxapp.getAccessToken 两次获取 token 不一致（缓存共享契约破坏）：首次前缀 %q 第二次前缀 %q",
			wxappTokenPrefix(first), wxappTokenPrefix(second))
	}

	// 新服务实例（同 appid）：应跨实例共享同一份缓存
	another := wxapp.NewWxAppService(appID, secret)
	another.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	third, err := another.GetAccessToken(ctx)
	assertAccepted(t, "wxapp.getAccessToken 跨实例（缓存共享）", err, wxappBizErrCode(err))
	if third != first {
		t.Fatalf("wxapp.getAccessToken 跨实例 token 不一致（缓存共享契约破坏）：首次前缀 %q 跨实例前缀 %q",
			wxappTokenPrefix(first), wxappTokenPrefix(third))
	}

	// 回写契约：缓存键应已写入 Redis 且非空（Get 出错时返回 nil var，先判错再取值防 panic）
	cached, getErr := g.Redis().Get(ctx, cacheKey)
	if getErr != nil {
		t.Fatalf("wxapp token 缓存回写核验失败（键 %s 读取错误）: %v", cacheKey, getErr)
	}
	if cached.String() == "" {
		t.Fatalf("wxapp token 缓存回写核验失败（键 %s 应非空）", cacheKey)
	}
}

// TestSmokeWxAppCode2SessionManualGate code2session 人工门用例：
// 仅当 SMOKE_LEVEL 达 costly 且真人扫码取得的 WXAPP_LOGIN_CODE 已注入时
// 执行；任一前置条件不满足即 Skip（code 无法全自动产生，见文件头注释）。
func TestSmokeWxAppCode2SessionManualGate(t *testing.T) {
	code := os.Getenv("WXAPP_LOGIN_CODE")
	if smokeRank[smokeLevel(t)] < smokeRank["costly"] || code == "" {
		t.Skip("人工门：需 SMOKE_LEVEL=costly 且真人扫码注入 WXAPP_LOGIN_CODE（code 只能真人扫码产生且短时失效，无法自动化）")
	}

	appID := os.Getenv("WXAPP_APPID")
	secret := os.Getenv("WXAPP_SECRET")
	if appID == "" || secret == "" {
		t.Skip("缺凭证：WXAPP_APPID / WXAPP_SECRET 未注入，跳过 wxapp code2session 冒烟")
	}

	// code2session 走 appid+secret 直连（不经 token 管理器），无需配置 Redis
	ctx := context.Background()
	svc := wxapp.NewWxAppService(appID, secret)
	svc.HTTPClient = &http.Client{Timeout: 15 * time.Second}

	var sess *wxapp.WxAppSession
	err, retried := retryOnceOnTransient(func() error {
		var e error
		sess, e = svc.Code2Session(ctx, code)
		return e
	})
	if retried {
		t.Logf("wxapp code2session 首试遇瞬时错误，休整重试一次后判定（注：code 可能已因重试失效，若失败请换新 code 复跑）")
	}
	assertAccepted(t, "wxapp.code2session", err, wxappBizErrCode(err))
	if sess == nil || sess.OpenID == "" {
		t.Fatalf("wxapp.code2session: 契约层通过但响应缺少 openid，响应形态异常")
	}
}
