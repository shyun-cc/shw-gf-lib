package wxoa

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultBaseURL = "https://api.weixin.qq.com"

// Endpoint 服务端 API 端点声明式表项（四包统一表驱动薄委托模式）。
// 注：token.fetch 的 Query 显式声明凭证参数（该端点本身是取 token 的，无 access_token 注入）；
// 业务端点 Query 不含 access_token（由 invoke 自动注入）。
type Endpoint struct {
	Name   string
	Method string
	Path   string
	Query  []string
	Body   bool
}

// 端点表：全量端点在此登记（allEndpoints 为测试遍历对照面）。
// 首批六基础域 51 项见 change wxoa-basic-domains proposal；消息/数据/智能/网页授权等域二批起铺设。
var (
	epToken = Endpoint{"token.fetch", http.MethodGet, "/cgi-bin/token", []string{"grant_type", "appid", "secret"}, false}
)

var allEndpoints = []Endpoint{
	epToken, epJSAPITicket,
	epMenuCreate, epMenuSelfGet, epMenuGet, epMenuDelete, epMenuAddCond, epMenuDelCond, epMenuTryMatch,
	epTagsCreate, epTagsUpdate, epTagsDelete, epTagsGet, epTagUsers,
	epBatchTagging, epBatchUntag, epUserTagIDs,
	epUserInfo, epUserInfoBat, epUserList, epBlackAdd, epBlackDel, epBlackGet, epUserRemark,
	epMaterialAdd, epMaterialGet, epMaterialCount, epMaterialBatch, epMaterialDel,
	epMediaUploadImg, epMediaUpload, epMediaGet, epMediaJSSDKVoice,
	epCommentOpen, epCommentList, epCommentClose, epCommentElect, epCommentUnelect,
	epCommentDelete, epCommentReplyAdd, epCommentReplyDel,
	epPublishSubmit, epPublishStatus, epPublishBatch, epPublishArticle, epPublishDelete,
	epDraftSwitch, epDraftAdd, epDraftGet, epDraftBatch, epDraftCount, epDraftDelete, epDraftUpdate,
	epMassSendAll, epMassSend, epMassPreview, epMassDelete, epMassGet, epMassSpdGet, epMassSpdSet, epMassNews,
	epSubsCategory, epSubsPubTitle, epSubsPubKw, epSubsAddTpl, epSubsGetTpl, epSubsDelTpl, epSubsBizSend,
	epTmplSetIndustry, epTmplGetIndustry, epTmplAdd, epTmplList, epTmplDel, epTmplSend, epTmplBlocked,
	epKFAccountAdd, epKFAccountUpd, epKFAccountDel, epKFHeadImg, epKFInvite, epKFList, epKFOnlineList,
	epKFSessionCreate, epKFSessionClose, epKFSessionGet, epKFSessionList, epKFWaitCases,
	epKFSend, epKFTyping, epKFMsgList,
	epStatsUsersummary, epStatsUsercumulate,
	epStatsArticlesummary, epStatsUserread, epStatsUserreadhour, epStatsArticletotal,
	epStatsUsershare, epStatsUsersharehour, epStatsArticleread, epStatsArticleshare,
	epStatsBizsummary, epStatsArticletotaldetail,
	epStatsUpstreammsg, epStatsUpstreammsghour, epStatsUpstreammsgweek, epStatsUpstreammsgmonth,
	epStatsUpstreammsgdist, epStatsUpstreammsgdistweek, epStatsUpstreammsgdistmonth,
	epStatsInterfacesummary, epStatsInterfacesummaryhour,
	epAITranslate, epAIVoiceAdd, epAIVoiceQuery,
	epOcrComm, epOcrIdcard, epOcrDriving, epOcrDrvLicense,
	epOcrBankcard, epOcrBizlicense, epOcrMenu,
	epSnsToken, epSnsRefresh, epSnsUserinfo, epSnsAuth,
}

// invoke 统一分发：query 键集按表项校验、业务端点自动注入 access_token（显式声明 access_token 的不重复注入）、
// errcode ≠ 0 → BizError、HTTP/解码失败 → InfraError。
func (s *WxOAService) invoke(ctx context.Context, ep Endpoint, query map[string]string, body any) (map[string]any, error) {
	if err := checkQuery(ep, query); err != nil {
		return nil, err
	}
	var raw []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, &InfraError{Op: "encode", Err: fmt.Errorf("请求体编码失败: %w", err)}
		}
		raw = b
	}
	return s.request(ctx, ep.Method, ep.Path, query, raw)
}

