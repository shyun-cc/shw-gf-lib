// Package attachment 附件全生命周期（对应 PHP Foundation/Attachment）：
// 前端直传 → commit → 归档 md5 去重 → 双清理 → 双表读取兜底。
//
// 仓储与业务钩子由项目实现（Repository/BusinessHook 接口）；
// 归档/清理以「单批函数」提供，进程调度由项目用 worker 周期装配（PeriodicWorker/ProcWorker 均可），
// 经 RunEnabled 注册、ENABLE_WORKERS 控制编成。
package attachment

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/shyun-cc/shw-gf-lib/exception"
	"github.com/shyun-cc/shw-gf-lib/storage"
)

// UploadStatus uploads 行状态机
const (
	UploadStatusPending  = 1 // 已发Ticket待上传
	UploadStatusUploaded = 2 // 业务已 commit（写 archive_directory）
	UploadStatusArchived = 3 // 已归档
)

// UploadRecord uploads 行
type UploadRecord struct {
	// ID uuidv7 文本（36 字符带连字符），由项目侧 PG uuid 列经驱动扫描而来
	ID               string
	Filename         string
	Disk             string
	ArchiveDirectory string
	Status           int
	ExpiresAt        *time.Time
	ArchivedAt       *time.Time
}

// AttachmentRecord attachments 行
type AttachmentRecord struct {
	// ID uuidv7 文本（36 字符带连字符），由项目侧 PG uuid 列经驱动扫描而来
	ID               string
	Hash             string // md5:{etag}
	Filename         string
	ArchiveDirectory string
	DatePartition    string // Y-m
	Disk             string
	MimeType         string
	Size             int64
}

// Repository 双表仓储（项目实现）
type Repository interface {
	// uploads
	InsertUpload(ctx context.Context, filename, disk string, expiresAt *time.Time) error
	FindUploadByFilename(ctx context.Context, filename string) (*UploadRecord, error)
	MarkUploaded(ctx context.Context, filename, archiveDirectory string) error // 1→2
	MarkArchived(ctx context.Context, filename string) error                   // 2→3（记 archived_at）
	FindPendingArchive(ctx context.Context, limit int) ([]*UploadRecord, error)
	FindExpiredUploads(ctx context.Context, limit int, grace time.Duration) ([]*UploadRecord, error) // status=3 且 archived_at+grace<now
	DeleteUpload(ctx context.Context, filename string) error
	// attachments
	InsertAttachment(ctx context.Context, a *AttachmentRecord) error
	FindAttachmentByHash(ctx context.Context, hash string) (*AttachmentRecord, error)
	FindAttachmentByFilename(ctx context.Context, filename string) (*AttachmentRecord, error)
	ListAttachments(ctx context.Context, limit int) ([]*AttachmentRecord, error)
	DeleteAttachment(ctx context.Context, id string) error
}

// BusinessHook 业务钩子（项目实现）
type BusinessHook interface {
	// RenameFilename 归档去重命中时通知业务表改名（old→existing；uploads 行不改名）
	RenameFilename(ctx context.Context, oldFilename, newFilename string) error
	// ShouldDeleteAttachment AttachmentCleanup 逐行询问
	ShouldDeleteAttachment(ctx context.Context, a *AttachmentRecord) bool
	// IsPublicArchiveDirectory 归档目录是否公有读
	IsPublicArchiveDirectory(ctx context.Context, archiveDirectory string) bool
}

// Service 附件服务
type Service struct {
	Repo          Repository
	Hook          BusinessHook
	Store         storage.Storage
	TempDirectory string // temp 目录
	PresignExpire int    // 预签名有效期秒
	DiskName      string
}

// RequestUploadURL 申请直传：uuid 文件名 + 票据 + uploads status=1 行
func (s *Service) RequestUploadURL(ctx context.Context, ext string, minSize, maxSize int64) (*storage.UploadTicket, error) {
	filename := generateFilename(ext)
	ticket, err := s.Store.UploadTicket(ctx, s.TempDirectory, filename, s.PresignExpire, minSize, maxSize)
	if err != nil {
		return nil, err
	}
	var expires *time.Time
	if s.PresignExpire > 0 {
		t := time.Now().Add(time.Duration(s.PresignExpire) * time.Second)
		expires = &t
	}
	if err := s.Repo.InsertUpload(ctx, filename, s.DiskName, expires); err != nil {
		return nil, err
	}
	return ticket, nil
}

// Commit 业务提交：uploads 行 1→2 并写 archive_directory
func (s *Service) Commit(ctx context.Context, filename, archiveDirectory string) error {
	upload, err := s.Repo.FindUploadByFilename(ctx, filename)
	if err != nil {
		return err
	}
	if upload == nil {
		return exception.New(exception.CodeAttachmentStoreError, "附件 uploads 记录不存在，无法提交: "+filename)
	}
	return s.Repo.MarkUploaded(ctx, filename, archiveDirectory)
}

// ResolveAccessURL 双表读取：uploads 优先（temp 私有预签名）→ attachments（isPublic 直链/预签名）
func (s *Service) ResolveAccessURL(ctx context.Context, filename string) (string, error) {
	if upload, err := s.Repo.FindUploadByFilename(ctx, filename); err == nil && upload != nil {
		key := s.TempDirectory + "/" + filename
		return s.Store.AccessURL(ctx, key, s.PresignExpire, "")
	}
	if a, err := s.Repo.FindAttachmentByFilename(ctx, filename); err == nil && a != nil {
		key := a.ArchiveDirectory + "/" + a.DatePartition + "/" + filename
		if s.Hook != nil && s.Hook.IsPublicArchiveDirectory(ctx, a.ArchiveDirectory) {
			return s.Store.PublicBase() + "/" + key, nil
		}
		return s.Store.AccessURL(ctx, key, s.PresignExpire, "")
	}
	return "", exception.New(exception.CodeAttachmentStoreError, "附件不存在: "+filename)
}

// generateFilename uuid v4 hex + .ext
func generateFilename(ext string) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	hexStr := hex.EncodeToString(b)
	if ext != "" {
		if ext[0] == '.' {
			ext = ext[1:]
		}
		return hexStr + "." + ext
	}
	return hexStr
}

var _ = fmt.Sprintf
