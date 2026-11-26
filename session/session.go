// Package session 基于 Redis Hash 的会话服务（对应 PHP Session）。
//
// 通用设计：Role/TTL/IDField 由调用方传入，不硬编码任何项目角色；
// 会话的业务字段由项目经 Create 的 data 参数传入，库不枚举其形状。
// 项目侧自己定义工厂函数，如：
//
//	func NewPlatformSession() *session.SessionService {
//	    return session.New("platform", 86400, "admin_id")
//	}
package session

import (
	"context"
	"fmt"
	"time"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/google/uuid"
)

// SessionData Create 的返回值，仅承载机制层可保证的字段：
// 会话 UUID 与登录/最近活跃时间戳。业务字段不在库内定义，
// 由调用方经 data 参数传入并落盘 Redis Hash。
type SessionData struct {
	UUID         string `json:"uuid"`
	LoginAt      string `json:"login_at"`
	LastActiveAt string `json:"last_active_at"`
}

// SessionService 基于 Redis Hash 的会话服务
type SessionService struct {
	Role    string // 角色标识（如 "platform" / "business" / "client"），由调用方定义
	TTL     int    // 会话有效期（秒）
	IDField string // Hash 中存储用户 ID 的字段名（如 "admin_id" / "id"）
}

// New 创建会话服务（通用构造函数，不硬编码任何角色）
func New(role string, ttl int, idField string) *SessionService {
	return &SessionService{Role: role, TTL: ttl, IDField: idField}
}

// Create 创建会话并写入 Redis。userID 接受字符串形态（主键 uuidv7 文本
// 或 int64 文本均适用）；无论 data 是否携带 IDField 键，均以参数值写入
// （data 中同名键被参数值覆盖），保证 Hash 中用户 ID 与索引一致。
func (s *SessionService) Create(ctx context.Context, userID string, data map[string]interface{}) (*SessionData, error) {
	sessionUUID := uuid.New().String()
	now := time.Now().Format("2006-01-02 15:04:05")
	sessionKey := s.sessionKey(sessionUUID)
	userSessionsKey := s.userSessionsKey(userID)
	rds := g.Redis()

	fields := make(map[string]interface{})
	for k, v := range data {
		fields[k] = v
	}
	// 用户 ID 以参数值为准写入 IDField（data 中同名键被覆盖），
	// 必须在 data 拷贝之后、落盘之前，保证与用户索引的 userID 一致
	fields[s.IDField] = userID
	fields["login_at"] = now
	fields["last_active_at"] = now

	_, err := rds.HSet(ctx, sessionKey, fields)
	if err != nil {
		return nil, err
	}
	_, err = rds.Expire(ctx, sessionKey, int64(s.TTL))
	if err != nil {
		return nil, err
	}
	_, err = rds.SAdd(ctx, userSessionsKey, sessionUUID)
	if err != nil {
		return nil, err
	}

	return &SessionData{
		UUID:         sessionUUID,
		LoginAt:      now,
		LastActiveAt: now,
	}, nil
}

// Validate 校验会话有效性，刷新 last_active_at 和 TTL
func (s *SessionService) Validate(ctx context.Context, sessionUUID string) (map[string]string, error) {
	sessionKey := s.sessionKey(sessionUUID)
	rds := g.Redis()

	result, err := rds.HGetAll(ctx, sessionKey)
	if err != nil {
		return nil, err
	}
	data := result.MapStrStr()
	if len(data) == 0 {
		return nil, nil
	}

	now := time.Now().Format("2006-01-02 15:04:05")

	_, err = rds.HSet(ctx, sessionKey, map[string]interface{}{"last_active_at": now})
	if err != nil {
		return nil, err
	}
	_, err = rds.Expire(ctx, sessionKey, int64(s.TTL))
	if err != nil {
		return nil, err
	}

	data["uuid"] = sessionUUID
	data["last_active_at"] = now

	return data, nil
}

// Destroy 销毁会话
func (s *SessionService) Destroy(ctx context.Context, sessionUUID string) error {
	sessionKey := s.sessionKey(sessionUUID)
	rds := g.Redis()

	userIDVar, err := rds.HGet(ctx, sessionKey, s.IDField)
	if err != nil {
		return err
	}
	userID := userIDVar.String()
	if userID != "" {
		_, err = rds.SRem(ctx, s.userSessionsKey(userID), sessionUUID)
		if err != nil {
			return err
		}
	}
	_, err = rds.Del(ctx, sessionKey)
	return err
}

// DestroyByUserID 销毁指定用户的所有会话（userID 为字符串形态，与 Create 一致）
func (s *SessionService) DestroyByUserID(ctx context.Context, userID string) error {
	rds := g.Redis()
	userSessionsKey := s.userSessionsKey(userID)

	uuidVars, err := rds.SMembers(ctx, userSessionsKey)
	if err != nil {
		return err
	}

	for _, u := range uuidVars {
		_, err = rds.Del(ctx, s.sessionKey(u.String()))
		if err != nil {
			return err
		}
	}
	_, err = rds.Del(ctx, userSessionsKey)
	return err
}

func (s *SessionService) sessionKey(sessionUUID string) string {
	return fmt.Sprintf("%s:session:%s", s.Role, sessionUUID)
}

func (s *SessionService) userSessionsKey(userID string) string {
	return fmt.Sprintf("%s:session:user:%s", s.Role, userID)
}
