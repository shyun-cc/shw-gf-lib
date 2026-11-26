// Package verifycode 验证码生成与校验（对应 PHP VerifyCode）。
//
// 支持：Redis 存储 + Verify 成功原子消费 + 频率限制 + 防爆破锁定 + 安全随机数（crypto/rand）。
// 配置通过 SetConfig 参数化注入，不硬编码任何项目参数。
package verifycode

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"

	"github.com/shyun-cc/shw-gf-lib/events"

	"github.com/gogf/gf/v2/frame/g"
)

// 事件名（events 派发；载荷 GeneratedPayload / VerifiedPayload / RateLimitedPayload）
const (
	EventGenerated   = "verifycode.Generated"
	EventVerified    = "verifycode.Verified"
	EventRateLimited = "verifycode.RateLimited"
)

// Scene 验证码场景类型
type Scene string

const (
	SceneLogin         Scene = "login"
	SceneRegister      Scene = "register"
	SceneResetPassword Scene = "reset_password"
	SceneChangePhone   Scene = "change_phone"
	SceneGeneric       Scene = "generic"
)

// 码型常量：数字（默认，每位 0-9）或 36 进制字母数字（每位 0-9a-z 小写）
const (
	CodeTypeNumeric = 0
	CodeTypeAlnum36 = 1
)

// dictAlnum36 36 进制小写字典（0-9a-z）
const dictAlnum36 = "0123456789abcdefghijklmnopqrstuvwxyz"

// Config 验证码配置
type Config struct {
	Prefix       string // Redis key 前缀
	CodeLen      int    // 验证码长度
	TTL          int    // 验证码有效期（秒）
	RateInterval int    // 发送频率限制间隔（秒）
	MaxAttempts  int    // 最大尝试次数（超过后锁定）
	LockDuration int    // 锁定时长（秒）
	FailCountTTL int    // 失败计数有效期（秒）
	CodeType     int    // 码型：CodeTypeNumeric（默认）或 CodeTypeAlnum36
}

var config = Config{
	Prefix:       "vc",
	CodeLen:      6,
	TTL:          300,
	RateInterval: 60,
	MaxAttempts:  5,
	LockDuration: 3600,
	FailCountTTL: 3600,
}

// SetConfig 设置全局验证码配置（项目 boot 层初始化时调用）
func SetConfig(cfg Config) {
	if cfg.Prefix != "" {
		config.Prefix = cfg.Prefix
	}
	if cfg.CodeLen > 0 {
		config.CodeLen = cfg.CodeLen
	}
	if cfg.TTL > 0 {
		config.TTL = cfg.TTL
	}
	if cfg.RateInterval > 0 {
		config.RateInterval = cfg.RateInterval
	}
	if cfg.MaxAttempts > 0 {
		config.MaxAttempts = cfg.MaxAttempts
	}
	if cfg.LockDuration > 0 {
		config.LockDuration = cfg.LockDuration
	}
	if cfg.FailCountTTL > 0 {
		config.FailCountTTL = cfg.FailCountTTL
	}
	if cfg.CodeType == CodeTypeAlnum36 {
		config.CodeType = cfg.CodeType
	}
}

// ErrRateLimited 频率限制错误
var ErrRateLimited = errors.New("发送过于频繁，请稍后再试")

// GeneratedPayload 产码事件载荷（Code 为明文验证码）
type GeneratedPayload struct {
	Key   string
	Scene Scene
	Code  string
}

// VerifiedPayload 验证通过事件载荷
type VerifiedPayload struct {
	Key   string
	Scene Scene
}

// RateLimitedPayload 限频拒绝事件载荷
type RateLimitedPayload struct {
	Key   string
	Scene Scene
}

// VerifyCodeService 验证码服务
type VerifyCodeService struct {
	Prefix     string
	dispatcher *events.Dispatcher // 事件派发器，nil 走 events.Default()
}

// New 创建验证码服务实例（使用全局配置）
func New() *VerifyCodeService {
	return &VerifyCodeService{Prefix: config.Prefix}
}

// SetDispatcher 注入独立事件分发器（测试或事件域隔离用；不设走 events.Default()）
func (s *VerifyCodeService) SetDispatcher(d *events.Dispatcher) {
	s.dispatcher = d
}

// dispatch 派发事件（dispatcher nil 走全局默认分发器）
func (s *VerifyCodeService) dispatch(ctx context.Context, name string, payload any) {
	if s.dispatcher != nil {
		s.dispatcher.Dispatch(ctx, name, payload)
		return
	}
	events.Dispatch(ctx, name, payload)
}

// Generate 生成验证码，返回明文验证码
func (s *VerifyCodeService) Generate(ctx context.Context, key string, scene Scene) (string, error) {
	rds := g.Redis()

	rateLimitKey := s.rateLimitKey(scene, key)
	result, err := rds.Do(ctx, "SET", rateLimitKey, "1", "NX", "EX", config.RateInterval)
	if err != nil {
		return "", err
	}
	if result == nil || result.String() != "OK" {
		// 派发限频拒绝事件
		s.dispatch(ctx, EventRateLimited, RateLimitedPayload{Key: key, Scene: scene})
		return "", ErrRateLimited
	}

	code := generateCode(config.CodeLen, config.CodeType)

	codeKey := s.codeKey(scene, key)
	err = rds.SetEX(ctx, codeKey, code, int64(config.TTL))
	if err != nil {
		return "", err
	}

	// 派发产码事件
	s.dispatch(ctx, EventGenerated, GeneratedPayload{Key: key, Scene: scene, Code: code})

	return code, nil
}

