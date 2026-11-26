//go:build smoke

// wecom 企微真 API 冒烟测试（change: rdev-testing-infra，task 3.6）。
//
// 用例矩阵（read 在前、write 最后——read 链路挂掉时 write 用例跳过，不对异常链路写入）：
//   - token 获取（read）：经组件 TokenManager 真 gettoken；缓存语义三连证据——
//     回写（Redis 键值 == 返回 token）→ 二次获取命中（同值）→ 哨兵探针（预写哨兵
//     后非强刷获取原样返回哨兵，证明不触发第二次 gettoken 网络调用）；
//   - 通讯录读（read）：升级版部门列表 SimpleListDepartment（/cgi-bin/department/
//     simplelist，2022 后新注册企业旧版 department/list 不可用），断言 errcode==0
//     与响应结构含 dept_id，不断言部门数量等业务数据；
//   - 测试群文本消息（write）：SendAppChat 向 WECOM_TEST_CHATID 发文本，断言
//     errcode==0。msgid 说明：官方契约 appchat/send 成功响应仅 errcode/errmsg
//     （见 https://developer.work.weixin.qq.com/document/path/90248 ，与返回 msgid
//     的 message/send 不同），故 msgid 为弹性断言——响应含该键时断言非空。
//
// 环境变量（凭证全来自 os.Getenv，缺任一必填凭证则 Skip）：
//   - WECOM_CORP_ID / WECOM_AGENT_SECRET：自建应用凭证，缺一跳过全部用例；
//   - WECOM_AGENT_ID（可选）：自建应用 agentid，参与 token 缓存键（
//     wecom:<corpID>:<agentID>:access_token，见 docs/wecom.md），未设置按 0；
//   - WECOM_TEST_CHATID（write 用例）：测试群 chatid，缺失仅跳过 write 用例；
//   - REDIS_ADDR（可选）：token 管理器依赖 g.Redis()，未设置降级进程内 miniredis；
//   - SMOKE_LEVEL：冒烟档位（smoke_helper_test.go 契约），write 用例需 write 档。
//
// 断言纪律：只断言契约层（errcode==0、响应结构可解析），不断言部门名/成员数等
// 业务数据；网络调用经 retryOnceOnTransient 包裹（厂商限频/抖动重试一次）。
package tests

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	// 注册 gredis 的 go-redis adapter：缺少它 g.Redis() 因无 adapter 返回 nil
	// （gins 在首次 g.Redis() 时把 adapter 固化进包级单例，必须提前 import；
	// 与 seqnum 集成测试同处理方式——smoke 与 integration 构建正交，各自注册）。
	_ "github.com/gogf/gf/contrib/nosql/redis/v2"

	"github.com/alicebob/miniredis/v2"
	"github.com/gogf/gf/v2/database/gredis"
	"github.com/gogf/gf/v2/frame/g"

	"github.com/shyun-cc/shw-gf-lib/wecom"
)

// wecom 冒烟进程级共享状态：Client 与凭证只构造一次。
// token 缓存键由凭证决定（与 Client 内建 TokenManager 同键），token 用例经独立
// TokenManager 实例建立的缓存被后续用例直接复用——「gettoken 实际调用次数受 token
// 管理器缓存控制、不随用例数线性增长」（spec: smoke-testing 微信系 token 配额纪律）。
var (
	wecomSmokeClientOnce sync.Once
	wecomSmokeClient     *wecom.Client
	wecomSmokeCreds      wecom.Credential

	// wecomSmokeReadFailed read 档用例失败标志：置位后 write 用例跳过，
	// 避免 read 链路（凭证/网络/契约）异常时仍向厂商侧写入。
	wecomSmokeReadFailed bool

	wecomSmokeRedisOnce sync.Once
	wecomSmokeMr        *miniredis.Miniredis
	wecomSmokeMrErr     error
)

