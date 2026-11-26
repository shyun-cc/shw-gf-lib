// Package response 单元测试（ghttp 本地 server + 标准库 http 客户端）。
//
// 覆盖点：
//   - Success/SuccessWithMessage：HTTP 200 + {traceId 非空, code:0, message, data 透传}
//   - Error/ErrorWithData：HTTP 200 + code/message/data 透传
//   - WriteException 三层分流：Domain→422、Infrastructure→500、Application/New→200 + 业务 code
//   - WriteException：Application Data 为 nil 时 data 输出 {}；Domain Data 为 nil 时 data 为 null（原样透传）
//   - WriteException：gcode.CodeValidationFailed 错误走 SetValidationErrorCode（默认 400，可参数化）
//   - WriteException：未捕获普通 error → HTTP 500 + code 99999
//   - Unauthorized/Forbidden/ServerError：401/403/500 + 固定 code
//
// 注意：response 函数内部通过 WriteJsonExit/WriteStatus+r.Exit() 退出（ghttp 用 panic 实现），
// 必须经 HTTP server 路径调用（ghttp 内部 recover），不能在测试里直接调用。
package response

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/shyun-cc/shw-gf-lib/exception"

	"github.com/gogf/gf/v2/errors/gcode"
	"github.com/gogf/gf/v2/errors/gerror"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/os/glog"
	"github.com/gogf/gf/v2/util/guid"
)

// respBody 统一 JSON 响应结构（与 response.go 输出契约对应）
type respBody struct {
	TraceID string      `json:"traceId"`
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data"`
}

// testBaseURL 本地测试 server 基地址（TestMain 中启动）
var testBaseURL string

// TestMain 启动随机命名 + 随机端口的 ghttp server，注册全部被测路由。
// server 名用 guid.S() 避免与其他测试实例冲突；SetAddr("127.0.0.1:0") 由内核分配空闲端口，
// Start() 返回时监听已同步建立（gf 内部 CreateListener 后才 wg.Wait），GetListenedPort 无竞态。
func TestMain(m *testing.M) {
	// 关闭日志输出，避免 WriteException 内部 g.Log()（DOMAIN/INFRA/UNCAUGHT 等）刷屏影响测试输出。
	// glog 级别为位掩码：LEVEL_NONE(0) 表示全部级别不输出。
	g.Log().SetStdoutPrint(false)
	g.Log().SetLevel(glog.LEVEL_NONE)

	s := g.Server(guid.S())
	s.SetAddr("127.0.0.1:0")
	s.SetLogStdout(false)
	s.SetAccessLogEnabled(false)
	s.SetDumpRouterMap(false)

	s.BindHandler("/success", func(r *ghttp.Request) {
		Success(r, g.Map{"id": 123, "name": "foo"})
	})
	s.BindHandler("/success-msg", func(r *ghttp.Request) {
		SuccessWithMessage(r, g.Map{"ok": true}, "创建成功")
	})
	s.BindHandler("/error", func(r *ghttp.Request) {
		Error(r, "参数不合法", 40001)
	})
	s.BindHandler("/error-data", func(r *ghttp.Request) {
		ErrorWithData(r, "余额不足", 40002, g.Map{"balance": 5, "need": 10})
	})
	// WriteException 各分支
	s.BindHandler("/exc/domain", func(r *ghttp.Request) {
		WriteException(r, exception.NewDomain(42201, "库存不足"))
	})
	s.BindHandler("/exc/domain-data", func(r *ghttp.Request) {
		WriteException(r, &exception.AppError{
			Code: 42202, Message: "订单状态不允许取消", Data: g.Map{"status": "shipped"}, Type: exception.TypeDomain,
		})
	})
	s.BindHandler("/exc/infra", func(r *ghttp.Request) {
		WriteException(r, exception.NewInfrastructure(50001, "redis 连接失败"))
	})
	s.BindHandler("/exc/app", func(r *ghttp.Request) {
		WriteException(r, exception.New(40003, "登录已过期"))
	})
	s.BindHandler("/exc/app-data", func(r *ghttp.Request) {
		WriteException(r, exception.NewWithData(40004, "请先实名认证", g.Map{"verified": false}))
	})
	s.BindHandler("/exc/validation", func(r *ghttp.Request) {
		// 只匹配 gcode.CodeValidationFailed 的数值，detail 可自定义
		WriteException(r, gerror.NewCode(
			gcode.New(gcode.CodeValidationFailed.Code(), "validation", nil),
			"用户名不能为空",
		))
	})
	s.BindHandler("/exc/uncaught", func(r *ghttp.Request) {
		WriteException(r, errors.New("boom"))
	})
	// HTTP 状态码语义响应
	s.BindHandler("/unauthorized", func(r *ghttp.Request) {
		Unauthorized(r, "未登录或 token 已失效")
	})
	s.BindHandler("/forbidden", func(r *ghttp.Request) {
		Forbidden(r, "无权访问该资源")
	})
	s.BindHandler("/server-error", func(r *ghttp.Request) {
		ServerError(r, "服务器开小差了")
	})

	if err := s.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "启动测试 server 失败: %v\n", err)
		os.Exit(1)
	}
	defer s.Shutdown()

	testBaseURL = fmt.Sprintf("http://127.0.0.1:%d", s.GetListenedPort())
	os.Exit(m.Run())
}

