package wxpay

import (
	"context"
)

// V2 尾批方法层（官方取证见 change wxpay-v2-rest proposal）。
// 六支付产品共用核心族 + 小程序红包/付款到银行卡/委托代扣/V2 分账。

// V2UnifiedOrder 统一下单（六产品共用：trade_type JSAPI/NATIVE/APP/MWEB；
// JSAPI 须 openid、NATIVE 返 code_url、H5 返 mweb_url）。
func (c *V2Client) V2UnifiedOrder(ctx context.Context, params map[string]string) (map[string]string, error) {
	return c.doV2(ctx, epV2UnifiedOrder.Path, params, false)
}

// V2OrderQuery 查询订单（transaction_id/out_trade_no 二选一）。
func (c *V2Client) V2OrderQuery(ctx context.Context, params map[string]string) (map[string]string, error) {
	return c.doV2(ctx, epV2OrderQuery.Path, params, false)
}

// V2CloseOrder 关闭订单（out_trade_no；须商户证书）。
func (c *V2Client) V2CloseOrder(ctx context.Context, params map[string]string) (map[string]string, error) {
	return c.doV2(ctx, epV2CloseOrder.Path, params, false)
}

// V2Refund 申请退款（金额单位分；须商户证书）。
func (c *V2Client) V2Refund(ctx context.Context, params map[string]string) (map[string]string, error) {
	return c.doV2(ctx, epV2Refund.Path, params, true)
}

// V2RefundQuery 查询退款（四键二选一定位）。
func (c *V2Client) V2RefundQuery(ctx context.Context, params map[string]string) (map[string]string, error) {
	return c.doV2(ctx, epV2RefundQuery.Path, params, false)
}

// V2DownloadBill 下载交易账单（bill_type ALL/REFUND/SUCCESS；应答文本 CSV 非标准 XML——
// 调用方按返回 map 的原始字段处理）。
func (c *V2Client) V2DownloadBill(ctx context.Context, params map[string]string) (map[string]string, error) {
	return c.doV2(ctx, epV2DownloadBill.Path, params, false)
}

// V2DownloadFundFlow 下载资金账单（account_type Basic/Operation；须商户证书）。
func (c *V2Client) V2DownloadFundFlow(ctx context.Context, params map[string]string) (map[string]string, error) {
	return c.doV2(ctx, epV2FundFlow.Path, params, true)
}

// V2Report 交易保障上报（接口耗时/失败上报给微信侧监控）。
func (c *V2Client) V2Report(ctx context.Context, params map[string]string) (map[string]string, error) {
	return c.doV2(ctx, epV2Report.Path, params, false)
}

// V2ShortURL 转换短链接（Native 长链接转短）。
func (c *V2Client) V2ShortURL(ctx context.Context, params map[string]string) (map[string]string, error) {
	return c.doV2(ctx, epV2ShortURL.Path, params, false)
}

// V2AuthCodeToOpenID 付款码查询 openid。
func (c *V2Client) V2AuthCodeToOpenID(ctx context.Context, params map[string]string) (map[string]string, error) {
	return c.doV2(ctx, epV2AuthCode2OID.Path, params, false)
}

// V2SandboxMicroPay 沙箱下单（验签调试：沙箱前缀 path；返回签名供本地验证）。
func (c *V2Client) V2SandboxMicroPay(ctx context.Context, params map[string]string) (map[string]string, error) {
	return c.doV2(ctx, epV2SandboxMicro.Path, params, false)
}

// SendMiniProgramRedPack 小程序发放红包（scene_id 必填；须商户证书）。
func (c *V2Client) SendMiniProgramRedPack(ctx context.Context, params map[string]string) (map[string]string, error) {
	return c.doV2(ctx, epV2MiniHBSend.Path, params, true)
}

// ReceiveMiniProgramRedPack 小程序领取红包（沿 user_ticket 形态）。
func (c *V2Client) ReceiveMiniProgramRedPack(ctx context.Context, params map[string]string) (map[string]string, error) {
	return c.doV2(ctx, epV2MiniHBReceive.Path, params, false)
}

// QueryMiniProgramRedPack 小程序查询红包记录。
func (c *V2Client) QueryMiniProgramRedPack(ctx context.Context, params map[string]string) (map[string]string, error) {
	return c.doV2(ctx, epV2MiniHBQuery.Path, params, false)
}

// PayBank 付款到银行卡（enc_bank_no/enc_name 须用 GetV2PublicKey 取得的 RSA 公钥加密；
// 须商户证书）。
func (c *V2Client) PayBank(ctx context.Context, params map[string]string) (map[string]string, error) {
	return c.doV2(ctx, epV2PayBank.Path, params, true)
}

