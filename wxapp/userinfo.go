package wxapp

import (
	"context"
)

// 用户信息族（官方取证见 change wxapp-qrcode-userinfo proposal）。

// GetPluginOpenPID 获取插件用户 openpid（code 由前端 wx.pluginLogin 获取，5 分钟有效且
// 仅可兑换一次）。
func (s *WxAppService) GetPluginOpenPID(ctx context.Context, code string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epGetPluginOpenPID, map[string]string{"access_token": token}, map[string]string{"code": code})
}

// CheckEncryptedData 检查加密信息是否由微信生成（目前仅手机号；仅能校验近 3 天数据；
// encryptedMsgHash 为加密数据的 sha256 hex 值）。
func (s *WxAppService) CheckEncryptedData(ctx context.Context, encryptedMsgHash string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epCheckEncryptedMsg, map[string]string{"access_token": token}, map[string]string{"encrypted_msg_hash": encryptedMsgHash})
}

// GetPaidUnionID 支付后获取 UnionID（支付完成后 5 分钟内有效；transactionID 或
// mchID+outTradeNo 二选一，语义由调用方保证）。
func (s *WxAppService) GetPaidUnionID(ctx context.Context, openid, transactionID, mchID, outTradeNo string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	query := map[string]string{"access_token": token, "openid": openid}
	if transactionID != "" {
		query["transaction_id"] = transactionID
	}
	if mchID != "" {
		query["mch_id"] = mchID
	}
	if outTradeNo != "" {
		query["out_trade_no"] = outTradeNo
	}
	return s.invoke(ctx, epGetPaidUnionID, query, nil)
}

// GetUserEncryptKey 获取用户加密 key（近 3 次、每次存活 3600s；signature 为
// hmac_sha256(session_key, "") 的结果，sigMethod 固定 hmac_sha256）。
func (s *WxAppService) GetUserEncryptKey(ctx context.Context, openid, signature, sigMethod string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epGetUserEncryptKey, map[string]string{
		"access_token": token, "openid": openid, "signature": signature, "sig_method": sigMethod,
	}, nil)
}
