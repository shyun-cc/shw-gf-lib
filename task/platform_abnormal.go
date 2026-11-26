package task

// publicStatus 将旧存储状态映射到当前公开契约；实际占用由 Recover 同事务释放。
func publicStatus(status string) string {
	if status == StatusNeedsReview {
		return StatusAbnormal
	}
	return status
}
