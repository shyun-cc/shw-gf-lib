package wecom

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/shyun-cc/shw-gf-lib/internal/testredis"
)

// TestEndpoints_TableDriven 表驱动端点断言：遍历全量表项，桩逐项断言
// method/path/query 键集（含 access_token）/body 形态；计数相等防漂移。
func TestEndpoints_TableDriven(t *testing.T) {
	testredis.Start(t)

	// 用例表：每表项一个 invoke 闭包（组最小合法参数发起真实调用）
	cases := []struct {
		ep     Endpoint
		invoke func(c *Client, base string) error
	}{
		{
			ep: epAuthGetUserinfo,
			invoke: func(c *Client, base string) error {
				_, err := c.GetUserinfoByCode(context.Background(), "CODE_T")
				return err
			},
		},
		{
			ep: epAuthGetUserDetail,
			invoke: func(c *Client, base string) error {
				_, err := c.GetUserDetailByTicket(context.Background(), "TICKET_T")
				return err
			},
		},
		{ep: epUserCreate, invoke: func(c *Client, b string) error {
			_, e := c.CreateUser(context.Background(), map[string]any{"userid": "u1", "name": "n"})
			return e
		}},
		{ep: epUserGet, invoke: func(c *Client, b string) error { _, e := c.GetUser(context.Background(), "u1"); return e }},
		{ep: epUserUpdate, invoke: func(c *Client, b string) error {
			_, e := c.UpdateUser(context.Background(), map[string]any{"userid": "u1"})
			return e
		}},
		{ep: epUserDelete, invoke: func(c *Client, b string) error { _, e := c.DeleteUser(context.Background(), "u1"); return e }},
		{ep: epUserBatchDelete, invoke: func(c *Client, b string) error {
			_, e := c.BatchDeleteUser(context.Background(), []string{"u1"})
			return e
		}},
		{ep: epUserSimpleList, invoke: func(c *Client, b string) error { _, e := c.SimpleListUser(context.Background(), 2, true); return e }},
		{ep: epUserList, invoke: func(c *Client, b string) error { _, e := c.ListUser(context.Background(), 2, false); return e }},
		{ep: epUserToOpenid, invoke: func(c *Client, b string) error { _, e := c.ConvertToOpenid(context.Background(), "u1"); return e }},
		{ep: epUserToUserid, invoke: func(c *Client, b string) error { _, e := c.ConvertToUserid(context.Background(), "o1"); return e }},
		{ep: epUserAuthSucc, invoke: func(c *Client, b string) error { _, e := c.AuthSucc(context.Background(), "u1"); return e }},
		{ep: epBatchInvite, invoke: func(c *Client, b string) error {
			_, e := c.InviteUser(context.Background(), map[string]any{"user": []string{"u1"}})
			return e
		}},
		{ep: epCorpJoinQrcode, invoke: func(c *Client, b string) error { _, e := c.GetJoinQrcode(context.Background(), "2"); return e }},
		{ep: epUserGetByMobile, invoke: func(c *Client, b string) error {
			_, e := c.GetUserIdByMobile(context.Background(), "10800000000")
			return e
		}},
		{ep: epUserGetByEmail, invoke: func(c *Client, b string) error {
			_, e := c.GetUserIdByEmail(context.Background(), "a@b.c", 1)
			return e
		}},
		{ep: epUserListID, invoke: func(c *Client, b string) error { _, e := c.ListUserId(context.Background(), "", 100); return e }},
		{ep: epDeptCreate, invoke: func(c *Client, b string) error {
			_, e := c.CreateDepartment(context.Background(), map[string]any{"name": "d", "parentid": 1})
			return e
		}},
		{ep: epDeptUpdate, invoke: func(c *Client, b string) error {
			_, e := c.UpdateDepartment(context.Background(), map[string]any{"id": 2, "name": "d"})
			return e
		}},
		{ep: epDeptDelete, invoke: func(c *Client, b string) error { _, e := c.DeleteDepartment(context.Background(), 2); return e }},
		{ep: epDeptList, invoke: func(c *Client, b string) error { _, e := c.ListDepartment(context.Background(), 1); return e }},
		{ep: epDeptSimpleList, invoke: func(c *Client, b string) error { _, e := c.SimpleListDepartment(context.Background(), 1); return e }},
		{ep: epDeptGet, invoke: func(c *Client, b string) error { _, e := c.GetDepartment(context.Background(), 2); return e }},
		{ep: epTagCreate, invoke: func(c *Client, b string) error { _, e := c.CreateTag(context.Background(), "t", 0); return e }},
		{ep: epTagUpdate, invoke: func(c *Client, b string) error { _, e := c.UpdateTag(context.Background(), 1, "t"); return e }},
		{ep: epTagDelete, invoke: func(c *Client, b string) error { _, e := c.DeleteTag(context.Background(), 1); return e }},
		{ep: epTagGet, invoke: func(c *Client, b string) error { _, e := c.GetTag(context.Background(), 1); return e }},
		{ep: epTagAddTagUsers, invoke: func(c *Client, b string) error {
			_, e := c.AddTagUsers(context.Background(), 1, []string{"u1"}, nil)
			return e
		}},
		{ep: epTagDelTagUsers, invoke: func(c *Client, b string) error {
			_, e := c.DelTagUsers(context.Background(), 1, []string{"u1"}, nil)
			return e
		}},
		{ep: epTagList, invoke: func(c *Client, b string) error { _, e := c.ListTag(context.Background()); return e }},
		{ep: epRuleCreate, invoke: func(c *Client, b string) error {
			_, e := c.CreateContactRule(context.Background(), []map[string]any{{"rule_type": 1}})
			return e
		}},
		{ep: epRuleList, invoke: func(c *Client, b string) error { _, e := c.ListContactRule(context.Background()); return e }},
		{ep: epRuleUpdate, invoke: func(c *Client, b string) error {
			_, e := c.UpdateContactRule(context.Background(), []map[string]any{{"rule_id": 1}})
			return e
		}},
		{ep: epRuleDelete, invoke: func(c *Client, b string) error {
			_, e := c.DeleteContactRule(context.Background(), []string{"1"})
			return e
		}},
		{ep: epBatchSyncUser, invoke: func(c *Client, b string) error {
			_, e := c.BatchSyncUser(context.Background(), map[string]any{"media_id": "m"})
			return e
		}},
		{ep: epBatchReplaceUser, invoke: func(c *Client, b string) error {
			_, e := c.BatchReplaceUser(context.Background(), map[string]any{"media_id": "m"})
			return e
		}},
		{ep: epBatchReplaceDept, invoke: func(c *Client, b string) error {
			_, e := c.BatchReplaceParty(context.Background(), map[string]any{"media_id": "m"})
			return e
		}},
		{ep: epBatchGetResult, invoke: func(c *Client, b string) error { _, e := c.BatchGetResult(context.Background(), "job1"); return e }},
		{ep: epExportSimpleUser, invoke: func(c *Client, b string) error {
			_, e := c.ExportSimpleUser(context.Background(), "aeskey", 1000000)
			return e
		}},
		{ep: epExportUser, invoke: func(c *Client, b string) error { _, e := c.ExportUser(context.Background(), "aeskey", 0); return e }},
		{ep: epExportDept, invoke: func(c *Client, b string) error {
			_, e := c.ExportDepartment(context.Background(), "aeskey", 0)
			return e
		}},
		{ep: epExportTagUser, invoke: func(c *Client, b string) error {
			_, e := c.ExportTagUser(context.Background(), 1, "aeskey", 0)
			return e
		}},
		{ep: epExportGetResult, invoke: func(c *Client, b string) error { _, e := c.ExportGetResult(context.Background(), "job1"); return e }},
		{ep: epMediaUpload, invoke: func(c *Client, b string) error {
			_, e := c.UploadMedia(context.Background(), "image", "p.jpg", strings.NewReader("DATA"))
			return e
		}},
		{ep: epMediaUploadImg, invoke: func(c *Client, b string) error {
			_, e := c.UploadImg(context.Background(), "p.jpg", strings.NewReader("DATA"))
			return e
		}},
		{ep: epMediaUploadByURL, invoke: func(c *Client, b string) error {
			_, e := c.UploadMediaByURL(context.Background(), map[string]any{"scene": 1, "type": "file", "filename": "f", "url": "https://x/f", "md5": "d"})
			return e
		}},
		{ep: epMediaUploadResult, invoke: func(c *Client, b string) error {
			_, e := c.GetUploadByURLResult(context.Background(), "J")
			return e
		}},
		{ep: epMediaGet, invoke: func(c *Client, b string) error {
			_, _, _, e := c.GetMedia(context.Background(), "M")
			return e
		}},
		{ep: epMediaGetJssdk, invoke: func(c *Client, b string) error {
			_, _, _, e := c.GetJssdkVoice(context.Background(), "M")
			return e
		}},
		{ep: epMsgSend, invoke: func(c *Client, b string) error {
			_, e := c.SendMessage(context.Background(), map[string]any{"touser": "u1", "msgtype": "text", "text": map[string]any{"content": "x"}})
			return e
		}},
		{ep: epMsgUpdateCard, invoke: func(c *Client, b string) error {
			_, e := c.UpdateTemplateCard(context.Background(), map[string]any{"response_code": "RC"})
			return e
		}},
		{ep: epMsgRecall, invoke: func(c *Client, b string) error {
			_, e := c.RecallMessage(context.Background(), "MSG")
			return e
		}},
		{ep: epAppChatCreate, invoke: func(c *Client, b string) error {
			_, e := c.CreateAppChat(context.Background(), map[string]any{"name": "g", "userlist": []string{"u1"}})
			return e
		}},
		{ep: epAppChatUpdate, invoke: func(c *Client, b string) error {
			_, e := c.UpdateAppChat(context.Background(), "CH", map[string]any{"name": "g"})
			return e
		}},
		{ep: epAppChatGet, invoke: func(c *Client, b string) error {
			_, e := c.GetAppChat(context.Background(), "CH")
			return e
		}},
		{ep: epAppChatSend, invoke: func(c *Client, b string) error {
			_, e := c.SendAppChat(context.Background(), map[string]any{"chatid": "CH", "msgtype": "text", "text": map[string]any{"content": "x"}})
			return e
		}},
		{ep: epSchoolNotice, invoke: func(c *Client, b string) error {
			_, e := c.SendSchoolNotice(context.Background(), map[string]any{"recv_scope": 1, "msgtype": "text", "text": map[string]any{"content": "x"}})
			return e
		}},
		{ep: epAgentGet, invoke: func(c *Client, b string) error {
			_, e := c.GetAgent(context.Background(), 1001)
			return e
		}},
		{ep: epAgentList, invoke: func(c *Client, b string) error { _, e := c.ListAgents(context.Background()); return e }},
		{ep: epAgentSet, invoke: func(c *Client, b string) error {
			_, e := c.SetAgent(context.Background(), map[string]any{"agentid": 1001, "name": "n"})
			return e
		}},
		{ep: epMenuCreate, invoke: func(c *Client, b string) error {
			_, e := c.CreateMenu(context.Background(), 1001, map[string]any{"button": []any{}})
			return e
		}},
		{ep: epMenuGet, invoke: func(c *Client, b string) error {
			_, e := c.GetMenu(context.Background(), 1001)
			return e
		}},
		{ep: epMenuDelete, invoke: func(c *Client, b string) error {
			_, e := c.DeleteMenu(context.Background(), 1001)
			return e
		}},
		{ep: epWBSetTemplate, invoke: func(c *Client, b string) error {
			_, e := c.SetWorkbenchTemplate(context.Background(), map[string]any{"agentid": 1001, "type": "keydata"})
			return e
		}},
		{ep: epWBGetTemplate, invoke: func(c *Client, b string) error {
			_, e := c.GetWorkbenchTemplate(context.Background(), 1001)
			return e
		}},
		{ep: epWBSetData, invoke: func(c *Client, b string) error {
			_, e := c.SetWorkbenchData(context.Background(), map[string]any{"agentid": 1001, "userid": "u1", "type": "keydata"})
			return e
		}},
		{ep: epWBBatchSetData, invoke: func(c *Client, b string) error {
			_, e := c.BatchSetWorkbenchData(context.Background(), map[string]any{"agentid": 1001, "userid_list": []string{"u1"}})
			return e
		}},
		{ep: epWBGetData, invoke: func(c *Client, b string) error {
			_, e := c.GetWorkbenchData(context.Background(), 1001, "u1")
			return e
		}},
		{ep: epECFollowUserList, invoke: func(c *Client, b string) error {
			_, e := c.GetFollowUserList(context.Background())
			return e
		}},
		{ep: epECList, invoke: func(c *Client, b string) error {
			_, e := c.ListExternalContacts(context.Background(), "u1")
			return e
		}},
		{ep: epECGet, invoke: func(c *Client, b string) error {
			_, e := c.GetExternalContact(context.Background(), "EXT1", "")
			return e
		}},
		{ep: epECBatchGet, invoke: func(c *Client, b string) error {
			_, e := c.BatchGetExternalContactsByUser(context.Background(), map[string]any{"userid_list": []string{"u1"}})
			return e
		}},
		{ep: epECRemark, invoke: func(c *Client, b string) error {
			_, e := c.UpdateExternalContactRemark(context.Background(), map[string]any{"userid": "u1", "external_userid": "EXT1"})
			return e
		}},
		{ep: epECStrategyList, invoke: func(c *Client, b string) error {
			_, e := c.ListCustomerStrategy(context.Background(), map[string]any{"cursor": "", "limit": 100})
			return e
		}},
		{ep: epECStrategyGet, invoke: func(c *Client, b string) error {
			_, e := c.GetCustomerStrategy(context.Background(), 1)
			return e
		}},
		{ep: epECStrategyRange, invoke: func(c *Client, b string) error {
			_, e := c.GetCustomerStrategyRange(context.Background(), map[string]any{"strategy_id": 1})
			return e
		}},
		{ep: epECStrategyCreate, invoke: func(c *Client, b string) error {
			_, e := c.CreateCustomerStrategy(context.Background(), map[string]any{"strategy_name": "s"})
			return e
		}},
		{ep: epECStrategyEdit, invoke: func(c *Client, b string) error {
			_, e := c.EditCustomerStrategy(context.Background(), map[string]any{"strategy_id": 1})
			return e
		}},
		{ep: epECStrategyDel, invoke: func(c *Client, b string) error {
			_, e := c.DeleteCustomerStrategy(context.Background(), 1)
			return e
		}},
		{ep: epECCorpTagList, invoke: func(c *Client, b string) error {
			_, e := c.GetCorpTagList(context.Background(), map[string]any{})
			return e
		}},
		{ep: epECCorpTagAdd, invoke: func(c *Client, b string) error {
			_, e := c.AddCorpTag(context.Background(), map[string]any{"tag": []any{}})
			return e
		}},
		{ep: epECCorpTagEdit, invoke: func(c *Client, b string) error {
			_, e := c.EditCorpTag(context.Background(), map[string]any{"id": "T1"})
			return e
		}},
		{ep: epECCorpTagDel, invoke: func(c *Client, b string) error {
			_, e := c.DelCorpTag(context.Background(), map[string]any{"tag_id": []string{"T1"}})
			return e
		}},
		{ep: epECStrategyTagList, invoke: func(c *Client, b string) error {
			_, e := c.GetStrategyTagList(context.Background(), map[string]any{"strategy_id": 1})
			return e
		}},
		{ep: epECStrategyTagAdd, invoke: func(c *Client, b string) error {
			_, e := c.AddStrategyTag(context.Background(), map[string]any{"strategy_id": 1, "tag": []any{}})
			return e
		}},
		{ep: epECStrategyTagEdit, invoke: func(c *Client, b string) error {
			_, e := c.EditStrategyTag(context.Background(), map[string]any{"id": "T1"})
			return e
		}},
		{ep: epECStrategyTagDel, invoke: func(c *Client, b string) error {
			_, e := c.DelStrategyTag(context.Background(), map[string]any{"tag_id": []string{"T1"}})
			return e
		}},
		{ep: epECMarkTag, invoke: func(c *Client, b string) error {
			_, e := c.MarkExternalContactTag(context.Background(), map[string]any{"userid": "u1", "external_userid": "EXT1", "add_tag": []string{"T1"}})
			return e
		}},
		{ep: epECOnjobTransferCustomer, invoke: func(c *Client, b string) error {
			_, e := c.TransferOnjobCustomer(context.Background(), map[string]any{"handover_userid": "u1", "takeover_userid": "u2", "external_userid": []string{"EXT1"}})
			return e
		}},
		{ep: epECOnjobTransferResult, invoke: func(c *Client, b string) error {
			_, e := c.GetOnjobTransferResult(context.Background(), map[string]any{"handover_userid": "u1", "takeover_userid": "u2"})
			return e
		}},
		{ep: epECOnjobTransferGroup, invoke: func(c *Client, b string) error {
			_, e := c.TransferOnjobGroupChat(context.Background(), map[string]any{"chat_id_list": []string{"CH1"}, "new_owner": "u2"})
			return e
		}},
		{ep: epECUnassignedList, invoke: func(c *Client, b string) error {
			_, e := c.GetUnassignedList(context.Background(), map[string]any{"page_size": 100})
			return e
		}},
		{ep: epECResignedTransferCust, invoke: func(c *Client, b string) error {
			_, e := c.TransferResignedCustomer(context.Background(), map[string]any{"handover_userid": "u1", "takeover_userid": "u2", "external_userid": []string{"EXT1"}})
			return e
		}},
		{ep: epECResignedTransferRes, invoke: func(c *Client, b string) error {
			_, e := c.GetResignedTransferResult(context.Background(), map[string]any{"handover_userid": "u1", "takeover_userid": "u2"})
			return e
		}},
		{ep: epECResignedTransferGroup, invoke: func(c *Client, b string) error {
			_, e := c.TransferResignedGroupChat(context.Background(), map[string]any{"chat_id_list": []string{"CH1"}, "new_owner": "u2"})
			return e
		}},
		{ep: epECGroupChatList, invoke: func(c *Client, b string) error {
			_, e := c.ListGroupChat(context.Background(), map[string]any{"limit": 100})
			return e
		}},
		{ep: epECGroupChatGet, invoke: func(c *Client, b string) error {
			_, e := c.GetGroupChat(context.Background(), "CH1", true)
			return e
		}},
		{ep: epECOpenGidToChat, invoke: func(c *Client, b string) error {
			_, e := c.OpenGidToChatId(context.Background(), "OG1")
			return e
		}},
		{ep: epECContactWayAdd, invoke: func(c *Client, b string) error {
			_, e := c.AddContactWay(context.Background(), map[string]any{"type": 1, "scene": 1, "user": []string{"u1"}})
			return e
		}},
		{ep: epECContactWayGet, invoke: func(c *Client, b string) error {
			_, e := c.GetContactWay(context.Background(), "CFG1")
			return e
		}},
		{ep: epECContactWayList, invoke: func(c *Client, b string) error {
			_, e := c.ListContactWay(context.Background(), map[string]any{"limit": 100})
			return e
		}},
		{ep: epECContactWayUpdate, invoke: func(c *Client, b string) error {
			_, e := c.UpdateContactWay(context.Background(), map[string]any{"config_id": "CFG1"})
			return e
		}},
		{ep: epECContactWayDel, invoke: func(c *Client, b string) error {
			_, e := c.DelContactWay(context.Background(), "CFG1")
			return e
		}},
		{ep: epECCloseTempChat, invoke: func(c *Client, b string) error {
			_, e := c.CloseTempChat(context.Background(), "u1", "EXT1")
			return e
		}},
		{ep: epECJoinWayAdd, invoke: func(c *Client, b string) error {
			_, e := c.AddJoinWay(context.Background(), map[string]any{"scene": 1, "chat_id_list": []string{"CH1"}})
			return e
		}},
		{ep: epECJoinWayGet, invoke: func(c *Client, b string) error {
			_, e := c.GetJoinWay(context.Background(), "CFG1")
			return e
		}},
		{ep: epECJoinWayUpdate, invoke: func(c *Client, b string) error {
			_, e := c.UpdateJoinWay(context.Background(), map[string]any{"config_id": "CFG1"})
			return e
		}},
		{ep: epECJoinWayDel, invoke: func(c *Client, b string) error {
			_, e := c.DelJoinWay(context.Background(), "CFG1")
			return e
		}},
		{ep: epMomentAddTask, invoke: func(c *Client, b string) error {
			_, e := c.AddMomentTask(context.Background(), map[string]any{"text": map[string]any{"content": "hi"}})
			return e
		}},
		{ep: epMomentTaskResult, invoke: func(c *Client, b string) error {
			_, e := c.GetMomentTaskResult(context.Background(), "J1")
			return e
		}},
		{ep: epMomentCancel, invoke: func(c *Client, b string) error {
			_, e := c.CancelMomentTask(context.Background(), "M1")
			return e
		}},
		{ep: epMomentList, invoke: func(c *Client, b string) error {
			_, e := c.GetMomentList(context.Background(), map[string]any{"limit": 100})
			return e
		}},
		{ep: epMomentTask, invoke: func(c *Client, b string) error {
			_, e := c.GetMomentTask(context.Background(), map[string]any{"moment_id": "M1"})
			return e
		}},
		{ep: epMomentCustomerList, invoke: func(c *Client, b string) error {
			_, e := c.GetMomentCustomerList(context.Background(), map[string]any{"moment_id": "M1", "userid": "u1"})
			return e
		}},
		{ep: epMomentSendResult, invoke: func(c *Client, b string) error {
			_, e := c.GetMomentSendResult(context.Background(), map[string]any{"moment_id": "M1", "userid": "u1"})
			return e
		}},
		{ep: epMomentComments, invoke: func(c *Client, b string) error {
			_, e := c.GetMomentComments(context.Background(), map[string]any{"moment_id": "M1", "userid": "u1"})
			return e
		}},
		{ep: epMomentStrategyList, invoke: func(c *Client, b string) error {
			_, e := c.ListMomentStrategy(context.Background(), map[string]any{"limit": 100})
			return e
		}},
		{ep: epMomentStrategyGet, invoke: func(c *Client, b string) error {
			_, e := c.GetMomentStrategy(context.Background(), 1)
			return e
		}},
		{ep: epMomentStrategyRange, invoke: func(c *Client, b string) error {
			_, e := c.GetMomentStrategyRange(context.Background(), map[string]any{"strategy_id": 1})
			return e
		}},
		{ep: epMomentStrategyNew, invoke: func(c *Client, b string) error {
			_, e := c.CreateMomentStrategy(context.Background(), map[string]any{"strategy_name": "s"})
			return e
		}},
		{ep: epMomentStrategyEdit, invoke: func(c *Client, b string) error {
			_, e := c.EditMomentStrategy(context.Background(), map[string]any{"strategy_id": 1})
			return e
		}},
		{ep: epMomentStrategyDel, invoke: func(c *Client, b string) error {
			_, e := c.DeleteMomentStrategy(context.Background(), 1)
			return e
		}},
		{ep: epAcqListLink, invoke: func(c *Client, b string) error {
			_, e := c.ListAcquisitionLink(context.Background(), map[string]any{"limit": 100})
			return e
		}},
		{ep: epAcqGet, invoke: func(c *Client, b string) error {
			_, e := c.GetAcquisitionLink(context.Background(), "L1")
			return e
		}},
		{ep: epAcqCreateLink, invoke: func(c *Client, b string) error {
			_, e := c.CreateAcquisitionLink(context.Background(), map[string]any{"link_name": "n"})
			return e
		}},
		{ep: epAcqUpdateLink, invoke: func(c *Client, b string) error {
			_, e := c.UpdateAcquisitionLink(context.Background(), map[string]any{"link_id": "L1"})
			return e
		}},
		{ep: epAcqDeleteLink, invoke: func(c *Client, b string) error {
			_, e := c.DeleteAcquisitionLink(context.Background(), "L1")
			return e
		}},
		{ep: epAcqCustomer, invoke: func(c *Client, b string) error {
			_, e := c.GetAcquisitionCustomer(context.Background(), map[string]any{"link_id": "L1"})
			return e
		}},
		{ep: epAcqQuota, invoke: func(c *Client, b string) error {
			_, e := c.GetAcquisitionQuota(context.Background())
			return e
		}},
		{ep: epAcqStatistic, invoke: func(c *Client, b string) error {
			_, e := c.GetAcquisitionStatistic(context.Background(), map[string]any{"link_id": "L1"})
			return e
		}},
		{ep: epAcqChatInfo, invoke: func(c *Client, b string) error {
			_, e := c.GetAcquisitionChatInfo(context.Background(), "CK1")
			return e
		}},
		{ep: epMsgPushAdd, invoke: func(c *Client, b string) error {
			_, e := c.AddMsgTemplate(context.Background(), map[string]any{"chat_type": "single", "text": map[string]any{"content": "hi"}})
			return e
		}},
		{ep: epMsgPushRemind, invoke: func(c *Client, b string) error {
			_, e := c.RemindGroupmsgSend(context.Background(), "MSG1")
			return e
		}},
		{ep: epMsgPushCancel, invoke: func(c *Client, b string) error {
			_, e := c.CancelGroupmsgSend(context.Background(), "MSG1")
			return e
		}},
		{ep: epMsgPushList, invoke: func(c *Client, b string) error {
			_, e := c.GetGroupmsgList(context.Background(), map[string]any{"chat_type": "single"})
			return e
		}},
		{ep: epMsgPushTask, invoke: func(c *Client, b string) error {
			_, e := c.GetGroupmsgTask(context.Background(), map[string]any{"msgid": "MSG1"})
			return e
		}},
		{ep: epMsgPushResult, invoke: func(c *Client, b string) error {
			_, e := c.GetGroupmsgSendResult(context.Background(), map[string]any{"msgid": "MSG1", "userid": "u1"})
			return e
		}},
		{ep: epWelcomeSend, invoke: func(c *Client, b string) error {
			_, e := c.SendWelcomeMsg(context.Background(), map[string]any{"welcome_code": "WC1"})
			return e
		}},
		{ep: epWelcomeTplAdd, invoke: func(c *Client, b string) error {
			_, e := c.AddGroupWelcomeTemplate(context.Background(), map[string]any{"text": map[string]any{"content": "hi"}})
			return e
		}},
		{ep: epWelcomeTplEdit, invoke: func(c *Client, b string) error {
			_, e := c.EditGroupWelcomeTemplate(context.Background(), map[string]any{"template_id": "T1"})
			return e
		}},
		{ep: epWelcomeTplGet, invoke: func(c *Client, b string) error {
			_, e := c.GetGroupWelcomeTemplate(context.Background(), "T1")
			return e
		}},
		{ep: epWelcomeTplDel, invoke: func(c *Client, b string) error {
			_, e := c.DelGroupWelcomeTemplate(context.Background(), "T1", 1001)
			return e
		}},
		{ep: epStatsBehavior, invoke: func(c *Client, b string) error {
			_, e := c.GetUserBehaviorData(context.Background(), map[string]any{"userid": []string{"u1"}})
			return e
		}},
		{ep: epStatsGroupChat, invoke: func(c *Client, b string) error {
			_, e := c.GetGroupChatStatistic(context.Background(), map[string]any{"day_begin_time": 1, "day_end_time": 2})
			return e
		}},
		{ep: epStatsGroupChatD, invoke: func(c *Client, b string) error {
			_, e := c.GetGroupChatStatisticByDay(context.Background(), map[string]any{"day_begin_time": 1, "day_end_time": 2})
			return e
		}},
		{ep: epAlbumAdd, invoke: func(c *Client, b string) error {
			_, e := c.AddProductAlbum(context.Background(), map[string]any{"description": "d"})
			return e
		}},
		{ep: epAlbumGet, invoke: func(c *Client, b string) error {
			_, e := c.GetProductAlbum(context.Background(), "P1")
			return e
		}},
		{ep: epAlbumList, invoke: func(c *Client, b string) error {
			_, e := c.GetProductAlbumList(context.Background(), map[string]any{"limit": 100})
			return e
		}},
		{ep: epAlbumUpdate, invoke: func(c *Client, b string) error {
			_, e := c.UpdateProductAlbum(context.Background(), map[string]any{"product_id": "P1"})
			return e
		}},
		{ep: epAlbumDelete, invoke: func(c *Client, b string) error {
			_, e := c.DeleteProductAlbum(context.Background(), "P1")
			return e
		}},
		{ep: epInterceptAdd, invoke: func(c *Client, b string) error {
			_, e := c.AddInterceptRule(context.Background(), map[string]any{"rule_name": "r", "word_list": []string{"w"}})
			return e
		}},
		{ep: epInterceptList, invoke: func(c *Client, b string) error {
			_, e := c.GetInterceptRuleList(context.Background())
			return e
		}},
		{ep: epInterceptGet, invoke: func(c *Client, b string) error {
			_, e := c.GetInterceptRule(context.Background(), "R1")
			return e
		}},
		{ep: epInterceptEdit, invoke: func(c *Client, b string) error {
			_, e := c.UpdateInterceptRule(context.Background(), map[string]any{"rule_id": "R1"})
			return e
		}},
		{ep: epInterceptDel, invoke: func(c *Client, b string) error {
			_, e := c.DelInterceptRule(context.Background(), "R1")
			return e
		}},
		{ep: epUploadAttach, invoke: func(c *Client, b string) error {
			_, e := c.UploadAttachment(context.Background(), "image", 1, "a.jpg", strings.NewReader("DATA"))
			return e
		}},
		{ep: epServedContacts, invoke: func(c *Client, b string) error {
			_, e := c.GetServedContactList(context.Background(), map[string]any{"limit": 100})
			return e
		}},
		{ep: epKfUpgradeCfg, invoke: func(c *Client, b string) error {
			_, e := c.GetUpgradeServiceConfig(context.Background())
			return e
		}},
		{ep: epKfUpgrade, invoke: func(c *Client, b string) error {
			_, e := c.UpgradeService(context.Background(), map[string]any{"open_kfid": "KF1", "external_userid": "EXT1", "type": 1})
			return e
		}},
		{ep: epKfUpgradeCancel, invoke: func(c *Client, b string) error {
			_, e := c.CancelUpgradeService(context.Background(), "KF1", "EXT1")
			return e
		}},
		{ep: epKfAccountAdd, invoke: func(c *Client, b string) error {
			_, e := c.AddKfAccount(context.Background(), map[string]any{"name": "n", "media_id": "M1"})
			return e
		}},
		{ep: epKfAccountDel, invoke: func(c *Client, b string) error {
			_, e := c.DelKfAccount(context.Background(), "KF1")
			return e
		}},
		{ep: epKfAccountUpdate, invoke: func(c *Client, b string) error {
			_, e := c.UpdateKfAccount(context.Background(), map[string]any{"open_kfid": "KF1"})
			return e
		}},
		{ep: epKfAccountList, invoke: func(c *Client, b string) error {
			_, e := c.ListKfAccount(context.Background(), map[string]any{"offset": 0, "limit": 100})
			return e
		}},
		{ep: epKfContactWay, invoke: func(c *Client, b string) error {
			_, e := c.GetKfContactWay(context.Background(), "KF1", "S1")
			return e
		}},
		{ep: epKfServicerAdd, invoke: func(c *Client, b string) error {
			_, e := c.AddKfServicer(context.Background(), map[string]any{"open_kfid": "KF1", "userid_list": []string{"u1"}})
			return e
		}},
		{ep: epKfServicerDel, invoke: func(c *Client, b string) error {
			_, e := c.DelKfServicer(context.Background(), map[string]any{"open_kfid": "KF1", "userid_list": []string{"u1"}})
			return e
		}},
		{ep: epKfServicerList, invoke: func(c *Client, b string) error {
			_, e := c.ListKfServicer(context.Background(), "KF1")
			return e
		}},
		{ep: epKfStateGet, invoke: func(c *Client, b string) error {
			_, e := c.GetKfServiceState(context.Background(), map[string]any{"open_kfid": "KF1", "external_userid": "EXT1"})
			return e
		}},
		{ep: epKfStateTrans, invoke: func(c *Client, b string) error {
			_, e := c.TransKfServiceState(context.Background(), map[string]any{"open_kfid": "KF1", "external_userid": "EXT1", "service_state": 3})
			return e
		}},
		{ep: epKfSendMsg, invoke: func(c *Client, b string) error {
			_, e := c.SendKfMsg(context.Background(), map[string]any{"touser": "EXT1", "open_kfid": "KF1", "msgtype": "text", "text": map[string]any{"content": "hi"}})
			return e
		}},
		{ep: epKfSendMsgOnEvent, invoke: func(c *Client, b string) error {
			_, e := c.SendKfMsgOnEvent(context.Background(), map[string]any{"code": "C1", "msgtype": "text", "text": map[string]any{"content": "hi"}})
			return e
		}},
		{ep: epKfCustomerGet, invoke: func(c *Client, b string) error {
			_, e := c.BatchGetKfCustomer(context.Background(), map[string]any{"external_userid_list": []string{"EXT1"}})
			return e
		}},
		{ep: epKfCorpStat, invoke: func(c *Client, b string) error {
			_, e := c.GetKfCorpStatistic(context.Background(), map[string]any{"open_kfid": "KF1", "start_time": 1, "end_time": 2})
			return e
		}},
		{ep: epKfServicerStat, invoke: func(c *Client, b string) error {
			_, e := c.GetKfServicerStatistic(context.Background(), map[string]any{"open_kfid": "KF1", "start_time": 1, "end_time": 2})
			return e
		}},
		{ep: epKfGroupAdd, invoke: func(c *Client, b string) error {
			_, e := c.AddKfGroup(context.Background(), "g")
			return e
		}},
		{ep: epKfGroupDel, invoke: func(c *Client, b string) error {
			_, e := c.DelKfGroup(context.Background(), "G1")
			return e
		}},
		{ep: epKfGroupMod, invoke: func(c *Client, b string) error {
			_, e := c.ModKfGroup(context.Background(), map[string]any{"group_id": "G1", "name": "g"})
			return e
		}},
		{ep: epKfGroupList, invoke: func(c *Client, b string) error {
			_, e := c.ListKfGroup(context.Background(), map[string]any{"limit": 100})
			return e
		}},
		{ep: epKfIntentAdd, invoke: func(c *Client, b string) error {
			_, e := c.AddKfIntent(context.Background(), map[string]any{"group_id": "G1", "question": "q"})
			return e
		}},
		{ep: epKfIntentDel, invoke: func(c *Client, b string) error {
			_, e := c.DelKfIntent(context.Background(), "I1")
			return e
		}},
		{ep: epKfIntentMod, invoke: func(c *Client, b string) error {
			_, e := c.ModKfIntent(context.Background(), map[string]any{"intent_id": "I1", "question": "q"})
			return e
		}},
		{ep: epKfIntentList, invoke: func(c *Client, b string) error {
			_, e := c.ListKfIntent(context.Background(), map[string]any{"limit": 100})
			return e
		}},
		{ep: epCheckinCorpOption, invoke: func(c *Client, b string) error {
			_, e := c.GetCorpCheckinOption(context.Background())
			return e
		}},
		{ep: epCheckinOption, invoke: func(c *Client, b string) error {
			_, e := c.GetCheckinOption(context.Background(), map[string]any{"datetime": 1723963200, "useridlist": []string{"u1"}})
			return e
		}},
		{ep: epCheckinData, invoke: func(c *Client, b string) error {
			_, e := c.GetCheckinData(context.Background(), map[string]any{"opencheckindatatype": 3, "starttime": 1, "endtime": 2, "useridlist": []string{"u1"}})
			return e
		}},
		{ep: epCheckinDayData, invoke: func(c *Client, b string) error {
			_, e := c.GetCheckinDayData(context.Background(), map[string]any{"starttime": 1, "endtime": 2, "useridlist": []string{"u1"}})
			return e
		}},
		{ep: epCheckinMonthData, invoke: func(c *Client, b string) error {
			_, e := c.GetCheckinMonthData(context.Background(), map[string]any{"starttime": 1, "endtime": 2, "useridlist": []string{"u1"}})
			return e
		}},
		{ep: epCheckinScheduGet, invoke: func(c *Client, b string) error {
			_, e := c.GetCheckinScheduList(context.Background(), map[string]any{"starttime": 1, "endtime": 2, "useridlist": []string{"u1"}})
			return e
		}},
		{ep: epCheckinScheduSet, invoke: func(c *Client, b string) error {
			_, e := c.SetCheckinScheduList(context.Background(), map[string]any{"groupid": 1, "items": []any{}})
			return e
		}},
		{ep: epCheckinPunchFix, invoke: func(c *Client, b string) error {
			_, e := c.PunchCorrection(context.Background(), map[string]any{"userid": "u1", "schedule_date_time": 1, "checkin_time": 2})
			return e
		}},
		{ep: epCheckinAddRecord, invoke: func(c *Client, b string) error {
			_, e := c.AddCheckinRecord(context.Background(), map[string]any{"records": []any{}})
			return e
		}},
		{ep: epCheckinUserFace, invoke: func(c *Client, b string) error {
			_, e := c.AddCheckinUserFace(context.Background(), "u1", "FACE")
			return e
		}},
		{ep: epCheckinHardware, invoke: func(c *Client, b string) error {
			_, e := c.GetHardwareCheckinData(context.Background(), map[string]any{"starttime": 1, "endtime": 2, "useridlist": []string{"u1"}})
			return e
		}},
		{ep: epCheckinOptionAdd, invoke: func(c *Client, b string) error {
			_, e := c.AddCheckinOption(context.Background(), map[string]any{"group": map[string]any{}})
			return e
		}},
		{ep: epCheckinOptionUpd, invoke: func(c *Client, b string) error {
			_, e := c.UpdateCheckinOption(context.Background(), map[string]any{"group": map[string]any{"groupid": 1}})
			return e
		}},
		{ep: epCheckinOptionClear, invoke: func(c *Client, b string) error {
			_, e := c.ClearCheckinOptionArrayField(context.Background(), map[string]any{"groupid": 1, "clear_field": "f"})
			return e
		}},
		{ep: epCheckinOptionDel, invoke: func(c *Client, b string) error {
			_, e := c.DelCheckinOption(context.Background(), map[string]any{"groupid": 1})
			return e
		}},
		{ep: epApprovalTemplateGet, invoke: func(c *Client, b string) error {
			_, e := c.GetApprovalTemplateDetail(context.Background(), "T1")
			return e
		}},
		{ep: epApprovalApply, invoke: func(c *Client, b string) error {
			_, e := c.ApplyApprovalEvent(context.Background(), map[string]any{"creator_userid": "u1", "template_id": "T1"})
			return e
		}},
		{ep: epApprovalInfo, invoke: func(c *Client, b string) error {
			_, e := c.GetApprovalInfo(context.Background(), map[string]any{"starttime": 1, "endtime": 2, "size": 100})
			return e
		}},
		{ep: epApprovalDetail, invoke: func(c *Client, b string) error {
			_, e := c.GetApprovalDetail(context.Background(), "SP1")
			return e
		}},
		{ep: epApprovalOldData, invoke: func(c *Client, b string) error {
			_, e := c.GetCorpApprovalData(context.Background(), map[string]any{"starttime": 1, "endtime": 2})
			return e
		}},
		{ep: epVacationCorpConf, invoke: func(c *Client, b string) error {
			_, e := c.GetVacationCorpConf(context.Background())
			return e
		}},
		{ep: epVacationQuotaGet, invoke: func(c *Client, b string) error {
			_, e := c.GetUserVacationQuota(context.Background(), "u1")
			return e
		}},
		{ep: epVacationQuotaSet, invoke: func(c *Client, b string) error {
			_, e := c.SetOneUserVacationQuota(context.Background(), map[string]any{"userid": "u1", "vacation_id": 1, "leftduration": 8, "time_attr": 0})
			return e
		}},
		{ep: epOpenApprovalData, invoke: func(c *Client, b string) error {
			_, e := c.GetOpenApprovalData(context.Background(), "NO1")
			return e
		}},
		{ep: epApprovalTplCreate, invoke: func(c *Client, b string) error {
			_, e := c.CreateApprovalTemplate(context.Background(), map[string]any{"template_name": "n"})
			return e
		}},
		{ep: epApprovalTplUpdate, invoke: func(c *Client, b string) error {
			_, e := c.UpdateApprovalTemplate(context.Background(), map[string]any{"template_id": "T1"})
			return e
		}},
		{ep: epCalendarAdd, invoke: func(c *Client, b string) error {
			_, e := c.AddCalendar(context.Background(), map[string]any{"calendar": map[string]any{"summary": "s"}})
			return e
		}},
		{ep: epCalendarUpdate, invoke: func(c *Client, b string) error {
			_, e := c.UpdateCalendar(context.Background(), map[string]any{"calendar": map[string]any{"cal_id": "C1"}})
			return e
		}},
		{ep: epCalendarGet, invoke: func(c *Client, b string) error {
			_, e := c.GetCalendar(context.Background(), []string{"C1"})
			return e
		}},
		{ep: epCalendarDel, invoke: func(c *Client, b string) error {
			_, e := c.DelCalendar(context.Background(), "C1")
			return e
		}},
		{ep: epScheduleAdd, invoke: func(c *Client, b string) error {
			_, e := c.AddSchedule(context.Background(), map[string]any{"schedule": map[string]any{"start_time": 1, "end_time": 2}})
			return e
		}},
		{ep: epScheduleUpdate, invoke: func(c *Client, b string) error {
			_, e := c.UpdateSchedule(context.Background(), map[string]any{"schedule": map[string]any{"schedule_id": "S1"}})
			return e
		}},
		{ep: epScheduleAddAttd, invoke: func(c *Client, b string) error {
			_, e := c.AddScheduleAttendees(context.Background(), "S1", []map[string]any{{"userid": "u1"}})
			return e
		}},
		{ep: epScheduleDelAttd, invoke: func(c *Client, b string) error {
			_, e := c.DelScheduleAttendees(context.Background(), "S1", []map[string]any{{"userid": "u1"}})
			return e
		}},
		{ep: epScheduleListByCal, invoke: func(c *Client, b string) error {
			_, e := c.ListScheduleByCalendar(context.Background(), map[string]any{"cal_id": "C1", "offset": 0, "limit": 100})
			return e
		}},
		{ep: epScheduleGet, invoke: func(c *Client, b string) error {
			_, e := c.GetSchedule(context.Background(), []string{"S1"})
			return e
		}},
		{ep: epScheduleDel, invoke: func(c *Client, b string) error {
			_, e := c.CancelSchedule(context.Background(), "S1")
			return e
		}},
		{ep: epMeetingCreate, invoke: func(c *Client, b string) error {
			_, e := c.CreateMeeting(context.Background(), map[string]any{"admin_userid": "u1", "title": "t", "meeting_start": 1723963200, "meeting_duration": 3600})
			return e
		}},
		{ep: epMeetingUpdate, invoke: func(c *Client, b string) error {
			_, e := c.UpdateMeeting(context.Background(), map[string]any{"meetingid": "M1", "title": "t2"})
			return e
		}},
		{ep: epMeetingCancel, invoke: func(c *Client, b string) error {
			_, e := c.CancelMeeting(context.Background(), "M1")
			return e
		}},
		{ep: epMeetingUserID, invoke: func(c *Client, b string) error {
			_, e := c.GetUserMeetingIDList(context.Background(), map[string]any{"userid": "u1"})
			return e
		}},
		{ep: epMeetingInfo, invoke: func(c *Client, b string) error {
			_, e := c.GetMeetingInfo(context.Background(), "M1")
			return e
		}},
		{ep: epMtgGetInvitees, invoke: func(c *Client, b string) error {
			_, e := c.GetMeetingInvitees(context.Background(), "M1")
			return e
		}},
		{ep: epMtgSetInvitees, invoke: func(c *Client, b string) error {
			_, e := c.SetMeetingInvitees(context.Background(), "M1", []map[string]any{{"userid": "u1"}})
			return e
		}},
		{ep: epMtgCreateShortURL, invoke: func(c *Client, b string) error {
			_, e := c.CreateCustomerShortURL(context.Background(), "M1", map[string]any{"userid": "u1"})
			return e
		}},
		{ep: epMtgGetShortURL, invoke: func(c *Client, b string) error {
			_, e := c.GetCustomerShortURL(context.Background(), "M1")
			return e
		}},
		{ep: epMtgRealtimeAtt, invoke: func(c *Client, b string) error {
			_, e := c.GetRealtimeAttendeeList(context.Background(), map[string]any{"meetingid": "M1"})
			return e
		}},
		{ep: epMtgAttendeeList, invoke: func(c *Client, b string) error {
			_, e := c.GetAttendeeList(context.Background(), map[string]any{"meetingid": "M1"})
			return e
		}},
		{ep: epMtgWaitCurrent, invoke: func(c *Client, b string) error {
			_, e := c.GetWaitingroomCurrentUserList(context.Background(), map[string]any{"meetingid": "M1"})
			return e
		}},
		{ep: epMtgWaitRecord, invoke: func(c *Client, b string) error {
			_, e := c.GetWaitingroomUserList(context.Background(), map[string]any{"meetingid": "M1"})
			return e
		}},
		{ep: epMtgDeviceCheck, invoke: func(c *Client, b string) error {
			_, e := c.CheckDeviceInMeeting(context.Background(), map[string]any{"userid": "u1", "meetingid_list": []string{"M1"}})
			return e
		}},
		{ep: epMtgGetGuests, invoke: func(c *Client, b string) error {
			_, e := c.GetMeetingGuests(context.Background(), "M1")
			return e
		}},
		{ep: epMtgSetGuests, invoke: func(c *Client, b string) error {
			_, e := c.SetMeetingGuests(context.Background(), "M1", []map[string]any{})
			return e
		}},
		{ep: epMtgQuality, invoke: func(c *Client, b string) error {
			_, e := c.GetMeetingQuality(context.Background(), map[string]any{"meetingid": "M1"})
			return e
		}},
		{ep: epMtgEnrollSetCfg, invoke: func(c *Client, b string) error {
			_, e := c.SetMeetingEnrollConfig(context.Background(), map[string]any{"meetingid": "M1"})
			return e
		}},
		{ep: epMtgEnrollGetCfg, invoke: func(c *Client, b string) error {
			_, e := c.GetMeetingEnrollConfig(context.Background(), "M1")
			return e
		}},
		{ep: epMtgEnrollQueryID, invoke: func(c *Client, b string) error {
			_, e := c.QueryMeetingEnrollByTmpOpenid(context.Background(), map[string]any{"meetingid": "M1", "tmp_openid_list": []string{"O1"}})
			return e
		}},
		{ep: epMtgEnrollList, invoke: func(c *Client, b string) error {
			_, e := c.ListMeetingEnroll(context.Background(), map[string]any{"meetingid": "M1"})
			return e
		}},
		{ep: epMtgEnrollApprove, invoke: func(c *Client, b string) error {
			_, e := c.ApproveMeetingEnroll(context.Background(), map[string]any{"meetingid": "M1", "action": 1, "enroll_id_list": []string{"E1"}})
			return e
		}},
		{ep: epMtgEnrollImport, invoke: func(c *Client, b string) error {
			_, e := c.ImportMeetingEnroll(context.Background(), map[string]any{"meetingid": "M1", "enroll_list": []any{}})
			return e
		}},
		{ep: epMtgEnrollDelete, invoke: func(c *Client, b string) error {
			_, e := c.DeleteMeetingEnroll(context.Background(), map[string]any{"meetingid": "M1", "enroll_id_list": []string{"E1"}})
			return e
		}},
		{ep: epRCSet, invoke: func(c *Client, b string) error {
			_, e := c.SetMeetingRealControl(context.Background(), map[string]any{"meetingid": "M1", "mute_all": true})
			return e
		}},
		{ep: epRCSetCohost, invoke: func(c *Client, b string) error {
			_, e := c.SetMeetingCohost(context.Background(), map[string]any{"meetingid": "M1", "action": 1, "operated_user": map[string]any{"tmp_openid": "O1"}})
			return e
		}},
		{ep: epRCMuteUser, invoke: func(c *Client, b string) error {
			_, e := c.MuteMeetingUser(context.Background(), map[string]any{"meetingid": "M1", "option": 1, "operated_user": map[string]any{"tmp_openid": "O1"}})
			return e
		}},
		{ep: epRCVideo, invoke: func(c *Client, b string) error {
			_, e := c.SwitchMeetingUserVideo(context.Background(), map[string]any{"meetingid": "M1", "video": 1, "operated_user": map[string]any{"tmp_openid": "O1"}})
			return e
		}},
		{ep: epRCWaitRoom, invoke: func(c *Client, b string) error {
			_, e := c.ManageWaitingroomUsers(context.Background(), map[string]any{"meetingid": "M1", "operate_type": 1})
			return e
		}},
		{ep: epRCKickout, invoke: func(c *Client, b string) error {
			_, e := c.KickoutMeetingUsers(context.Background(), map[string]any{"meetingid": "M1", "operated_users": []any{}})
			return e
		}},
		{ep: epRCDismiss, invoke: func(c *Client, b string) error {
			_, e := c.DismissMeeting(context.Background(), map[string]any{"meetingid": "M1", "force_dismiss": 1})
			return e
		}},
		{ep: epRCCloseShare, invoke: func(c *Client, b string) error {
			_, e := c.CloseMeetingScreenShare(context.Background(), map[string]any{"meetingid": "M1", "operated_user": map[string]any{"tmp_openid": "T1", "instance_id": 1}})
			return e
		}},
		{ep: epRCSetNickname, invoke: func(c *Client, b string) error {
			_, e := c.SetMeetingNicknames(context.Background(), map[string]any{"meetingid": "M1", "operated_users": []any{map[string]any{"tmp_openid": "T1", "instance_id": 1, "nickname": "n"}}})
			return e
		}},
		{ep: epPollCreateTheme, invoke: func(c *Client, b string) error {
			_, e := c.CreateMeetingPollTheme(context.Background(), map[string]any{"operator_userid": "o1", "instance_id": 1, "meetingid": "M1", "poll_topic": "t", "poll_desc": "d", "poll_questions": []any{}})
			return e
		}},
		{ep: epPollUpdateTheme, invoke: func(c *Client, b string) error {
			_, e := c.UpdateMeetingPollTheme(context.Background(), map[string]any{"operator_userid": "o1", "instance_id": 1, "meetingid": "M1", "poll_theme_id": "PT1", "poll_topic": "t", "poll_desc": "d", "poll_questions": []any{}})
			return e
		}},
		{ep: epPollGetList, invoke: func(c *Client, b string) error {
			_, e := c.ListMeetingPolls(context.Background(), map[string]any{"operator_userid": "o1", "instance_id": 1, "meetingid": "M1"})
			return e
		}},
		{ep: epPollGetThemeInfo, invoke: func(c *Client, b string) error {
			_, e := c.GetMeetingPollTheme(context.Background(), map[string]any{"operator_userid": "o1", "instance_id": 1, "poll_theme_id": "PT1"})
			return e
		}},
		{ep: epPollGetDetail, invoke: func(c *Client, b string) error {
			_, e := c.GetMeetingPollDetail(context.Background(), map[string]any{"operator_userid": "o1", "instance_id": 1, "meetingid": "M1", "poll_id": "P1"})
			return e
		}},
		{ep: epPollDelete, invoke: func(c *Client, b string) error {
			_, e := c.DeleteMeetingPoll(context.Background(), map[string]any{"operator_userid": "o1", "instance_id": 1, "meetingid": "M1", "poll_id": "P1"})
			return e
		}},
		{ep: epPollStart, invoke: func(c *Client, b string) error {
			_, e := c.StartMeetingPoll(context.Background(), map[string]any{"operator_userid": "o1", "instance_id": 1, "meetingid": "M1", "poll_theme_id": "PT1"})
			return e
		}},
		{ep: epPollFinish, invoke: func(c *Client, b string) error {
			_, e := c.FinishMeetingPoll(context.Background(), map[string]any{"operator_userid": "o1", "instance_id": 1, "meetingid": "M1", "poll_theme_id": "PT1", "poll_id": "P1"})
			return e
		}},
		{ep: epMtgLayoutListTpl, invoke: func(c *Client, b string) error {
			_, e := c.ListMeetingLayoutTemplates(context.Background())
			return e
		}},
		{ep: epMtgLayoutAdd, invoke: func(c *Client, b string) error {
			_, e := c.AddMeetingLayout(context.Background(), map[string]any{"meetingid": "M1", "layout_list": []any{}})
			return e
		}},
		{ep: epMtgLayoutUpdate, invoke: func(c *Client, b string) error {
			_, e := c.UpdateMeetingLayout(context.Background(), map[string]any{"meetingid": "M1", "layout_id": "L1", "page_list": []any{}})
			return e
		}},
		{ep: epMtgLayoutSetDefault, invoke: func(c *Client, b string) error {
			_, e := c.SetMeetingDefaultLayout(context.Background(), map[string]any{"meetingid": "M1", "selected_layout_id": "L1"})
			return e
		}},
		{ep: epAdvLayoutAdd, invoke: func(c *Client, b string) error {
			_, e := c.AddMeetingAdvancedLayout(context.Background(), map[string]any{"meetingid": "M1", "layout_list": []any{}})
			return e
		}},
		{ep: epAdvLayoutUpdate, invoke: func(c *Client, b string) error {
			_, e := c.UpdateMeetingAdvancedLayout(context.Background(), map[string]any{"meetingid": "M1", "layout_id": "L1", "page_list": []any{}})
			return e
		}},
		{ep: epAdvLayoutApply, invoke: func(c *Client, b string) error {
			_, e := c.ApplyMeetingAdvancedLayout(context.Background(), map[string]any{"meetingid": "M1", "layout_id": "L1"})
			return e
		}},
		{ep: epAdvLayoutList, invoke: func(c *Client, b string) error {
			_, e := c.ListMeetingAdvancedLayouts(context.Background(), map[string]any{"meetingid": "M1"})
			return e
		}},
		{ep: epAdvLayoutUserGet, invoke: func(c *Client, b string) error {
			_, e := c.GetMeetingUserLayout(context.Background(), map[string]any{"meetingid": "M1", "tmp_openid": "T1", "instance_id": 1})
			return e
		}},
		{ep: epAdvLayoutBatchDel, invoke: func(c *Client, b string) error {
			_, e := c.BatchDeleteMeetingLayouts(context.Background(), map[string]any{"meetingid": "M1", "layout_id_list": []string{"L1"}})
			return e
		}},
		{ep: epMtgBgAdd, invoke: func(c *Client, b string) error {
			_, e := c.AddMeetingBackground(context.Background(), map[string]any{"meetingid": "M1", "image_list": []any{}})
			return e
		}},
		{ep: epMtgBgSetDefault, invoke: func(c *Client, b string) error {
			_, e := c.SetMeetingDefaultBackground(context.Background(), map[string]any{"meetingid": "M1", "selected_background_id": "BG1"})
			return e
		}},
		{ep: epMtgBgList, invoke: func(c *Client, b string) error {
			_, e := c.ListMeetingBackgrounds(context.Background(), map[string]any{"meetingid": "M1"})
			return e
		}},
		{ep: epMtgBgDelete, invoke: func(c *Client, b string) error {
			_, e := c.DeleteMeetingBackground(context.Background(), map[string]any{"meetingid": "M1", "background_id": "BG1"})
			return e
		}},
		{ep: epMtgBgBatchDelete, invoke: func(c *Client, b string) error {
			_, e := c.BatchDeleteMeetingBackgrounds(context.Background(), map[string]any{"meetingid": "M1", "background_id_list": []string{"BG1"}})
			return e
		}},
		{ep: epMtgRecList, invoke: func(c *Client, b string) error {
			_, e := c.ListMeetingRecords(context.Background(), map[string]any{"start_time": 1, "end_time": 2})
			return e
		}},
		{ep: epMtgRecStats, invoke: func(c *Client, b string) error {
			_, e := c.GetMeetingRecordStatistics(context.Background(), map[string]any{"meeting_record_id": "MR1"})
			return e
		}},
		{ep: epMtgRecShareUpd, invoke: func(c *Client, b string) error {
			_, e := c.UpdateMeetingRecordSharingConfig(context.Background(), map[string]any{"meeting_record_id": "MR1", "meetingid": "M1"})
			return e
		}},
		{ep: epMtgRecDelete, invoke: func(c *Client, b string) error {
			_, e := c.DeleteMeetingRecord(context.Background(), map[string]any{"meeting_record_id": "MR1", "meetingid": "M1"})
			return e
		}},
		{ep: epMtgRecFileDelete, invoke: func(c *Client, b string) error {
			_, e := c.DeleteMeetingRecordFile(context.Background(), map[string]any{"meetingid": "M1", "record_file_id": "RF1"})
			return e
		}},
		{ep: epMtgRecFileGet, invoke: func(c *Client, b string) error {
			_, e := c.GetMeetingRecordFile(context.Background(), map[string]any{"record_file_id": "RF1", "meetingid": "M1"})
			return e
		}},
		{ep: epMtgRecFileList, invoke: func(c *Client, b string) error {
			_, e := c.ListMeetingRecordFiles(context.Background(), map[string]any{"meeting_record_id": "MR1", "meetingid": "M1"})
			return e
		}},
		{ep: epMtgTsParagraphs, invoke: func(c *Client, b string) error {
			_, e := c.ListMeetingTranscriptParagraphs(context.Background(), map[string]any{"record_file_id": "RF1", "meetingid": "M1"})
			return e
		}},
		{ep: epMtgTsDetail, invoke: func(c *Client, b string) error {
			_, e := c.GetMeetingTranscriptDetail(context.Background(), map[string]any{"record_file_id": "RF1", "meetingid": "M1"})
			return e
		}},
		{ep: epMtgTsSearch, invoke: func(c *Client, b string) error {
			_, e := c.SearchMeetingTranscript(context.Background(), map[string]any{"record_file_id": "RF1", "meetingid": "M1", "text": "kw"})
			return e
		}},
		{ep: epMtgVipAddJob, invoke: func(c *Client, b string) error {
			_, e := c.SubmitMeetingVIPAddJob(context.Background(), map[string]any{"userid_list": []string{"u1"}})
			return e
		}},
		{ep: epMtgVipAddResult, invoke: func(c *Client, b string) error {
			_, e := c.GetMeetingVIPAddJobResult(context.Background(), map[string]any{"jobid": "J1"})
			return e
		}},
		{ep: epMtgVipDelJob, invoke: func(c *Client, b string) error {
			_, e := c.SubmitMeetingVIPDelJob(context.Background(), map[string]any{"userid_list": []string{"u1"}})
			return e
		}},
		{ep: epMtgVipDelResult, invoke: func(c *Client, b string) error {
			_, e := c.GetMeetingVIPDelJobResult(context.Background(), map[string]any{"jobid": "J1"})
			return e
		}},
		{ep: epMtgVipList, invoke: func(c *Client, b string) error {
			_, e := c.ListMeetingVIPAccounts(context.Background(), map[string]any{})
			return e
		}},
		{ep: epWbCreate, invoke: func(c *Client, b string) error {
			_, e := c.CreateWebinar(context.Background(), map[string]any{"admin_userid": "u1", "title": "t", "start_time": "1724000000", "end_time": "1724003600", "admission_type": 0, "playback_for_audience": false})
			return e
		}},
		{ep: epWbUpdate, invoke: func(c *Client, b string) error {
			_, e := c.UpdateWebinar(context.Background(), map[string]any{"meetingid": "M1", "title": "t", "start_time": "1724000000", "end_time": "1724003600", "admission_type": 0, "playback_for_audience": false})
			return e
		}},
		{ep: epWbCancel, invoke: func(c *Client, b string) error {
			_, e := c.CancelWebinar(context.Background(), "M1")
			return e
		}},
		{ep: epWbGet, invoke: func(c *Client, b string) error {
			_, e := c.GetWebinar(context.Background(), map[string]any{"meetingid": "M1"})
			return e
		}},
		{ep: epWbGuestList, invoke: func(c *Client, b string) error {
			_, e := c.ListWebinarGuests(context.Background(), map[string]any{"meetingid": "M1"})
			return e
		}},
		{ep: epWbGuestUpdate, invoke: func(c *Client, b string) error {
			_, e := c.UpdateWebinarGuestList(context.Background(), map[string]any{"meetingid": "M1", "guests": []any{}})
			return e
		}},
		{ep: epWbWarmUp, invoke: func(c *Client, b string) error {
			_, e := c.UpdateWebinarWarmUp(context.Background(), map[string]any{"meetingid": "M1", "warm_up_picture": "https://x/p.png"})
			return e
		}},
		{ep: epWbEnrollSet, invoke: func(c *Client, b string) error {
			_, e := c.SetWebinarEnrollConfig(context.Background(), map[string]any{"meetingid": "M1", "approve_type": 1})
			return e
		}},
		{ep: epWbEnrollGet, invoke: func(c *Client, b string) error {
			_, e := c.GetWebinarEnrollConfig(context.Background(), "M1")
			return e
		}},
		{ep: epWbEnrollQuery, invoke: func(c *Client, b string) error {
			_, e := c.QueryWebinarEnrollIDByTmpOpenid(context.Background(), map[string]any{"meetingid": "M1", "tmp_openid_list": []string{"T1"}})
			return e
		}},
		{ep: epWbEnrollList, invoke: func(c *Client, b string) error {
			_, e := c.ListWebinarEnrolls(context.Background(), map[string]any{"meetingid": "M1"})
			return e
		}},
		{ep: epWbEnrollAppr, invoke: func(c *Client, b string) error {
			_, e := c.ApproveWebinarEnrolls(context.Background(), map[string]any{"meetingid": "M1", "enroll_id_list": []string{"ER1"}, "action": 3})
			return e
		}},
		{ep: epWbEnrollImp, invoke: func(c *Client, b string) error {
			_, e := c.ImportWebinarEnrolls(context.Background(), map[string]any{"meetingid": "M1", "enroll_list": []any{}})
			return e
		}},
		{ep: epWbEnrollDel, invoke: func(c *Client, b string) error {
			_, e := c.DeleteWebinarEnrolls(context.Background(), map[string]any{"meetingid": "M1", "enroll_id_list": []any{map[string]any{"enroll_id": "ER1"}}})
			return e
		}},
		{ep: epPhoneCallout, invoke: func(c *Client, b string) error {
			_, e := c.PhoneCallout(context.Background(), map[string]any{"meetingid": "M1", "phone_numbers": []any{map[string]any{"area": 86, "phone": "10800000000"}}})
			return e
		}},
		{ep: epPhoneStatus, invoke: func(c *Client, b string) error {
			_, e := c.GetPhoneCalloutStatus(context.Background(), map[string]any{"meetingid": "M1"})
			return e
		}},
		{ep: epPhoneTmpOID, invoke: func(c *Client, b string) error {
			_, e := c.GetPhoneTmpOpenid(context.Background(), map[string]any{"meetingid": "M1", "phone_numbers": []any{map[string]any{"area": 86, "phone": "10800000000"}}})
			return e
		}},
		{ep: epRoomsBook, invoke: func(c *Client, b string) error {
			_, e := c.BookMeetingRooms(context.Background(), map[string]any{"meetingid": "M1", "meeting_room_id_list": []string{"RM1"}})
			return e
		}},
		{ep: epRoomsRelease, invoke: func(c *Client, b string) error {
			_, e := c.ReleaseMeetingRooms(context.Background(), map[string]any{"meetingid": "M1", "meeting_room_id_list": []string{"RM1"}})
			return e
		}},
		{ep: epRoomsList, invoke: func(c *Client, b string) error {
			_, e := c.ListMeetingRooms(context.Background(), map[string]any{})
			return e
		}},
		{ep: epRoomsGetInfo, invoke: func(c *Client, b string) error {
			_, e := c.GetMeetingRoomInfo(context.Background(), "RM1")
			return e
		}},
		{ep: epRoomsGetConfig, invoke: func(c *Client, b string) error {
			_, e := c.GetMeetingRoomConfig(context.Background(), "RM1")
			return e
		}},
		{ep: epRoomsListMtgs, invoke: func(c *Client, b string) error {
			_, e := c.ListMeetingRoomMeetings(context.Background(), map[string]any{"meeting_room_id": "RM1"})
			return e
		}},
		{ep: epRoomsListDev, invoke: func(c *Client, b string) error {
			_, e := c.ListMeetingRoomDevices(context.Background(), map[string]any{})
			return e
		}},
		{ep: epRoomsListCtrl, invoke: func(c *Client, b string) error {
			_, e := c.ListMeetingRoomControllers(context.Background(), map[string]any{})
			return e
		}},
		{ep: epRoomsInventory, invoke: func(c *Client, b string) error {
			_, e := c.GetMeetingRoomInventory(context.Background())
			return e
		}},
		{ep: epRoomsCall, invoke: func(c *Client, b string) error {
			_, e := c.CallMeetingRoom(context.Background(), map[string]any{"meetingid": "M1", "meeting_room_id": "RM1"})
			return e
		}},
		{ep: epRoomsCancelCall, invoke: func(c *Client, b string) error {
			_, e := c.CancelCallMeetingRoom(context.Background(), map[string]any{"meetingid": "M1", "invite_id": "IV1", "meeting_room_id": "RM1"})
			return e
		}},
		{ep: epRoomsRespStatus, invoke: func(c *Client, b string) error {
			_, e := c.GetMeetingRoomResponseStatus(context.Background(), map[string]any{"meetingid": "M1", "meeting_room_id": "RM1"})
			return e
		}},
		{ep: epMRAStatus, invoke: func(c *Client, b string) error {
			_, e := c.QueryMRAStatus(context.Background(), map[string]any{"meetingid": "M1", "tmp_openid": "T1"})
			return e
		}},
		{ep: epMRALayout, invoke: func(c *Client, b string) error {
			_, e := c.SetMRADefaultLayout(context.Background(), map[string]any{"meetingid": "M1", "default_layout": 1, "default_novideo_user": 1, "mra": map[string]any{"tmp_openid": "T1"}})
			return e
		}},
		{ep: epMRARaiseHand, invoke: func(c *Client, b string) error {
			_, e := c.SetMRARaiseHand(context.Background(), map[string]any{"meetingid": "M1", "raise_hand": true, "mra": map[string]any{"tmp_openid": "T1"}})
			return e
		}},
		{ep: epMRAHangup, invoke: func(c *Client, b string) error {
			_, e := c.HangupMRA(context.Background(), map[string]any{"meetingid": "M1", "mra": map[string]any{"tmp_openid": "T1"}})
			return e
		}},
		{ep: epEmailActAccount, invoke: func(c *Client, b string) error {
			_, e := c.ActivateEmailAccount(context.Background(), map[string]any{"userid": "u1", "type": 1})
			return e
		}},
		{ep: epEmailNewCount, invoke: func(c *Client, b string) error {
			_, e := c.GetEmailNewCount(context.Background(), "u1")
			return e
		}},
		{ep: epMailSend, invoke: func(c *Client, b string) error {
			_, e := c.SendMail(context.Background(), map[string]any{"to": map[string]any{"userids": []string{"u1"}}, "subject": "s", "content": "c"})
			return e
		}},
		{ep: epMailSendSched, invoke: func(c *Client, b string) error {
			_, e := c.SendScheduleMail(context.Background(), map[string]any{"to": map[string]any{"userids": []string{"u1"}}, "subject": "s", "content": "c", "schedule": map[string]any{"start_time": 1, "end_time": 2}})
			return e
		}},
		{ep: epMailSendMeeting, invoke: func(c *Client, b string) error {
			_, e := c.SendMeetingMail(context.Background(), map[string]any{"to": map[string]any{"userids": []string{"u1"}}, "subject": "s", "content": "c", "schedule": map[string]any{"start_time": 1, "end_time": 2}, "meeting": map[string]any{"meeting_admins": map[string]any{"userid": "u1"}}})
			return e
		}},
		{ep: epMailList, invoke: func(c *Client, b string) error {
			_, e := c.GetMailList(context.Background(), map[string]any{"begin_time": 1, "end_time": 2})
			return e
		}},
		{ep: epMailRead, invoke: func(c *Client, b string) error {
			_, e := c.ReadMail(context.Background(), "mail1")
			return e
		}},
		{ep: epEmailAliasUpd, invoke: func(c *Client, b string) error {
			_, e := c.UpdateEmailAlias(context.Background(), "app@corp.test")
			return e
		}},
		{ep: epEmailAliasGet, invoke: func(c *Client, b string) error {
			_, e := c.GetEmailAlias(context.Background())
			return e
		}},
		{ep: epMailGroupCreate, invoke: func(c *Client, b string) error {
			_, e := c.CreateMailGroup(context.Background(), map[string]any{"groupid": "g1@corp.test", "groupname": "g", "email_list": []string{"u1@corp.test"}})
			return e
		}},
		{ep: epMailGroupUpdate, invoke: func(c *Client, b string) error {
			_, e := c.UpdateMailGroup(context.Background(), map[string]any{"groupid": "g1@corp.test", "groupname": "g2"})
			return e
		}},
		{ep: epMailGroupDelete, invoke: func(c *Client, b string) error {
			_, e := c.DeleteMailGroup(context.Background(), "g1@corp.test")
			return e
		}},
		{ep: epMailGroupGet, invoke: func(c *Client, b string) error {
			_, e := c.GetMailGroup(context.Background(), "g1@corp.test")
			return e
		}},
		{ep: epMailGroupSearch, invoke: func(c *Client, b string) error {
			_, e := c.SearchMailGroup(context.Background(), 1, "g1@corp.test")
			return e
		}},
		{ep: epPubMailCreate, invoke: func(c *Client, b string) error {
			_, e := c.CreatePublicMail(context.Background(), map[string]any{"email": "p1@corp.test", "name": "p", "userid_list": []string{"u1"}})
			return e
		}},
		{ep: epPubMailUpdate, invoke: func(c *Client, b string) error {
			_, e := c.UpdatePublicMail(context.Background(), map[string]any{"id": 1, "name": "p2"})
			return e
		}},
		{ep: epPubMailDelete, invoke: func(c *Client, b string) error {
			_, e := c.DeletePublicMail(context.Background(), 1)
			return e
		}},
		{ep: epPubMailGet, invoke: func(c *Client, b string) error {
			_, e := c.GetPublicMail(context.Background(), []uint32{1})
			return e
		}},
		{ep: epPubMailSearch, invoke: func(c *Client, b string) error {
			_, e := c.SearchPublicMail(context.Background(), 1, "p1@corp.test")
			return e
		}},
		{ep: epPubMailAuthList, invoke: func(c *Client, b string) error {
			_, e := c.ListPublicMailAuthCodes(context.Background(), 1)
			return e
		}},
		{ep: epPubMailAuthDel, invoke: func(c *Client, b string) error {
			_, e := c.DeletePublicMailAuthCode(context.Background(), 1, 2)
			return e
		}},
		{ep: epVipAdd, invoke: func(c *Client, b string) error {
			_, e := c.AddVIPAccounts(context.Background(), []string{"u1"})
			return e
		}},
		{ep: epVipDel, invoke: func(c *Client, b string) error {
			_, e := c.RemoveVIPAccounts(context.Background(), []string{"u1"})
			return e
		}},
		{ep: epVipList, invoke: func(c *Client, b string) error {
			_, e := c.ListVIPAccounts(context.Background(), "", 100)
			return e
		}},
		{ep: epUserOptGet, invoke: func(c *Client, b string) error {
			_, e := c.GetUserOptions(context.Background(), "u1", []int{1, 2})
			return e
		}},
		{ep: epUserOptUpdate, invoke: func(c *Client, b string) error {
			_, e := c.UpdateUserOptions(context.Background(), "u1", []map[string]any{{"type": 2, "value": "1"}})
			return e
		}},
		{ep: epMiscAPIDomainIP, invoke: func(c *Client, b string) error { _, e := c.GetAPIDomainIPs(context.Background()); return e }},
		{ep: epMiscCallbackIP, invoke: func(c *Client, b string) error { _, e := c.GetCallbackIPs(context.Background()); return e }},
		{ep: epMiscServerDomain, invoke: func(c *Client, b string) error { _, e := c.GetServerDomainIPs(context.Background()); return e }},
		{ep: epMiscConvertTmpID, invoke: func(c *Client, b string) error {
			_, e := c.ConvertTmpExternalUserid(context.Background(), map[string]any{"business_type": 1, "user_type": 1, "tmp_external_userid_list": []string{"T1"}})
			return e
		}},
		{ep: epMiscMemberLog, invoke: func(c *Client, b string) error {
			_, e := c.ListMemberOperLogs(context.Background(), map[string]any{"start_time": 1723900000, "end_time": 1724000000})
			return e
		}},
		{ep: epMiscAdminLog, invoke: func(c *Client, b string) error {
			_, e := c.ListAdminOperLogs(context.Background(), map[string]any{"start_time": 1723900000, "end_time": 1724000000})
			return e
		}},
		{ep: epSecScreen, invoke: func(c *Client, b string) error {
			_, e := c.GetScreenOperRecords(context.Background(), map[string]any{"start_time": 1723900000, "end_time": 1724000000})
			return e
		}},
		{ep: epSecFile, invoke: func(c *Client, b string) error {
			_, e := c.GetFileOperRecords(context.Background(), map[string]any{"start_time": 1723900000, "end_time": 1724000000})
			return e
		}},
		{ep: epSecDevImport, invoke: func(c *Client, b string) error {
			_, e := c.ImportTrustDevices(context.Background(), map[string]any{"device_list": []any{}})
			return e
		}},
		{ep: epSecDevList, invoke: func(c *Client, b string) error {
			_, e := c.ListTrustDevices(context.Background(), map[string]any{"type": 1})
			return e
		}},
		{ep: epSecDevByUser, invoke: func(c *Client, b string) error {
			_, e := c.GetUserTrustDevices(context.Background(), map[string]any{"last_login_userid": "u1", "type": 1})
			return e
		}},
		{ep: epSecDevDelete, invoke: func(c *Client, b string) error {
			_, e := c.DeleteTrustDevices(context.Background(), map[string]any{"type": 1, "device_code_list": []string{"DC1"}})
			return e
		}},
		{ep: epSecDevApprove, invoke: func(c *Client, b string) error {
			_, e := c.ApproveTrustDevices(context.Background(), map[string]any{"device_code_list": []string{"DC1"}})
			return e
		}},
		{ep: epSecDevReject, invoke: func(c *Client, b string) error {
			_, e := c.RejectTrustDevices(context.Background(), map[string]any{"device_code_list": []string{"DC1"}})
			return e
		}},
		{ep: epSecVipAddJob, invoke: func(c *Client, b string) error {
			_, e := c.SubmitSecVIPAddJob(context.Background(), map[string]any{"userid_list": []string{"u1"}})
			return e
		}},
		{ep: epSecVipAddRes, invoke: func(c *Client, b string) error {
			_, e := c.GetSecVIPAddJobResult(context.Background(), map[string]any{"jobid": "J1"})
			return e
		}},
		{ep: epSecVipDelJob, invoke: func(c *Client, b string) error {
			_, e := c.SubmitSecVIPDelJob(context.Background(), map[string]any{"userid_list": []string{"u1"}})
			return e
		}},
		{ep: epSecVipDelRes, invoke: func(c *Client, b string) error {
			_, e := c.GetSecVIPDelJobResult(context.Background(), map[string]any{"jobid": "J1"})
			return e
		}},
		{ep: epSecVipList, invoke: func(c *Client, b string) error {
			_, e := c.ListSecVIPAccounts(context.Background(), map[string]any{})
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
		ep     Endpoint
		method string
		path   string
		query  []string
		body   string
	}
	var hits []hit
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		// 下载型端点回文件流（Content-Type 非 JSON），其余回 JSON
		if strings.HasSuffix(r.URL.Path, "/media/get") || strings.HasSuffix(r.URL.Path, "/media/get/jssdk") {
			keys := make([]string, 0, len(r.URL.Query()))
			for k := range r.URL.Query() {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			mu.Lock()
			hits = append(hits, hit{method: r.Method, path: r.URL.Path, query: keys, body: ""})
			mu.Unlock()
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-disposition", `attachment; filename="f.bin"`)
			_, _ = w.Write([]byte("FILE"))
			return
		}
		keys := make([]string, 0, len(r.URL.Query()))
		for k := range r.URL.Query() {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		mu.Lock()
		hits = append(hits, hit{method: r.Method, path: r.URL.Path, query: keys, body: string(body)})
		mu.Unlock()
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(Credential{CorpID: "corp1", AgentID: 100, Secret: "sec", AppType: AppAgent})
	c.SetBaseURL(srv.URL)
	seedToken(t, "TK_EP")

	for i, tc := range cases {
		if err := tc.invoke(c, srv.URL); err != nil {
			t.Fatalf("[%d] %s 调用失败: %v", i, tc.ep.Name, err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(hits) != len(allEndpoints) {
		t.Fatalf("桩命中 %d 次 != 表项数 %d", len(hits), len(allEndpoints))
	}
	for i, tc := range cases {
		h := hits[i]
		if h.method != tc.ep.Method || h.path != tc.ep.Path {
			t.Fatalf("%s 命中 %s %s，表项声明 %s %s", tc.ep.Name, h.method, h.path, tc.ep.Method, tc.ep.Path)
		}
		// query 键集 ⊆ 表项 Query 清单 ∪ {access_token}（可选参数零值跳过时为子集），access_token 必在
		want := append([]string{"access_token"}, tc.ep.Query...)
		sort.Strings(want)
		wantSet := map[string]bool{}
		for _, k := range want {
			wantSet[k] = true
		}
		if !wantSet["access_token"] || !contains(h.query, "access_token") {
			t.Fatalf("%s query 缺 access_token: %v", tc.ep.Name, h.query)
		}
		for _, k := range h.query {
			if !wantSet[k] {
				t.Fatalf("%s query 键 %s 超出表项声明 %v", tc.ep.Name, k, want)
			}
		}
		if tc.ep.Body && h.body == "" {
			t.Fatalf("%s 表项声明带 body 但桩收到空 body", tc.ep.Name)
		}
		if !tc.ep.Body && h.body != "" {
			t.Fatalf("%s 表项声明无 body 但桩收到 %q", tc.ep.Name, h.body)
		}
	}
}

// TestEndpoints_InvokeQueryGuard 未声明 query 参数 fail fast（表项声明式约束）
func TestEndpoints_InvokeQueryGuard(t *testing.T) {
	testredis.Start(t)
	c := NewClient(Credential{CorpID: "corp1", AgentID: 100, Secret: "sec", AppType: AppAgent})
	_, err := c.invoke(context.Background(), epAuthGetUserinfo, map[string]string{"undeclared": "x"}, nil)
	if err == nil {
		t.Fatal("未声明参数应 fail fast")
	}
	var biz *BizError
	var infra *InfraError
	if errors.As(err, &biz) || errors.As(err, &infra) {
		t.Fatal("参数守卫错误是编码 bug，不应伪装成业务/基础设施异常")
	}
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
