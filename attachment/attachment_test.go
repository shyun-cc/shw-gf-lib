package attachment

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shyun-cc/shw-gf-lib/storage"
)

// ---- 测试替身 ----

// memRepo 内存双表仓储
type memRepo struct {
	mu          sync.Mutex
	uploads     map[string]*UploadRecord
	attachments map[string]*AttachmentRecord // key: filename
	nextID      int                          // 模拟 uuidv7 生成序号
}

func newMemRepo() *memRepo {
	return &memRepo{uploads: map[string]*UploadRecord{}, attachments: map[string]*AttachmentRecord{}}
}

// fakeUUIDv7 生成 36 字符 uuidv7 形态文本（仅测试替身，保证带连字符长度 36）
func fakeUUIDv7(seq int) string {
	return fmt.Sprintf("0192a1f0-0000-7000-8000-%012x", seq)
}

func (r *memRepo) InsertUpload(_ context.Context, filename, disk string, expires *time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.uploads[filename] = &UploadRecord{Filename: filename, Disk: disk, Status: UploadStatusPending, ExpiresAt: expires}
	return nil
}

func (r *memRepo) FindUploadByFilename(_ context.Context, filename string) (*UploadRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if u, ok := r.uploads[filename]; ok {
		cp := *u
		return &cp, nil
	}
	return nil, nil
}

func (r *memRepo) MarkUploaded(_ context.Context, filename, dir string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.uploads[filename].Status = UploadStatusUploaded
	r.uploads[filename].ArchiveDirectory = dir
	return nil
}

func (r *memRepo) MarkArchived(_ context.Context, filename string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	u := r.uploads[filename]
	u.Status = UploadStatusArchived
	now := time.Now()
	u.ArchivedAt = &now
	return nil
}

