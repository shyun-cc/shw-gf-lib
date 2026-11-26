package wecom

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shyun-cc/shw-gf-lib/internal/testredis"
)

// TestErr_BizErrorCarriesErrcode 业务异常：errors.As 捕获原始 errcode
func TestErr_BizErrorCarriesErrcode(t *testing.T) {
	testredis.Start(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"errcode":40029,"errmsg":"invalid code"}`))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(Credential{CorpID: "corp1", AgentID: 100, Secret: "sec", AppType: AppAgent})
	c.SetBaseURL(srv.URL)
	seedToken(t, "TK")

	_, err := c.GetUserinfoByCode(context.Background(), "BAD")
	if err == nil {
		t.Fatal("40029 应报错")
	}
	var biz *BizError
	if !errors.As(err, &biz) {
		t.Fatalf("应可 errors.As 捕获 BizError: %v", err)
	}
	if biz.Errcode != 40029 || biz.Errmsg != "invalid code" {
		t.Fatalf("BizError 应携带原始值: %+v", biz)
	}
	var infra *InfraError
	if errors.As(err, &infra) {
		t.Fatal("业务错误不应同时是 InfraError")
	}
}

// TestErr_InfraErrorNonJSON 响应非 JSON：InfraError 单独可断言
func TestErr_InfraErrorNonJSON(t *testing.T) {
	testredis.Start(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html>gateway error</html>"))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(Credential{CorpID: "corp1", AgentID: 100, Secret: "sec", AppType: AppAgent})
	c.SetBaseURL(srv.URL)
	seedToken(t, "TK")

	_, err := c.GetUserinfoByCode(context.Background(), "C1")
	if err == nil {
		t.Fatal("非 JSON 应报错")
	}
	var infra *InfraError
	if !errors.As(err, &infra) {
		t.Fatalf("应可 errors.As 捕获 InfraError: %v", err)
	}
	var biz *BizError
	if errors.As(err, &biz) {
		t.Fatal("基础设施错误不应同时是 BizError")
	}
}

// TestErr_InfraErrorNetworkFail 网络失败：InfraError 且 Unwrap 非 nil
func TestErr_InfraErrorNetworkFail(t *testing.T) {
	testredis.Start(t)
	// 立即关闭的 server：连接拒绝
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()

	c := NewClient(Credential{CorpID: "corp1", AgentID: 100, Secret: "sec", AppType: AppAgent})
	c.SetBaseURL(srv.URL)
	seedToken(t, "TK")

	_, err := c.GetUserinfoByCode(context.Background(), "C1")
	if err == nil {
		t.Fatal("连接拒绝应报错")
	}
	var infra *InfraError
	if !errors.As(err, &infra) {
		t.Fatalf("应可 errors.As 捕获 InfraError: %v", err)
	}
	if errors.Unwrap(infra) == nil {
		t.Fatal("InfraError 应可 Unwrap 底层错误")
	}
}

// TestErr_TokenFetchBizError gettoken errcode ≠ 0：BizError（凭证链路业务侧）
func TestErr_TokenFetchBizError(t *testing.T) {
	testredis.Start(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"errcode":40001,"errmsg":"invalid credential"}`))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(Credential{CorpID: "corp1", AgentID: 100, Secret: "sec", AppType: AppAgent})
	c.SetBaseURL(srv.URL)

	_, err := c.GetUserinfoByCode(context.Background(), "C1")
	if err == nil {
		t.Fatal("gettoken 失败应报错")
	}
	var biz *BizError
	if !errors.As(err, &biz) || biz.Errcode != 40001 {
		t.Fatalf("gettoken errcode 应为 BizError: %v", err)
	}
}
