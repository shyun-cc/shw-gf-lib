package wxoa

import (
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shyun-cc/shw-gf-lib/internal/testredis"
	"github.com/shyun-cc/shw-gf-lib/wecom"
)

// TestToken_CacheAndConcurrentSingleFetch 缓存命中零 HTTP + 并发恰一次 fetch（redislock 去重）
func TestToken_CacheAndConcurrentSingleFetch(t *testing.T) {
	mr := testredis.Start(t)
	var fetchN int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == epToken.Path {
			atomic.AddInt32(&fetchN, 1)
			time.Sleep(30 * time.Millisecond) // 慢响应制造并发窗口
		}
		_, _ = w.Write([]byte(`{"access_token":"TK_RACE"}`))
	}))
	t.Cleanup(srv.Close)

	s := NewWxOAService("wx123", "sec")
	s.BaseURL = srv.URL
	ctx := context.Background()

	first, err := s.GetAccessToken(ctx)
	if err != nil || first != "TK_RACE" {
		t.Fatalf("首次获取: %v %s", err, first)
	}
	if n := atomic.LoadInt32(&fetchN); n != 1 {
		t.Fatalf("首次应 fetch 1 次，实际 %d", n)
	}
	ttl := mr.TTL("wxoa:access_token:wx123")
	if ttl <= 0 || ttl > 7000e9 {
		t.Fatalf("TTL 应 7000s 附近，实际 %v", ttl)
	}
	if second, _ := s.GetAccessToken(ctx); second != "TK_RACE" || atomic.LoadInt32(&fetchN) != 1 {
		t.Fatal("缓存命中不应发 HTTP")
	}

	mr.Del("wxoa:access_token:wx123")
	var wg sync.WaitGroup
	results := make([]string, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tok, err := s.GetAccessToken(ctx)
			if err != nil {
				t.Error(err)
				return
			}
			results[i] = tok
		}(i)
	}
	wg.Wait()
	if n := atomic.LoadInt32(&fetchN); n != 2 {
		t.Fatalf("并发应恰再 fetch 1 次（总 2），实际 %d", n)
	}
	for i, r := range results {
		if r != "TK_RACE" {
			t.Fatalf("结果[%d]应同值: %s", i, r)
		}
	}
}

