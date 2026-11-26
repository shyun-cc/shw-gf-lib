package wxapp

import "fmt"

// BizError 业务异常：微信侧 errcode ≠ 0。携带原始 errcode 数值，
// 供调用方 errors.As 差异化捕获（如 40029 invalid code 与 45011 频率限制走不同分支）。
type BizError struct {
	Errcode int
	Errmsg  string
}

func (e *BizError) Error() string {
	return fmt.Sprintf("wxapp: 业务错误 errcode=%d errmsg=%s", e.Errcode, e.Errmsg)
}

// InfraError 基础设施异常：HTTP 请求失败 / 响应非 JSON / 凭证获取链路失败。
// 与 BizError 类型互斥（errcode 判定发生在 JSON 解码成功之后），供告警按类型分流：
// 基础设施异常触发告警，业务 errcode 不触发。
type InfraError struct {
	Op  string // 出错环节：http（请求失败）/ decode（响应非 JSON 或缺字段）/ token（凭证链路）/ encode（请求体编码）
	Err error
}

func (e *InfraError) Error() string {
	return fmt.Sprintf("wxapp: 基础设施错误 op=%s: %v", e.Op, e.Err)
}

func (e *InfraError) Unwrap() error { return e.Err }
