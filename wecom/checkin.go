package wecom

import (
	"context"
	"net/http"
)

// 打卡域表项（OA 族首域）：规则 5 + 记录报表 3 + 排班 2 + 补卡/记录/人脸 3 + 设备 1 + 硬件规则 1
// （官方清点见 change wecom-checkin-domain proposal）
var (
	epCheckinCorpOption  = Endpoint{"checkin.corp_option", http.MethodPost, "/cgi-bin/checkin/getcorpcheckinoption", nil, true}
	epCheckinOption      = Endpoint{"checkin.option", http.MethodPost, "/cgi-bin/checkin/getcheckinoption", nil, true}
	epCheckinData        = Endpoint{"checkin.data", http.MethodPost, "/cgi-bin/checkin/getcheckindata", nil, true}
	epCheckinDayData     = Endpoint{"checkin.daydata", http.MethodPost, "/cgi-bin/checkin/getcheckin_daydata", nil, true}
	epCheckinMonthData   = Endpoint{"checkin.monthdata", http.MethodPost, "/cgi-bin/checkin/getcheckin_monthdata", nil, true}
	epCheckinScheduGet   = Endpoint{"checkin.schedu_list", http.MethodPost, "/cgi-bin/checkin/getcheckinschedulist", nil, true}
	epCheckinScheduSet   = Endpoint{"checkin.set_schedu_list", http.MethodPost, "/cgi-bin/checkin/setcheckinschedulist", nil, true}
	epCheckinPunchFix    = Endpoint{"checkin.punch_correction", http.MethodPost, "/cgi-bin/checkin/punch_correction", nil, true}
	epCheckinAddRecord   = Endpoint{"checkin.add_record", http.MethodPost, "/cgi-bin/checkin/add_checkin_record", nil, true}
	epCheckinUserFace    = Endpoint{"checkin.userface", http.MethodPost, "/cgi-bin/checkin/addcheckinuserface", nil, true}
	epCheckinHardware    = Endpoint{"checkin.hardware_data", http.MethodPost, "/cgi-bin/hardware/get_hardware_checkin_data", nil, true}
	epCheckinOptionAdd   = Endpoint{"checkin.option_add", http.MethodPost, "/cgi-bin/checkin/add_checkin_option", nil, true}
	epCheckinOptionUpd   = Endpoint{"checkin.option_update", http.MethodPost, "/cgi-bin/checkin/update_checkin_option", nil, true}
	epCheckinOptionClear = Endpoint{"checkin.option_clear_array", http.MethodPost, "/cgi-bin/checkin/clear_checkin_option_array_field", nil, true}
	epCheckinOptionDel   = Endpoint{"checkin.option_del", http.MethodPost, "/cgi-bin/checkin/del_checkin_option", nil, true}
)

// GetCorpCheckinOption 获取企业所有打卡规则（空 body）
func (c *Client) GetCorpCheckinOption(ctx context.Context) (map[string]any, error) {
	return c.invoke(ctx, epCheckinCorpOption, nil, map[string]any{})
}

// GetCheckinOption 获取员工打卡规则（body：datetime/useridlist，最多 100 人）
func (c *Client) GetCheckinOption(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epCheckinOption, nil, body)
}

// GetCheckinData 获取打卡记录数据（body：opencheckindatatype/starttime/endtime/useridlist）
func (c *Client) GetCheckinData(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epCheckinData, nil, body)
}

// GetCheckinDayData 获取打卡日报数据
func (c *Client) GetCheckinDayData(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epCheckinDayData, nil, body)
}

// GetCheckinMonthData 获取打卡月报数据
func (c *Client) GetCheckinMonthData(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epCheckinMonthData, nil, body)
}

// GetCheckinScheduList 获取打卡人员排班信息（body：starttime/endtime/useridlist）
func (c *Client) GetCheckinScheduList(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epCheckinScheduGet, nil, body)
}

// SetCheckinScheduList 为打卡人员排班（body：groupid/items/yearmonth）
func (c *Client) SetCheckinScheduList(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epCheckinScheduSet, nil, body)
}

// PunchCorrection 为打卡人员补卡（body：userid/schedule_date_time/checkin_time/…）
func (c *Client) PunchCorrection(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epCheckinPunchFix, nil, body)
}

// AddCheckinRecord 添加打卡记录（body：records 数组）
func (c *Client) AddCheckinRecord(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epCheckinAddRecord, nil, body)
}

// AddCheckinUserFace 录入打卡人员人脸信息（body：userid/userface）
func (c *Client) AddCheckinUserFace(ctx context.Context, userid, userface string) (map[string]any, error) {
	return c.invoke(ctx, epCheckinUserFace, nil, map[string]any{"userid": userid, "userface": userface})
}

// GetHardwareCheckinData 获取设备打卡数据（body：filter_type/starttime/endtime/useridlist）
func (c *Client) GetHardwareCheckinData(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epCheckinHardware, nil, body)
}

// AddCheckinOption 创建打卡规则（body：effective_now/group）
func (c *Client) AddCheckinOption(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epCheckinOptionAdd, nil, body)
}

// UpdateCheckinOption 修改打卡规则（group 内须含 groupid）
func (c *Client) UpdateCheckinOption(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epCheckinOptionUpd, nil, body)
}

// ClearCheckinOptionArrayField 清空打卡规则数组元素（body：groupid/clear_field/effective_now）
func (c *Client) ClearCheckinOptionArrayField(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epCheckinOptionClear, nil, body)
}

// DelCheckinOption 删除打卡规则（body：groupid/effective_now）
func (c *Client) DelCheckinOption(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epCheckinOptionDel, nil, body)
}
