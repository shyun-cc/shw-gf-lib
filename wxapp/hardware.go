package wxapp

import (
	"context"
	"net/http"
)

// 硬件设备表项（官方取证见 change wxapp-hardware-ops-plugin proposal；license 三件
// 官方 2024-12-31 已回收——表项保留供存量调用方，godoc 注记）
var (
	epHWDeviceMsg      = Endpoint{"hw.device_msg", http.MethodPost, "/cgi-bin/message/device/subscribe/send", []string{"access_token"}, true}
	epHWSnTicket       = Endpoint{"hw.snticket", http.MethodPost, "/wxa/getsnticket", []string{"access_token"}, true}
	epHWIotGroupCreate = Endpoint{"hw.iotgroup_create", http.MethodPost, "/wxa/business/group/createid", []string{"access_token"}, true}
	epHWIotGroupGet    = Endpoint{"hw.iotgroup_get", http.MethodPost, "/wxa/business/group/getinfo", []string{"access_token"}, true}
	epHWIotGroupAddDev = Endpoint{"hw.iotgroup_adddev", http.MethodPost, "/wxa/business/group/adddevice", []string{"access_token"}, true}
	epHWIotGroupRmDev  = Endpoint{"hw.iotgroup_removedev", http.MethodPost, "/wxa/business/group/removedevice", []string{"access_token"}, true}
	epHWLicensePkgs    = Endpoint{"hw.license_pkgs", http.MethodPost, "/wxa/business/license/getpkglist", []string{"access_token"}, true}
	epHWLicenseActive  = Endpoint{"hw.license_active", http.MethodPost, "/wxa/business/license/activedevice", []string{"access_token"}, true}
	epHWLicenseDevInfo = Endpoint{"hw.license_deviceinfo", http.MethodPost, "/wxa/business/license/getdeviceinfo", []string{"access_token"}, true}
)

// SendHardwareDeviceMessage 发送设备订阅消息（sn ≤128 字节限字符集；日 100 万次；
// miniprogram_state 选填默认 formal）。
func (s *WxAppService) SendHardwareDeviceMessage(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epHWDeviceMsg, map[string]string{"access_token": token}, body)
}

// GetSnTicket 获取设备票据（sn_ticket 5 分钟有效，供设备侧 SDK 拉起小程序用）。
func (s *WxAppService) GetSnTicket(ctx context.Context, sn, modelID string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epHWSnTicket, map[string]string{"access_token": token}, map[string]string{"sn": sn, "model_id": modelID})
}

// CreateIotGroup 创建设备组（group_name 1-32 字节创建后不可改；组仅可纳入与 model_id
// 同类型设备）。
func (s *WxAppService) CreateIotGroup(ctx context.Context, modelID, groupName string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epHWIotGroupCreate, map[string]string{"access_token": token}, map[string]string{"model_id": modelID, "group_name": groupName})
}

// GetIotGroupInfo 查询设备组信息（含 device_list 与 model_type）。
func (s *WxAppService) GetIotGroupInfo(ctx context.Context, groupID string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epHWIotGroupGet, map[string]string{"access_token": token}, map[string]string{"group_id": groupID})
}

// AddIotGroupDevice 设备组添加设备（≤50 台/组；同一设备同时仅属一组；force_add=true 会把
// 已属其他组的设备移出再加入本组，慎用）。
func (s *WxAppService) AddIotGroupDevice(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epHWIotGroupAddDev, map[string]string{"access_token": token}, body)
}

// RemoveIotGroupDevice 设备组删除设备（应答 device_list 逐台携带 errcode）。
func (s *WxAppService) RemoveIotGroupDevice(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epHWIotGroupRmDev, map[string]string{"access_token": token}, body)
}

// GetLicensePkgList 查询 license 资源包列表（pkg_type 0 测试体验包/1-5 对应 A-E 类设备）。
// 官方 2024-12-31 已回收本接口——保留表项供存量调用方，新业务勿用。
func (s *WxAppService) GetLicensePkgList(ctx context.Context, pkgType int) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epHWLicensePkgs, map[string]string{"access_token": token}, map[string]any{"pkg_type": pkgType})
}

// ActiveLicenseDevice 激活设备 license（device_list ≤100 项含 active_number 防重复激活）。
// 官方 2024-12-31 已回收（2024-09-09 起激活不消耗 license）——保留表项供存量调用方。
func (s *WxAppService) ActiveLicenseDevice(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epHWLicenseActive, map[string]string{"access_token": token}, body)
}

// GetLicenseDeviceInfo 查询设备激活详情（批量查剩余有效期）。官方 2024-12-31 已回收
// （expire_time 固定返回 2030-01-01 无实际意义）——保留表项供存量调用方。
func (s *WxAppService) GetLicenseDeviceInfo(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epHWLicenseDevInfo, map[string]string{"access_token": token}, body)
}