// wecomSmokeSetupRedis 注入 GoFrame 默认分组的 Redis 配置（token 管理器与
// redislock 均经 g.Redis() 取默认分组）：REDIS_ADDR 指向真实 Redis，未设置降级
// 进程内 miniredis。miniredis 常驻不关闭——gins 按分组缓存已创建的 *gredis.Redis
// 实例且包外无法清除，实例中途关闭会让后续 g.Redis() 全部失败（同 seqnum 集成
// 测试教训）。
func wecomSmokeSetupRedis(t *testing.T) {
	t.Helper()
	if addr := os.Getenv("REDIS_ADDR"); addr != "" {
		gredis.SetConfig(&gredis.Config{Address: addr})
	} else {
		wecomSmokeRedisOnce.Do(func() {
			wecomSmokeMr, wecomSmokeMrErr = miniredis.Run()
		})
		if wecomSmokeMrErr != nil {
			t.Fatalf("启动 miniredis 失败（REDIS_ADDR 未设置时的兜底方案）: %v", wecomSmokeMrErr)
		}
		gredis.SetConfig(&gredis.Config{Address: wecomSmokeMr.Addr()})
	}
	if _, err := g.Redis().Do(context.Background(), "PING"); err != nil {
		t.Fatalf("Redis 连接失败: %v；可设置 REDIS_ADDR 指向真实 Redis，或留空由 miniredis 兜底", err)
	}
}

// wecomSmokeSetup 凭证检查 + 一次性构造共享 Client。
// WECOM_CORP_ID / WECOM_AGENT_SECRET 缺一即 Skip 全部 wecom 冒烟用例。
func wecomSmokeSetup(t *testing.T) (*wecom.Client, wecom.Credential) {
	t.Helper()
	corpID := os.Getenv("WECOM_CORP_ID")
	secret := os.Getenv("WECOM_AGENT_SECRET")
	if corpID == "" || secret == "" {
		t.Skip("未设置 WECOM_CORP_ID / WECOM_AGENT_SECRET（企微自建应用凭证），跳过 wecom 冒烟")
	}
	wecomSmokeClientOnce.Do(func() {
		creds := wecom.Credential{
			CorpID:  corpID,
			Secret:  secret,
			AppType: wecom.AppAgent,
		}
		// WECOM_AGENT_ID 可选：不参与 gettoken，只参与 token 缓存键的应用隔离段
		if v := os.Getenv("WECOM_AGENT_ID"); v != "" {
			id, err := strconv.Atoi(v)
			if err != nil {
				t.Fatalf("WECOM_AGENT_ID=%q 非法（应为整型 agentid）: %v", v, err)
			}
			creds.AgentID = id
		}
		wecomSmokeSetupRedis(t)
		wecomSmokeClient = wecom.NewClient(creds)
		wecomSmokeCreds = creds
	})
	return wecomSmokeClient, wecomSmokeCreds
}

// wecomSmokeReadAccept read 用例专用契约断言：与 assertAccepted 同语义，
// 额外在失败时置 wecomSmokeReadFailed，联动 write 用例跳过。
func wecomSmokeReadAccept(t *testing.T, name string, err error, code int) {
	t.Helper()
	if err != nil || code != 0 {
		wecomSmokeReadFailed = true
	}
	assertAccepted(t, name, err, code)
}

// TestSmokeWecomToken token 经组件 TokenManager 真 gettoken + 缓存语义三连证据（read）。
func TestSmokeWecomToken(t *testing.T) {
	_, creds := wecomSmokeSetup(t)
	ctx := context.Background()
	mgr := wecom.NewTokenManager(creds)
	// 缓存键为文档化契约（docs/wecom.md「TokenManager 并发语义」）：
	// wecom:<corpID>:<agentID>:access_token（AppAgent 形态）
	key := fmt.Sprintf("wecom:%s:%d:access_token", creds.CorpID, creds.AgentID)

	// 清残留缓存：保证首轮真走 gettoken（断言链自洽，不依赖进程外状态）
	if _, err := g.Redis().Del(ctx, key); err != nil {
		t.Fatalf("清理 token 缓存键 %s 失败: %v", key, err)
	}

	// ① 首次获取：真 gettoken 网络调用（瞬时错误休整后重试一次）
	var token1 string
	err, retried := retryOnceOnTransient(func() error {
		var e error
		token1, e = mgr.Token(ctx, false)
		if e == nil && token1 == "" {
			return fmt.Errorf("gettoken 成功但返回空 token")
		}
		return e
	})
	if retried {
		t.Logf("wecom gettoken 首试瞬时失败，休整后重试一次")
	}
	wecomSmokeReadAccept(t, "wecom token 获取", err, 0)

	// ② 缓存回写语义：Redis 键值 == 首次返回的 token
	cached, err := g.Redis().Get(ctx, key)
	if err != nil {
		t.Fatalf("读取 token 缓存键 %s 失败（组件缓存键格式是否变更？）: %v", key, err)
	}
	if cached.String() != token1 {
		t.Fatalf("wecom token 缓存回写缺失或不一致：键 %s 缓存值与首次获取的 token 不相等", key)
	}

	// ③ 二次获取共享缓存：非强刷路径应命中缓存返回同一 token
	token2, err := mgr.Token(ctx, false)
	if err != nil {
		t.Fatalf("wecom token 二次获取失败: %v", err)
	}
	if token2 != token1 {
		t.Fatalf("wecom token 二次获取未命中共享缓存：两次返回值不一致")
	}

	// ④ 哨兵探针（不触发第二次 gettoken 的直接证据）：向缓存键写入哨兵后非强刷
	// 获取，应原样返回哨兵——若实现误走网络，gettoken 会返回真 token 而非哨兵
	const sentinel = "smoke-wecom-cache-probe"
	if _, err := g.Redis().Do(ctx, "SET", key, sentinel); err != nil {
		t.Fatalf("写入 token 缓存哨兵失败: %v", err)
	}
	token3, err := mgr.Token(ctx, false)
	if err != nil {
		t.Fatalf("wecom token 哨兵探测获取失败: %v", err)
	}
	if token3 != sentinel {
		t.Fatalf("wecom token 获取未走缓存读路径：哨兵探测返回了非哨兵值（疑似触发了第二次 gettoken 网络调用）")
	}

	// ⑤ 清哨兵还原：后续用例重新获取真 token 并回写，contact/message 用例再次共享
	if _, err := g.Redis().Del(ctx, key); err != nil {
		t.Fatalf("清理 token 缓存哨兵失败: %v", err)
	}
}

