# wxpay — 微信支付商户侧 API，V2+V3 双轨（按范围裁决收官）

> 面向项目侧接入者。wxpay 已按 2026-08-24 范围裁决收官（V3 64 + V2 38 = 102 表项；V3 未铺域见第 3 节挂起清单）；本文各节与 `wxpay/` 源码逐字一致，若发现不一致，以源码为准并修正本文档。

## 1. 组件定位

微信支付商户侧服务端 API 自主复刻，零厂商 SDK，**V2+V3 双轨并存**：

| 轨 | 报文 | 签名 | 回调/验签 | 覆盖域 |
|---|---|---|---|---|
| V3 | JSON | SHA256-RSA（商户私钥签名） | 平台证书/微信支付公钥验签 + 回调 AES-256-GCM 解密 | 五种下单（JSAPI/APP/H5/Native/小程序）+ 合单、查单/关单、退款、账单、分账、商家转账、支付分、营销族、消费者投诉 2.0 |
| V2 | XML | API 密钥 MD5 / HMAC-SHA256 | p12 商户证书双向认证 | 付款码、现金红包、清关及仅 V2 提供的端点（首个 wxpay 域 change 清点划定） |

两轨各持独立的表驱动分发层（V3 `Endpoint` 表项 + 统一 `invoke`；V2 `V2Endpoint` 表项走 `doV2`、无签名头），编解码与签名栈各自实现。端点全集以微信官方文档为基准，随 wechat-ecosystem roadmap 各域 change 逐端点清点铺设。

## 2. 密钥注入原则

一切证书与密钥（商户私钥、证书序列号、APIv2 密钥、APIv3 密钥、平台证书）经**构造参数注入**——库不做项目决策，包内无硬编码密钥或证书路径。回调处理的验签步骤与业务报文解密是两个可独立调用的入口。

## 3. V3 轨（2026-08-22 铺入，change wxpay-v3-basic）

- **`NewClient(Credentials)`**：一切密钥构造注入——`Credentials{MchID, AppID, SerialNo, PrivateKey *rsa.PrivateKey, APIv3Key []byte(32), VerifyKeys map[serial]*rsa.PublicKey}`（平台证书公钥或微信支付公钥均可入池）；包内零硬编码。
- **签名栈**：`AuthorizationHeader(method, pathWithQuery, body)`——官方 5 行签名串（METHOD
 URL
 ts
 nonce
 body
）商户私钥 SHA256-RSA，头格式 `WECHATPAY2-SHA256-RSA2048 mchid="…",nonce_str="…",timestamp="…",serial_no="…",signature="…"`；`VerifyResponse/VerifyNotify`——3 行验签串（ts
 nonce
 body
