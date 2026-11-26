// Package response 统一 JSON 响应封装（对应 PHP Res）。
//
// 响应格式：{"traceId":"...","code":0,"message":"success","data":...}
// 异常处理通过 WriteException 按异常类型分层返回对应 HTTP 状态码。
package response

import (
	"net/http"

	"github.com/shyun-cc/shw-gf-lib/exception"
	"github.com/shyun-cc/shw-gf-lib/tracex"

	"github.com/gogf/gf/v2/errors/gcode"
	"github.com/gogf/gf/v2/errors/gerror"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/ghttp"
)

// validationErrorCode 参数校验失败的业务 code（各项目可不同，通过 SetValidationErrorCode 设置）
var validationErrorCode = 400

// SetValidationErrorCode 设置参数校验失败的业务 code（项目 boot 层调用）
func SetValidationErrorCode(code int) {
	validationErrorCode = code
}

// MessageCarrier 可自定义成功响应消息的 Res 结构体实现此接口
type MessageCarrier interface {
	GetMessage() string
}

// RawDataCarrier 可自定义 data 字段原始内容的 Res 结构体实现此接口
type RawDataCarrier interface {
	GetRawData() interface{}
}

// traceID 从请求 ctx 同源取 traceId（与请求期间 glog 输出同值）；
// 未挂 Trace 中间件/无注入时惰性生成稳定值，保证响应始终携带 traceId
func traceID(r *ghttp.Request) string {
	_, tid := tracex.Ensure(r.Context())
	return tid
}

// Success 成功响应
func Success(r *ghttp.Request, data interface{}) {
	r.Response.WriteJsonExit(g.Map{
		"traceId": traceID(r),
		"code":    0,
		"message": "success",
		"data":    data,
	})
}

// SuccessWithMessage 成功响应（自定义消息）
func SuccessWithMessage(r *ghttp.Request, data interface{}, message string) {
	r.Response.WriteJsonExit(g.Map{
		"traceId": traceID(r),
		"code":    0,
		"message": message,
		"data":    data,
	})
}

// Error 业务错误响应（HTTP 200，前端通过 code 区分）
func Error(r *ghttp.Request, message string, code int) {
	r.Response.WriteJsonExit(g.Map{
		"traceId": traceID(r),
		"code":    code,
		"message": message,
		"data":    g.Map{},
	})
}

// ErrorWithData 业务错误响应（带附加数据）
func ErrorWithData(r *ghttp.Request, message string, code int, data interface{}) {
	r.Response.WriteJsonExit(g.Map{
		"traceId": traceID(r),
		"code":    code,
		"message": message,
		"data":    data,
	})
}

// WriteSuccess 写入成功响应（供规范路由统一响应中间件调用）
func WriteSuccess(r *ghttp.Request, res interface{}) {
	message := "success"
	if mc, ok := res.(MessageCarrier); ok {
		if m := mc.GetMessage(); m != "" {
			message = m
		}
	}
	data := res
	if rdc, ok := res.(RawDataCarrier); ok {
		data = rdc.GetRawData()
	}
	if data == nil {
		data = g.Map{}
	}
	r.Response.WriteJsonExit(g.Map{
		"traceId": traceID(r),
		"code":    0,
		"message": message,
		"data":    data,
	})
}

// WriteException 根据异常类型返回对应 HTTP 状态码
func WriteException(r *ghttp.Request, err error) {
	if appErr, ok := err.(*exception.AppError); ok {
		code := appErr.NormalizedCode()
		switch appErr.Type {
		case exception.TypeDomain:
			g.Log().Warningf(r.Context(), "[DOMAIN] code=%d msg=%s data=%v", code, appErr.Message, appErr.Data)
			r.Response.WriteStatus(http.StatusUnprocessableEntity, g.Map{
				"traceId": traceID(r),
				"code":    code,
				"message": appErr.Message,
				"data":    appErr.Data,
			})
			r.Exit()
		case exception.TypeInfrastructure:
			g.Log().Errorf(r.Context(), "[INFRA] code=%d msg=%s", code, appErr.Message)
			r.Response.WriteStatus(http.StatusInternalServerError, g.Map{
				"traceId": traceID(r),
				"code":    code,
				"message": appErr.Message,
				"data":    g.Map{},
			})
			r.Exit()
		default:
			g.Log().Infof(r.Context(), "[APP] code=%d msg=%s", code, appErr.Message)
			appData := appErr.Data
			if appData == nil {
				appData = g.Map{}
			}
			r.Response.WriteJsonExit(g.Map{
				"traceId": traceID(r),
				"code":    code,
				"message": appErr.Message,
				"data":    appData,
			})
		}
		return
	}

	if gc := gerror.Code(err); gc != nil && gc.Code() == gcode.CodeValidationFailed.Code() {
		g.Log().Infof(r.Context(), "[VALIDATION] %s", err.Error())
		r.Response.WriteJsonExit(g.Map{
			"traceId": traceID(r),
			"code":    validationErrorCode,
			"message": err.Error(),
			"data":    g.Map{},
		})
		return
	}

	g.Log().Errorf(r.Context(), "[UNCAUGHT] %+v", err)
	r.Response.WriteStatus(http.StatusInternalServerError, g.Map{
		"traceId": traceID(r),
		"code":    exception.FallbackErrorCode,
		"message": "服务器内部错误",
		"data":    g.Map{},
	})
	r.Exit()
}

// Unauthorized 未认证（HTTP 401）
func Unauthorized(r *ghttp.Request, message string) {
	r.Response.WriteStatus(http.StatusUnauthorized, g.Map{
		"traceId": traceID(r),
		"code":    401,
		"message": message,
		"data":    g.Map{},
	})
	r.Exit()
}

// Forbidden 权限不足（HTTP 403）
func Forbidden(r *ghttp.Request, message string) {
	r.Response.WriteStatus(http.StatusForbidden, g.Map{
		"traceId": traceID(r),
		"code":    403,
		"message": message,
		"data":    g.Map{},
	})
	r.Exit()
}

// ServerError 服务器错误（HTTP 500）
func ServerError(r *ghttp.Request, message string) {
	r.Response.WriteStatus(http.StatusInternalServerError, g.Map{
		"traceId": traceID(r),
		"code":    exception.FallbackErrorCode,
		"message": message,
		"data":    g.Map{},
	})
	r.Exit()
}