// getJSON 请求指定路径，返回 HTTP 状态码、Content-Type 与反序列化后的响应体。
func getJSON(t *testing.T, path string) (int, string, respBody) {
	t.Helper()

	resp, err := http.Get(testBaseURL + path)
	if err != nil {
		t.Fatalf("请求 %s 失败: %v", path, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取 %s 响应体失败: %v", path, err)
	}

	var body respBody
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("解析 %s 响应 JSON 失败: %v, 原始内容: %s", path, err, raw)
	}
	return resp.StatusCode, resp.Header.Get("Content-Type"), body
}

// assertCommonMeta 断言 traceId 非空且各字段符合期望（code/message/data）。
func assertCommonMeta(t *testing.T, body respBody, wantCode int, wantMessage string, wantData interface{}) {
	t.Helper()

	if body.TraceID == "" {
		t.Errorf("traceId 为空, 期望非空")
	}
	if body.Code != wantCode {
		t.Errorf("code = %d, want %d", body.Code, wantCode)
	}
	if body.Message != wantMessage {
		t.Errorf("message = %q, want %q", body.Message, wantMessage)
	}
	if !objectsEqual(body.Data, wantData) {
		t.Errorf("data = %#v, want %#v", body.Data, wantData)
	}
}

// objectsEqual 比较两个 JSON 反序列化后的值（map/slice 需逐键比较，避免 DeepEqual 对
// nil 与空 map 的差异误判——本测试中两者语义不同：nil 表示 JSON null，空 map 表示 {}）。
func objectsEqual(got, want interface{}) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	gotRaw, err := json.Marshal(got)
	if err != nil {
		return false
	}
	wantRaw, err := json.Marshal(want)
	if err != nil {
		return false
	}
	return string(gotRaw) == string(wantRaw)
}

// TestSuccess 验证成功响应：HTTP 200、code 0、message "success"、data 原样透传、JSON Content-Type。
func TestSuccess(t *testing.T) {
	status, contentType, body := getJSON(t, "/success")

	if status != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d, want %d", status, http.StatusOK)
	}
	if !strings.Contains(contentType, "application/json") {
		t.Errorf("Content-Type = %q, 期望包含 application/json", contentType)
	}
	assertCommonMeta(t, body, 0, "success", g.Map{"id": 123, "name": "foo"})
}

// TestSuccessWithMessage 验证自定义消息的成功响应。
func TestSuccessWithMessage(t *testing.T) {
	status, _, body := getJSON(t, "/success-msg")

	if status != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d, want %d", status, http.StatusOK)
	}
	assertCommonMeta(t, body, 0, "创建成功", g.Map{"ok": true})
}

// TestError 验证业务错误响应：HTTP 200、code/message 透传、data 为空对象。
func TestError(t *testing.T) {
	status, _, body := getJSON(t, "/error")

	if status != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d, want %d（业务错误应保持 200，前端通过 code 区分）", status, http.StatusOK)
	}
	assertCommonMeta(t, body, 40001, "参数不合法", g.Map{})
}

// TestErrorWithData 验证带附加数据的业务错误响应：data 原样透传。
func TestErrorWithData(t *testing.T) {
	status, _, body := getJSON(t, "/error-data")

	if status != http.StatusOK {
		t.Errorf("HTTP 状态码 = %d, want %d", status, http.StatusOK)
	}
	assertCommonMeta(t, body, 40002, "余额不足", g.Map{"balance": 5, "need": 10})
}

