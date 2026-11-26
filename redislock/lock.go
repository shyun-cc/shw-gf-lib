// Package redislock 基于 Redis 的分布式锁（对应 PHP RedisLock）。
//
// 使用 SET NX EX 获取锁 + Lua 脚本比对 owner 安全释放，
// 避免误删其他持锁者的锁。
package redislock

import (
	"context"
	"fmt"

	"github.com/gogf/gf/v2/container/gvar"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/google/uuid"
)

// keyPrefix Redis 键前缀
const keyPrefix = "redis-lock:"

// Lock 分布式锁
type Lock struct {
	name    string
	owner   string
	seconds int
}

// Create 创建锁实例（不获取）
func Create(name string, seconds int) *Lock {
	return &Lock{
		name:    keyPrefix + name,
		owner:   uuid.New().String(),
		seconds: seconds,
	}
}

// Get 获取锁。seconds>0 为 SET NX EX（带过期）；seconds=0 为 SET NX（无过期，TTL=-1 持有至释放）
func (l *Lock) Get(ctx context.Context) bool {
	var result *gvar.Var
	var err error
	if l.seconds > 0 {
		result, err = g.Redis().Do(ctx, "SET", l.name, l.owner, "NX", "EX", l.seconds)
	} else if l.seconds == 0 {
		result, err = g.Redis().Do(ctx, "SET", l.name, l.owner, "NX")
	} else {
		g.Log().Errorf(ctx, "redislock: 非法 seconds=%d（须 ≥0），拒绝获取锁 %s", l.seconds, l.name)
		return false
	}
	if err != nil || result == nil {
		return false
	}
	return result.String() == "OK"
}

// Release 安全释放（Lua 脚本比对 owner 后才删除）
func (l *Lock) Release(ctx context.Context) bool {
	script := `if redis.call("get",KEYS[1])==ARGV[1] then return redis.call("del",KEYS[1]) else return 0 end`
	result, err := g.Redis().Eval(ctx, script, 1, []string{l.name}, []any{l.owner})
	if err != nil || result == nil {
		return false
	}
	return result.Int() == 1
}

// Extend 续期（仅续期自己的锁）
func (l *Lock) Extend(ctx context.Context, seconds int) bool {
	owner, err := g.Redis().Get(ctx, l.name)
	if err != nil {
		return false
	}
	if owner.String() != l.owner {
		return false
	}
	_, err = g.Redis().Do(ctx, "SET", l.name, l.owner, "XX", "EX", seconds)
	return err == nil
}

// ForceRelease 强制释放（忽略 owner）
func (l *Lock) ForceRelease(ctx context.Context) {
	_, _ = g.Redis().Del(ctx, l.name)
}

// Owner 返回当前 owner
func (l *Lock) Owner() string {
	return l.owner
}

// Name 返回锁名称（含前缀）
func (l *Lock) Name() string {
	return l.name
}

// String 实现 fmt.Stringer
func (l *Lock) String() string {
	return fmt.Sprintf("Lock{name=%s, owner=%s, seconds=%d}", l.name, l.owner, l.seconds)
}
