# attachment — 附件全生命周期

> 面向项目侧接入者（第 1–4 节）与库维护者（加读第 5 节）。事实来源是 `attachment/` 源码，本文的代码示例、签名、状态机与错误文案均与其逐字一致；若发现不一致，以源码为准并修正本文档。

## 1. 组件定位

附件全生命周期引擎（对应 PHP Foundation/Attachment）：**前端直传 → commit → 归档 md5 去重 → 双清理 → 双表读取兜底**。组件编排流程与状态机，两张表（uploads / attachments）与业务钩子由项目实现（`Repository` / `BusinessHook` 接口），对象存储经 `storage` 组件抽象。

生命周期：

```
前端: 申请票据 → POST 表单直传对象存储(temp/)
业务: Commit(filename)            uploads: 1 → 2（写 archive_directory）
归档: Archiver.ArchiveBatch       md5 去重命中 → 业务表改名，不 copy
                                 未命中 → copy 到 {archiveDirectory}/{Y-m} 插 attachments
                                 uploads: 2 → 3
读取: Service.ResolveAccessURL    uploads 优先（temp 预签名）
                                 → attachments（公有目录直链 / 私有预签名）
清理: UploadCleaner               status=3 且过 grace 期：删 temp 物理文件 + 删行
      AttachmentCleaner           逐行问业务钩子，仅删确认无引用行
```

## 2. 快速接入

### ① 项目实现两个接口

```go
// Repository 双表仓储（uploads / attachments 行操作，项目自己的 DB 访问层）
type repo struct{}

func (r *repo) InsertUpload(ctx context.Context, filename, disk string, expiresAt *time.Time) error { /* 项目实现 */ }
// ……接口全量方法见下表

// BusinessHook 业务钩子（去重改名 / 删除确认 / 目录可见性，项目语义）
type hook struct{}

func (h *hook) RenameFilename(ctx context.Context, oldFilename, newFilename string) error { /* 业务表改名 */ }
func (h *hook) ShouldDeleteAttachment(ctx context.Context, a *attachment.AttachmentRecord) bool { /* 引用审计 */ return false }
func (h *hook) IsPublicArchiveDirectory(ctx context.Context, archiveDirectory string) bool { /* 公有读判定 */ return false }
```

### ② 直传 + 提交 + 读取（在线链路）

```go
svc := &attachment.Service{
	Repo:          &repo{},
	Hook:          &hook{},
	Store:         store,        // storage.Storage 实现（见 docs/storage.md）
	TempDirectory: "temp",       // temp 目录
	PresignExpire: 3600,         // 预签名有效期秒
	DiskName:      "public",     // uploads 行 disk 字段值
}

// 前端申请直传：返回 uuid 文件名 + 票据（POST policy 表单）；uploads 插 status=1 行
ticket, err := svc.RequestUploadURL(ctx, "png", 1, 10<<20)

// 业务表单提交时：uploads 1→2 并写归档目录（记录不存在返回
// 「附件 uploads 记录不存在，无法提交: <filename>」）
err = svc.Commit(ctx, filename, "avatars")

// 双表读取兜底：uploads 命中 → temp 预签名；attachments 命中 → 公有直链或预签名
url, err := svc.ResolveAccessURL(ctx, filename) // 均未命中返回「附件不存在: <filename>」
```

### ③ 归档与清理（离线链路，装配 worker 周期跑，PeriodicWorker/ProcWorker 均可）

```go
archiver := &attachment.Archiver{Repo: &repo{}, Hook: &hook{}, Store: store, TempDirectory: "temp"}
uploadCleaner := &attachment.UploadCleaner{Repo: &repo{}, Store: store, TempDirectory: "temp"}
attachCleaner := &attachment.AttachmentCleaner{Repo: &repo{}, Hook: &hook{}, Store: store}

// 统一经 RunEnabled 注册，环境变量 ENABLE_WORKERS（逗号分隔白名单）控制编成
err := worker.RunEnabled(ctx,
	worker.NewProcWorker("attachment-archive", time.Minute, func(ctx context.Context) error {
		_, _, err := archiver.ArchiveBatch(ctx, 100) // (归档数, 去重数, err)
		return err
	}),
	worker.NewProcWorker("attachment-cleanup", time.Minute, func(ctx context.Context) error {
		if _, err := uploadCleaner.CleanupBatch(ctx, 100, 24*time.Hour); err != nil {
			return err
		}
		_, err := attachCleaner.CleanupBatch(ctx, 100)
		return err
	}),
)
```

## 3. 参数语义

| 字段 | 位置 | 语义 |
| --- | --- | --- |
| `TempDirectory` | Service / Archiver / UploadCleaner | 直传暂存目录（对象存储 key 前缀），三处须一致 |
| `PresignExpire` | Service | 预签名 URL 有效期秒；也用作 uploads 行 expires_at |
| `DiskName` | Service | 写进 uploads 行 disk 字段的盘名（项目自由语义） |
| `grace` | UploadCleaner.CleanupBatch 参数 | status=3 行的保留期（archived_at + grace < now 才清理）——去重改名后新旧文件名并存的过渡窗口 |
| `limit` | 各 Batch 参数 | 单批处理行数上限 |

## 4. 契约

