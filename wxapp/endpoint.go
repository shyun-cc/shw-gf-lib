package wxapp

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

// Endpoint 服务端 API 端点声明式表项：每个域方法 = 一行表项 + 一行薄委托（经 invoke 统一分发）。
// 新增/修正一个端点 = 改一行表项；表项是表驱动端点断言测试的遍历对照面。
// 注：小程序侧各端点的凭证注入形态不一（code2Session/token 走 appid+secret、业务端点显式带
// access_token query），故 Query 清单原样声明所用参数、invoke 不做 token 自动注入。
type Endpoint struct {
	Name   string   // 端点名（表键 + 测试标识）
	Method string   // HTTP method
	Path   string   // API path
	Query  []string // 显式 query 参数名清单
	Body   bool     // 是否携带 JSON body
}

// 端点表：全量端点在此登记（allEndpoints 为测试遍历对照面，新表项必须同步配测试用例）
var (
	epCode2Session = Endpoint{"login.code2session", http.MethodGet, "/sns/jscode2session", []string{"appid", "secret", "js_code", "grant_type"}, false}
	epGetUserPhone = Endpoint{"phone.getuserphonenumber", http.MethodPost, "/wxa/business/getuserphonenumber", []string{"access_token"}, true}
	epToken        = Endpoint{"token.fetch", http.MethodGet, "/cgi-bin/token", []string{"grant_type", "appid", "secret"}, false}
)

// 用户信息 + 小程序码与链接表项（官方取证见 change wxapp-qrcode-userinfo proposal；
// 图片三件应答为二进制 buffer，经 invokeRaw 分发）
var (
	epGetPluginOpenPID  = Endpoint{"user.getpluginopenpid", http.MethodPost, "/wxa/getpluginopenpid", []string{"access_token"}, true}
	epCheckEncryptedMsg = Endpoint{"user.checkencryptedmsg", http.MethodPost, "/wxa/business/checkencryptedmsg", []string{"access_token"}, true}
	epGetPaidUnionID    = Endpoint{"user.getpaidunionid", http.MethodGet, "/wxa/getpaidunionid", []string{"access_token", "openid", "transaction_id", "mch_id", "out_trade_no"}, false}
	epGetUserEncryptKey = Endpoint{"user.getuserencryptkey", http.MethodGet, "/wxa/business/getuserencryptkey", []string{"access_token", "openid", "signature", "sig_method"}, false}

	epGetWXACode        = Endpoint{"qrcode.getwxacode", http.MethodPost, "/wxa/getwxacode", []string{"access_token"}, true}
	epGetWXACodeUnlimit = Endpoint{"qrcode.getwxacodeunlimit", http.MethodPost, "/wxa/getwxacodeunlimit", []string{"access_token"}, true}
	epCreateWXAQRCode   = Endpoint{"qrcode.createwxaqrcode", http.MethodPost, "/cgi-bin/wxaapp/createwxaqrcode", []string{"access_token"}, true}
	epQueryScheme       = Endpoint{"qrcode.queryscheme", http.MethodPost, "/wxa/queryscheme", []string{"access_token"}, true}
	epGenerateScheme    = Endpoint{"qrcode.generatescheme", http.MethodPost, "/wxa/generatescheme", []string{"access_token"}, true}
	epGenerateNFCScheme = Endpoint{"qrcode.generatenfcscheme", http.MethodPost, "/wxa/generatenfcscheme", []string{"access_token"}, true}
	epGenerateURLLink   = Endpoint{"qrcode.generate_urllink", http.MethodPost, "/wxa/generate_urllink", []string{"access_token"}, true}
	epQueryURLLink      = Endpoint{"qrcode.query_urllink", http.MethodPost, "/wxa/query_urllink", []string{"access_token"}, true}
	epGenShortLink      = Endpoint{"qrcode.genwxashortlink", http.MethodPost, "/wxa/genwxashortlink", []string{"access_token"}, true}
)

