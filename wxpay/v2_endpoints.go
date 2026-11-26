package wxpay

import "context"

// V2 轨端点表（官方清点见 change wxpay-v2-basic proposal；与 V3 表段独立——V2 无签名头走 doV2）
type V2Endpoint struct {
	Name     string
	Path     string
	NeedCert bool
}

var (
	epV2MicroPay     = V2Endpoint{"v2.micropay", "/pay/micropay", false}
	epV2SendRedPack  = V2Endpoint{"v2.redpack.send", "/mmpaymkttransfers/sendredpack", true}
	epV2QueryRedPack = V2Endpoint{"v2.redpack.query", "/mmpaymkttransfers/gethbinfo", true}
	epV2CustomsOrder = V2Endpoint{"v2.customs.declare_order", "/cgi-bin/mch/customs/customdeclareorder", false}
	epV2CustomsQuery = V2Endpoint{"v2.customs.declare_query", "/cgi-bin/mch/customs/customdeclarequery", false}
	epV2CustomsRe    = V2Endpoint{"v2.customs.redeclare", "/cgi-bin/mch/newcustoms/customdeclareredeclare", false}
)

// V2 尾批表项（官方 llms.txt 全量清点去重 2026-08-24，见 change wxpay-v2-rest proposal；
// 六支付产品共用核心族 + 小程序红包/银行卡/委托代扣/V2 分账；证书件 needCert=true）
var (
	epV2UnifiedOrder = V2Endpoint{"v2.unifiedorder", "/pay/unifiedorder", false}
	epV2OrderQuery   = V2Endpoint{"v2.orderquery", "/pay/orderquery", false}
	epV2CloseOrder   = V2Endpoint{"v2.closeorder", "/pay/closeorder", false}
	epV2Refund       = V2Endpoint{"v2.refund", "/secapi/pay/refund", true}
	epV2RefundQuery  = V2Endpoint{"v2.refundquery", "/pay/refundquery", false}
	epV2DownloadBill = V2Endpoint{"v2.downloadbill", "/pay/downloadbill", false}
	epV2FundFlow     = V2Endpoint{"v2.downloadfundflow", "/pay/downloadfundflow", true}
	epV2Report       = V2Endpoint{"v2.report", "/pay/report", false}
	epV2ShortURL     = V2Endpoint{"v2.shorturl", "/tools/shorturl", false}
	epV2AuthCode2OID = V2Endpoint{"v2.authcodetoopenid", "/tools/authcodetoopenid", false}
	epV2SandboxMicro = V2Endpoint{"v2.sandbox_micropay", "/sandboxnew/pay/micropay", false}

	epV2MiniHBSend    = V2Endpoint{"v2.miniprogramhb_send", "/mmpaymkttransfers/sendminiprogramhb", true}
	epV2MiniHBReceive = V2Endpoint{"v2.miniprogramhb_receive", "/mmpaymkttransfers/receiveminiprogramhb", false}
	epV2MiniHBQuery   = V2Endpoint{"v2.miniprogramhb_query", "/mmpaymkttransfers/queryminiprogramredpack", false}

	epV2PayBank      = V2Endpoint{"v2.bank_pay", "/mmpaymkttransfers/pay_bank", true}
	epV2QueryBank    = V2Endpoint{"v2.bank_query", "/mmpaymkttransfers/query_bank", true}
	epV2GetPublicKey = V2Endpoint{"v2.bank_getpublickey", "/risk/getpublickey", true}

	epV2EntrustWeb     = V2Endpoint{"v2.entrust_web", "/pay/entrustweb", false}
	epV2ContractOrder  = V2Endpoint{"v2.entrust_contractorder", "/pay/contractorder", false}
	epV2PappayApply    = V2Endpoint{"v2.entrust_pappayapply", "/pay/pappayapply", true}
	epV2PreConsult     = V2Endpoint{"v2.entrust_preconsult", "/pay/pappaypreconsult", false}
	epV2DeleteContract = V2Endpoint{"v2.entrust_deletecontract", "/pay/deletecontract", false}
	epV2QueryContract  = V2Endpoint{"v2.entrust_querycontract", "/pay/querycontract", false}

	epV2PSAddReceiver    = V2Endpoint{"v2.ps_addreceiver", "/pay/profitsharingaddreceiver", false}
	epV2PSRemoveReceiver = V2Endpoint{"v2.ps_removereceiver", "/pay/profitsharingremovereceiver", false}
	epV2PSOrder          = V2Endpoint{"v2.ps_order", "/secapi/pay/profitsharing", true}
	epV2PSMulti          = V2Endpoint{"v2.ps_multi", "/secapi/pay/multiprofitsharing", true}
	epV2PSFinish         = V2Endpoint{"v2.ps_finish", "/secapi/pay/profitsharingfinish", true}
	epV2PSQuery          = V2Endpoint{"v2.ps_query", "/pay/profitsharingquery", false}
	epV2PSReturn         = V2Endpoint{"v2.ps_return", "/secapi/pay/profitsharingreturn", true}
	epV2PSReturnQuery    = V2Endpoint{"v2.ps_returnquery", "/pay/profitsharingreturnquery", false}
	epV2PSAmountQuery    = V2Endpoint{"v2.ps_amountquery", "/pay/profitsharingorderamountquery", false}
)

