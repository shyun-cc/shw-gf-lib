// Package exception 单元测试（纯逻辑，无外部依赖）。
//
// 覆盖点：
//   - NewDomain/NewApplication/NewInfrastructure 各自写入正确的 Type 常量并透传 Code/Message
//   - New 向后兼容（等价于 NewApplication，Type 为 application）
//   - NormalizedCode 规范化：非 0 返回原值，0 返回 FallbackErrorCode(99999)
//   - Error() 输出格式 "[code] message"
//   - NewWithData 携带 Data 字段
package exception

import (
	"reflect"
	"testing"
)

// TestNewTyped 验证三种分层构造器写入正确的 Type 常量，并透传 Code 与 Message。
func TestNewTyped(t *testing.T) {
	tests := []struct {
		name     string
		got      *AppError
		wantCode int
		wantMsg  string
		wantType string
	}{
		{"领域异常_Type为domain", NewDomain(1001, "库存不足"), 1001, "库存不足", TypeDomain},
		{"应用异常_Type为application", NewApplication(2001, "参数缺失"), 2001, "参数缺失", TypeApplication},
		{"基础设施异常_Type为infrastructure", NewInfrastructure(3001, "db down"), 3001, "db down", TypeInfrastructure},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got == nil {
				t.Fatalf("构造器返回了 nil")
			}
			if tt.got.Type != tt.wantType {
				t.Errorf("Type = %q, want %q", tt.got.Type, tt.wantType)
			}
			if tt.got.Code != tt.wantCode {
				t.Errorf("Code = %d, want %d", tt.got.Code, tt.wantCode)
			}
			if tt.got.Message != tt.wantMsg {
				t.Errorf("Message = %q, want %q", tt.got.Message, tt.wantMsg)
			}
		})
	}
}

// TestNewBackwardCompat 验证 New 为向后兼容入口：行为等价于 NewApplication（Type 为 application）。
func TestNewBackwardCompat(t *testing.T) {
	got := New(1002, "登录已过期")
	want := NewApplication(1002, "登录已过期")

	if got.Type != TypeApplication {
		t.Errorf("New 的 Type = %q, want %q（应与 NewApplication 一致）", got.Type, TypeApplication)
	}
	if got.Code != want.Code || got.Message != want.Message || got.Type != want.Type {
		t.Errorf("New 与 NewApplication 行为不一致: got %+v, want %+v", got, want)
	}
}

// TestNormalizedCode 验证错误码规范化：非 0（含负数）返回原值，0 兜底为 FallbackErrorCode。
func TestNormalizedCode(t *testing.T) {
	tests := []struct {
		name string
		err  *AppError
		want int
	}{
		{"非零错误码_返回原值", NewApplication(40001, "参数错误"), 40001},
		{"负数错误码_非零同样返回原值", NewApplication(-1, "负码"), -1},
		{"零错误码_兜底为FallbackErrorCode", &AppError{Code: 0, Message: "未设码"}, FallbackErrorCode},
		{"零错误码_兜底值为99999", NewApplication(0, "零码"), 99999},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.NormalizedCode(); got != tt.want {
				t.Errorf("NormalizedCode() = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestErrorMessage 验证 Error() 输出格式为 "[code] message"。
func TestErrorMessage(t *testing.T) {
	tests := []struct {
		name string
		err  *AppError
		want string
	}{
		{"常规格式", NewDomain(1001, "库存不足"), "[1001] 库存不足"},
		{"零码格式", NewApplication(0, "兜底场景"), "[0] 兜底场景"},
		{"英文消息", NewInfrastructure(500, "db down"), "[500] db down"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestNewWithData 验证 NewWithData 在应用异常基础上额外携带 Data 字段。
func TestNewWithData(t *testing.T) {
	data := map[string]interface{}{"field": "username"}
	got := NewWithData(40001, "校验失败", data)

	if got.Code != 40001 {
		t.Errorf("Code = %d, want 40001", got.Code)
	}
	if got.Message != "校验失败" {
		t.Errorf("Message = %q, want %q", got.Message, "校验失败")
	}
	if got.Type != TypeApplication {
		t.Errorf("Type = %q, want %q", got.Type, TypeApplication)
	}
	// Data 必须原样携带（内容相等）
	if !reflect.DeepEqual(got.Data, data) {
		t.Errorf("Data = %#v, want %#v", got.Data, data)
	}
}

// TestLibErrorCodeSegments 十六段库内错误码常量：值 99901-99916 逐段对齐 hyperf-lib LibErrorCode
func TestLibErrorCodeSegments(t *testing.T) {
	segments := []struct {
		name string
		got  int
		want int
	}{
		{"CodeSeqNumGenFailed", CodeSeqNumGenFailed, 99901},
		{"CodeFileUploadFailed", CodeFileUploadFailed, 99902},
		{"CodeAliyunServiceError", CodeAliyunServiceError, 99903},
		{"CodeProcessLockError", CodeProcessLockError, 99904},
		{"CodeProcessSignalError", CodeProcessSignalError, 99905},
		{"CodeProcessIterationMax", CodeProcessIterationMax, 99906},
		{"CodeProcessMemoryLimit", CodeProcessMemoryLimit, 99907},
		{"CodeSmsServiceError", CodeSmsServiceError, 99908},
		{"CodeVerifyCodeRateLimited", CodeVerifyCodeRateLimited, 99909},
		{"CodeVerifyCodeExpired", CodeVerifyCodeExpired, 99910},
		{"CodeVerifyCodeMaxAttempts", CodeVerifyCodeMaxAttempts, 99911},
		{"CodeWecomServiceError", CodeWecomServiceError, 99912},
		{"CodeAttachmentStoreError", CodeAttachmentStoreError, 99913},
		{"CodeExpressServiceError", CodeExpressServiceError, 99914},
		{"CodeKd100ServiceError", CodeKd100ServiceError, 99915},
		{"CodeRegionServiceError", CodeRegionServiceError, 99916},
	}
	if len(segments) != 16 {
		t.Fatalf("应恰为十六段, 实际 %d", len(segments))
	}
	for _, s := range segments {
		if s.got != s.want {
			t.Errorf("%s = %d, want %d", s.name, s.got, s.want)
		}
	}
	if FallbackErrorCode != 99999 {
		t.Errorf("FallbackErrorCode = %d, want 99999", FallbackErrorCode)
	}
}