// 二批表项：凭证 stable_token + openApi 管理 8 + 客服消息 10（官方取证见 change
// wxapp-openapi-kf proposal；clear_v2 为 POST query 传参特例、media_upload 为 multipart）
var (
	epStableToken   = Endpoint{"token.stable", http.MethodPost, "/cgi-bin/stable_token", nil, true}
	epQuotaGet      = Endpoint{"openapi.quota_get", http.MethodPost, "/cgi-bin/openapi/quota/get", []string{"access_token"}, true}
	epQuotaClearAll = Endpoint{"openapi.clear_quota", http.MethodPost, "/cgi-bin/clear_quota", []string{"access_token"}, true}
	epQuotaClearOne = Endpoint{"openapi.quota_clear", http.MethodPost, "/cgi-bin/openapi/quota/clear", []string{"access_token"}, true}
	epQuotaClearV2  = Endpoint{"openapi.clear_v2", http.MethodPost, "/cgi-bin/clear_quota/v2", []string{"appid", "appsecret"}, false}
	epRidGet        = Endpoint{"openapi.rid_get", http.MethodPost, "/cgi-bin/openapi/rid/get", []string{"access_token"}, true}
	epCallbackCheck = Endpoint{"openapi.callback_check", http.MethodPost, "/cgi-bin/callback/check", []string{"access_token"}, true}
	epApiDomainIP   = Endpoint{"openapi.api_domain_ip", http.MethodGet, "/cgi-bin/get_api_domain_ip", []string{"access_token"}, false}
	epCallbackIP    = Endpoint{"openapi.callback_ip", http.MethodGet, "/cgi-bin/getcallbackip", []string{"access_token"}, false}

	epKFAccountAdd  = Endpoint{"kf.account_add", http.MethodPost, "/customservice/kfaccount/add", []string{"access_token"}, true}
	epKFList        = Endpoint{"kf.list", http.MethodGet, "/cgi-bin/customservice/getkflist", []string{"access_token", "business_id"}, false}
	epKFOnlineList  = Endpoint{"kf.online_list", http.MethodGet, "/cgi-bin/customservice/getonlinekflist", []string{"access_token", "business_id"}, false}
	epKFAccountDel  = Endpoint{"kf.account_del", http.MethodPost, "/customservice/kfaccount/del", []string{"access_token"}, true}
	epKFSend        = Endpoint{"kf.send", http.MethodPost, "/cgi-bin/message/custom/send", []string{"access_token"}, true}
	epKFTyping      = Endpoint{"kf.typing", http.MethodPost, "/cgi-bin/message/custom/business/typing", []string{"access_token"}, true}
	epKFSetAdmin    = Endpoint{"kf.set_admin", http.MethodGet, "/customservice/kfaccount/setadmin", []string{"access_token", "kf_openid"}, false}
	epKFCancelAdmin = Endpoint{"kf.cancel_admin", http.MethodGet, "/customservice/kfaccount/canceladmin", []string{"access_token", "kf_openid"}, false}
	epKFMediaUpload = Endpoint{"kf.media_upload", http.MethodPost, "/cgi-bin/media/upload", []string{"access_token", "type"}, true}
	epKFMediaGet    = Endpoint{"kf.media_get", http.MethodGet, "/cgi-bin/media/get", []string{"access_token", "media_id"}, false}
)