// request 发起微信 API 请求，返回解码 map（errcode≠0 → BizError）
func (s *WxOAService) request(ctx context.Context, method, path string, query map[string]string, body []byte) (map[string]any, error) {
	base := s.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	target := base + path
	if len(query) > 0 {
		target += "?" + encodeQuery(query)
	}
	var reader io.Reader
	if body != nil {
		reader = strings.NewReader(string(body))
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, &InfraError{Op: "http", Err: err}
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := s.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, &InfraError{Op: "http", Err: err}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &InfraError{Op: "http", Err: err}
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, &InfraError{Op: "decode", Err: fmt.Errorf("响应非 JSON: %w", err)}
	}
	if code, ok := decoded["errcode"].(float64); ok && code != 0 {
		errmsg, _ := decoded["errmsg"].(string)
		return decoded, &BizError{Errcode: int(code), Errmsg: errmsg}
	}
	return decoded, nil
}

func checkQuery(ep Endpoint, query map[string]string) error {
	for k := range query {
		if k == "access_token" {
			continue // invoke 自动注入的凭证键，全局豁免（原实现豁免在 Query 循环内，Query=nil 的端点失效）
		}
		declared := false
		for _, q := range ep.Query {
			if k == q {
				declared = true
				break
			}
		}
		if !declared {
			return fmt.Errorf("wxoa: 端点 %s 未声明 query 参数 %s", ep.Name, k)
		}
	}
	return nil
}

func encodeQuery(query map[string]string) string {
	vals := url.Values{}
	for k, v := range query {
		vals.Set(k, v)
	}
	return vals.Encode()
}

// invokeMultipart multipart 上传分发（素材上传 part 名 media 等）：access_token 走 query，
// body 为 multipart/form-data 单 part（同 wxapp 形态，各包独立实现）。
func (s *WxOAService) invokeMultipart(ctx context.Context, ep Endpoint, query map[string]string, partName, filename string, content []byte) (map[string]any, error) {
	if err := checkQuery(ep, query); err != nil {
		return nil, err
	}
	base := s.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	target := base + ep.Path
	if len(query) > 0 {
		target += "?" + encodeQuery(query)
	}
	boundary := "wxoa-" + fmt.Sprint(time.Now().UnixNano())
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "--%s\r\nContent-Disposition: form-data; name=%q; filename=%q\r\nContent-Type: application/octet-stream\r\n\r\n", boundary, partName, filename)
	buf.Write(content)
	fmt.Fprintf(&buf, "\r\n--%s--\r\n", boundary)
	req, err := http.NewRequestWithContext(ctx, ep.Method, target, &buf)
	if err != nil {
		return nil, &InfraError{Op: "http", Err: err}
	}
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	client := s.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, &InfraError{Op: "http", Err: err}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &InfraError{Op: "http", Err: err}
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, &InfraError{Op: "decode", Err: fmt.Errorf("响应非 JSON: %w", err)}
	}
	if code, ok := decoded["errcode"].(float64); ok && code != 0 {
		errmsg, _ := decoded["errmsg"].(string)
		return decoded, &BizError{Errcode: int(code), Errmsg: errmsg}
	}
	return decoded, nil
}

// invokeRaw 二进制应答分发（临时素材下载：Content-Type 含 image/video 等 → 原始字节；
// JSON 失败应答 → BizError）。
func (s *WxOAService) invokeRaw(ctx context.Context, ep Endpoint, query map[string]string) ([]byte, error) {
	if err := checkQuery(ep, query); err != nil {
		return nil, err
	}
	base := s.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	target := base + ep.Path
	if len(query) > 0 {
		target += "?" + encodeQuery(query)
	}
	req, err := http.NewRequestWithContext(ctx, ep.Method, target, nil)
	if err != nil {
		return nil, &InfraError{Op: "http", Err: err}
	}
	client := s.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, &InfraError{Op: "http", Err: err}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &InfraError{Op: "http", Err: err}
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "json") && !strings.Contains(ct, "text") {
		return raw, nil
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return raw, nil // 非 JSON 非已知类型按二进制透传
	}
	if code, ok := decoded["errcode"].(float64); ok && code != 0 {
		errmsg, _ := decoded["errmsg"].(string)
		return nil, &BizError{Errcode: int(code), Errmsg: errmsg}
	}
	return raw, nil
}