）按 Wechatpay-Serial 选公钥验 Wechatpay-Signature。
- **回调解密（与验签分离的两入口）**：`DecryptResource(NotifyResource)`——AES-256-GCM，APIv3 密钥，ciphertext=C‖T（T=末 16 字节）、nonce=IV、associated_data=AAD，明文 UTF-8 JSON；`VerifyNotify` 验签独立可调。
- **V3 错误形态**：HTTP≥400 或 body 含 code → `BizError{Code,Message,HTTPStatus}`（与微信系 errcode 形态不同）；网络/解码/签名/验签/解密 → `InfraError{Op,Err}`。
- **基础支付 7 端点**：`PrepayJSAPI` / `PrepayAPP` / `PrepayH5` / `PrepayNative` / `PrepayCombineNative`（合单）/ `QueryTransactionByID`（微信订单号）/ `CloseTransaction`（204 无包体）；表驱动断言遍历计数相等（Authorization 头命中）。
- **合单与退款族（2026-08-22 铺入，change wxpay-v3-refund）**：合单三变体 `PrepayCombineJSAPI` / `PrepayCombineAPP` / `PrepayCombineH5`（sub_orders 2-10 笔）、商户订单号查单 `QueryTransactionByOutTradeNo`（query mchid）、`CreateRefund`（out_refund_no + amount + transaction_id/out_trade_no 二选一；退款回调 REFUND.* 走 VerifyNotify+DecryptResource）、`QueryRefund`（按商户退款单号）、`ApplyAbnormalRefund`（type=USER_BANK_CARD 时 bank_* 实必填）。
- **账单与分账族（2026-08-22 铺入，change wxpay-v3-bill-profitsharing）**：`ApplyTradeBill`（bill_date 必填）/ `ApplyProfitsharingBill` / `DownloadBill(downloadURL)`（对申请接口返回的 download_url 30 秒内签名下载，官方明确响应无签名值故跳过验签，返回账单字节）；分账四件 `AddProfitsharingReceiver`（接收方上限 2 万）/ `CreateProfitsharingOrder`（unfreeze_unsplit=true 解冻后不可再分账）/ `QueryProfitsharingOrder` / `UnfreezeProfitsharingRemainder`（异步处理；资金冻结期商户模式 30 天）。
- **商家转账族（2026-08-22 铺入，change wxpay-v3-transfer）**：`CreateTransferBill`（用户确认模式；transfer_scene_report_infos 必填族）/ `PreTransferWithAuthorization`（免确认收款授权，≥2000 元须 user_name）/ `TransferWithAuthorization`（授权后转账，authorization_id 二选一）/ 双路查单 `QueryTransferByOutBillNo` / `QueryTransferByTransferBillNo`（微信单号仅近 30 天）/ `QueryTransferElecSign`（回单下载地址 10 分钟有效）/ `ApplyTransferBillReceipt`（旧版批次族回单申请）。
- **资金账单与投诉 2.0（2026-08-22 铺入，change wxpay-v3-misc）**：`ApplyFundFlowBill`（资金实际到账流水，区别于交易账单；下载走 DownloadBill）；投诉五件 `ListComplaints`（begin/end_date 必填）/ `GetComplaint` / `ListComplaintNegotiationHistorys`（limit 1-300）/ `ReplyComplaint`（首次回复后脱离待处理）/ `GetComplaintNotificationURL`。
- **V2 尾批（2026-08-24 铺入，change wxpay-v2-rest，四包按裁决收官）：32 端点**——六支付产品**共用核心族 11**（`V2UnifiedOrder`（trade_type 四态共用）/查单/关单/`V2Refund` 证书/退款查询/账单双件（资金账单证书）/`V2Report` 交易保障/短链/付款码换 openid/`V2SandboxMicroPay` 沙箱验签）；小程序红包三件（发放证书 scene_id/领取 user_ticket/查询）；**付款到银行卡三件**（`PayBank` enc_* 须 `GetV2PublicKey` RSA 公钥加密+证书/查询/公钥获取）；**委托代扣六件**（纯签约四渠道同 path 去重/支付中签约/`PappayApply` 扣款证书/预扣费通知/解约/查签约）；**V2 分账九件**（addreceiver/removereceiver + 单次/多次/完结/回退证书四件 + 双查询 + 待分账金额——与 V3 分账不同 path 族）。回调类（支付/退款/扣款/动账通知）为 XML 回调形态——包内不立端点，回调报文的解析与验签未提供导出入口；调起支付前端参数为前端构造不立端点。

