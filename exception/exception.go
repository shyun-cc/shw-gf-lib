// Package exception 分层异常体系（对应 PHP DomainException/ApplicationException/InfrastructureException）。
//
// 提供三种异常类型，通过 HTTP 状态码区分语义：
//   - Domain:         领域规则违规 → HTTP 422
//   - Application:    应用层异常   → HTTP 200（前端通过 code 区分）
//   - Infrastructure: 基础设施异常 → HTTP 500
package exception

import "fmt"

// 异常类型（对应 PHP 异常分层）
const (
	TypeDomain         = "domain"
	TypeApplication    = "application"
	TypeInfrastructure = "infrastructure"
)

// FallbackErrorCode 兜底错误码
const FallbackErrorCode = 99999

// 库内分段错误码（99901-99916，段位语义对齐 hyperf-lib LibErrorCode）。
// 各组件异常的 code 取自对应段位；99999 保留为未分类兜底。
const (
	CodeSeqNumGenFailed       = 99901 // 序列号生成失败（seqnum）
	CodeFileUploadFailed      = 99902 // 文件上传失败（storage/attachment）
	CodeAliyunServiceError    = 99903 // 阿里云服务错误（aliyun）
	CodeProcessLockError      = 99904 // 进程锁错误（worker）
	CodeProcessSignalError    = 99905 // 进程信号错误（worker）
	CodeProcessIterationMax   = 99906 // 进程循环次数上限（worker）
	CodeProcessMemoryLimit    = 99907 // 进程内存超限（worker）
	CodeSmsServiceError       = 99908 // 短信发送服务异常（sms）
	CodeVerifyCodeRateLimited = 99909 // 验证码发送频率受限（verifycode）
	CodeVerifyCodeExpired     = 99910 // 验证码已过期（verifycode）
	CodeVerifyCodeMaxAttempts = 99911 // 验证码验证次数超限（verifycode）
	CodeWecomServiceError     = 99912 // 企微/微信服务错误（wecom/wxa）
	CodeAttachmentStoreError  = 99913 // 附件存储错误（attachment）
	CodeExpressServiceError   = 99914 // 快递服务错误（express）
	CodeKd100ServiceError     = 99915 // Kd100 服务错误（express/kd100）
	CodeRegionServiceError    = 99916 // 行政区划服务错误（region）
)

// AppError 业务异常（携带错误码、消息和异常类型）
// 对应 PHP 的 DomainException / ApplicationException / InfrastructureException
type AppError struct {
	Code    int
	Message string
	Data    interface{}
	Type    string // domain / application / infrastructure
}

func (e *AppError) Error() string {
	return fmt.Sprintf("[%d] %s", e.Code, e.Message)
}

// NormalizedCode 返回规范化的错误码（0 → FallbackErrorCode）
func (e *AppError) NormalizedCode() int {
	if e.Code != 0 {
		return e.Code
	}
	return FallbackErrorCode
}

// NewDomain 创建领域异常（HTTP 422）
func NewDomain(code int, message string) *AppError {
	return &AppError{Code: code, Message: message, Type: TypeDomain}
}

// NewApplication 创建应用异常（HTTP 200）
func NewApplication(code int, message string) *AppError {
	return &AppError{Code: code, Message: message, Type: TypeApplication}
}

// NewInfrastructure 创建基础设施异常（HTTP 500）
func NewInfrastructure(code int, message string) *AppError {
	return &AppError{Code: code, Message: message, Type: TypeInfrastructure}
}

// New 创建业务异常（默认应用异常，HTTP 200）。向后兼容已有代码
func New(code int, message string) *AppError {
	return &AppError{Code: code, Message: message, Type: TypeApplication}
}

// NewWithData 创建带附加数据的业务异常
func NewWithData(code int, message string, data interface{}) *AppError {
	return &AppError{Code: code, Message: message, Data: data, Type: TypeApplication}
}
