package wecom

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"

	"github.com/gogf/gf/v2/frame/g"
)

// OAuth 与 JsApi/扫码登录 URL 构造 + state 一次性消费

const stateTTL = 30 // state 有效期（秒），对齐 PHP WecomOauthStateStore：一次授权回调内有效

// StateStore OAuth state 存储：Create 存 returnUrl，Consume 一次性取回（防 CSRF + 承载回跳地址）。
// key 格式 wecom:oauth:state:{state}，TTL 30s，对齐 PHP 语义（多项目沉淀的公司标准）。
type StateStore struct{}

// NewStateStore 构造
func NewStateStore() *StateStore {
	return &StateStore{}
}

// Create 生成随机 state（32 字符 hex）并以 TTL 30s 存入 returnURL，返回 state 供拼接授权链接
func (s *StateStore) Create(ctx context.Context, returnURL string) (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("wecom: state 生成失败: %w", err)
	}
	state := hex.EncodeToString(raw)
	if _, err := g.Redis().Do(ctx, "SETEX", "wecom:oauth:state:"+state, stateTTL, returnURL); err != nil {
		return "", fmt.Errorf("wecom: state 写入失败: %w", err)
	}
	return state, nil
}

// Consume 原子地一次性取回 returnURL（GETDEL，Redis ≥6.2）；state 不存在或已消费返回 ok=false。
// err 仅表 Redis 故障；ok=false 是正常业务分支（无效/过期/重放）
func (s *StateStore) Consume(ctx context.Context, state string) (returnURL string, ok bool, err error) {
	v, err := g.Redis().Do(ctx, "GETDEL", "wecom:oauth:state:"+state)
	if err != nil {
		return "", false, fmt.Errorf("wecom: state 消费失败: %w", err)
	}
	if v == nil || v.String() == "" {
		return "", false, nil
	}
	return v.String(), true, nil
}

// authorizeURL scope 参数化的授权 URL 拼接（双 scope 函数共享，防格式漂移）
func authorizeURL(corpID string, agentID int, redirectURI, state, scope string) string {
	return fmt.Sprintf(
		"https://open.weixin.qq.com/connect/oauth2/authorize?appid=%s&redirect_uri=%s&response_type=code&scope=%s&state=%s&agentid=%d#wechat_redirect",
		corpID, url.QueryEscape(redirectURI), scope, state, agentID,
	)
}

// BuildAuthorizeURL 网页授权跳转 URL（snsapi_base 静默授权；redirect_uri 已 URL 编码）
func BuildAuthorizeURL(corpID string, agentID int, redirectURI, state string) string {
	return authorizeURL(corpID, agentID, redirectURI, state, "snsapi_base")
}

// BuildPrivateInfoAuthorizeURL 网页授权跳转 URL（snsapi_privateinfo 敏感信息授权；
// 与 BuildAuthorizeURL 除 scope 外格式完全一致）
func BuildPrivateInfoAuthorizeURL(corpID string, agentID int, redirectURI, state string) string {
	return authorizeURL(corpID, agentID, redirectURI, state, "snsapi_privateinfo")
}

// BuildQrConnectURL 扫码登录跳转 URL
func BuildQrConnectURL(corpID string, agentID int, redirectURI, state string) string {
	return fmt.Sprintf(
		"https://login.work.weixin.qq.com/wwlogin/sso/login?login_type=CorpApp&appid=%s&agentid=%d&redirect_uri=%s&state=%s",
		corpID, agentID, url.QueryEscape(redirectURI), state,
	)
}

// BuildFrontendRedirectURL 前端回跳 URL：按 returnURL 是否含 ? 选 &/? 分隔拼接 code
func BuildFrontendRedirectURL(returnURL, code string) string {
	sep := "?"
	if strings.Contains(returnURL, "?") {
		sep = "&"
	}
	return returnURL + sep + "code=" + code
}

// OauthManager OAuth 登录编排层（对齐 PHP WecomOauthManager）：串联 state 存取、
// 授权 URL 拼接、前端回跳与 code/ticket 域方法。凭证与回跳地址经构造注入（库不做项目决策）。
type OauthManager struct {
	client      *Client
	store       *StateStore
	corpID      string
	agentID     int
	redirectURI string
}

// NewOauthManager 构造：client 供域方法调用（仅用 BuildXxx 时可传 nil），redirectURI 为企微后台
// 配置的网页授权可信回跳地址
func NewOauthManager(client *Client, store *StateStore, corpID string, agentID int, redirectURI string) *OauthManager {
	return &OauthManager{client: client, store: store, corpID: corpID, agentID: agentID, redirectURI: redirectURI}
}

// BuildAuthorizeURL 生成 snsapi_base 静默授权 URL：内部创建 state（存 returnURL，TTL 30s）
func (m *OauthManager) BuildAuthorizeURL(ctx context.Context, returnURL string) (string, error) {
	return m.buildAuthorize(ctx, returnURL, "snsapi_base")
}

// BuildPrivateInfoAuthorizeURL 生成 snsapi_privateinfo 敏感信息授权 URL（内部创建 state）
func (m *OauthManager) BuildPrivateInfoAuthorizeURL(ctx context.Context, returnURL string) (string, error) {
	return m.buildAuthorize(ctx, returnURL, "snsapi_privateinfo")
}

func (m *OauthManager) buildAuthorize(ctx context.Context, returnURL, scope string) (string, error) {
	state, err := m.store.Create(ctx, returnURL)
	if err != nil {
		return "", err
	}
	return authorizeURL(m.corpID, m.agentID, m.redirectURI, state, scope), nil
}

// BuildFrontendRedirectURL 消费 state 取 returnURL，拼接 code 生成前端回跳 URL。
// ok=false 表 state 无效/过期/已消费（对齐 PHP ?string 的 null 语义）；err 仅表 Redis 故障
func (m *OauthManager) BuildFrontendRedirectURL(ctx context.Context, state, code string) (string, bool, error) {
	returnURL, ok, err := m.store.Consume(ctx, state)
	if err != nil || !ok {
		return "", false, err
	}
	return BuildFrontendRedirectURL(returnURL, code), true, nil
}

// GetUserinfoByCode OAuth code 换成员身份信息（委托 Client）
func (m *OauthManager) GetUserinfoByCode(ctx context.Context, code string) (map[string]any, error) {
	return m.client.GetUserinfoByCode(ctx, code)
}

// GetUserDetailByTicket user_ticket 换成员敏感信息（委托 Client）
func (m *OauthManager) GetUserDetailByTicket(ctx context.Context, userTicket string) (map[string]any, error) {
	return m.client.GetUserDetailByTicket(ctx, userTicket)
}
