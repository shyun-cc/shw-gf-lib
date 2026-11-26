package attachment

import (
	"context"
	"time"

	"github.com/shyun-cc/shw-gf-lib/storage"

	"github.com/gogf/gf/v2/frame/g"
)

// Archiver 归档器（单批函数；进程调度由项目用 worker 周期装配，经 RunEnabled 注册）
type Archiver struct {
	Repo          Repository
	Hook          BusinessHook
	Store         storage.Storage
	TempDirectory string
}

// ArchiveBatch 归档一批 pending uploads：
// Head 取 md5 → hash 命中 attachments 则调 renameFilename(A→existing) 且不 copy、uploads 行不改名；
// 未命中 copy 到 {archiveDirectory}/{Y-m} + 插 attachments；两者最终 uploads → status=3。
func (a *Archiver) ArchiveBatch(ctx context.Context, limit int) (archived, deduped int, err error) {
	pending, err := a.Repo.FindPendingArchive(ctx, limit)
	if err != nil {
		return 0, 0, err
	}
	datePartition := time.Now().Format("2006-01")

	for _, u := range pending {
		if err := a.archiveOne(ctx, u, datePartition, &deduped); err != nil {
			g.Log().Warningf(ctx, "attachment: 归档单条失败 filename=%s: %v", u.Filename, err)
			continue
		}
		archived++
	}
	return archived, deduped, nil
}

func (a *Archiver) archiveOne(ctx context.Context, u *UploadRecord, datePartition string, deduped *int) error {
	key := a.TempDirectory + "/" + u.Filename
	meta, err := a.Store.Head(ctx, key)
	if err != nil {
		return err
	}
	if meta == nil {
		// temp 文件不存在（未真正上传/已被清理）——跳过本轮
		return nil
	}
	hash := "md5:" + meta.ETag

	if existing, err := a.Repo.FindAttachmentByHash(ctx, hash); err == nil && existing != nil {
		// 去重命中：通知业务表改名；uploads 行 filename 不改、不 copy
		if a.Hook != nil {
			if err := a.Hook.RenameFilename(ctx, u.Filename, existing.Filename); err != nil {
				return err
			}
		}
		*deduped++
		return a.Repo.MarkArchived(ctx, u.Filename)
	}

	// 新文件：copy 到正式目录 + 插 attachments + uploads → status=3
	dst := u.ArchiveDirectory + "/" + datePartition + "/" + u.Filename
	if err := a.Store.Copy(ctx, key, dst); err != nil {
		return err
	}
	if err := a.Repo.InsertAttachment(ctx, &AttachmentRecord{
		Hash: hash, Filename: u.Filename, ArchiveDirectory: u.ArchiveDirectory,
		DatePartition: datePartition, Disk: u.Disk, MimeType: meta.ContentType, Size: meta.Size,
	}); err != nil {
		return err
	}
	return a.Repo.MarkArchived(ctx, u.Filename)
}

var _ = storage.UploadTicket{}
