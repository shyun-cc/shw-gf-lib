package wxapp

import (
	"context"
	"net/http"
)

// 生物认证 + 人脸核身表项（官方取证见 change wxapp-ocr-face-misc proposal）
var (
	epSoterVerify     = Endpoint{"soter.verify", http.MethodPost, "/cgi-bin/soter/verify_signature", []string{"access_token"}, true}
	epFaceGetVerify   = Endpoint{"face.getverifyid", http.MethodPost, "/cityservice/face/identify/getverifyid", []string{"access_token"}, true}
	epFaceQueryVerify = Endpoint{"face.queryverify", http.MethodPost, "/cityservice/face/identify/queryverifyinfo", []string{"access_token"}, true}
)

// VerifySoterSignature SOTER 生物认证秘钥签名验证（jsonString/jsonSignature 取自前端
// wx.startSoterAuthentication 成功回调的 resultJSON/resultJSONSignature；返回 is_ok）。
func (s *WxAppService) VerifySoterSignature(ctx context.Context, openid, jsonString, jsonSignature string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epSoterVerify, map[string]string{"access_token": token}, map[string]string{
		"openid": openid, "json_string": jsonString, "json_signature": jsonSignature,
	})
}

// GetFaceVerifyID 获取用户人脸核身会话唯一标识（outSeqNo 5-32 字符 appid 内唯一；
// cert_info{cert_type IDENTITY_CARD/cert_name/cert_no}；verify_id 默认 3600 秒有效；
// 生成 openid 的 appid 须与前端发起核身的小程序一致）。
func (s *WxAppService) GetFaceVerifyID(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epFaceGetVerify, map[string]string{"access_token": token}, body)
}

// QueryFaceVerifyInfo 查询用户人脸核身真实验证结果（cert_hash 为证件信息 SHA256 摘要；
// 核身通过判定：errcode=0 且 verify_ret=10000；out_seq_no/openid 须与获取时一致）。
func (s *WxAppService) QueryFaceVerifyInfo(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epFaceQueryVerify, map[string]string{"access_token": token}, body)
}
