package wxapp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"

	"github.com/shyun-cc/shw-gf-lib/internal/testredis"
)

// TestEndpoints_TableDriven 表驱动端点断言：遍历全量表项，桩逐项断言
// method/path/query 键集/body 形态；计数相等防漂移。
// token 端点断言 query 为 appid/secret/grant_type（无 access_token——该端点本身是取 token 的）。
func TestEndpoints_TableDriven(t *testing.T) {
	testredis.Start(t) // getuserphonenumber 走 GetAccessToken（缓存+fetch）

	cases := []struct {
		ep     Endpoint
		invoke func(s *WxAppService, base string) error
	}{
		{
			ep: epCode2Session,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.Code2Session(context.Background(), "CODE_T")
				return err
			},
		},
		{
			ep: epGetUserPhone,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.GetPhoneNumber(context.Background(), "PHONE_T")
				return err
			},
		},
		{
			ep: epToken,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.GetAccessToken(context.Background())
				return err
			},
		},
		{
			ep: epGetPluginOpenPID,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.GetPluginOpenPID(context.Background(), "PC1")
				return err
			},
		},
		{
			ep: epCheckEncryptedMsg,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.CheckEncryptedData(context.Background(), "hash1")
				return err
			},
		},
		{
			ep: epGetPaidUnionID,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.GetPaidUnionID(context.Background(), "o1", "TX1", "MCH1", "N1")
				return err
			},
		},
		{
			ep: epGetUserEncryptKey,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.GetUserEncryptKey(context.Background(), "o1", "sig1", "hmac_sha256")
				return err
			},
		},
		{
			ep: epGetWXACode,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.CreateWXACode(context.Background(), map[string]any{"path": "pages/i"})
				return err
			},
		},
		{
			ep: epGetWXACodeUnlimit,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.CreateUnlimitedWXACode(context.Background(), map[string]any{"scene": "a=1"})
				return err
			},
		},
		{
			ep: epCreateWXAQRCode,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.CreateWXAQRCode(context.Background(), map[string]any{"path": "pages/i"})
				return err
			},
		},
		{
			ep: epQueryScheme,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.QueryScheme(context.Background(), map[string]any{"scheme": "x"})
				return err
			},
		},
		{
			ep: epGenerateScheme,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.GenerateScheme(context.Background(), map[string]any{})
				return err
			},
		},
		{
			ep: epGenerateNFCScheme,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.GenerateNFCScheme(context.Background(), map[string]any{"model_id": "M1"})
				return err
			},
		},
		{
			ep: epGenerateURLLink,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.GenerateURLLink(context.Background(), map[string]any{})
				return err
			},
		},
		{
			ep: epQueryURLLink,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.QueryURLLink(context.Background(), map[string]any{"url_link": "x"})
				return err
			},
		},
		{
			ep: epGenShortLink,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.GenerateShortLink(context.Background(), map[string]any{"page_url": "pages/i"})
				return err
			},
		},
		{
			ep: epStableToken,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.GetStableAccessToken(context.Background(), false)
				return err
			},
		},
		{
			ep: epQuotaGet,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.GetApiQuota(context.Background(), "/cgi-bin/message/custom/send")
				return err
			},
		},
		{
			ep: epQuotaClearAll,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.ClearQuota(context.Background(), "wx123")
				return err
			},
		},
		{
			ep: epQuotaClearOne,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.ClearApiQuota(context.Background(), "/cgi-bin/message/custom/send")
				return err
			},
		},
		{
			ep: epQuotaClearV2,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.ClearQuotaByAppSecret(context.Background())
				return err
			},
		},
		{
			ep: epRidGet,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.GetRidInfo(context.Background(), "rid1")
				return err
			},
		},
		{
			ep: epCallbackCheck,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.CallbackCheck(context.Background(), "all", "DEFAULT")
				return err
			},
		},
		{
			ep: epApiDomainIP,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.GetApiDomainIPs(context.Background())
				return err
			},
		},
		{
			ep: epCallbackIP,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.GetCallbackIPs(context.Background())
				return err
			},
		},
		{
			ep: epKFAccountAdd,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.AddKFAccount(context.Background(), map[string]any{"kf_account": "k1@wx", "nickname": "n"})
				return err
			},
		},
		{
			ep: epKFList,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.ListKFAccounts(context.Background(), "B1")
				return err
			},
		},
		{
			ep: epKFOnlineList,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.ListOnlineKFAccounts(context.Background(), "B1")
				return err
			},
		},
		{
			ep: epKFAccountDel,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.DelKFAccount(context.Background(), map[string]any{"kf_account": "k1@wx"})
				return err
			},
		},
		{
			ep: epKFSend,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.SendCustomMessage(context.Background(), map[string]any{"touser": "o1", "msgtype": "text", "text": map[string]any{"content": "hi"}})
				return err
			},
		},
		{
			ep: epKFTyping,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.SetTyping(context.Background(), "o1", "Typing")
				return err
			},
		},
		{
			ep: epKFSetAdmin,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.SetKFAdmin(context.Background(), "KO1")
				return err
			},
		},
		{
			ep: epKFCancelAdmin,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.CancelKFAdmin(context.Background(), "KO1")
				return err
			},
		},
		{
			ep: epKFMediaUpload,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.UploadTempMedia(context.Background(), "image", "a.png", []byte("pngbytes"))
				return err
			},
		},
		{
			ep: epKFMediaGet,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.GetTempMedia(context.Background(), "MD1")
				return err
			},
		},
		{
			ep: epActivityIDCreate,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.CreateActivityID(context.Background(), "UN1", "o1")
				return err
			},
		},
		{
			ep: epUpdatableSend,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.SetUpdatableMsg(context.Background(), map[string]any{"activity_id": "A1", "target_state": 0})
				return err
			},
		},
		{
			ep: epChatToolSend,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.SetChatToolMsg(context.Background(), map[string]any{"activity_id": "A1", "target_state": 0, "template_id": "T1", "version_type": 0})
				return err
			},
		},
		{
			ep: epSubsGetCategory,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.GetSubscribeCategory(context.Background())
				return err
			},
		},
		{
			ep: epSubsPubTitles,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.ListPublicTemplateTitles(context.Background(), "2,31", 0, 30)
				return err
			},
		},
		{
			ep: epSubsPubKeywords,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.GetPublicTemplateKeywords(context.Background(), "TID1")
				return err
			},
		},
		{
			ep: epSubsAddTemplate,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.AddSubscribeTemplate(context.Background(), "TID1", []int{1, 2}, "场景")
				return err
			},
		},
		{
			ep: epSubsGetTemplate,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.ListSubscribeTemplates(context.Background())
				return err
			},
		},
		{
			ep: epSubsDelTemplate,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.DelSubscribeTemplate(context.Background(), "PT1")
				return err
			},
		},
		{
			ep: epSubsSend,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.SendSubscribeMessage(context.Background(), map[string]any{"touser": "o1", "template_id": "PT1", "data": map[string]any{}, "miniprogram_state": "formal", "lang": "zh_CN"})
				return err
			},
		},
		{
			ep: epSubsSetNotify,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.SetUserNotify(context.Background(), map[string]any{"openid": "o1", "notify_type": 1, "notify_code": "C1", "content_json": "{}"})
				return err
			},
		},
		{
			ep: epSubsSetNotifyEx,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.SetUserNotifyExt(context.Background(), map[string]any{"openid": "o1", "notify_type": 1, "notify_code": "C1", "ext_json": "{}"})
				return err
			},
		},
		{
			ep: epSubsGetNotify,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.GetUserNotify(context.Background(), map[string]any{"openid": "o1", "notify_code": "C1", "notify_type": 1})
				return err
			},
		},
		{
			ep: epKFBizRegister,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.RegisterKFBusiness(context.Background(), map[string]any{"account_name": "biz_1", "nickname": "商户", "icon_media_id": "MD1"})
				return err
			},
		},
		{
			ep: epKFBizUpdate,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.UpdateKFBusiness(context.Background(), map[string]any{"business_id": "B1", "nickname": "新名"})
				return err
			},
		},
		{
			ep: epKFBizGet,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.GetKFBusiness(context.Background(), "B1", "")
				return err
			},
		},
		{
			ep: epKFBizList,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.ListKFBusiness(context.Background(), 0, 200)
				return err
			},
		},
		{
			ep: epMsgSecCheck,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.MsgSecCheck(context.Background(), map[string]any{"content": "txt", "version": 2, "scene": 2, "openid": "o1"})
				return err
			},
		},
		{
			ep: epMediaCheck,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.MediaCheckAsync(context.Background(), map[string]any{"media_url": "https://x/a.png", "media_type": 2, "version": 2, "scene": 2, "openid": "o1"})
				return err
			},
		},
		{
			ep: epUserRiskRank,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.GetUserRiskRank(context.Background(), map[string]any{"appid": "wx123", "openid": "o1", "scene": 0, "client_ip": "1.2.3.4"})
				return err
			},
		},
		{
			ep: epNearbyPoiAdd,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.AddNearbyPoi(context.Background(), map[string]any{"store_name": "店", "contract_phone": "10800000000", "hour": "09:00-18:00", "company_name": "c", "credential": "123456789012345", "address": "a", "pic_list": "[]", "service_infos": "[]", "is_comm_nearby": "1", "qualification_list": "[]"})
				return err
			},
		},
		{
			ep: epNearbyPoiDel,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.DelNearbyPoi(context.Background(), "POI1")
				return err
			},
		},
		{
			ep: epNearbyPoiList,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.GetNearbyPoiList(context.Background(), 1, 100)
				return err
			},
		},
		{
			ep: epNearbyPoiStats,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.SetNearbyPoiShowStatus(context.Background(), "POI1", 1)
				return err
			},
		},
		{
			ep: epRetainDaily,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.GetDailyRetain(context.Background(), "20260822", "20260822")
				return err
			},
		},
		{
			ep: epRetainWeekly,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.GetWeeklyRetain(context.Background(), "20260817", "20260823")
				return err
			},
		},
		{
			ep: epRetainMonthly,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.GetMonthlyRetain(context.Background(), "20260801", "20260831")
				return err
			},
		},
		{
			ep: epTrendDaily,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.GetDailyVisitTrend(context.Background(), "20260822", "20260822")
				return err
			},
		},
		{
			ep: epTrendWeekly,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.GetWeeklyVisitTrend(context.Background(), "20260817", "20260823")
				return err
			},
		},
		{
			ep: epTrendMonthly,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.GetMonthlyVisitTrend(context.Background(), "20260801", "20260831")
				return err
			},
		},
		{
			ep: epSummaryDaily,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.GetDailySummary(context.Background(), "20260822", "20260822")
				return err
			},
		},
		{
			ep: epVisitPage,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.GetVisitPage(context.Background(), "20260822", "20260822")
				return err
			},
		},
		{
			ep: epUserPortrait,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.GetUserPortrait(context.Background(), "20260816", "20260822")
				return err
			},
		},
		{
			ep: epVisitDist,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.GetVisitDistribution(context.Background(), "20260822", "20260822")
				return err
			},
		},
		{
			ep: epPerformance,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.GetPerformanceData(context.Background(), map[string]any{"module": 1, "time": map[string]any{"beginTimestamp": 1, "endTimestamp": 2}, "params": []any{}})
				return err
			},
		},
		{
			ep: epImgAiCrop,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.ImgAiCrop(context.Background(), "https://x/a.png", nil, "1,1.5")
				return err
			},
		},
		{
			ep: epImgQrcode,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.ImgQrcode(context.Background(), "https://x/a.png", nil)
				return err
			},
		},
		{
			ep: epImgSuperRes,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.ImgSuperResolution(context.Background(), "https://x/a.png", nil)
				return err
			},
		},
		{
			ep: epOCRComm,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.OCRComm(context.Background(), "https://x/a.png", nil)
				return err
			},
		},
		{
			ep: epOCRIdcard,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.OCRIdcard(context.Background(), "https://x/a.png", nil)
				return err
			},
		},
		{
			ep: epOCRDriving,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.OCRDriving(context.Background(), "https://x/a.png", nil)
				return err
			},
		},
		{
			ep: epOCRBankcard,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.OCRBankcard(context.Background(), "https://x/a.png", nil)
				return err
			},
		},
		{
			ep: epOCRBizlicense,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.OCRBizlicense(context.Background(), "https://x/a.png", nil)
				return err
			},
		},
		{
			ep: epOCRDrivingLicense,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.OCRDrivingLicense(context.Background(), "https://x/a.png", nil)
				return err
			},
		},
		{
			ep: epSoterVerify,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.VerifySoterSignature(context.Background(), "o1", "{}", "sig1")
				return err
			},
		},
		{
			ep: epFaceGetVerify,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.GetFaceVerifyID(context.Background(), map[string]any{"out_seq_no": "SEQ1", "cert_info": map[string]any{"cert_type": "IDENTITY_CARD", "cert_name": "n", "cert_no": "1"}, "openid": "o1"})
				return err
			},
		},
		{
			ep: epFaceQueryVerify,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.QueryFaceVerifyInfo(context.Background(), map[string]any{"verify_id": "V1", "out_seq_no": "SEQ1", "cert_hash": "H1", "openid": "o1"})
				return err
			},
		},
		{
			ep: epSearchSubmit,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.SubmitSearchPages(context.Background(), map[string]any{"pages": []any{}})
				return err
			},
		},
		{
			ep: epRedCoverURL,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.GetRedPacketCoverURL(context.Background(), "o1", "CT1")
				return err
			},
		},
		{
			ep: epSvcMktInvoke,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.InvokeService(context.Background(), map[string]any{"service": "svc1", "api": "api1", "data": "{}", "client_msg_id": "M1"})
				return err
			},
		},
		{
			ep: epSvcMktRetrieve,
			invoke: func(s *WxAppService, base string) error {
				_, err := s.RetrieveServiceData(context.Background(), "REQ1")
				return err
			},
		},
		{ep: epLiveRoomCreate, invoke: func(s *WxAppService, b string) error {
			_, e := s.CreateLiveRoom(context.Background(), map[string]any{"name": "直播间", "coverImg": "M1", "startTime": 1724000000, "endTime": 1724003600, "anchorName": "主播", "anchorWechat": "w1", "shareImg": "M2", "feedsImg": "M3", "type": 0, "closeLike": 0, "closeGoods": 0, "closeComment": 0})
			return e
		}},
		{ep: epLiveRoomEdit, invoke: func(s *WxAppService, b string) error {
			_, e := s.EditLiveRoom(context.Background(), map[string]any{"id": 1, "name": "直播间"})
			return e
		}},
		{ep: epLiveRoomDelete, invoke: func(s *WxAppService, b string) error { _, e := s.DeleteLiveRoom(context.Background(), 1); return e }},
		{ep: epLiveInfo, invoke: func(s *WxAppService, b string) error {
			_, e := s.GetLiveInfo(context.Background(), map[string]any{"start": 0, "limit": 10})
			return e
		}},
		{ep: epLivePushURL, invoke: func(s *WxAppService, b string) error { _, e := s.GetLivePushURL(context.Background(), 1); return e }},
		{ep: epLiveSharedCode, invoke: func(s *WxAppService, b string) error {
			_, e := s.GetLiveSharedCode(context.Background(), 1, "a=1")
			return e
		}},
		{ep: epLiveSubAnchorGet, invoke: func(s *WxAppService, b string) error { _, e := s.GetLiveSubAnchor(context.Background(), 1); return e }},
		{ep: epLiveSubAnchorMod, invoke: func(s *WxAppService, b string) error {
			_, e := s.ModifyLiveSubAnchor(context.Background(), 1, "w2")
			return e
		}},
		{ep: epLiveSubAnchorDel, invoke: func(s *WxAppService, b string) error {
			_, e := s.DeleteLiveSubAnchor(context.Background(), 1)
			return e
		}},
		{ep: epLiveSubAnchorAdd, invoke: func(s *WxAppService, b string) error {
			_, e := s.AddLiveSubAnchor(context.Background(), 1, "w2")
			return e
		}},
		{ep: epLiveGoodsImport, invoke: func(s *WxAppService, b string) error {
			_, e := s.ImportLiveGoods(context.Background(), 1, []int{9})
			return e
		}},
		{ep: epLiveGoodsInRoomDel, invoke: func(s *WxAppService, b string) error {
			_, e := s.DeleteLiveRoomGoods(context.Background(), 1, 9)
			return e
		}},
		{ep: epLiveGoodsPush, invoke: func(s *WxAppService, b string) error { _, e := s.PushLiveGoods(context.Background(), 1, 9); return e }},
		{ep: epLiveGoodsOnSale, invoke: func(s *WxAppService, b string) error {
			_, e := s.OnSaleLiveGoods(context.Background(), 1, 9, 1)
			return e
		}},
		{ep: epLiveGoodsSort, invoke: func(s *WxAppService, b string) error {
			_, e := s.SortLiveGoods(context.Background(), 1, []map[string]any{{"goodsId": 9}})
			return e
		}},
		{ep: epLiveAstAdd, invoke: func(s *WxAppService, b string) error {
			_, e := s.AddLiveAssistant(context.Background(), 1, []map[string]any{{"username": "w3", "nickname": "n"}})
			return e
		}},
		{ep: epLiveAstModify, invoke: func(s *WxAppService, b string) error {
			_, e := s.ModifyLiveAssistant(context.Background(), 1, "w3", "n2")
			return e
		}},
		{ep: epLiveAstRemove, invoke: func(s *WxAppService, b string) error {
			_, e := s.RemoveLiveAssistant(context.Background(), 1, "w3")
			return e
		}},
		{ep: epLiveAstList, invoke: func(s *WxAppService, b string) error {
			_, e := s.GetLiveAssistantList(context.Background(), 1)
			return e
		}},
		{ep: epLiveComment, invoke: func(s *WxAppService, b string) error {
			_, e := s.UpdateLiveComment(context.Background(), 1, 1)
			return e
		}},
		{ep: epLiveFeedPublic, invoke: func(s *WxAppService, b string) error {
			_, e := s.UpdateLiveFeedPublic(context.Background(), 1, 1)
			return e
		}},
		{ep: epLiveKF, invoke: func(s *WxAppService, b string) error { _, e := s.UpdateLiveKF(context.Background(), 1, 0); return e }},
		{ep: epLiveReplay, invoke: func(s *WxAppService, b string) error {
			_, e := s.UpdateLiveReplay(context.Background(), 1, 0)
			return e
		}},
		{ep: epLiveGoodsVideo, invoke: func(s *WxAppService, b string) error {
			_, e := s.GetLiveGoodsVideo(context.Background(), 1, 9)
			return e
		}},
		{ep: epLiveGoodsKeySet, invoke: func(s *WxAppService, b string) error {
			_, e := s.SetLiveGoodsKey(context.Background(), []string{"k1"})
			return e
		}},
		{ep: epLiveGoodsKeyGet, invoke: func(s *WxAppService, b string) error { _, e := s.GetLiveGoodsKey(context.Background()); return e }},
		{ep: epLiveGoodsAdd, invoke: func(s *WxAppService, b string) error {
			_, e := s.AddLiveGoods(context.Background(), map[string]any{"coverImgUrl": "M1", "name": "商品", "priceType": 1, "price": 9.9, "url": "pages/g"})
			return e
		}},
		{ep: epLiveGoodsAudit, invoke: func(s *WxAppService, b string) error {
			_, e := s.ResubmitLiveGoodsAudit(context.Background(), 9)
			return e
		}},
		{ep: epLiveGoodsWhse, invoke: func(s *WxAppService, b string) error {
			_, e := s.GetLiveGoodsAuditInfo(context.Background(), []int{9})
			return e
		}},
		{ep: epLiveGoodsReset, invoke: func(s *WxAppService, b string) error {
			_, e := s.ResetLiveGoodsAudit(context.Background(), 9, 1)
			return e
		}},
		{ep: epLiveGoodsUpdate, invoke: func(s *WxAppService, b string) error {
			_, e := s.UpdateLiveGoods(context.Background(), map[string]any{"goodsId": 9, "price": 19.9})
			return e
		}},
		{ep: epLiveGoodsApprove, invoke: func(s *WxAppService, b string) error {
			_, e := s.ListApprovedLiveGoods(context.Background(), 0, 30, 2)
			return e
		}},
		{ep: epLiveGoodsDelete, invoke: func(s *WxAppService, b string) error { _, e := s.DeleteLiveGoods(context.Background(), 9); return e }},
		{ep: epLiveRoleAdd, invoke: func(s *WxAppService, b string) error { _, e := s.AddLiveRole(context.Background(), "w1", 2); return e }},
		{ep: epLiveRoleDelete, invoke: func(s *WxAppService, b string) error {
			_, e := s.DeleteLiveRole(context.Background(), "w1", 2)
			return e
		}},
		{ep: epLiveRoleList, invoke: func(s *WxAppService, b string) error {
			_, e := s.ListLiveRoles(context.Background(), 0, 30, "kw")
			return e
		}},
		{ep: epLivePushMsg, invoke: func(s *WxAppService, b string) error {
			_, e := s.PushLiveStartMessage(context.Background(), 1, []string{"o1"})
			return e
		}},
		{ep: epLiveFollowers, invoke: func(s *WxAppService, b string) error {
			_, e := s.GetLiveFollowers(context.Background(), 200, "")
			return e
		}},
		{ep: epHWDeviceMsg, invoke: func(s *WxAppService, b string) error {
			_, e := s.SendHardwareDeviceMessage(context.Background(), map[string]any{"template_id": "T1", "sn": "SN1", "page": "pages/i", "to_openid_list": []string{"o1"}, "modelId": "M1", "data": map[string]any{}, "lang": "zh_CN"})
			return e
		}},
		{ep: epHWSnTicket, invoke: func(s *WxAppService, b string) error {
			_, e := s.GetSnTicket(context.Background(), "SN1", "M1")
			return e
		}},
		{ep: epHWIotGroupCreate, invoke: func(s *WxAppService, b string) error {
			_, e := s.CreateIotGroup(context.Background(), "M1", "组")
			return e
		}},
		{ep: epHWIotGroupGet, invoke: func(s *WxAppService, b string) error { _, e := s.GetIotGroupInfo(context.Background(), "G1"); return e }},
		{ep: epHWIotGroupAddDev, invoke: func(s *WxAppService, b string) error {
			_, e := s.AddIotGroupDevice(context.Background(), map[string]any{"group_id": "G1", "device_list": []any{}})
			return e
		}},
		{ep: epHWIotGroupRmDev, invoke: func(s *WxAppService, b string) error {
			_, e := s.RemoveIotGroupDevice(context.Background(), map[string]any{"group_id": "G1", "device_list": []any{}})
			return e
		}},
		{ep: epHWLicensePkgs, invoke: func(s *WxAppService, b string) error { _, e := s.GetLicensePkgList(context.Background(), 1); return e }},
		{ep: epHWLicenseActive, invoke: func(s *WxAppService, b string) error {
			_, e := s.ActiveLicenseDevice(context.Background(), map[string]any{"pkg_type": 1, "device_list": []any{}})
			return e
		}},
		{ep: epHWLicenseDevInfo, invoke: func(s *WxAppService, b string) error {
			_, e := s.GetLicenseDeviceInfo(context.Background(), map[string]any{"device_list": []any{}})
			return e
		}},
		{ep: epOpsDomainInfo, invoke: func(s *WxAppService, b string) error {
			_, e := s.GetDomainInfo(context.Background(), "getserverdomain")
			return e
		}},
		{ep: epOpsPerformance, invoke: func(s *WxAppService, b string) error {
			_, e := s.GetOpsPerformance(context.Background(), map[string]any{"cost_time_type": 1, "default_start_time": 1, "default_end_time": 2, "device": "@_all:", "is_download_code": "@_all:", "scene": "@_all:", "networktype": "@_all:"})
			return e
		}},
		{ep: epOpsSceneList, invoke: func(s *WxAppService, b string) error { _, e := s.GetSceneList(context.Background()); return e }},
		{ep: epOpsVersionList, invoke: func(s *WxAppService, b string) error { _, e := s.GetVersionList(context.Background()); return e }},
		{ep: epOpsRtLogSearch, invoke: func(s *WxAppService, b string) error {
			_, e := s.SearchRealtimeLog(context.Background(), map[string]string{"date": "20260822", "begintime": "1", "endtime": "2", "start": "0", "limit": "20", "traceId": "TR1", "url": "pages/i", "id": "o1", "filterMsg": "f", "level": "4"})
			return e
		}},
		{ep: epOpsFeedbackList, invoke: func(s *WxAppService, b string) error {
			_, e := s.GetFeedbackList(context.Background(), 0, 1, 20)
			return e
		}},
		{ep: epOpsFeedbackImg, invoke: func(s *WxAppService, b string) error {
			_, e := s.GetFeedbackMedia(context.Background(), 1, "MD1")
			return e
		}},
		{ep: epOpsJsErrDetail, invoke: func(s *WxAppService, b string) error {
			_, e := s.GetJsErrDetail(context.Background(), map[string]any{"startTime": "2026-08-01", "endTime": "2026-08-22", "errorMsgMd5": "m1", "errorStackMd5": "m2", "appVersion": "0", "sdkVersion": "0", "osName": "0", "clientVersion": "0", "openid": "o1", "offset": 0, "limit": 30, "desc": "1"})
			return e
		}},
		{ep: epOpsJsErrList, invoke: func(s *WxAppService, b string) error {
			_, e := s.GetJsErrList(context.Background(), map[string]any{"appVersion": "0", "errType": "0", "startTime": "2026-08-01", "endTime": "2026-08-22", "keyword": "", "openid": "", "orderby": "uv", "desc": "1", "offset": 0, "limit": 30})
			return e
		}},
		{ep: epOpsGrayRelease, invoke: func(s *WxAppService, b string) error { _, e := s.GetGrayReleasePlan(context.Background()); return e }},
		{ep: epPluginApplication, invoke: func(s *WxAppService, b string) error {
			_, e := s.ManagePluginApplication(context.Background(), map[string]any{"action": "dev_apply_list", "page": 1, "num": 10})
			return e
		}},
		{ep: epPluginManage, invoke: func(s *WxAppService, b string) error {
			_, e := s.ManagePlugin(context.Background(), map[string]any{"action": "list", "plugin_appid": "wxplug"})
			return e
		}},
		{ep: epTradeUploadShip, invoke: func(s *WxAppService, b string) error {
			_, e := s.UploadShippingInfo(context.Background(), map[string]any{"order_key": map[string]any{"order_number_type": 2, "transaction_id": "TX1"}, "logistics_type": 1, "delivery_mode": 1, "shipping_list": []any{}, "upload_time": "2026-08-23T00:00:00+08:00", "payer": map[string]any{"openid": "o1"}})
			return e
		}},
		{ep: epTradeUploadComb, invoke: func(s *WxAppService, b string) error {
			_, e := s.UploadCombinedShippingInfo(context.Background(), map[string]any{"order_key": map[string]any{"order_number_type": 2, "transaction_id": "TX1"}, "upload_time": "t", "payer": map[string]any{"openid": "o1"}})
			return e
		}},
		{ep: epTradeGetOrder, invoke: func(s *WxAppService, b string) error {
			_, e := s.GetTradeOrder(context.Background(), map[string]any{"transaction_id": "TX1"})
			return e
		}},
		{ep: epTradeGetOrderList, invoke: func(s *WxAppService, b string) error {
			_, e := s.GetTradeOrderList(context.Background(), map[string]any{"last_index": "", "page_size": 100})
			return e
		}},
		{ep: epTradeNotifyRecv, invoke: func(s *WxAppService, b string) error {
			_, e := s.NotifyConfirmReceive(context.Background(), map[string]any{"transaction_id": "TX1", "received_time": 1724000000})
			return e
		}},
		{ep: epTradeSetJumpPath, invoke: func(s *WxAppService, b string) error {
			_, e := s.SetMsgJumpPath(context.Background(), "pages/order/list")
			return e
		}},
		{ep: epTradeIsManaged, invoke: func(s *WxAppService, b string) error {
			_, e := s.IsTradeManaged(context.Background(), "wx123")
			return e
		}},
		{ep: epTradeIsConfirm, invoke: func(s *WxAppService, b string) error {
			_, e := s.IsTradeManagementConfirmationCompleted(context.Background(), "wx123")
			return e
		}},
		{ep: epTradeOpSpecial, invoke: func(s *WxAppService, b string) error {
			_, e := s.OpSpecialOrder(context.Background(), map[string]any{"order_id": "TX1", "type": 1, "delay_to": 1725000000})
			return e
		}},
		{ep: epTradeBrandApply, invoke: func(s *WxAppService, b string) error {
			_, e := s.FamousBrandApply(context.Background(), map[string]any{"apply_for": 2})
			return e
		}},
		{ep: epTradeBrandStatus, invoke: func(s *WxAppService, b string) error {
			_, e := s.GetFamousBrandApplyStatus(context.Background())
			return e
		}},
		{ep: epTradeSetType, invoke: func(s *WxAppService, b string) error {
			_, e := s.SetTradeType(context.Background(), map[string]any{"trade_type": 2, "material_list": []any{}, "reason": "r"})
			return e
		}},
		{ep: epTGPenaltyList, invoke: func(s *WxAppService, b string) error {
			_, e := s.GetPenaltyList(context.Background(), 0, 100)
			return e
		}},
		{ep: epTGGuarantee, invoke: func(s *WxAppService, b string) error { _, e := s.GetGuaranteeStatus(context.Background()); return e }},
		{ep: epTGCommentList, invoke: func(s *WxAppService, b string) error {
			_, e := s.GetCommentList(context.Background(), "2026-08-01 00:00:00", "2026-08-22 00:00:00", 6, 0, 8)
			return e
		}},
		{ep: epTGReplyList, invoke: func(s *WxAppService, b string) error { _, e := s.GetReplyList(context.Background(), "C1"); return e }},
		{ep: epTGCommentInfo, invoke: func(s *WxAppService, b string) error { _, e := s.GetCommentInfo(context.Background(), "C1"); return e }},
		{ep: epTGReplyAdd, invoke: func(s *WxAppService, b string) error {
			_, e := s.AddReply(context.Background(), "C1", "评论")
			return e
		}},
		{ep: epTGReplyDel, invoke: func(s *WxAppService, b string) error {
			_, e := s.DeleteReply(context.Background(), "C1", "R1")
			return e
		}},
		{ep: epTGCReplyAdd, invoke: func(s *WxAppService, b string) error {
			_, e := s.AddCommentReply(context.Background(), "C1", "R1", "回复")
			return e
		}},
		{ep: epTGCReplyDel, invoke: func(s *WxAppService, b string) error {
			_, e := s.DeleteCommentReply(context.Background(), "C1", "R1", "CR1")
			return e
		}},
		{ep: epTGKfQuota, invoke: func(s *WxAppService, b string) error { _, e := s.ResetApiKfQuota(context.Background(), "C1"); return e }},
		{ep: epTGCompromise, invoke: func(s *WxAppService, b string) error {
			_, e := s.ConfirmCompromise(context.Background(), "C1", []string{"MD1"}, "已和解")
			return e
		}},
		{ep: epChargeUsageDetail, invoke: func(s *WxAppService, b string) error {
			_, e := s.GetChargeUsageDetail(context.Background(), "SPU1", 0, 20)
			return e
		}},
		{ep: epChargeRecentAvg, invoke: func(s *WxAppService, b string) error {
			_, e := s.GetRecentAverageUsage(context.Background(), "SPU1")
			return e
		}},
		{ep: epExpAccountBind, invoke: func(s *WxAppService, b string) error {
			_, e := s.BindExpressAccount(context.Background(), map[string]any{"type": "bind", "biz_id": "B1", "delivery_id": "YTO"})
			return e
		}},
		{ep: epExpAccountGet, invoke: func(s *WxAppService, b string) error { _, e := s.GetAllExpressAccounts(context.Background()); return e }},
		{ep: epExpDeliveryGet, invoke: func(s *WxAppService, b string) error { _, e := s.GetAllDeliveries(context.Background()); return e }},
		{ep: epExpOrderAdd, invoke: func(s *WxAppService, b string) error {
			_, e := s.AddExpressOrder(context.Background(), map[string]any{"order_id": "O1", "delivery_id": "YTO", "biz_id": "B1", "add_source": 0, "sender": map[string]any{}, "receiver": map[string]any{}, "cargo": map[string]any{}, "shop": map[string]any{}, "insured": map[string]any{}, "service": map[string]any{}})
			return e
		}},
		{ep: epExpOrderCancel, invoke: func(s *WxAppService, b string) error {
			_, e := s.CancelExpressOrder(context.Background(), map[string]any{"order_id": "O1", "delivery_id": "YTO", "waybill_id": "W1"})
			return e
		}},
		{ep: epExpOrderGet, invoke: func(s *WxAppService, b string) error {
			_, e := s.GetExpressOrder(context.Background(), map[string]any{"order_id": "O1", "delivery_id": "YTO"})
			return e
		}},
		{ep: epExpOrderBatch, invoke: func(s *WxAppService, b string) error {
			_, e := s.BatchGetExpressOrders(context.Background(), []map[string]any{{"order_id": "O1", "delivery_id": "YTO"}})
			return e
		}},
		{ep: epExpPathGet, invoke: func(s *WxAppService, b string) error {
			_, e := s.GetExpressPath(context.Background(), "YTO", "W1", "")
			return e
		}},
		{ep: epExpPrinterUpd, invoke: func(s *WxAppService, b string) error {
			_, e := s.UpdateExpressPrinter(context.Background(), map[string]any{"openid": "o1", "update_type": "bind"})
			return e
		}},
		{ep: epExpPrinterGet, invoke: func(s *WxAppService, b string) error { _, e := s.GetExpressPrinters(context.Background()); return e }},
		{ep: epExpQuotaGet, invoke: func(s *WxAppService, b string) error {
			_, e := s.GetExpressQuota(context.Background(), "YTO", "B1")
			return e
		}},
		{ep: epExpTestUpdate, invoke: func(s *WxAppService, b string) error {
			_, e := s.TestUpdateExpressOrder(context.Background(), map[string]any{"biz_id": "test_biz_id", "order_id": "O1", "delivery_id": "TEST", "waybill_id": "W1", "action_time": 1724000000, "action_type": 100001, "action_msg": "揽件"})
			return e
		}},
		{ep: epExpProvBizUpdate, invoke: func(s *WxAppService, b string) error {
			_, e := s.UpdateExpressBusiness(context.Background(), map[string]any{"shop_app_id": "wx123", "biz_id": "B1", "result_code": 0})
			return e
		}},
		{ep: epExpProvPath, invoke: func(s *WxAppService, b string) error {
			_, e := s.UpdateExpressPath(context.Background(), map[string]any{"token": "T1", "waybill_id": "W1", "action_time": 1724000000, "action_type": 100001, "action_msg": "揽件"})
			return e
		}},
		{ep: epExpProvPreview, invoke: func(s *WxAppService, b string) error {
			_, e := s.PreviewExpressTemplate(context.Background(), map[string]any{"waybill_id": "W1", "waybill_template": "PGh0bWw+", "waybill_data": "[]", "custom": map[string]any{}})
			return e
		}},
		{ep: epExpProvContact, invoke: func(s *WxAppService, b string) error {
			_, e := s.GetExpressContact(context.Background(), "T1", "W1")
			return e
		}},
		{ep: epExpScatterCancel, invoke: func(s *WxAppService, b string) error {
			_, e := s.ScatterCancelOrder(context.Background(), "T1", "原因")
			return e
		}},
		{ep: epExpScatterFee, invoke: func(s *WxAppService, b string) error {
			_, e := s.ScatterUpdateOrderFee(context.Background(), map[string]any{"token": "T1", "waybill_id": "W1", "need_pay": 1, "fee": 1000, "original_fee": 1200, "base_fee": 1000})
			return e
		}},
		{ep: epExpScatterRefund, invoke: func(s *WxAppService, b string) error {
			_, e := s.ScatterRefundOrder(context.Background(), "T1", "500")
			return e
		}},
		{ep: epExpScatterBill, invoke: func(s *WxAppService, b string) error {
			_, e := s.ScatterGetBill(context.Background(), "20260822", "ALL")
			return e
		}},
		{ep: epExpScatterComp, invoke: func(s *WxAppService, b string) error {
			_, e := s.ScatterUpdateComplaintResult(context.Background(), map[string]any{"access_token": "TK", "token": "T1", "waybill_id": "W1", "result": "已处理", "desc": "说明"})
			return e
		}},
		{ep: epExpScatterStatus, invoke: func(s *WxAppService, b string) error {
			_, e := s.ScatterUpdateOrderStatus(context.Background(), map[string]any{"token": "T1", "action_time": 1724000000, "action_type": 100001, "action_msg": "揽件", "pickup_courier_name": "张三", "pickup_courier_phone": "10800000000", "delivery_courier_name": "李四", "delivery_courier_phone": "10900000000"})
			return e
		}},
		{ep: epLocalDeliveryGet, invoke: func(s *WxAppService, b string) error { _, e := s.GetAllImmeDeliveries(context.Background()); return e }},
		{ep: epLocalOrderPreAdd, invoke: func(s *WxAppService, b string) error {
			_, e := s.PreAddLocalOrder(context.Background(), map[string]any{"shopid": "S1", "shop_order_id": "LO1", "delivery_id": "SFTC", "openid": "o1", "sender": map[string]any{}, "receiver": map[string]any{}, "cargo": map[string]any{}, "order_info": map[string]any{}, "shop": map[string]any{}, "delivery_sign": "SIG", "shop_no": "SN1", "sub_biz_id": "SB1"})
			return e
		}},
		{ep: epLocalShopGet, invoke: func(s *WxAppService, b string) error { _, e := s.GetBindAccounts(context.Background()); return e }},
		{ep: epLocalPreCancel, invoke: func(s *WxAppService, b string) error {
			_, e := s.PreCancelLocalOrder(context.Background(), map[string]any{"shopid": "S1", "shop_order_id": "LO1", "delivery_id": "SFTC", "waybill_id": "LW1", "shop_no": "SN1", "delivery_sign": "SIG"})
			return e
		}},
		{ep: epLocalOpen, invoke: func(s *WxAppService, b string) error { _, e := s.OpenDelivery(context.Background()); return e }},
		{ep: epLocalShopAdd, invoke: func(s *WxAppService, b string) error {
			_, e := s.BindLocalAccount(context.Background(), "SFTC")
			return e
		}},
		{ep: epLocalOrderReAdd, invoke: func(s *WxAppService, b string) error {
			_, e := s.ReAddLocalOrder(context.Background(), map[string]any{"shopid": "S1", "shop_order_id": "LO1", "delivery_id": "SFTC", "openid": "o1", "sender": map[string]any{}, "receiver": map[string]any{}, "cargo": map[string]any{}, "order_info": map[string]any{}, "shop": map[string]any{}, "delivery_token": "DT1", "delivery_sign": "SIG", "shop_no": "SN1"})
			return e
		}},
		{ep: epLocalRealMock, invoke: func(s *WxAppService, b string) error {
			_, e := s.RealMockUpdateOrder(context.Background(), map[string]any{"shopid": "S1", "shop_order_id": "LO1", "order_status": 302, "action_time": 1724000000, "delivery_sign": "SIG"})
			return e
		}},
		{ep: epLocalMock, invoke: func(s *WxAppService, b string) error {
			_, e := s.MockUpdateOrder(context.Background(), map[string]any{"shopid": "test_shop_id", "shop_order_id": "LO1", "order_status": 302, "action_time": 1724000000})
			return e
		}},
		{ep: epLocalOrderGet, invoke: func(s *WxAppService, b string) error {
			_, e := s.GetLocalOrder(context.Background(), map[string]any{"shopid": "S1", "shop_order_id": "LO1", "shop_no": "SN1", "delivery_sign": "SIG"})
			return e
		}},
		{ep: epLocalConfirmRet, invoke: func(s *WxAppService, b string) error {
			_, e := s.AbnormalConfirmReturn(context.Background(), map[string]any{"shopid": "S1", "shop_order_id": "LO1", "waybill_id": "LW1", "delivery_sign": "SIG", "shop_no": "SN1"})
			return e
		}},
		{ep: epLocalOrderCancel, invoke: func(s *WxAppService, b string) error {
			_, e := s.CancelLocalOrder(context.Background(), map[string]any{"shopid": "S1", "shop_order_id": "LO1", "delivery_id": "SFTC", "cancel_reason_id": 1, "cancel_reason": "原因", "shop_no": "SN1", "delivery_sign": "SIG"})
			return e
		}},
		{ep: epLocalAddTips, invoke: func(s *WxAppService, b string) error {
			_, e := s.AddLocalTips(context.Background(), map[string]any{"shopid": "S1", "shop_order_id": "LO1", "waybill_id": "LW1", "tips": 5, "remark": "加急", "delivery_sign": "SIG", "shop_no": "SN1"})
			return e
		}},
		{ep: epLocalOrderAdd, invoke: func(s *WxAppService, b string) error {
			_, e := s.AddLocalOrder(context.Background(), map[string]any{"shopid": "S1", "shop_order_id": "LO1", "delivery_id": "SFTC", "openid": "o1", "sender": map[string]any{}, "receiver": map[string]any{}, "cargo": map[string]any{}, "order_info": map[string]any{}, "shop": map[string]any{}, "delivery_token": "DT1", "delivery_sign": "SIG", "shop_no": "SN1"})
			return e
		}},
		{ep: epLocalProvUpdate, invoke: func(s *WxAppService, b string) error {
			_, e := s.UpdateLocalDeliveryOrder(context.Background(), map[string]any{"wx_token": "WT1", "order_status": 302, "waybill_id": "LW1", "action_time": 1724000000, "shopid": "S1", "shop_order_id": "LO1", "wxa_path": "pages/o"})
			return e
		}},
		{ep: epWexpTraceWaybill, invoke: func(s *WxAppService, b string) error {
			_, e := s.TraceWaybill(context.Background(), map[string]any{"openid": "o1", "receiver_phone": "10800000000", "waybill_id": "W1", "goods_info": map[string]any{}, "trans_id": "4201", "order_detail_path": "pages/o"})
			return e
		}},
		{ep: epWexpQueryTrace, invoke: func(s *WxAppService, b string) error {
			_, e := s.QueryTrace(context.Background(), "WT1", "o1")
			return e
		}},
		{ep: epWexpUpdateGoods, invoke: func(s *WxAppService, b string) error {
			_, e := s.UpdateWaybillGoods(context.Background(), map[string]any{"waybill_token": "WT1", "goods_info": map[string]any{"detail_list": []any{}}})
			return e
		}},
		{ep: epWexpDeliveryList, invoke: func(s *WxAppService, b string) error {
			_, e := s.GetOpenMsgDeliveryList(context.Background())
			return e
		}},
		{ep: epWexpFollowWaybill, invoke: func(s *WxAppService, b string) error {
			_, e := s.FollowWaybill(context.Background(), map[string]any{"openid": "o1", "receiver_phone": "10800000000", "waybill_id": "W1", "goods_info": map[string]any{}, "trans_id": "4201", "order_detail_path": "pages/o"})
			return e
		}},
		{ep: epWexpQueryFollow, invoke: func(s *WxAppService, b string) error {
			_, e := s.QueryFollowTrace(context.Background(), "WT1", "")
			return e
		}},
		{ep: epWexpUpdateFollow, invoke: func(s *WxAppService, b string) error {
			_, e := s.UpdateFollowWaybillGoods(context.Background(), map[string]any{"waybill_token": "WT1", "goods_info": map[string]any{}})
			return e
		}},
		{ep: epWexpUserQuery, invoke: func(s *WxAppService, b string) error {
			_, e := s.DeliveryUserQuery(context.Background(), "10800000000")
			return e
		}},
		{ep: epWexpPathNotify, invoke: func(s *WxAppService, b string) error {
			_, e := s.DeliveryPathNotify(context.Background(), map[string]any{"sender": map[string]any{}, "receiver": map[string]any{}, "waybill_id": "W1", "path": map[string]any{}, "create_time": 1724000000})
			return e
		}},
		{ep: epInsOpen, invoke: func(s *WxAppService, b string) error { _, e := s.OpenInsuranceFreight(context.Background()); return e }},
		{ep: epInsQueryOpen, invoke: func(s *WxAppService, b string) error { _, e := s.QueryInsuranceOpen(context.Background()); return e }},
		{ep: epInsCreateOrder, invoke: func(s *WxAppService, b string) error {
			_, e := s.CreateInsuranceOrder(context.Background(), map[string]any{"openid": "o1", "order_no": "4201", "pay_time": 1724000000, "pay_amount": 1000, "delivery_no": "D1", "delivery_place": map[string]any{}, "receipt_place": map[string]any{}, "product_info": map[string]any{}})
			return e
		}},
		{ep: epInsClaim, invoke: func(s *WxAppService, b string) error {
			_, e := s.ClaimInsurance(context.Background(), map[string]any{"openid": "o1", "order_no": "4201", "refund_delivery_no": "RD1", "refund_company": "SF"})
			return e
		}},
		{ep: epInsChargeID, invoke: func(s *WxAppService, b string) error { _, e := s.CreateChargeID(context.Background(), 5000); return e }},
		{ep: epInsApplyPay, invoke: func(s *WxAppService, b string) error {
			_, e := s.ApplyInsurancePay(context.Background(), "2850151276313431996")
			return e
		}},
		{ep: epInsPayOrders, invoke: func(s *WxAppService, b string) error {
			_, e := s.GetInsurancePayOrders(context.Background(), []int{2}, 0, 10)
			return e
		}},
		{ep: epInsRefund, invoke: func(s *WxAppService, b string) error { _, e := s.RefundInsurance(context.Background()); return e }},
		{ep: epInsSummary, invoke: func(s *WxAppService, b string) error {
			_, e := s.GetInsuranceSummary(context.Background(), 1723900000, 1724000000)
			return e
		}},
		{ep: epInsOrders, invoke: func(s *WxAppService, b string) error {
			_, e := s.GetInsuranceOrders(context.Background(), map[string]any{"order_no": "4201"})
			return e
		}},
		{ep: epInsNotifyFunds, invoke: func(s *WxAppService, b string) error {
			_, e := s.SetInsuranceNotifyFunds(context.Background(), 1000)
			return e
		}},
		{ep: epRetAdd, invoke: func(s *WxAppService, b string) error {
			_, e := s.AddReturnId(context.Background(), map[string]any{"shop_order_id": "RO1", "biz_addr": map[string]any{}, "openid": "o1", "order_path": "pages/r", "goods_list": []any{}, "order_price": 1000, "wx_pay_id": "4201"})
			return e
		}},
		{ep: epRetGet, invoke: func(s *WxAppService, b string) error { _, e := s.GetReturnId(context.Background(), "RT1"); return e }},
		{ep: epRetUnbind, invoke: func(s *WxAppService, b string) error { _, e := s.UnbindReturnId(context.Background(), "RT1"); return e }},
		{ep: epCityApply, invoke: func(s *WxAppService, b string) error { _, e := s.ApplyIntracity(context.Background()); return e }},
		{ep: epCityCreateStore, invoke: func(s *WxAppService, b string) error {
			_, e := s.CreateIntracityStore(context.Background(), map[string]any{"out_store_id": "OS1", "store_name": "店", "address_info": map[string]any{"province": "广东省", "city": "深圳市", "area": "南山区", "house": "x", "lat": 1.0, "lng": 2.0, "phone": "10800000000"}})
			return e
		}},
		{ep: epCityQueryStore, invoke: func(s *WxAppService, b string) error {
			_, e := s.QueryIntracityStore(context.Background(), "WS1", "")
			return e
		}},
		{ep: epCityUpdateStore, invoke: func(s *WxAppService, b string) error {
			_, e := s.UpdateIntracityStore(context.Background(), map[string]any{"keys": map[string]any{"wx_store_id": "WS1"}, "content": map[string]any{"store_name": "新名"}})
			return e
		}},
		{ep: epCityCharge, invoke: func(s *WxAppService, b string) error {
			_, e := s.ChargeIntracity(context.Background(), map[string]any{"service_trans_id": "DADA", "amount": 5000, "wx_store_id": "WS1"})
			return e
		}},
		{ep: epCityRefund, invoke: func(s *WxAppService, b string) error {
			_, e := s.RefundIntracity(context.Background(), map[string]any{"service_trans_id": "DADA", "wx_store_id": "WS1"})
			return e
		}},
		{ep: epCityQueryFlow, invoke: func(s *WxAppService, b string) error {
			_, e := s.QueryIntracityFlow(context.Background(), map[string]any{"wx_store_id": "WS1", "flow_type": 1, "pay_mode": "PAY_MODE_STORE"})
			return e
		}},
		{ep: epCityBalance, invoke: func(s *WxAppService, b string) error {
			_, e := s.QueryIntracityBalance(context.Background(), map[string]any{"wx_store_id": "WS1"})
			return e
		}},
		{ep: epCityPreAddOrder, invoke: func(s *WxAppService, b string) error {
			_, e := s.PreAddIntracityOrder(context.Background(), map[string]any{"wx_store_id": "WS1", "user_name": "n", "user_phone": "10800000000", "user_lng": 1.0, "user_lat": 2.0, "user_address": "a", "cargo_name": "c", "cargo": map[string]any{}, "use_sandbox": 1})
			return e
		}},
		{ep: epCityAddOrder, invoke: func(s *WxAppService, b string) error {
			_, e := s.AddIntracityOrder(context.Background(), map[string]any{"wx_store_id": "WS1", "store_order_id": "CO1", "user_openid": "o1", "user_lng": 1.0, "user_lat": 2.0, "user_address": "a", "user_name": "n", "user_phone": "10800000000", "order_detail_path": "pages/o"})
			return e
		}},
		{ep: epCityQueryOrder, invoke: func(s *WxAppService, b string) error {
			_, e := s.QueryIntracityOrder(context.Background(), map[string]any{"wx_order_id": "WO1"})
			return e
		}},
		{ep: epCityCancelOrder, invoke: func(s *WxAppService, b string) error {
			_, e := s.CancelIntracityOrder(context.Background(), map[string]any{"wx_order_id": "WO1", "cancel_reason_id": 1})
			return e
		}},
		{ep: epCitySetPayMode, invoke: func(s *WxAppService, b string) error {
			_, e := s.SetIntracityPayMode(context.Background(), "wx123", "PAY_MODE_APP")
			return e
		}},
		{ep: epCityGetPayMode, invoke: func(s *WxAppService, b string) error {
			_, e := s.GetIntracityPayMode(context.Background(), "wx123")
			return e
		}},
		{ep: epCityGetCity, invoke: func(s *WxAppService, b string) error {
			_, e := s.GetIntracityCities(context.Background(), "SFTC")
			return e
		}},
		{ep: epCityMockNotify, invoke: func(s *WxAppService, b string) error {
			_, e := s.MockIntracityNotify(context.Background(), map[string]any{"wx_order_id": "WO1", "order_status": 70000})
			return e
		}},
		{ep: epKfWorkGet, invoke: func(s *WxAppService, b string) error { _, e := s.GetKfWorkBound(context.Background()); return e }},
		{ep: epKfWorkBind, invoke: func(s *WxAppService, b string) error { _, e := s.BindKfWork(context.Background(), "wwcorp1"); return e }},
		{ep: epKfWorkUnbind, invoke: func(s *WxAppService, b string) error {
			_, e := s.UnbindKfWork(context.Background(), "wwcorp1")
			return e
		}},
	}

	// 防漂移：用例数 == 表项数，且 Name 一一对应
	if len(cases) != len(allEndpoints) {
		t.Fatalf("用例数 %d != 表项数 %d——新表项必须配对应用例", len(cases), len(allEndpoints))
	}
	caseNames := make(map[string]bool, len(cases))
	for _, c := range cases {
		caseNames[c.ep.Name] = true
	}
	for _, ep := range allEndpoints {
		if !caseNames[ep.Name] {
			t.Fatalf("表项 %s 缺对应用例", ep.Name)
		}
	}

	var mu sync.Mutex
	type hit struct {
		method string
		path   string
		query  []string
		body   string
	}
	var hits []hit
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		keys := make([]string, 0, len(r.URL.Query()))
		for k := range r.URL.Query() {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		mu.Lock()
		hits = append(hits, hit{method: r.Method, path: r.URL.Path, query: keys, body: string(body)})
		mu.Unlock()
		switch r.URL.Path {
		case epToken.Path:
			_, _ = w.Write([]byte(`{"access_token":"TK_EP"}`))
		case epGetUserPhone.Path:
			_, _ = w.Write([]byte(`{"errcode":0,"phone_info":{"phoneNumber":"10800000000"}}`))
		case epStableToken.Path:
			_, _ = w.Write([]byte(`{"access_token":"STK_EP","expires_in":7200}`))
		default:
			_, _ = w.Write([]byte(`{"errcode":0,"openid":"oX","session_key":"sk"}`))
		}
	}))
	t.Cleanup(srv.Close)

	s := NewWxAppService("wx123", "sec")
	s.BaseURL = srv.URL

	for i, tc := range cases {
		if err := tc.invoke(s, srv.URL); err != nil {
			t.Fatalf("[%d] %s 调用失败: %v", i, tc.ep.Name, err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(hits) < len(allEndpoints) {
		t.Fatalf("桩命中 %d 次 < 表项数 %d", len(hits), len(allEndpoints))
	}
	// 每表项至少一次命中其声明的 method/path/query 键集
	for _, tc := range cases {
		want := append([]string{}, tc.ep.Query...)
		sort.Strings(want)
		matched := false
		for _, h := range hits {
			if h.method != tc.ep.Method || h.path != tc.ep.Path {
				continue
			}
			if len(h.query) != len(want) {
				continue
			}
			same := true
			for j := range want {
				if h.query[j] != want[j] {
					same = false
					break
				}
			}
			if !same {
				continue
			}
			if tc.ep.Body && h.body == "" {
				continue
			}
			if !tc.ep.Body && h.body != "" {
				continue
			}
			matched = true
			break
		}
		if !matched {
			t.Fatalf("%s 无命中断言（method/path/query 键集/body 形态）", tc.ep.Name)
		}
	}
	// token 端点专项：query 恰为 appid/secret/grant_type，无 access_token
	for _, h := range hits {
		if h.path != epToken.Path {
			continue
		}
		for _, k := range h.query {
			if k == "access_token" {
				t.Fatalf("token 端点不应带 access_token: %v", h.query)
			}
		}
	}
}