var allEndpoints = []Endpoint{
	epCode2Session, epGetUserPhone, epToken,
	epGetPluginOpenPID, epCheckEncryptedMsg, epGetPaidUnionID, epGetUserEncryptKey,
	epGetWXACode, epGetWXACodeUnlimit, epCreateWXAQRCode,
	epQueryScheme, epGenerateScheme, epGenerateNFCScheme,
	epGenerateURLLink, epQueryURLLink, epGenShortLink,
	epStableToken, epQuotaGet, epQuotaClearAll, epQuotaClearOne, epQuotaClearV2,
	epRidGet, epCallbackCheck, epApiDomainIP, epCallbackIP,
	epKFAccountAdd, epKFList, epKFOnlineList, epKFAccountDel,
	epKFSend, epKFTyping, epKFSetAdmin, epKFCancelAdmin, epKFMediaUpload, epKFMediaGet,
	epActivityIDCreate, epUpdatableSend, epChatToolSend,
	epSubsGetCategory, epSubsPubTitles, epSubsPubKeywords, epSubsAddTemplate,
	epSubsGetTemplate, epSubsDelTemplate, epSubsSend,
	epSubsSetNotify, epSubsSetNotifyEx, epSubsGetNotify,
	epKFBizRegister, epKFBizUpdate, epKFBizGet, epKFBizList,
	epMsgSecCheck, epMediaCheck, epUserRiskRank,
	epNearbyPoiAdd, epNearbyPoiDel, epNearbyPoiList, epNearbyPoiStats,
	epRetainDaily, epRetainWeekly, epRetainMonthly,
	epTrendDaily, epTrendWeekly, epTrendMonthly,
	epSummaryDaily, epVisitPage, epUserPortrait, epVisitDist, epPerformance,
	epImgAiCrop, epImgQrcode, epImgSuperRes,
	epOCRComm, epOCRIdcard, epOCRDriving, epOCRBankcard, epOCRBizlicense, epOCRDrivingLicense,
	epSoterVerify, epFaceGetVerify, epFaceQueryVerify,
	epSearchSubmit, epRedCoverURL, epSvcMktInvoke, epSvcMktRetrieve,
	epLiveRoomCreate, epLiveRoomEdit, epLiveRoomDelete, epLiveInfo,
	epLivePushURL, epLiveSharedCode, epLiveSubAnchorGet, epLiveSubAnchorMod,
	epLiveSubAnchorDel, epLiveSubAnchorAdd, epLiveGoodsImport, epLiveGoodsInRoomDel,
	epLiveGoodsPush, epLiveGoodsOnSale, epLiveGoodsSort,
	epLiveAstAdd, epLiveAstModify, epLiveAstRemove, epLiveAstList,
	epLiveComment, epLiveFeedPublic, epLiveKF, epLiveReplay,
	epLiveGoodsVideo, epLiveGoodsKeySet, epLiveGoodsKeyGet,
	epLiveGoodsAdd, epLiveGoodsAudit, epLiveGoodsWhse, epLiveGoodsReset,
	epLiveGoodsUpdate, epLiveGoodsApprove, epLiveGoodsDelete,
	epLiveRoleAdd, epLiveRoleDelete, epLiveRoleList, epLivePushMsg, epLiveFollowers,
	epHWDeviceMsg, epHWSnTicket, epHWIotGroupCreate, epHWIotGroupGet,
	epHWIotGroupAddDev, epHWIotGroupRmDev, epHWLicensePkgs, epHWLicenseActive, epHWLicenseDevInfo,
	epOpsDomainInfo, epOpsPerformance, epOpsSceneList, epOpsVersionList,
	epOpsRtLogSearch, epOpsFeedbackList, epOpsFeedbackImg,
	epOpsJsErrDetail, epOpsJsErrList, epOpsGrayRelease,
	epPluginApplication, epPluginManage,
	epTradeUploadShip, epTradeUploadComb, epTradeGetOrder, epTradeGetOrderList,
	epTradeNotifyRecv, epTradeSetJumpPath, epTradeIsManaged, epTradeIsConfirm,
	epTradeOpSpecial, epTradeBrandApply, epTradeBrandStatus, epTradeSetType,
	epTGPenaltyList, epTGGuarantee, epTGCommentList, epTGReplyList, epTGCommentInfo,
	epTGReplyAdd, epTGReplyDel, epTGCReplyAdd, epTGCReplyDel, epTGKfQuota, epTGCompromise,
	epChargeUsageDetail, epChargeRecentAvg,
	epExpAccountBind, epExpAccountGet, epExpDeliveryGet, epExpOrderAdd,
	epExpOrderCancel, epExpOrderGet, epExpOrderBatch, epExpPathGet,
	epExpPrinterUpd, epExpPrinterGet, epExpQuotaGet, epExpTestUpdate,
	epExpProvBizUpdate, epExpProvPath, epExpProvPreview, epExpProvContact,
	epExpScatterCancel, epExpScatterFee, epExpScatterRefund, epExpScatterBill,
	epExpScatterComp, epExpScatterStatus,
	epLocalDeliveryGet, epLocalOrderPreAdd, epLocalShopGet, epLocalPreCancel,
	epLocalOpen, epLocalShopAdd, epLocalOrderReAdd, epLocalRealMock,
	epLocalMock, epLocalOrderGet, epLocalConfirmRet, epLocalOrderCancel,
	epLocalAddTips, epLocalOrderAdd, epLocalProvUpdate,
	epWexpTraceWaybill, epWexpQueryTrace, epWexpUpdateGoods, epWexpDeliveryList,
	epWexpFollowWaybill, epWexpQueryFollow, epWexpUpdateFollow,
	epWexpUserQuery, epWexpPathNotify,
	epInsOpen, epInsQueryOpen, epInsCreateOrder, epInsClaim,
	epInsChargeID, epInsApplyPay, epInsPayOrders, epInsRefund,
	epInsSummary, epInsOrders, epInsNotifyFunds,
	epRetAdd, epRetGet, epRetUnbind,
	epCityApply, epCityCreateStore, epCityQueryStore, epCityUpdateStore,
	epCityCharge, epCityRefund, epCityQueryFlow, epCityBalance,
	epCityPreAddOrder, epCityAddOrder, epCityQueryOrder, epCityCancelOrder,
	epCitySetPayMode, epCityGetPayMode, epCityGetCity, epCityMockNotify,
	epKfWorkGet, epKfWorkBind, epKfWorkUnbind,
}