**V2 轨（2026-08-22 铺入，change wxpay-v2-basic）**：`NewV2Client(V2Credentials{MchID, AppID, APIKey, TLSCertPEM, TLSKeyPEM})`——一切密钥构造注入（p12 由调用方转 PEM）；**XML 编解码栈**（组包值转义/解析叶子 map）；**双签名算法**（MD5 默认 / HMAC-SHA256，k=v 字典序拼接 + &key= 后 hex 大写）；`V2BizError{ReturnCode,ResultCode,ErrCode,ErrCodeDes}`（V2 错误形态）。**6 端点**：`MicroPay`（付款码支付，无需证书）、`SendRedPack` / `QueryRedPack`（现金红包，**商户证书双向认证**——TLS 桩双证取证）、清关三件 `CustomsDeclareOrder` / `CustomsDeclareQuery` / `CustomsRedeclare`（MD5）。表段独立 v2AllEndpoints。
- **支付分族（2026-08-22 铺入，change wxpay-v3-payscore）**：服务订单四件 `CreatePayScoreOrder`（out_order_no/service_id/service_introduction/time_range/risk_fund/notify_url）/ `QueryPayScoreOrder`（out_order_no 与 query_id 二选一）/ `CancelPayScoreOrder`（appid 须与创单一致，reason≤50 字符）/ `CompletePayScoreOrder`（post_payments）——状态机 CREATED/DOING/DONE/REVOKED/EXPIRED。
- **营销族·代金券核心（2026-08-22 铺入，change wxpay-v3-coupon）**：`CreateCouponStock`（批次创建：stock_use_rule/coupon_use_rule/no_cash/stock_type=NORMAL）/ `StartCouponStock`（激活——预充值券锁定营销资金）/ `SendCoupon`（发放至 {openid}）/ `GetCoupon`（券详情，appid 与 openid 绑定匹配）。
- **营销族·批次管理（2026-08-22 铺入，change wxpay-v3-coupon-mgmt）**：`ListCouponStocks`（条件查询：offset/limit≤10/stock_creator_mchid + 时间/状态选填，状态机 unactivated/audit/running/stoped/paused）/ `GetCouponStock` / `PauseCouponStock` / `RestartCouponStock`（仅全场/单品券）。
- **营销余域（2026-08-22 铺入，change wxpay-v3-coupon-extra）**：`DownloadCouponUseFlow`（核销明细 download_url，文件下载走 DownloadBill）、`ListUserCoupons`（用户券列表——GET 形态与 POST 发放同 path 不同方法）。
- **券通知地址设置（2026-08-22 铺入，change wxpay-v3-callbacks）**：`SetFavorNotifyURL`（代金券核销事件推送前提，notify_url 须 https 无参数）/ `SetBusiFavorNotifyURL`（商家券事件）。
- **商家券族核心（2026-08-22 铺入，change wxpay-v3-busifavor）**：`GetBusiFavorStock` / `UpdateBusiFavorStock`（PATCH 首例）/ `UseBusiFavorCoupon`（核销，500 QPS）；通知地址见前节 `SetBusiFavorNotifyURL`。
- **商家券族第二批（2026-08-22 铺入，change wxpay-v3-busifavor2）**：`CreateBusiFavorStock`（批次创建：stock_name≤21 字符/coupon_code_mode 等）/ `UpdateBusiFavorBudget`（PATCH）/ `UploadBusiFavorCouponCodes`（预存 code）/ `GetUserBusiFavorCoupon`（三段路径 {openid}/{coupon_code}/appids/{appid} 首例）。
- **商家券族第三批（2026-08-22 铺入，change wxpay-v3-busifavor3）**：`SendProductCoupon`（商品券发放，仅限已关联商品批次，coupon_type NORMAL/MEMBER）/ `ListUserCouponsByFilter`（过滤查券：appid 必填，调用方为创建/归属/发放方时对应商户号参数必传）/ `SetBusiFavorCallbacks` + `GetBusiFavorCallbacks`（通知地址设置/获取双件，POST/GET 同 path）/ `PatchBusiFavorStock`（修改基本信息，out_request_no 必填，204 无应答体）/ `ReturnBusiFavorCoupon`（退券，限已核销状态）/ `DeactivateBusiFavorCoupon`（使券失效）/ `UploadMarketingImage`（营销专用图片上传——**multipart 形态首例**：Authorization 签名主体为 meta JSON 而非实际 body，sha256 方法内计算，JPG/BMP/PNG≤2M）。
- **挂起清单**（后续 change 逐页取证）：发券族（小程序/H5 插件）、委托营销、支付有礼、商圈、品牌门店、积分兑券、核销组件、支付分免确认变体与同步状态、旧批次族、投诉回调地址更新/删除。

## 4. 显式排除

- **EasyWeChat 不迁**：Go 侧定案全自主复刻、零厂商 SDK（含 EasyWeChat 形态的工厂封装）。