// TestToken_BizError errcode≠0 → BizError
func TestToken_BizError(t *testing.T) {
	testredis.Start(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"errcode":40125,"errmsg":"invalid appsecret"}`))
	}))
	t.Cleanup(srv.Close)
	s := NewWxOAService("wx123", "bad")
	s.BaseURL = srv.URL
	_, err := s.GetAccessToken(context.Background())
	var biz *BizError
	if !errors.As(err, &biz) || biz.Errcode != 40125 {
		t.Fatalf("应 BizError 40125: %v", err)
	}
}

// TestEndpoints_TableDriven token.fetch 表驱动断言（计数相等防漂移）
func TestEndpoints_TableDriven(t *testing.T) {
	testredis.Start(t)
	cases := []struct {
		ep     Endpoint
		invoke func(s *WxOAService, base string) error
	}{
		{ep: epToken, invoke: func(s *WxOAService, base string) error {
			_, e := s.GetAccessToken(context.Background())
			return e
		}},
		{ep: epJSAPITicket, invoke: func(s *WxOAService, base string) error { _, e := s.GetJSAPITicket(context.Background()); return e }},
		{ep: epMenuCreate, invoke: func(s *WxOAService, b string) error {
			_, e := s.CreateMenu(context.Background(), map[string]any{"button": []any{}})
			return e
		}},
		{ep: epMenuSelfGet, invoke: func(s *WxOAService, b string) error { _, e := s.GetCurrentSelfMenu(context.Background()); return e }},
		{ep: epMenuGet, invoke: func(s *WxOAService, b string) error { _, e := s.GetMenu(context.Background()); return e }},
		{ep: epMenuDelete, invoke: func(s *WxOAService, b string) error { _, e := s.DeleteMenu(context.Background()); return e }},
		{ep: epMenuAddCond, invoke: func(s *WxOAService, b string) error {
			_, e := s.AddConditionalMenu(context.Background(), map[string]any{"button": []any{}})
			return e
		}},
		{ep: epMenuDelCond, invoke: func(s *WxOAService, b string) error {
			_, e := s.DeleteConditionalMenu(context.Background(), "M1")
			return e
		}},
		{ep: epMenuTryMatch, invoke: func(s *WxOAService, b string) error { _, e := s.TryMatchMenu(context.Background(), "o1"); return e }},
		{ep: epTagsCreate, invoke: func(s *WxOAService, b string) error { _, e := s.CreateTag(context.Background(), "标签"); return e }},
		{ep: epTagsUpdate, invoke: func(s *WxOAService, b string) error { _, e := s.UpdateTag(context.Background(), 1, "新"); return e }},
		{ep: epTagsDelete, invoke: func(s *WxOAService, b string) error { _, e := s.DeleteTag(context.Background(), 1); return e }},
		{ep: epTagsGet, invoke: func(s *WxOAService, b string) error { _, e := s.GetTags(context.Background()); return e }},
		{ep: epTagUsers, invoke: func(s *WxOAService, b string) error { _, e := s.GetTagUsers(context.Background(), 1, ""); return e }},
		{ep: epBatchTagging, invoke: func(s *WxOAService, b string) error {
			_, e := s.BatchTagging(context.Background(), 1, []string{"o1"})
			return e
		}},
		{ep: epBatchUntag, invoke: func(s *WxOAService, b string) error {
			_, e := s.BatchUntagging(context.Background(), 1, []string{"o1"})
			return e
		}},
		{ep: epUserTagIDs, invoke: func(s *WxOAService, b string) error { _, e := s.GetUserTagIDs(context.Background(), "o1"); return e }},
		{ep: epUserInfo, invoke: func(s *WxOAService, b string) error { _, e := s.GetUserInfo(context.Background(), "o1", ""); return e }},
		{ep: epUserInfoBat, invoke: func(s *WxOAService, b string) error {
			_, e := s.BatchGetUserInfo(context.Background(), []string{"o1"})
			return e
		}},
		{ep: epUserList, invoke: func(s *WxOAService, b string) error { _, e := s.GetUserList(context.Background(), ""); return e }},
		{ep: epBlackAdd, invoke: func(s *WxOAService, b string) error {
			_, e := s.BatchBlacklist(context.Background(), []string{"o1"})
			return e
		}},
		{ep: epBlackDel, invoke: func(s *WxOAService, b string) error {
			_, e := s.BatchUnblacklist(context.Background(), []string{"o1"})
			return e
		}},
		{ep: epBlackGet, invoke: func(s *WxOAService, b string) error { _, e := s.GetBlacklist(context.Background(), ""); return e }},
		{ep: epUserRemark, invoke: func(s *WxOAService, b string) error {
			_, e := s.UpdateUserRemark(context.Background(), "o1", "备注")
			return e
		}},
		{ep: epMaterialAdd, invoke: func(s *WxOAService, b string) error {
			_, e := s.AddMaterial(context.Background(), "image", "a.png", []byte("png"), nil)
			return e
		}},
		{ep: epMaterialGet, invoke: func(s *WxOAService, b string) error { _, e := s.GetMaterial(context.Background(), "MD1"); return e }},
		{ep: epMaterialCount, invoke: func(s *WxOAService, b string) error { _, e := s.GetMaterialCount(context.Background()); return e }},
		{ep: epMaterialBatch, invoke: func(s *WxOAService, b string) error {
			_, e := s.BatchGetMaterial(context.Background(), map[string]any{"type": "image", "offset": 0, "count": 20})
			return e
		}},
		{ep: epMaterialDel, invoke: func(s *WxOAService, b string) error { _, e := s.DelMaterial(context.Background(), "MD1"); return e }},
		{ep: epMediaUploadImg, invoke: func(s *WxOAService, b string) error {
			_, e := s.UploadImg(context.Background(), "a.png", []byte("png"))
			return e
		}},
		{ep: epMediaUpload, invoke: func(s *WxOAService, b string) error {
			_, e := s.UploadTempMedia(context.Background(), "image", "a.png", []byte("png"))
			return e
		}},
		{ep: epMediaGet, invoke: func(s *WxOAService, b string) error { _, e := s.GetTempMedia(context.Background(), "MD1"); return e }},
		{ep: epMediaJSSDKVoice, invoke: func(s *WxOAService, b string) error { _, e := s.GetJSSDKVoice(context.Background(), "MD1"); return e }},
		{ep: epCommentOpen, invoke: func(s *WxOAService, b string) error {
			_, e := s.OpenComment(context.Background(), map[string]any{"msg_data_id": 1})
			return e
		}},
		{ep: epCommentList, invoke: func(s *WxOAService, b string) error {
			_, e := s.ListComments(context.Background(), map[string]any{"msg_data_id": 1, "begin": 0, "count": 10, "type": 0})
			return e
		}},
		{ep: epCommentClose, invoke: func(s *WxOAService, b string) error {
			_, e := s.CloseComment(context.Background(), map[string]any{"msg_data_id": 1})
			return e
		}},
		{ep: epCommentElect, invoke: func(s *WxOAService, b string) error {
			_, e := s.MarkElect(context.Background(), map[string]any{"msg_data_id": 1, "user_comment_id": 1})
			return e
		}},
		{ep: epCommentUnelect, invoke: func(s *WxOAService, b string) error {
			_, e := s.UnmarkElect(context.Background(), map[string]any{"msg_data_id": 1, "user_comment_id": 1})
			return e
		}},
		{ep: epCommentDelete, invoke: func(s *WxOAService, b string) error {
			_, e := s.DeleteComment(context.Background(), map[string]any{"msg_data_id": 1, "user_comment_id": 1})
			return e
		}},
		{ep: epCommentReplyAdd, invoke: func(s *WxOAService, b string) error {
			_, e := s.AddCommentReply(context.Background(), map[string]any{"msg_data_id": 1, "user_comment_id": 1, "content": "回复"})
			return e
		}},
		{ep: epCommentReplyDel, invoke: func(s *WxOAService, b string) error {
			_, e := s.DeleteCommentReply(context.Background(), map[string]any{"msg_data_id": 1, "user_comment_id": 1, "reply_id": 1})
			return e
		}},
		{ep: epPublishSubmit, invoke: func(s *WxOAService, b string) error { _, e := s.SubmitPublish(context.Background(), "MD1"); return e }},
		{ep: epPublishStatus, invoke: func(s *WxOAService, b string) error {
			_, e := s.GetPublishStatus(context.Background(), "PUB1")
			return e
		}},
		{ep: epPublishBatch, invoke: func(s *WxOAService, b string) error {
			_, e := s.BatchGetPublished(context.Background(), 0, 20)
			return e
		}},
		{ep: epPublishArticle, invoke: func(s *WxOAService, b string) error {
			_, e := s.GetPublishedArticle(context.Background(), map[string]any{"article_id": "A1"})
			return e
		}},
		{ep: epPublishDelete, invoke: func(s *WxOAService, b string) error { _, e := s.DeletePublish(context.Background(), "A1", 0); return e }},
		{ep: epDraftSwitch, invoke: func(s *WxOAService, b string) error { _, e := s.SetDraftSwitch(context.Background(), false); return e }},
		{ep: epDraftAdd, invoke: func(s *WxOAService, b string) error {
			_, e := s.AddDraft(context.Background(), []map[string]any{{"title": "t"}})
			return e
		}},
		{ep: epDraftGet, invoke: func(s *WxOAService, b string) error { _, e := s.GetDraft(context.Background(), "MD1"); return e }},
		{ep: epDraftBatch, invoke: func(s *WxOAService, b string) error {
			_, e := s.BatchGetDrafts(context.Background(), 0, 20, false)
			return e
		}},
		{ep: epDraftCount, invoke: func(s *WxOAService, b string) error { _, e := s.GetDraftCount(context.Background()); return e }},
		{ep: epDraftDelete, invoke: func(s *WxOAService, b string) error { _, e := s.DeleteDraft(context.Background(), "MD1"); return e }},
		{ep: epDraftUpdate, invoke: func(s *WxOAService, b string) error {
			_, e := s.UpdateDraft(context.Background(), "MD1", 0, map[string]any{"title": "t"})
			return e
		}},
		{ep: epMassSendAll, invoke: func(s *WxOAService, b string) error {
			_, e := s.MassSendAll(context.Background(), map[string]any{"filter": map[string]any{"is_to_all": true}, "msgtype": "text", "text": map[string]any{"content": "hi"}})
			return e
		}},
		{ep: epMassSend, invoke: func(s *WxOAService, b string) error {
			_, e := s.MassSend(context.Background(), map[string]any{"touser": []string{"o1"}, "msgtype": "text", "text": map[string]any{"content": "hi"}})
			return e
		}},
		{ep: epMassPreview, invoke: func(s *WxOAService, b string) error {
			_, e := s.MassPreview(context.Background(), map[string]any{"touser": "o1", "msgtype": "text", "text": map[string]any{"content": "hi"}})
			return e
		}},
		{ep: epMassDelete, invoke: func(s *WxOAService, b string) error { _, e := s.MassDelete(context.Background(), 1001); return e }},
		{ep: epMassGet, invoke: func(s *WxOAService, b string) error { _, e := s.MassGet(context.Background(), 1001); return e }},
		{ep: epMassSpdGet, invoke: func(s *WxOAService, b string) error { _, e := s.MassSpeedGet(context.Background()); return e }},
		{ep: epMassSpdSet, invoke: func(s *WxOAService, b string) error { _, e := s.MassSpeedSet(context.Background(), 2); return e }},
		{ep: epMassNews, invoke: func(s *WxOAService, b string) error {
			_, e := s.UploadNews(context.Background(), map[string]any{"articles": []any{}})
			return e
		}},
		{ep: epSubsCategory, invoke: func(s *WxOAService, b string) error { _, e := s.GetSubsCategory(context.Background()); return e }},
		{ep: epSubsPubTitle, invoke: func(s *WxOAService, b string) error {
			_, e := s.ListPubTitles(context.Background(), "2,31", 0, 30)
			return e
		}},
		{ep: epSubsPubKw, invoke: func(s *WxOAService, b string) error { _, e := s.GetPubKeywords(context.Background(), "TID1"); return e }},
		{ep: epSubsAddTpl, invoke: func(s *WxOAService, b string) error {
			_, e := s.AddSubsTemplate(context.Background(), map[string]any{"tid": "TID1", "kidList": []int{1, 2}, "sceneDesc": "场景"})
			return e
		}},
		{ep: epSubsGetTpl, invoke: func(s *WxOAService, b string) error { _, e := s.ListSubsTemplates(context.Background()); return e }},
		{ep: epSubsDelTpl, invoke: func(s *WxOAService, b string) error { _, e := s.DelSubsTemplate(context.Background(), "PT1"); return e }},
		{ep: epSubsBizSend, invoke: func(s *WxOAService, b string) error {
			_, e := s.SendSubsMsg(context.Background(), map[string]any{"touser": "o1", "template_id": "PT1", "data": map[string]any{}})
			return e
		}},
		{ep: epTmplSetIndustry, invoke: func(s *WxOAService, b string) error {
			_, e := s.SetTmplIndustry(context.Background(), map[string]any{"industry_id1": 1, "industry_id2": 2})
			return e
		}},
		{ep: epTmplGetIndustry, invoke: func(s *WxOAService, b string) error { _, e := s.GetTmplIndustry(context.Background()); return e }},
		{ep: epTmplAdd, invoke: func(s *WxOAService, b string) error { _, e := s.AddTmpl(context.Background(), "SHORT1"); return e }},
		{ep: epTmplList, invoke: func(s *WxOAService, b string) error { _, e := s.ListTmpl(context.Background()); return e }},
		{ep: epTmplDel, invoke: func(s *WxOAService, b string) error { _, e := s.DelTmpl(context.Background(), "TID1"); return e }},
		{ep: epTmplSend, invoke: func(s *WxOAService, b string) error {
			_, e := s.SendTmplMsg(context.Background(), map[string]any{"touser": "o1", "template_id": "TID1", "data": map[string]any{}})
			return e
		}},
		{ep: epTmplBlocked, invoke: func(s *WxOAService, b string) error {
			_, e := s.QueryBlockedTmplMsg(context.Background(), map[string]any{"begin_date": "2026-08-01", "end_date": "2026-08-22"})
			return e
		}},
		{ep: epKFAccountAdd, invoke: func(s *WxOAService, b string) error {
			_, e := s.AddKFAccount(context.Background(), map[string]any{"kf_account": "k1@gh", "nickname": "客服"})
			return e
		}},
		{ep: epKFAccountUpd, invoke: func(s *WxOAService, b string) error {
			_, e := s.UpdateKFAccount(context.Background(), map[string]any{"kf_account": "k1@gh", "nickname": "新名"})
			return e
		}},
		{ep: epKFAccountDel, invoke: func(s *WxOAService, b string) error {
			_, e := s.DelKFAccount(context.Background(), map[string]any{"kf_account": "k1@gh"})
			return e
		}},
		{ep: epKFHeadImg, invoke: func(s *WxOAService, b string) error {
			_, e := s.UploadKFHeadImg(context.Background(), "k1@gh", "h.jpg", []byte("jpg"))
			return e
		}},
		{ep: epKFInvite, invoke: func(s *WxOAService, b string) error {
			_, e := s.InviteKFWorker(context.Background(), map[string]any{"kf_account": "k1@gh", "invite_wx": "wx1"})
			return e
		}},
		{ep: epKFList, invoke: func(s *WxOAService, b string) error { _, e := s.ListKFAccounts(context.Background()); return e }},
		{ep: epKFOnlineList, invoke: func(s *WxOAService, b string) error { _, e := s.ListOnlineKF(context.Background()); return e }},
		{ep: epKFSessionCreate, invoke: func(s *WxOAService, b string) error {
			_, e := s.CreateKFSession(context.Background(), map[string]any{"kf_account": "k1@gh", "openid": "o1"})
			return e
		}},
		{ep: epKFSessionClose, invoke: func(s *WxOAService, b string) error {
			_, e := s.CloseKFSession(context.Background(), map[string]any{"kf_account": "k1@gh", "openid": "o1"})
			return e
		}},
		{ep: epKFSessionGet, invoke: func(s *WxOAService, b string) error { _, e := s.GetKFSession(context.Background(), "o1"); return e }},
		{ep: epKFSessionList, invoke: func(s *WxOAService, b string) error {
			_, e := s.ListKFSessions(context.Background(), "k1@gh")
			return e
		}},
		{ep: epKFWaitCases, invoke: func(s *WxOAService, b string) error { _, e := s.ListKFWaitCases(context.Background()); return e }},
		{ep: epKFSend, invoke: func(s *WxOAService, b string) error {
			_, e := s.SendKFMsg(context.Background(), map[string]any{"touser": "o1", "msgtype": "text", "text": map[string]any{"content": "hi"}})
			return e
		}},
		{ep: epKFTyping, invoke: func(s *WxOAService, b string) error {
			_, e := s.SetKFTyping(context.Background(), "o1", "Typing")
			return e
		}},
		{ep: epKFMsgList, invoke: func(s *WxOAService, b string) error {
			_, e := s.GetKFMsgRecords(context.Background(), map[string]any{"endtime": 1724000000, "pageindex": 1, "pagesize": 50})
			return e
		}},
		{ep: epStatsUsersummary, invoke: func(s *WxOAService, b string) error {
			_, e := s.GetUserSummary(context.Background(), "20260817", "20260823")
			return e
		}},
		{ep: epStatsUsercumulate, invoke: func(s *WxOAService, b string) error {
			_, e := s.GetUserCumulate(context.Background(), "20260817", "20260823")
			return e
		}},
		{ep: epStatsArticlesummary, invoke: func(s *WxOAService, b string) error {
			_, e := s.GetArticleSummary(context.Background(), "20260817", "20260823")
			return e
		}},
		{ep: epStatsUserread, invoke: func(s *WxOAService, b string) error {
			_, e := s.GetUserRead(context.Background(), "20260817", "20260823")
			return e
		}},
		{ep: epStatsUserreadhour, invoke: func(s *WxOAService, b string) error {
			_, e := s.GetUserReadHour(context.Background(), "20260817", "20260823")
			return e
		}},
		{ep: epStatsArticletotal, invoke: func(s *WxOAService, b string) error {
			_, e := s.GetArticleTotal(context.Background(), "20260817", "20260823")
			return e
		}},
		{ep: epStatsUsershare, invoke: func(s *WxOAService, b string) error {
			_, e := s.GetUserShare(context.Background(), "20260817", "20260823")
			return e
		}},
		{ep: epStatsUsersharehour, invoke: func(s *WxOAService, b string) error {
			_, e := s.GetUserShareHour(context.Background(), "20260817", "20260823")
			return e
		}},
		{ep: epStatsArticleread, invoke: func(s *WxOAService, b string) error {
			_, e := s.GetArticleRead(context.Background(), "20260817", "20260823")
			return e
		}},
		{ep: epStatsArticleshare, invoke: func(s *WxOAService, b string) error {
			_, e := s.GetArticleShare(context.Background(), "20260817", "20260823")
			return e
		}},
		{ep: epStatsBizsummary, invoke: func(s *WxOAService, b string) error {
			_, e := s.GetBizSummary(context.Background(), "20260817", "20260823")
			return e
		}},
		{ep: epStatsArticletotaldetail, invoke: func(s *WxOAService, b string) error {
			_, e := s.GetArticleTotalDetail(context.Background(), "20260817", "20260823")
			return e
		}},
		{ep: epStatsUpstreammsg, invoke: func(s *WxOAService, b string) error {
			_, e := s.GetUpstreamMsg(context.Background(), "20260817", "20260823")
			return e
		}},
		{ep: epStatsUpstreammsghour, invoke: func(s *WxOAService, b string) error {
			_, e := s.GetUpstreamMsgHour(context.Background(), "20260817", "20260823")
			return e
		}},
		{ep: epStatsUpstreammsgweek, invoke: func(s *WxOAService, b string) error {
			_, e := s.GetUpstreamMsgWeek(context.Background(), "20260817", "20260823")
			return e
		}},
		{ep: epStatsUpstreammsgmonth, invoke: func(s *WxOAService, b string) error {
			_, e := s.GetUpstreamMsgMonth(context.Background(), "20260801", "20260831")
			return e
		}},
		{ep: epStatsUpstreammsgdist, invoke: func(s *WxOAService, b string) error {
			_, e := s.GetUpstreamMsgDist(context.Background(), "20260817", "20260823")
			return e
		}},
		{ep: epStatsUpstreammsgdistweek, invoke: func(s *WxOAService, b string) error {
			_, e := s.GetUpstreamMsgDistWeek(context.Background(), "20260817", "20260823")
			return e
		}},
		{ep: epStatsUpstreammsgdistmonth, invoke: func(s *WxOAService, b string) error {
			_, e := s.GetUpstreamMsgDistMonth(context.Background(), "20260801", "20260831")
			return e
		}},
		{ep: epStatsInterfacesummary, invoke: func(s *WxOAService, b string) error {
			_, e := s.GetInterfaceSummary(context.Background(), "20260817", "20260823")
			return e
		}},
		{ep: epStatsInterfacesummaryhour, invoke: func(s *WxOAService, b string) error {
			_, e := s.GetInterfaceSummaryHour(context.Background(), "20260817", "20260823")
			return e
		}},
		{ep: epAITranslate, invoke: func(s *WxOAService, b string) error {
			_, e := s.VoiceTranslate(context.Background(), map[string]any{"lfrom": "zh", "lto": "en", "content": "你好"})
			return e
		}},
		{ep: epAIVoiceAdd, invoke: func(s *WxOAService, b string) error {
			_, e := s.VoiceUploadReco(context.Background(), map[string]any{"format": "mp3", "voice_base64": "Yg=="})
			return e
		}},
		{ep: epAIVoiceQuery, invoke: func(s *WxOAService, b string) error {
			_, e := s.VoiceQueryReco(context.Background(), map[string]any{"voice_id": "V1"})
			return e
		}},
		{ep: epOcrComm, invoke: func(s *WxOAService, b string) error {
			_, e := s.OcrComm(context.Background(), "https://x/a.png")
			return e
		}},
		{ep: epOcrIdcard, invoke: func(s *WxOAService, b string) error {
			_, e := s.OcrIdcard(context.Background(), "https://x/a.png")
			return e
		}},
		{ep: epOcrDriving, invoke: func(s *WxOAService, b string) error {
			_, e := s.OcrDriving(context.Background(), "https://x/a.png")
			return e
		}},
		{ep: epOcrDrvLicense, invoke: func(s *WxOAService, b string) error {
			_, e := s.OcrDrivingLicense(context.Background(), "https://x/a.png")
			return e
		}},
		{ep: epOcrBankcard, invoke: func(s *WxOAService, b string) error {
			_, e := s.OcrBankcard(context.Background(), "https://x/a.png")
			return e
		}},
		{ep: epOcrBizlicense, invoke: func(s *WxOAService, b string) error {
			_, e := s.OcrBizlicense(context.Background(), "https://x/a.png")
			return e
		}},
		{ep: epOcrMenu, invoke: func(s *WxOAService, b string) error {
			_, e := s.OcrMenu(context.Background(), "https://x/a.png")
			return e
		}},
		{ep: epSnsToken, invoke: func(s *WxOAService, b string) error {
			_, e := s.GetOAuthAccessToken(context.Background(), "CODE1")
			return e
		}},
		{ep: epSnsRefresh, invoke: func(s *WxOAService, b string) error {
			_, e := s.RefreshOAuthToken(context.Background(), "RT1")
			return e
		}},
		{ep: epSnsUserinfo, invoke: func(s *WxOAService, b string) error {
			_, e := s.GetOAuthUserInfo(context.Background(), "OAT1", "o1", "")
			return e
		}},
		{ep: epSnsAuth, invoke: func(s *WxOAService, b string) error {
			_, e := s.CheckOAuthAuth(context.Background(), "OAT1", "o1")
			return e
		}},
	}
	if len(cases) != len(allEndpoints) {
		t.Fatalf("用例数 %d != 表项数 %d", len(cases), len(allEndpoints))
	}
	// Name 一一对应防漂移
	caseNames := map[string]bool{}
	for _, c := range cases {
		caseNames[c.ep.Name] = true
	}
	for _, ep := range allEndpoints {
		if !caseNames[ep.Name] {
			t.Fatalf("表项 %s 缺对应用例", ep.Name)
		}
	}
	var mu sync.Mutex
	type hit struct{ method, path string }
	hits := map[string][]string{} // path -> methods
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Path] = append(hits[r.URL.Path], r.Method)
		mu.Unlock()
		if r.URL.Path == epToken.Path {
			_, _ = w.Write([]byte(`{"access_token":"TK"}`))
			return
		}
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok","media_id":"MD1","access_token":"TK","ticket":"JT","expires_in":7200}`))
	}))
	t.Cleanup(srv.Close)
	s := NewWxOAService("wx123", "sec")
	s.BaseURL = srv.URL
	for _, tc := range cases {
		if err := tc.invoke(s, srv.URL); err != nil {
			t.Fatalf("%s 调用失败: %v", tc.ep.Name, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	// 每表项至少一次命中声明的 method/path
	for _, tc := range cases {
		ms, ok := hits[tc.ep.Path]
		if !ok {
			t.Fatalf("%s 未命中 path %s", tc.ep.Name, tc.ep.Path)
		}
		matched := false
		for _, m := range ms {
			if m == tc.ep.Method {
				matched = true
				break
			}
		}
		if !matched {
			t.Fatalf("%s 命中 method %v，声明 %s", tc.ep.Name, ms, tc.ep.Method)
		}
	}
}

const (
	fxToken  = "tok123"
	fxAESKey = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFG"
	fxAppID  = "wx_app_1"
)

// sign 复现官方签名：SHA1(字典序拼接 token,timestamp,nonce,encryptMsg)
func sign(token, timestamp, nonce, encryptMsg string) string {
	parts := []string{token, timestamp, nonce, encryptMsg}
	sort.Strings(parts)
	h := sha1.Sum([]byte(strings.Join(parts, "")))
	return fmt.Sprintf("%x", h)
}

// TestCallbackCrypt_RoundTrip crypt 复用 wecom：加密→解密 round-trip + 签名篡改拒绝 + appID 尾校验
func TestCallbackCrypt_RoundTrip(t *testing.T) {
	c, err := NewCallbackCrypt(fxAppID, fxToken, fxAESKey)
	if err != nil {
		t.Fatal(err)
	}
	plain := `<xml><ToUserName><![CDATA[wx_app_1]]></ToUserName><FromUserName><![CDATA[oU1]]></FromUserName><CreateTime>1723963200</CreateTime><MsgType><![CDATA[text]]></MsgType><Content><![CDATA[hi]]></Content></xml>`

	inner, err := wecom.NewWXBizMsgCrypt(fxToken, fxAESKey, fxAppID)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := inner.Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	ts, nonce := "1723963201", "n1"
	sig := sign(fxToken, ts, nonce, enc)
	body := `<xml><Encrypt><![CDATA[` + enc + `]]></Encrypt></xml>`

	got, err := c.DecryptEvent(sig, ts, nonce, body)
	if err != nil {
		t.Fatal(err)
	}
	if got != plain {
		t.Fatalf("round-trip 明文不符:\n got %s\nwant %s", got, plain)
	}

	// 签名篡改拒绝
	if _, err := c.DecryptEvent("bad"+sig[3:], ts, nonce, body); err == nil {
		t.Fatal("签名篡改应拒绝")
	}

	// 尾校验：不同 appID 的实例解密同一密文失败（receiveid 尾不匹配）
	c2, err := NewCallbackCrypt("wx_app_2", fxToken, fxAESKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c2.DecryptEvent(sig, ts, nonce, body); err == nil {
		t.Fatal("appID 尾校验应拒绝")
	}

	// 被动回复加密 round-trip
	reply := `<xml><Content><![CDATA[ok]]></Content></xml>`
	encMsg, err := c.EncryptMsg(reply, ts, nonce)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(encMsg, "<Encrypt>") || !strings.Contains(encMsg, "<MsgSignature>") {
		t.Fatalf("EncryptMsg 输出形态不符: %s", encMsg)
	}
}