// TestSmokeWecomContact 通讯录读（read）：升级版部门列表，断言契约层与结构。
func TestSmokeWecomContact(t *testing.T) {
	client, _ := wecomSmokeSetup(t)
	ctx := context.Background()

	var resp map[string]any
	err, retried := retryOnceOnTransient(func() error {
		var e error
		resp, e = client.SimpleListDepartment(ctx, 0)
		return e
	})
	if retried {
		t.Logf("wecom 部门列表首试瞬时失败，休整后重试一次")
	}
	wecomSmokeReadAccept(t, "wecom 通讯录部门列表", err, 0)

	// 结构可解析断言（非业务数据）：升级版 simplelist 成功响应必含 dept_id 数组；
	// 不断言部门数量——那是会变化的业务数据
	if _, ok := resp["dept_id"]; !ok {
		wecomSmokeReadFailed = true
		t.Fatalf("wecom 通讯录部门列表: 响应缺少 dept_id 字段: %#v", resp)
	}
}

// TestSmokeWecomChatSend 测试群文本消息（write，放最后：read 挂了不执行）。
func TestSmokeWecomChatSend(t *testing.T) {
	smokeWant(t, "write")
	if wecomSmokeReadFailed {
		t.Skip("read 档用例已失败，跳过 write 用例：不对异常链路继续写入")
	}
	chatID := os.Getenv("WECOM_TEST_CHATID")
	if chatID == "" {
		t.Skip("未设置 WECOM_TEST_CHATID（write 档发测试群文本消息所需），跳过本用例")
	}
	client, _ := wecomSmokeSetup(t)
	ctx := context.Background()

	content := fmt.Sprintf("gf-lib wecom 冒烟测试 %s", time.Now().Format(time.RFC3339))
	var resp map[string]any
	err, retried := retryOnceOnTransient(func() error {
		var e error
		resp, e = client.SendAppChat(ctx, map[string]any{
			"chatid": chatID,
			"safe":   0,
			"msg":    wecom.TextMsg{Content: content}, // msg 键经 SendAppChat 自动展开合并
		})
		return e
	})
	if retried {
		t.Logf("wecom 测试群消息首试瞬时失败，休整后重试一次")
	}
	assertAccepted(t, "wecom 测试群文本消息", err, 0)

	// msgid 弹性断言：官方契约 appchat/send 成功响应仅 errcode/errmsg（见文件头
	// 注释的官方文档链接）；若厂商演进后返回 msgid，则断言非空并记录，不静默放过
	if raw, ok := resp["msgid"]; ok {
		msgID, _ := raw.(string)
		if msgID == "" {
			t.Fatalf("wecom 测试群文本消息: 响应含 msgid 但为空串")
		}
		t.Logf("msgid: %s", msgID)
	}
	t.Logf("消息已发至测试群 %s（内容 %q），送达情况请在企微测试群人工核对", chatID, content)
}
