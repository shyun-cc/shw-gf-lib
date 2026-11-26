package attachment

import (
	"context"
	"time"

	"github.com/shyun-cc/shw-gf-lib/storage"

	"github.com/gogf/gf/v2/frame/g"
)

// UploadCleaner uploads 行清理器（单批函数；status=3 且超过 grace 期）
type UploadCleaner struct {
	Repo          Repository
	Store         storage.Storage
	TempDirectory string
}

// CleanupBatch 删一批过期 uploads 行：先删 temp 物理文件（幂等）再删行。
// grace 语义：去重改名后的过渡期内 uploads 行仍在 → 新旧 filename 均可读；
// grace 尽后行被清理 → 旧 filename resolve 返回未找到。
func (c *UploadCleaner) CleanupBatch(ctx context.Context, limit int, grace time.Duration) (deleted int, err error) {
	expired, err := c.Repo.FindExpiredUploads(ctx, limit, grace)
	if err != nil {
		return 0, err
	}
	for _, u := range expired {
		if err := c.Store.Delete(ctx, c.TempDirectory+"/"+u.Filename); err != nil {
			g.Log().Warningf(ctx, "attachment: temp 物理删除失败 filename=%s: %v", u.Filename, err)
			continue
		}
		if err := c.Repo.DeleteUpload(ctx, u.Filename); err != nil {
			g.Log().Warningf(ctx, "attachment: uploads 行删除失败 filename=%s: %v", u.Filename, err)
			continue
		}
		deleted++
	}
	return deleted, nil
}

// AttachmentCleaner attachments 行清理器（shouldDeleteAttachment 逐行询问）
type AttachmentCleaner struct {
	Repo  Repository
	Hook  BusinessHook
	Store storage.Storage
}

// CleanupBatch 逐行询问业务钩子，仅删确认无引用的行（物理文件不删——对象存储低成本，
// 引用审计兜底；如需物理删除项目可自行扩展）
func (c *AttachmentCleaner) CleanupBatch(ctx context.Context, limit int) (deleted int, err error) {
	list, err := c.Repo.ListAttachments(ctx, limit)
	if err != nil {
		return 0, err
	}
	for _, a := range list {
		if c.Hook == nil || !c.Hook.ShouldDeleteAttachment(ctx, a) {
			continue
		}
		if err := c.Repo.DeleteAttachment(ctx, a.ID); err != nil {
			g.Log().Warningf(ctx, "attachment: attachments 行删除失败 id=%s: %v", a.ID, err)
			continue
		}
		deleted++
	}
	return deleted, nil
}