// verifyScript 保留既有键布局与计数窗口，把锁定检查、比对和消费放在同一原子操作内。
// 多键脚本要求 Redis Cluster 的三个键处于同一 hash slot；不得退回非原子的分步操作。
const verifyScript = `
if redis.call("EXISTS", KEYS[1]) > 0 then return 0 end
local stored = redis.call("GET", KEYS[2])
if not stored or stored == "" then return 0 end
if stored == ARGV[1] then
    redis.call("DEL", KEYS[2], KEYS[3])
    return 1
end
local count = redis.call("INCR", KEYS[3])
if count == 1 then redis.call("EXPIRE", KEYS[3], ARGV[2]) end
if count >= tonumber(ARGV[3]) then
    redis.call("SETEX", KEYS[1], ARGV[4], "1")
    redis.call("DEL", KEYS[3])
end
return 0
`

const invalidateIfMatchesScript = `
local stored = redis.call("GET", KEYS[1])
if not stored or stored == "" or stored ~= ARGV[1] then return 0 end
return redis.call("DEL", KEYS[1])
`

// Verify 原子校验并消费验证码，成功时清除验证码及失败计数。
// 成功后的同一码不可再次校验；多步业务须保存流程绑定证明，不重复验证旧码。
// 错误码、过期或锁定返回 (false, nil)；存储故障返回 (false, error)，不得据此签发身份。
// 仅成功消费后派发 EventVerified，不清除限频或锁定键。
// Redis Cluster 要求既有 lock/code/fail 键同槽（例如 Prefix 含相同 hash tag）。
func (s *VerifyCodeService) Verify(ctx context.Context, key, code string, scene Scene) (bool, error) {
	result, err := g.Redis().Eval(ctx, verifyScript, 3,
		[]string{s.lockKey(scene, key), s.codeKey(scene, key), s.failKey(scene, key)},
		[]any{code, config.FailCountTTL, config.MaxAttempts, config.LockDuration})
	// Redis adapter 错误可能包含 EVAL 明文参数；不透传、不记录原始错误。
	if err != nil || result == nil || (result.String() != "0" && result.String() != "1") {
		return false, errors.New("verifycode: 原子校验消费失败")
	}
	if result.String() != "1" {
		return false, nil
	}
	s.dispatch(ctx, EventVerified, VerifiedPayload{Key: key, Scene: scene})
	return true, nil
}

// InvalidateIfMatches 仅在当前验证码等于 expected 时原子删除码键。
// 用于发送失败补偿；不清除失败计数、限频或锁定，不派发事件。
// 同值的重发挑战无法区分；需要区分时调用方应以独立 key 标识挑战。
func (s *VerifyCodeService) InvalidateIfMatches(ctx context.Context, key, expected string, scene Scene) (bool, error) {
	result, err := g.Redis().Eval(ctx, invalidateIfMatchesScript, 1,
		[]string{s.codeKey(scene, key)}, []any{expected})
	if err != nil || result == nil || (result.String() != "0" && result.String() != "1") {
		return false, errors.New("verifycode: 条件撤销失败")
	}
	return result.String() == "1", nil
}

// GetStoredCode 获取存储的验证码明文（测试环境使用）
func (s *VerifyCodeService) GetStoredCode(ctx context.Context, key string, scene Scene) (string, error) {
	rds := g.Redis()
	val, err := rds.Get(ctx, s.codeKey(scene, key))
	if err != nil {
		return "", err
	}
	return val.String(), nil
}

// CheckRateLimit 独立查询限频状态（只读）：Generate 后的限频窗口内返回 true（被限），
// 窗口过期后返回 false（未限）。不写入验证码、不重置或延长限频标记。
func (s *VerifyCodeService) CheckRateLimit(ctx context.Context, key string, scene Scene) (bool, error) {
	n, err := g.Redis().Exists(ctx, s.rateLimitKey(scene, key))
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *VerifyCodeService) codeKey(scene Scene, key string) string {
	return fmt.Sprintf("%s:%s:%s", s.Prefix, scene, key)
}

func (s *VerifyCodeService) rateLimitKey(scene Scene, key string) string {
	return fmt.Sprintf("%s:rl:%s:%s", s.Prefix, scene, key)
}

func (s *VerifyCodeService) failKey(scene Scene, key string) string {
	return fmt.Sprintf("%s:fail:%s:%s", s.Prefix, scene, key)
}

func (s *VerifyCodeService) lockKey(scene Scene, key string) string {
	return fmt.Sprintf("%s:lock:%s:%s", s.Prefix, scene, key)
}

// generateCode 按码型生成验证码：数字（0-9）或 36 进制小写（0-9a-z），crypto/rand 安全随机
func generateCode(n int, codeType int) string {
	base := 10
	dict := "0123456789"
	if codeType == CodeTypeAlnum36 {
		base = 36
		dict = dictAlnum36
	}
	chars := make([]byte, n)
	for i := 0; i < n; i++ {
		num, err := rand.Int(rand.Reader, big.NewInt(int64(base)))
		if err != nil {
			chars[i] = dict[0]
			continue
		}
		chars[i] = dict[num.Int64()]
	}
	return string(chars)
}