var v2AllEndpoints = []V2Endpoint{
	epV2MicroPay, epV2SendRedPack, epV2QueryRedPack, epV2CustomsOrder, epV2CustomsQuery, epV2CustomsRe,
	epV2UnifiedOrder, epV2OrderQuery, epV2CloseOrder, epV2Refund, epV2RefundQuery,
	epV2DownloadBill, epV2FundFlow, epV2Report, epV2ShortURL, epV2AuthCode2OID, epV2SandboxMicro,
	epV2MiniHBSend, epV2MiniHBReceive, epV2MiniHBQuery,
	epV2PayBank, epV2QueryBank, epV2GetPublicKey,
	epV2EntrustWeb, epV2ContractOrder, epV2PappayApply, epV2PreConsult, epV2DeleteContract, epV2QueryContract,
	epV2PSAddReceiver, epV2PSRemoveReceiver, epV2PSOrder, epV2PSMulti, epV2PSFinish,
	epV2PSQuery, epV2PSReturn, epV2PSReturnQuery, epV2PSAmountQuery,
}

// MicroPay 付款码支付（提交付款码；params：body/out_trade_no/total_fee/spbill_create_ip/auth_code；
// sign_type 可选 HMAC-SHA256，默认 MD5；无需证书）
func (c *V2Client) MicroPay(ctx context.Context, params map[string]string) (map[string]string, error) {
	return c.doV2(ctx, epV2MicroPay.Path, params, false)
}

// SendRedPack 发放红包（params：mch_billno/wxappid（默认取 AppID）/send_name/re_openid/total_amount/
// total_num=1/wishing/client_ip/act_name/remark；需商户证书双向认证；1-200 元，超限须 scene_id）
func (c *V2Client) SendRedPack(ctx context.Context, params map[string]string) (map[string]string, error) {
	if _, ok := params["wxappid"]; !ok {
		params["wxappid"] = c.Creds.AppID
	}
	return c.doV2(ctx, epV2SendRedPack.Path, params, true)
}

// QueryRedPack 查询红包记录（bill_type 固定 MCHT；需商户证书）
func (c *V2Client) QueryRedPack(ctx context.Context, mchBillno string) (map[string]string, error) {
	return c.doV2(ctx, epV2QueryRedPack.Path, map[string]string{
		"mch_billno": mchBillno, "bill_type": "MCHT",
	}, true)
}

// CustomsDeclareOrder 订单附加信息提交（清关；params：out_trade_no/transaction_id/customs/mch_customs_no）
func (c *V2Client) CustomsDeclareOrder(ctx context.Context, params map[string]string) (map[string]string, error) {
	return c.doV2(ctx, epV2CustomsOrder.Path, params, false)
}

// CustomsDeclareQuery 订单附加信息查询（订单号四选一）
func (c *V2Client) CustomsDeclareQuery(ctx context.Context, params map[string]string) (map[string]string, error) {
	return c.doV2(ctx, epV2CustomsQuery.Path, params, false)
}

// CustomsRedeclare 订单附加信息重推（out_trade_no/transaction_id 二选一）
func (c *V2Client) CustomsRedeclare(ctx context.Context, params map[string]string) (map[string]string, error) {
	return c.doV2(ctx, epV2CustomsRe.Path, params, false)
}
