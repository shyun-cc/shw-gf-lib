package wxoa

import "fmt"

// BizError 业务异常：微信侧 errcode ≠ 0。携带原始 errcode 数值，
// 供调用方 errors.As 差异化捕获。与 wecom/wxapp 同构、类型各自包内定义。
type BizError struct {
	Errcode int
	Errmsg  string
}

func (e *BizError) Error() string {
	return fmt.Sprintf("wxoa: 业务错误 errcode=%d errmsg=%s", e.Errcode, e.Errmsg)
}

// InfraError 基础设施异常：HTTP 请求失败 / 响应非 JSON / 凭证获取链路失败。
// 与 BizError 类型互斥，供告警按类型分流。
type InfraError struct {
	Op  string // 出错环节：http / decode / token / encode / ticket / sign
	Err error
}

func (e *InfraError) Error() string {
	return fmt.Sprintf("wxoa: 基础设施错误 op=%s: %v", e.Op, e.Err)
}

func (e *InfraError) Unwrap() error { return e.Err }
