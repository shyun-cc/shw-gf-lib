package wecom

import (
	"context"
	"net/http"
)

// 日程域表项（OA 族）：日历 4 + 日程 7（官方清点见 change wecom-schedule-domain proposal）
var (
	epCalendarAdd       = Endpoint{"calendar.add", http.MethodPost, "/cgi-bin/oa/calendar/add", nil, true}
	epCalendarUpdate    = Endpoint{"calendar.update", http.MethodPost, "/cgi-bin/oa/calendar/update", nil, true}
	epCalendarGet       = Endpoint{"calendar.get", http.MethodPost, "/cgi-bin/oa/calendar/get", nil, true}
	epCalendarDel       = Endpoint{"calendar.del", http.MethodPost, "/cgi-bin/oa/calendar/del", nil, true}
	epScheduleAdd       = Endpoint{"schedule.add", http.MethodPost, "/cgi-bin/oa/schedule/add", nil, true}
	epScheduleUpdate    = Endpoint{"schedule.update", http.MethodPost, "/cgi-bin/oa/schedule/update", nil, true}
	epScheduleAddAttd   = Endpoint{"schedule.add_attendees", http.MethodPost, "/cgi-bin/oa/schedule/add_attendees", nil, true}
	epScheduleDelAttd   = Endpoint{"schedule.del_attendees", http.MethodPost, "/cgi-bin/oa/schedule/del_attendees", nil, true}
	epScheduleListByCal = Endpoint{"schedule.get_by_calendar", http.MethodPost, "/cgi-bin/oa/schedule/get_by_calendar", nil, true}
	epScheduleGet       = Endpoint{"schedule.get", http.MethodPost, "/cgi-bin/oa/schedule/get", nil, true}
	epScheduleDel       = Endpoint{"schedule.del", http.MethodPost, "/cgi-bin/oa/schedule/del", nil, true}
)

// AddCalendar 创建日历（body：calendar{admins/summary/color/description/shares/…}）
func (c *Client) AddCalendar(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epCalendarAdd, nil, body)
}

// UpdateCalendar 更新日历（body：skip_public_range/calendar，calendar 内含 cal_id）
func (c *Client) UpdateCalendar(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epCalendarUpdate, nil, body)
}

// GetCalendar 获取日历详情（cal_id_list 最多 1000）
func (c *Client) GetCalendar(ctx context.Context, calIDList []string) (map[string]any, error) {
	return c.invoke(ctx, epCalendarGet, nil, map[string]any{"cal_id_list": calIDList})
}

// DelCalendar 删除日历
func (c *Client) DelCalendar(ctx context.Context, calID string) (map[string]any, error) {
	return c.invoke(ctx, epCalendarDel, nil, map[string]any{"cal_id": calID})
}

// AddSchedule 创建日程（body：schedule{organizer/start_time/end_time/attendees/…}）
func (c *Client) AddSchedule(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epScheduleAdd, nil, body)
}

// UpdateSchedule 更新日程（body：skip_attendees/op_mode/op_start_time/schedule；重复日程见官方 96204）
func (c *Client) UpdateSchedule(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epScheduleUpdate, nil, body)
}

// AddScheduleAttendees 新增日程参与者（body：schedule_id/attendees[{userid}]）
func (c *Client) AddScheduleAttendees(ctx context.Context, scheduleID string, attendees []map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epScheduleAddAttd, nil, map[string]any{"schedule_id": scheduleID, "attendees": attendees})
}

// DelScheduleAttendees 删除日程参与者（单次最多 1000 人）
func (c *Client) DelScheduleAttendees(ctx context.Context, scheduleID string, attendees []map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epScheduleDelAttd, nil, map[string]any{"schedule_id": scheduleID, "attendees": attendees})
}

// ListScheduleByCalendar 获取日历下的日程列表（body：cal_id/offset/limit）
func (c *Client) ListScheduleByCalendar(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epScheduleListByCal, nil, body)
}

// GetSchedule 获取日程详情（schedule_id_list）
func (c *Client) GetSchedule(ctx context.Context, scheduleIDList []string) (map[string]any, error) {
	return c.invoke(ctx, epScheduleGet, nil, map[string]any{"schedule_id_list": scheduleIDList})
}

// CancelSchedule 取消日程（body：schedule_id/op_mode/op_start_time）
func (c *Client) CancelSchedule(ctx context.Context, scheduleID string) (map[string]any, error) {
	return c.invoke(ctx, epScheduleDel, nil, map[string]any{"schedule_id": scheduleID})
}