// QueryBank 查询付款到银行卡（须商户证书）。
func (c *V2Client) QueryBank(ctx context.Context, params map[string]string) (map[string]string, error) {
	return c.doV2(ctx, epV2QueryBank.Path, params, true)
}

// GetV2PublicKey 获取 RSA 加密公钥（pay_bank 的 enc_* 字段加密用；须商户证书）。
func (c *V2Client) GetV2PublicKey(ctx context.Context, params map[string]string) (map[string]string, error) {
	return c.doV2(ctx, epV2GetPublicKey.Path, params, true)
}

// EntrustWeb 纯签约（公众号/APP/小程序/H5 四渠道共用同 path；plan_id+contract_code；
// 返回签约跳转 URL）。
func (c *V2Client) EntrustWeb(ctx context.Context, params map[string]string) (map[string]string, error) {
	return c.doV2(ctx, epV2EntrustWeb.Path, params, false)
}

// ContractOrder 支付中签约（下单+签约一体）。
func (c *V2Client) ContractOrder(ctx context.Context, params map[string]string) (map[string]string, error) {
	return c.doV2(ctx, epV2ContractOrder.Path, params, false)
}

// PappayApply 委托代扣申请扣款（contract_id；须商户证书）。
func (c *V2Client) PappayApply(ctx context.Context, params map[string]string) (map[string]string, error) {
	return c.doV2(ctx, epV2PappayApply.Path, params, true)
}

// PappayPreConsult 预扣费通知（扣款前通知避免失败）。
func (c *V2Client) PappayPreConsult(ctx context.Context, params map[string]string) (map[string]string, error) {
	return c.doV2(ctx, epV2PreConsult.Path, params, false)
}

// DeleteContract 申请解约。
func (c *V2Client) DeleteContract(ctx context.Context, params map[string]string) (map[string]string, error) {
	return c.doV2(ctx, epV2DeleteContract.Path, params, false)
}

// QueryContract 查询签约关系。
func (c *V2Client) QueryContract(ctx context.Context, params map[string]string) (map[string]string, error) {
	return c.doV2(ctx, epV2QueryContract.Path, params, false)
}

// V2AddProfitSharingReceiver 添加分账接收方（V2 形态，与 V3 分账不同 path 族）。
func (c *V2Client) V2AddProfitSharingReceiver(ctx context.Context, params map[string]string) (map[string]string, error) {
	return c.doV2(ctx, epV2PSAddReceiver.Path, params, false)
}

// V2RemoveProfitSharingReceiver 删除分账接收方。
func (c *V2Client) V2RemoveProfitSharingReceiver(ctx context.Context, params map[string]string) (map[string]string, error) {
	return c.doV2(ctx, epV2PSRemoveReceiver.Path, params, false)
}

// V2ProfitSharing 请求单次分账（须商户证书）。
func (c *V2Client) V2ProfitSharing(ctx context.Context, params map[string]string) (map[string]string, error) {
	return c.doV2(ctx, epV2PSOrder.Path, params, true)
}

// V2MultiProfitSharing 请求多次分账（须商户证书）。
func (c *V2Client) V2MultiProfitSharing(ctx context.Context, params map[string]string) (map[string]string, error) {
	return c.doV2(ctx, epV2PSMulti.Path, params, true)
}

// V2ProfitSharingFinish 完结分账（须商户证书）。
func (c *V2Client) V2ProfitSharingFinish(ctx context.Context, params map[string]string) (map[string]string, error) {
	return c.doV2(ctx, epV2PSFinish.Path, params, true)
}

// V2ProfitSharingQuery 查询分账结果。
func (c *V2Client) V2ProfitSharingQuery(ctx context.Context, params map[string]string) (map[string]string, error) {
	return c.doV2(ctx, epV2PSQuery.Path, params, false)
}

// V2ProfitSharingReturn 分账回退（须商户证书）。
func (c *V2Client) V2ProfitSharingReturn(ctx context.Context, params map[string]string) (map[string]string, error) {
	return c.doV2(ctx, epV2PSReturn.Path, params, true)
}

// V2ProfitSharingReturnQuery 回退结果查询。
func (c *V2Client) V2ProfitSharingReturnQuery(ctx context.Context, params map[string]string) (map[string]string, error) {
	return c.doV2(ctx, epV2PSReturnQuery.Path, params, false)
}

// V2ProfitSharingAmountQuery 查询订单待分账金额。
func (c *V2Client) V2ProfitSharingAmountQuery(ctx context.Context, params map[string]string) (map[string]string, error) {
	return c.doV2(ctx, epV2PSAmountQuery.Path, params, false)
}
