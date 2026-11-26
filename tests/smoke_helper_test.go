//go:build smoke

// 冒烟测试公共 helper（change: rdev-testing-infra，task 2.1）。
//
// 冒烟框架契约（本文件四个 helper 的共同语义）：
//   - 只断言「厂商接受请求形态」，不断言业务数据：冒烟验证签名、加密、
//     报文组装等契约层不被厂商拒绝（assertAccepted），不校验余额、回执
//     内容等厂商侧业务结果——那是业务测试的事；
//   - 档位体系（smokeWant + 环境变量 SMOKE_LEVEL）：read（纯读）<
//     write（轻写：素材传删、测试群消息、测试桶对象）< costly（真金写：
//     短信、下单），默认 read；用例按自身副作用声明所需档位，当前档位
//     不足时自动 Skip，避免误触发写入或付费操作；
//   - 瞬时错误重试（retryOnceOnTransient）：厂商偶发超时/限流不算失败，
//     休整 2 秒后重试一次，仍失败才把错误交给断言层；
//   - 已识别缺陷（knownDefect）：v0.8.0 审计登记、等待修复 change 的
//     已知问题以 Skip 记录，不计为回归。
//
// 构建标签 smoke 与 integration 正交：本文件只在 -tags smoke 下参与构建，
// 供各厂商连通性冒烟用例（sms / wxapp / wxoa / wxpay / wecom …）复用。
package tests

import (
	"os"
	"regexp"
	"testing"
	"time"
)

// transientRetryBackoff 瞬时错误重试前的休整时长：给厂商侧限流/抖动留缓冲。
const transientRetryBackoff = 2 * time.Second

// smokeRank 冒烟档位序：read（纯读）< write（轻写）< costly（真金写）。
var smokeRank = map[string]int{
	"read":   0,
	"write":  1,
	"costly": 2,
}

// transientErrRe 瞬时错误模式（大小写不敏感）：超时、连接重置、厂商限流/
// 繁忙业务码（42901、45009）、rate limit、temporarily unavailable、
// context deadline exceeded 等。匹配即视为可重试的瞬时抖动。
var transientErrRe = regexp.MustCompile(`(?i)(timeout|connection reset|42901|45009|rate limit|temporarily|context deadline)`)

// smokeLevel 返回环境变量 SMOKE_LEVEL 的当前档位：未设置或空视为 read；
// 非法值直接 t.Fatalf——档位拼错应当场暴露，而不是默默按 read 跑漏用例。
func smokeLevel(t *testing.T) string {
	t.Helper()
	level := os.Getenv("SMOKE_LEVEL")
	if level == "" {
		return "read"
	}
	if _, ok := smokeRank[level]; !ok {
		t.Fatalf("SMOKE_LEVEL=%q 非法：合法值为 read / write / costly", level)
	}
	return level
}

// smokeWant 声明用例所需的冒烟档位，当前档位低于请求档位时 Skip。
//
// 档位语义：read 纯读（默认）；write 轻写（素材传删、测试群消息、测试桶
// 对象）；costly 真金写（短信、下单）。用例按自身副作用声明，例如发消息
// 用例应 smokeWant(t, "write")，未开 write 档自动跳过。
func smokeWant(t *testing.T, level string) {
	t.Helper()
	want, ok := smokeRank[level]
	if !ok {
		t.Fatalf("smokeWant: 请求档位 %q 非法：合法值为 read / write / costly", level)
	}
	if current := smokeLevel(t); smokeRank[current] < want {
		t.Skipf("needs SMOKE_LEVEL=%s (current=%s)", level, current)
	}
}

// assertAccepted 契约层断言：厂商接口接受了请求形态。
//
// err 非 nil 即请求未过契约层（网络、签名、报文错误），t.Fatalf；
// code 为用例从各组件响应中自行提取的厂商业务码，非 0 即契约层被厂商
// 拒绝，t.Fatalf。各组件响应结构不同，业务码由用例提取后传入，本函数
// 保持最小通用形态，不断言业务数据。
func assertAccepted(t *testing.T, name string, err error, code int) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: 契约层未通过，请求失败: %v", name, err)
	}
	if code != 0 {
		t.Fatalf("%s: 契约层被拒绝，厂商业务码 code=%d", name, code)
	}
}

// retryOnceOnTransient 执行 fn，首次错误为瞬时错误时休整后重试一次。
//
// 语义：fn 首次成功返回 (nil, false)；首次错误非瞬时则不重试，返回
// (首次错误, false)；首次错误为瞬时（匹配 transientErrRe）则 sleep
// transientRetryBackoff 后重试一次，无论重试成败 retried=true，
// 返回重试的最终错误。返回 retried 供用例在日志中区分首试失败与重试失败。
func retryOnceOnTransient(fn func() error) (err error, retried bool) {
	err = fn()
	if err == nil || !transientErrRe.MatchString(err.Error()) {
		return err, false
	}
	time.Sleep(transientRetryBackoff)
	return fn(), true
}

// knownDefect 标记已识别缺陷：问题已在 v0.8.0 审计中登记、等待专门修复
// change 处理，冒烟以 Skip 记录，不计为回归。
func knownDefect(t *testing.T, id string) {
	t.Helper()
	t.Skipf("known defect %s: awaiting fix change (v0.8.0 audit)", id)
}