**uploads 状态机**：`1（已发Ticket待上传）→ 2（业务已 commit，写 archive_directory）→ 3（已归档）`，常量 `UploadStatusPending / UploadStatusUploaded / UploadStatusArchived`。

**归档去重语义**：Head 取对象 ETag 构造 `md5:{etag}` 哈希——命中已有 attachments 行时**只调 `RenameFilename(old, existing)` 通知业务表改名**，不 copy 对象、uploads 行不改名；未命中 copy 到 `{archiveDirectory}/{Y-m}` 分区并插 attachments 行。两条路径最终 uploads 都转 status=3。

**grace 语义**：去重改名后的过渡期内 uploads 行仍在 → 新旧 filename 均可 resolve；grace 尽后行被清理 → 旧 filename 返回未找到。

OSS `AccessURL` 统一按桶域名签名，最终访问 URL 走读取域名链；CNAME/CDN 私有读取必须桶域名回源并完整透传路径/query（详见 [storage CDN 配置](storage.md)）。临时附件与私有归档直接使用该存储契约，公有归档继续走 `PublicBase()/key`。v0.15.0 升级时删除旧读取签名开关配置及消费方临时包装。

**读取兜底顺序**：uploads 优先（temp 私有预签名）→ attachments（`IsPublicArchiveDirectory` 判定：公有目录直链 `PublicBase()/key`，私有目录预签名）。

**清理边界**：UploadCleaner 先删 temp 物理文件（幂等，删除失败跳过该行下轮再试）再删行；AttachmentCleaner 只删行不删物理文件——对象存储低成本，引用审计兜底，需要物理删除的项目自行扩展。

## 5. FAQ

**Q：前端直传失败/放弃，temp 里的垃圾文件怎么清？**

uploads 行停在 status=1（未 commit），不进归档链路；expires_at 到点后由项目按需清理（UploadCleaner 只清 status=3）。temp 目录的生命周期策略建议在对象存储侧配（如 7 天自动过期）。

**Q：Commit 时传了不存在的 filename？**

返回错误「附件 uploads 记录不存在，无法提交: \<filename\>」——通常是前端伪造 filename 或 uploads 行已被清理，按业务异常处理。

**Q：同一文件被两个用户上传，归档后谁的文件名有效？**

先归档者不变；后归档者上传时业务表被 RenameFilename 改指向已有文件名。grace 期内旧文件名仍可读（uploads 行还在），前端已展示的 URL 不会立刻失效。

**Q：Repository 接口有多少方法要实现？**

uploads 侧 7 个（InsertUpload / FindUploadByFilename / MarkUploaded / MarkArchived / FindPendingArchive / FindExpiredUploads / DeleteUpload）+ attachments 侧 5 个（InsertAttachment / FindAttachmentByHash / FindAttachmentByFilename / ListAttachments / DeleteAttachment）。全部是单行 CRUD 语义，项目 DB 层直接翻译 SQL 即可。

行 ID（`UploadRecord.ID` / `AttachmentRecord.ID`）为 uuidv7 文本（36 字符带连字符，项目侧 PG uuid 列经驱动扫描而来），`DeleteAttachment(ctx, id string)` 入参即该 id。

**Q：能不配 BusinessHook 吗？**

Service / Archiver 的 Hook 为 nil 时跳过钩子调用（去重命中只标 status=3 不通知改名）；AttachmentCleaner 的 Hook 为 nil 时永不删行。生产环境建议必配——去重改名不通知业务表会造成悬挂引用。

## 6. 维护者注记

**单批函数而非常驻循环**：归档/清理以 ArchiveBatch / CleanupBatch 单批函数提供，进程调度由项目用 worker 周期装配（PeriodicWorker/ProcWorker 均可），经 RunEnabled 注册、ENABLE_WORKERS 控制编成——组件不内置进程模型，与 worker 组件正交组合。

**uuid v4 文件名**：`generateFilename` 手写 uuid v4（rand 16 字节 + 版本/变体位修正 + hex），不引外部 uuid 库——文件名只需全局唯一不苛求合规格式。

**测试形态**：`attachment/attachment_test.go` 包内五用例——内存双表仓储（memRepo）+ 对象存储 fake（fakeStore）+ 业务钩子 fake 全链验证：直传→commit→归档→去重改名→清理→双表读取。storage 层真 MinIO 集成在 `tests/storage_minio_test.go`（见 docs/storage.md）。

**改动规矩**：uploads 状态机数值（1/2/3）、md5 哈希格式、grace 语义是对外契约；错误文案前缀是对外可观察行为，改动须走 change 流程并同步本文档。

### 固定 key 直传（v0.15.0，#156）

继续使用 `Service.RequestUploadURL(ctx context.Context, ext string, minSize, maxSize int64) (*storage.UploadTicket, error)`，无需新入口。生成随机文件名后，OSS/S3 adapter 的原 `UploadTicket` 直接把完整 `TempDirectory/filename` 作为 eq 条件签名，随后照旧写 uploads status=1、DiskName 与期限。

未过期票据允许同 key 重传，归档时使用当前最终内容。该行为不保证 Verify 与归档读取的是同一份内容，项目须协调重传和归档时机。原过期和大小约束保留。v0.15.0 删除 `RequestStrictUploadURL`，没有严格/普通模式切换；桶版本状态无需查询。旧目录票据仍在其有效期内生效，迁移和安全边界见 [storage](storage.md)。