func (r *memRepo) FindPendingArchive(_ context.Context, limit int) ([]*UploadRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*UploadRecord
	for _, u := range r.uploads {
		if u.Status == UploadStatusUploaded {
			cp := *u
			out = append(out, &cp)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (r *memRepo) FindExpiredUploads(_ context.Context, limit int, grace time.Duration) ([]*UploadRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*UploadRecord
	for _, u := range r.uploads {
		if u.Status == UploadStatusArchived && u.ArchivedAt != nil && time.Since(*u.ArchivedAt) > grace {
			cp := *u
			out = append(out, &cp)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (r *memRepo) DeleteUpload(_ context.Context, filename string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.uploads, filename)
	return nil
}

func (r *memRepo) InsertAttachment(_ context.Context, a *AttachmentRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	a.ID = fakeUUIDv7(r.nextID)
	r.attachments[a.Filename] = a
	return nil
}

func (r *memRepo) FindAttachmentByHash(_ context.Context, hash string) (*AttachmentRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, a := range r.attachments {
		if a.Hash == hash {
			cp := *a
			return &cp, nil
		}
	}
	return nil, nil
}

func (r *memRepo) FindAttachmentByFilename(_ context.Context, filename string) (*AttachmentRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if a, ok := r.attachments[filename]; ok {
		cp := *a
		return &cp, nil
	}
	return nil, nil
}

func (r *memRepo) ListAttachments(_ context.Context, limit int) ([]*AttachmentRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*AttachmentRecord
	for _, a := range r.attachments {
		cp := *a
		out = append(out, &cp)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (r *memRepo) DeleteAttachment(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, a := range r.attachments {
		if a.ID == id {
			delete(r.attachments, k)
		}
	}
	return nil
}

// fakeStore storage 伪实现（计数 + 可编程元数据）
type fakeStore struct {
	mu      sync.Mutex
	objects map[string]storage.ObjectMeta // key → meta（模拟已上传对象）
	copies  []string                      // copy 调用记录
	deletes []string
	copiedN int
}

func (f *fakeStore) seed(key string, meta storage.ObjectMeta) {
	if f.objects == nil {
		f.objects = map[string]storage.ObjectMeta{}
	}
	f.objects[key] = meta
}

func (f *fakeStore) UploadTicket(_ context.Context, _, filename string, _ int, _, _ int64) (*storage.UploadTicket, error) {
	return &storage.UploadTicket{Filename: filename, Method: "POST", FormData: map[string]string{"key": filename}}, nil
}
func (f *fakeStore) UploadURL(_ context.Context, _ string, _ int) (string, error) {
	return "http://put", nil
}
func (f *fakeStore) AccessURL(_ context.Context, _ string, _ int, _ string) (string, error) {
	return "http://presigned-get", nil
}
func (f *fakeStore) Head(_ context.Context, key string) (*storage.ObjectMeta, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m, ok := f.objects[key]; ok {
		return &m, nil
	}
	return nil, nil
}
func (f *fakeStore) Copy(_ context.Context, srcKey, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.copies = append(f.copies, srcKey)
	f.copiedN++
	return nil
}
func (f *fakeStore) Exists(_ context.Context, _ string) (bool, error) { return false, nil }
func (f *fakeStore) Delete(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletes = append(f.deletes, key)
	delete(f.objects, key)
	return nil
}
func (f *fakeStore) PublicBase() string { return "http://public" }

// 操作线桩（storage 接口扩展至 12 方法，fake 补齐保编译；逻辑下批实现）
func (f *fakeStore) Put(context.Context, string, string, io.Reader) error {
	return errors.New("put not implemented")
}
func (f *fakeStore) Get(context.Context, string) (*storage.ObjectMeta, io.ReadCloser, error) {
	return nil, nil, errors.New("get not implemented")
}
func (f *fakeStore) List(context.Context, string, string, int) (*storage.ObjectListing, error) {
	return nil, errors.New("list not implemented")
}
func (f *fakeStore) Internal() storage.Storage { return f }

// fakeHook 业务钩子记录器
type fakeHook struct {
	renames   [][2]string
	shouldDel map[string]bool
	publicDir map[string]bool
}

func (h *fakeHook) RenameFilename(_ context.Context, old, new string) error {
	h.renames = append(h.renames, [2]string{old, new})
	return nil
}
func (h *fakeHook) ShouldDeleteAttachment(_ context.Context, a *AttachmentRecord) bool {
	return h.shouldDel[a.Filename]
}
func (h *fakeHook) IsPublicArchiveDirectory(_ context.Context, dir string) bool {
	return h.publicDir[dir]
}

// ---- 用例 ----

func newFixture() (*memRepo, *fakeStore, *fakeHook) {
	return newMemRepo(), &fakeStore{}, &fakeHook{shouldDel: map[string]bool{}, publicDir: map[string]bool{}}
}

func newService(repo *memRepo, store *fakeStore, hook *fakeHook) *Service {
	return &Service{Repo: repo, Hook: hook, Store: store, TempDirectory: "temp", PresignExpire: 600, DiskName: "oss"}
}

// TestLifecycle_NewFileArchive 新文件：request→commit→archive（copy 调用/attachments 行/status=3）
func TestLifecycle_NewFileArchive(t *testing.T) {
	repo, store, hook := newFixture()
	svc := newService(repo, store, hook)
	ctx := context.Background()

	ticket, err := svc.RequestUploadURL(ctx, ".png", 0, 1024)
	if err != nil {
		t.Fatal(err)
	}
	upload, _ := repo.FindUploadByFilename(ctx, ticket.Filename)
	if upload == nil || upload.Status != UploadStatusPending {
		t.Fatalf("request 后应有 status=1 行: %+v", upload)
	}

	// 模拟前端上传成功（seed temp 对象）
	store.seed("temp/"+ticket.Filename, storage.ObjectMeta{Size: 100, ETag: "md5a", ContentType: "image/png"})

	if err := svc.Commit(ctx, ticket.Filename, "biz/avatar"); err != nil {
		t.Fatal(err)
	}
	upload, _ = repo.FindUploadByFilename(ctx, ticket.Filename)
	if upload.Status != UploadStatusUploaded || upload.ArchiveDirectory != "biz/avatar" {
		t.Fatalf("commit 后应 status=2 且写目录: %+v", upload)
	}

	archiver := &Archiver{Repo: repo, Hook: hook, Store: store, TempDirectory: "temp"}
	archived, deduped, err := archiver.ArchiveBatch(ctx, 10)
	if err != nil || archived != 1 || deduped != 0 {
		t.Fatalf("归档结果不符: %d %d %v", archived, deduped, err)
	}
	if store.copiedN != 1 {
		t.Fatalf("新文件应 copy 一次，实际 %d", store.copiedN)
	}
	att, _ := repo.FindAttachmentByFilename(ctx, ticket.Filename)
	if att == nil || att.Hash != "md5:md5a" || att.DatePartition != time.Now().Format("2006-01") {
		t.Fatalf("attachments 行不符: %+v", att)
	}
	upload, _ = repo.FindUploadByFilename(ctx, ticket.Filename)
	if upload.Status != UploadStatusArchived {
		t.Fatalf("归档后应 status=3: %d", upload.Status)
	}
}

// TestLifecycle_DedupHit 去重命中：不 copy、rename hook 一次、uploads 行 filename 不改
func TestLifecycle_DedupHit(t *testing.T) {
	repo, store, hook := newFixture()
	svc := newService(repo, store, hook)
	ctx := context.Background()

	// 第一个文件（相同内容 md5=x）
	t1, _ := svc.RequestUploadURL(ctx, ".jpg", 0, 0)
	store.seed("temp/"+t1.Filename, storage.ObjectMeta{ETag: "sameMD5", ContentType: "image/jpeg", Size: 50})
	_ = svc.Commit(ctx, t1.Filename, "biz/a")
	archiver := &Archiver{Repo: repo, Hook: hook, Store: store, TempDirectory: "temp"}
	if _, _, err := archiver.ArchiveBatch(ctx, 10); err != nil {
		t.Fatal(err)
	}

	// 第二个文件（相同 md5）
	t2, _ := svc.RequestUploadURL(ctx, ".jpg", 0, 0)
	store.seed("temp/"+t2.Filename, storage.ObjectMeta{ETag: "sameMD5", ContentType: "image/jpeg", Size: 50})
	_ = svc.Commit(ctx, t2.Filename, "biz/b")
	archived, deduped, err := archiver.ArchiveBatch(ctx, 10)
	if err != nil || archived != 1 || deduped != 1 {
		t.Fatalf("去重归档结果不符: %d %d %v", archived, deduped, err)
	}
	if store.copiedN != 1 {
		t.Fatalf("去重命中不应再 copy，copy 计数 %d", store.copiedN)
	}
	if len(hook.renames) != 1 || hook.renames[0][0] != t2.Filename || hook.renames[0][1] != t1.Filename {
		t.Fatalf("rename hook 应一次 old→existing: %v", hook.renames)
	}
	// uploads 行 filename 不改
	u, _ := repo.FindUploadByFilename(ctx, t2.Filename)
	if u == nil || u.Filename != t2.Filename || u.Status != UploadStatusArchived {
		t.Fatalf("uploads 行应保留原名且 status=3: %+v", u)
	}
}

// TestCleanup_Boundary 清理边界四类行
func TestCleanup_Boundary(t *testing.T) {
	repo, store, hook := newFixture()
	ctx := context.Background()

	// status=3 已过 grace（应删）/ status=3 未过 grace（不删）
	mkUpload := func(name string, archivedAgo time.Duration) {
		repo.InsertUpload(ctx, name, "oss", nil)
		store.seed("temp/"+name, storage.ObjectMeta{ETag: "e"})
		repo.MarkUploaded(ctx, name, "d")
		repo.MarkArchived(ctx, name)
		repo.mu.Lock()
		at := time.Now().Add(-archivedAgo)
		repo.uploads[name].ArchivedAt = &at
		repo.mu.Unlock()
	}
	mkUpload("expired.bin", 48*time.Hour)
	mkUpload("fresh.bin", time.Hour)

	uc := &UploadCleaner{Repo: repo, Store: store, TempDirectory: "temp"}
	deleted, err := uc.CleanupBatch(ctx, 10, 24*time.Hour)
	if err != nil || deleted != 1 {
		t.Fatalf("UploadCleanup 应仅删 1 行: %d %v", deleted, err)
	}
	if u, _ := repo.FindUploadByFilename(ctx, "expired.bin"); u != nil {
		t.Fatal("过期行应被删")
	}
	if u, _ := repo.FindUploadByFilename(ctx, "fresh.bin"); u == nil {
		t.Fatal("未过期行应保留")
	}
	if len(store.deletes) != 1 || store.deletes[0] != "temp/expired.bin" {
		t.Fatalf("应删 temp 物理文件: %v", store.deletes)
	}

	// attachments：shouldDel true/false
	repo.InsertAttachment(ctx, &AttachmentRecord{Filename: "del.txt", Hash: "h1"})
	repo.InsertAttachment(ctx, &AttachmentRecord{Filename: "keep.txt", Hash: "h2"})
	hook.shouldDel["del.txt"] = true
	ac := &AttachmentCleaner{Repo: repo, Hook: hook, Store: store}
	deleted2, err := ac.CleanupBatch(ctx, 10)
	if err != nil || deleted2 != 1 {
		t.Fatalf("AttachmentCleanup 应仅删 1 行: %d %v", deleted2, err)
	}
	if a, _ := repo.FindAttachmentByFilename(ctx, "keep.txt"); a == nil {
		t.Fatal("未确认行应保留")
	}
}

// TestResolveAccessURL_GraceDualRead grace 双读与清理后未找到
func TestResolveAccessURL_GraceDualRead(t *testing.T) {
	repo, store, hook := newFixture()
	svc := newService(repo, store, hook)
	ctx := context.Background()

	// 建一个已归档附件（既有 B）
	t1, _ := svc.RequestUploadURL(ctx, ".pdf", 0, 0)
	store.seed("temp/"+t1.Filename, storage.ObjectMeta{ETag: "dedupMD5", ContentType: "application/pdf"})
	_ = svc.Commit(ctx, t1.Filename, "docs")
	archiver := &Archiver{Repo: repo, Hook: hook, Store: store, TempDirectory: "temp"}
	if _, _, err := archiver.ArchiveBatch(ctx, 10); err != nil {
		t.Fatal(err)
	}

	// 去重第二个（A）→ 业务表改名 A→B
	t2, _ := svc.RequestUploadURL(ctx, ".pdf", 0, 0)
	store.seed("temp/"+t2.Filename, storage.ObjectMeta{ETag: "dedupMD5", ContentType: "application/pdf"})
	_ = svc.Commit(ctx, t2.Filename, "docs")
	if _, _, err := archiver.ArchiveBatch(ctx, 10); err != nil {
		t.Fatal(err)
	}

	// grace 期内：A（uploads 仍在）与 B（attachments）均可读
	if _, err := svc.ResolveAccessURL(ctx, t2.Filename); err != nil {
		t.Fatalf("grace 期旧 filename 应可读: %v", err)
	}
	if _, err := svc.ResolveAccessURL(ctx, t1.Filename); err != nil {
		t.Fatalf("既有 filename 应可读: %v", err)
	}

	// grace 过期清理 uploads 行 → 旧 filename 未找到
	uc := &UploadCleaner{Repo: repo, Store: store, TempDirectory: "temp"}
	if _, err := uc.CleanupBatch(ctx, 10, -time.Hour); err != nil { // 负 grace 立即过期
		t.Fatal(err)
	}
	_, err := svc.ResolveAccessURL(ctx, t2.Filename)
	if err == nil || !strings.Contains(err.Error(), "附件不存在") {
		t.Fatalf("清理后旧 filename 应未找到: %v", err)
	}
	// B 仍可读（attachments 兜底）
	if _, err := svc.ResolveAccessURL(ctx, t1.Filename); err != nil {
		t.Fatalf("既有 filename 仍应可读: %v", err)
	}
}

// TestPublicDirectoryAccessURL 公有目录直链 vs 私有预签名
func TestPublicDirectoryAccessURL(t *testing.T) {
	repo, store, hook := newFixture()
	svc := newService(repo, store, hook)
	ctx := context.Background()

	repo.InsertAttachment(ctx, &AttachmentRecord{
		Filename: "pub.png", ArchiveDirectory: "public", DatePartition: "2026-08", Hash: "h",
	})
	hook.publicDir["public"] = true
	u, err := svc.ResolveAccessURL(ctx, "pub.png")
	if err != nil || !strings.HasPrefix(u, "http://public/public/2026-08/pub.png") {
		t.Fatalf("公有目录应直链: %s %v", u, err)
	}

	hook.publicDir["public"] = false
	u2, _ := svc.ResolveAccessURL(ctx, "pub.png")
	if u2 != "http://presigned-get" {
		t.Fatalf("私有目录应预签名: %s", u2)
	}
}

// TestResolveAccessURL_OssBucketSigning 覆盖公有归档、私有归档和临时附件的公共读取入口。
func TestResolveAccessURL_OssBucketSigning(t *testing.T) {
	repo, fake, hook := newFixture()
	svc := newService(repo, fake, hook)
	oss := storage.NewOss("AK_TEST", "SK_TEST", "bucket1", "oss-cn-hangzhou.aliyuncs.com")
	oss.Now = func() time.Time { return time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC) }
	oss.AccelerateDomain = "cdn.example.com"
	svc.Store = oss
	ctx := context.Background()
	repo.InsertAttachment(ctx, &AttachmentRecord{Filename: "private.png", ArchiveDirectory: "private", DatePartition: "2026-11"})
	repo.InsertAttachment(ctx, &AttachmentRecord{Filename: "public.png", ArchiveDirectory: "public", DatePartition: "2026-11"})
	repo.InsertUpload(ctx, "temp.png", "oss", nil)
	hook.publicDir["public"] = true
	for _, tc := range []struct {
		filename, key string
		public        bool
	}{
		{"private.png", "private/2026-11/private.png", false},
		{"public.png", "public/2026-11/public.png", true},
		{"temp.png", "temp/temp.png", false},
	} {
		got, err := svc.ResolveAccessURL(ctx, tc.filename)
		if err != nil {
			t.Fatal(err)
		}
		want := "https://cdn.example.com/" + tc.key
		if !tc.public {
			direct := *oss
			direct.AccelerateDomain = ""
			signed, err := direct.AccessURL(ctx, tc.key, svc.PresignExpire, "")
			if err != nil {
				t.Fatal(err)
			}
			want = strings.Replace(signed, "bucket1.oss-cn-hangzhou.aliyuncs.com", "cdn.example.com", 1)
		}
		if got != want {
			t.Fatalf("%s: got %s want %s", tc.filename, got, want)
		}
	}
}

// TestRequestUploadURLExactKey 验证原对外入口直接获得两个真实 adapter 的固定 key 票据。
func TestRequestUploadURLExactKey(t *testing.T) {
	for name, store := range map[string]storage.Storage{
		"oss": storage.NewOss("AK_TEST", "SK_TEST", "bucket", "oss-cn-hangzhou.aliyuncs.com"),
		"s3":  storage.NewS3("AK_TEST", "SK_TEST", "bucket", "s3.example.com"),
	} {
		t.Run(name, func(t *testing.T) {
			repo := newMemRepo()
			service := &Service{Repo: repo, Store: store, TempDirectory: "temp", PresignExpire: 600, DiskName: "default"}
			ticket, err := service.RequestUploadURL(context.Background(), "png", 1, 1024)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := base64.StdEncoding.DecodeString(ticket.FormData["policy"])
			if err != nil {
				t.Fatal(err)
			}
			var policy struct {
				Conditions []json.RawMessage `json:"conditions"`
			}
			if err := json.Unmarshal(raw, &policy); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, condition := range policy.Conditions {
				var value []string
				if json.Unmarshal(condition, &value) == nil && len(value) == 3 && value[0] == "eq" && value[1] == "$key" && value[2] == "temp/"+ticket.Filename {
					found = true
				}
			}
			record, err := repo.FindUploadByFilename(context.Background(), ticket.Filename)
			if !found || err != nil || record == nil || record.ExpiresAt == nil {
				t.Fatal("original entry lost key binding or persistence")
			}
			count := len(repo.uploads)
			if invalid, err := service.RequestUploadURL(context.Background(), "${filename}", 1, 1024); err == nil || invalid != nil {
				t.Fatal("attachment accepted key template")
			}
			if len(repo.uploads) != count {
				t.Fatal("invalid ticket persisted upload")
			}
		})
	}
}