// TestWriteExceptionLayers 表驱动验证 WriteException 按异常类型分流：
// Domain→422、Infrastructure→500、Application/New→200 + 业务 code；Data 为 nil 时各分支的 data 输出。
func TestWriteExceptionLayers(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		wantStatus int
		wantCode   int
		wantMsg    string
		wantData   interface{} // nil 表示期望 JSON null
	}{
		{
			name:       "Domain异常_HTTP422_code与message透传",
			path:       "/exc/domain",
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   42201,
			wantMsg:    "库存不足",
			wantData:   nil, // Domain 分支 Data 原样输出，NewDomain 无 Data → JSON null
		},
		{
			name:       "Domain异常带Data_data透传",
			path:       "/exc/domain-data",
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   42202,
			wantMsg:    "订单状态不允许取消",
			wantData:   g.Map{"status": "shipped"},
		},
		{
			name:       "Infrastructure异常_HTTP500_code透传_data为空对象",
			path:       "/exc/infra",
			wantStatus: http.StatusInternalServerError,
			wantCode:   50001,
			wantMsg:    "redis 连接失败",
			wantData:   g.Map{},
		},
		{
			name:       "Application异常_HTTP200_Data为nil时data输出空对象",
			path:       "/exc/app",
			wantStatus: http.StatusOK,
			wantCode:   40003,
			wantMsg:    "登录已过期",
			wantData:   g.Map{},
		},
		{
			name:       "Application异常带Data_data透传",
			path:       "/exc/app-data",
			wantStatus: http.StatusOK,
			wantCode:   40004,
			wantMsg:    "请先实名认证",
			wantData:   g.Map{"verified": false},
		},
		{
			name:       "未捕获普通error_HTTP500_兜底code99999",
			path:       "/exc/uncaught",
			wantStatus: http.StatusInternalServerError,
			wantCode:   exception.FallbackErrorCode,
			wantMsg:    "服务器内部错误",
			wantData:   g.Map{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, _, body := getJSON(t, tt.path)

			if status != tt.wantStatus {
				t.Errorf("HTTP 状态码 = %d, want %d", status, tt.wantStatus)
			}
			assertCommonMeta(t, body, tt.wantCode, tt.wantMsg, tt.wantData)
		})
	}
}

// TestWriteExceptionValidation 验证校验失败路径：gcode.CodeValidationFailed 错误的 code
// 来自 SetValidationErrorCode 设置的全局值。先断言默认值 400，再设非常见值 42200 验证参数化生效，
// defer 还原默认值，避免污染全局状态。
func TestWriteExceptionValidation(t *testing.T) {
	// 兜底还原默认 code（包内初始值为 400）
	defer SetValidationErrorCode(400)

	// 默认值：HTTP 200 + code 400
	status, _, body := getJSON(t, "/exc/validation")
	if status != http.StatusOK {
		t.Errorf("默认: HTTP 状态码 = %d, want %d", status, http.StatusOK)
	}
	assertCommonMeta(t, body, 400, "用户名不能为空", g.Map{})

	// 参数化：设为非常见值 42200，验证设置真正生效（而非恰好等于默认值）
	SetValidationErrorCode(42200)
	status, _, body = getJSON(t, "/exc/validation")
	if status != http.StatusOK {
		t.Errorf("参数化后: HTTP 状态码 = %d, want %d", status, http.StatusOK)
	}
	assertCommonMeta(t, body, 42200, "用户名不能为空", g.Map{})
}

// TestHttpStatusHelpers 表驱动验证固定 HTTP 状态码语义响应：
// Unauthorized→401/code 401、Forbidden→403/code 403、ServerError→500/code 99999。
func TestHttpStatusHelpers(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		wantStatus int
		wantCode   int
		wantMsg    string
	}{
		{"Unauthorized_HTTP401", "/unauthorized", http.StatusUnauthorized, 401, "未登录或 token 已失效"},
		{"Forbidden_HTTP403", "/forbidden", http.StatusForbidden, 403, "无权访问该资源"},
		{"ServerError_HTTP500_固定code99999", "/server-error", http.StatusInternalServerError, 99999, "服务器开小差了"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, _, body := getJSON(t, tt.path)

			if status != tt.wantStatus {
				t.Errorf("HTTP 状态码 = %d, want %d", status, tt.wantStatus)
			}
			assertCommonMeta(t, body, tt.wantCode, tt.wantMsg, g.Map{})
		})
	}
}