// invoke 统一分发：query 键集按表项校验、errcode ≠ 0 → BizError、HTTP/解码失败 → InfraError。
// 表项字段变更不需要改动本实现。
func (s *WxAppService) invoke(ctx context.Context, ep Endpoint, query map[string]string, body any) (map[string]any, error) {
	for k := range query {
		declared := false
		for _, q := range ep.Query {
			if k == q {
				declared = true
				break
			}
		}
		if !declared {
			return nil, fmt.Errorf("wxapp: 端点 %s 未声明 query 参数 %s（补表项 Query 清单或改传已声明参数）", ep.Name, k)
		}
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

// invokeRaw 二进制应答分发：小程序码三件成功时应答为图片 buffer（Content-Type 含 image），
// 失败时为 JSON（errcode≠0 → BizError）。表项与 query 校验复用 invoke 语义。
func (s *WxAppService) invokeRaw(ctx context.Context, ep Endpoint, query map[string]string, body any) ([]byte, error) {
	for k := range query {
		declared := false
		for _, q := range ep.Query {
			if k == q {
				declared = true
				break
			}
		}
		if !declared {
			return nil, fmt.Errorf("wxapp: 端点 %s 未声明 query 参数 %s（补表项 Query 清单或改传已声明参数）", ep.Name, k)
		}
	}
	var raw []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, &InfraError{Op: "encode", Err: fmt.Errorf("请求体编码失败: %w", err)}
		}
		raw = b
	}
	return s.requestRaw(ctx, ep.Method, ep.Path, query, raw)
}

// request 发起微信 API 请求（GET query / POST JSON body），返回解码 map
func (s *WxAppService) request(ctx context.Context, method, path string, query map[string]string, body []byte) (map[string]any, error) {
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

func encodeQuery(query map[string]string) string {
	vals := url.Values{}
	for k, v := range query {
		vals.Set(k, v)
	}
	return vals.Encode()
}

// requestRaw 发起请求并按 Content-Type 分流：image → 原始字节；JSON → errcode≠0 报 BizError。
func (s *WxAppService) requestRaw(ctx context.Context, method, path string, query map[string]string, body []byte) ([]byte, error) {
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
	if strings.Contains(resp.Header.Get("Content-Type"), "image") {
		return raw, nil
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, &InfraError{Op: "decode", Err: fmt.Errorf("响应非 JSON 非图片: %w", err)}
	}
	if code, ok := decoded["errcode"].(float64); ok && code != 0 {
		errmsg, _ := decoded["errmsg"].(string)
		return nil, &BizError{Errcode: int(code), Errmsg: errmsg}
	}
	// JSON 无 errcode（二进制端点成功应答恒为图片，此分支仅防御）
	return raw, nil
}

// invokeMultipart multipart 上传分发（客服临时素材 part 名 media、图像/OCR part 名 img）：
// access_token 等走 query，body 为 multipart/form-data 单 part（无签名 meta 语义，区别于 wxpay）。
func (s *WxAppService) invokeMultipart(ctx context.Context, ep Endpoint, query map[string]string, partName, filename string, content []byte) (map[string]any, error) {
	for k := range query {
		declared := false
		for _, q := range ep.Query {
			if k == q {
				declared = true
				break
			}
		}
		if !declared {
			return nil, fmt.Errorf("wxapp: 端点 %s 未声明 query 参数 %s", ep.Name, k)
		}
	}
	base := s.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	target := base + ep.Path
	if len(query) > 0 {
		target += "?" + encodeQuery(query)
	}
	boundary := "wxapp-" + fmt.Sprint(time.Now().UnixNano())
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
