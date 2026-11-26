package wecom

import "fmt"

// CorpManager 多应用体系：同一 corpID 下注册一个通讯录同步应用 + 多个自建应用，
// 各自凭证与回调 Token/AESKey 独立，按名取用（token 缓存键含 agentID 天然隔离）。
type CorpManager struct {
	corpID string
	apps   map[string]*appEntry
}

type appEntry struct {
	client *Client
	crypt  *WXBizMsgCrypt
}

// NewCorpManager 构造（corpID 校验一致性）
func NewCorpManager(corpID string) *CorpManager {
	return &CorpManager{corpID: corpID, apps: map[string]*appEntry{}}
}

// Register 注册应用（name 业务名；重名覆盖）；callbackToken/callbackAESKey 为该应用回调配置
func (m *CorpManager) Register(name string, creds Credential, callbackToken, callbackAESKey string) error {
	if creds.CorpID != m.corpID {
		return fmt.Errorf("wecom: 应用 %s 的 corpID %s 与管理器 %s 不一致", name, creds.CorpID, m.corpID)
	}
	crypt, err := NewWXBizMsgCrypt(callbackToken, callbackAESKey, m.corpID)
	if err != nil {
		return fmt.Errorf("wecom: 应用 %s 回调密钥非法: %w", name, err)
	}
	m.apps[name] = &appEntry{client: NewClient(creds), crypt: crypt}
	return nil
}

// Client 按名取应用客户端（未知名报错）
func (m *CorpManager) Client(name string) (*Client, error) {
	e, ok := m.apps[name]
	if !ok {
		return nil, fmt.Errorf("wecom: 未注册应用 %s", name)
	}
	return e.client, nil
}

// Crypt 按名取该应用回调解密器（各持独立 Token/AESKey）
func (m *CorpManager) Crypt(name string) (*WXBizMsgCrypt, error) {
	e, ok := m.apps[name]
	if !ok {
		return nil, fmt.Errorf("wecom: 未注册应用 %s", name)
	}
	return e.crypt, nil
}
