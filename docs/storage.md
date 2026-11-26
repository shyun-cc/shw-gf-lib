# storage — 对象存储抽象（oss / s3 双 adapter）

> 面向项目侧接入者（第 1–4 节）与库维护者（加读第 5 节）。事实来源是 `storage/` 源码，本文的代码示例、签名与算法描述均与其逐字一致；若发现不一致，以源码为准并修正本文档。

## 1. 组件定位

对象存储抽象：`Storage` 接口 + **oss（阿里云 OSS）/ s3（S3 兼容：MinIO/腾讯 COS 等）双 adapter**，全部自主复刻签名（零厂商 SDK）。接口三线：**签名线**（票据/预签名/直链前缀，浏览器直传直读）+ **操作线**（Put/Get/Head/Copy/Exists/Delete/List，服务端传输与元数据）+ **视图**（Internal 内网操作视图）。域名模型为双链三线：上传链、读取链各自解析优先域名，操作线默认走 Endpoint。

| adapter | 构造（region 已移除，由 endpoint 推导） | 签名体系 | 适用 |
| --- | --- | --- | --- |
| `NewOss(ak, sk, bucket, endpoint)` | 四参 | OSS4-HMAC-SHA256 V4 | 阿里云 OSS |
| `NewS3(ak, sk, bucket, endpoint)` | 四参 | AWS SigV4 | MinIO / AWS S3 / 腾讯 COS（需 virtual-hosted）等 S3 兼容 |

构造后按需配置可选域名字段（均导出字段，直接赋值）：

| 字段 | 语义 |
| --- | --- |
| `Endpoint` | 服务商标准端点（如 `https://oss-cn-hangzhou.aliyuncs.com`）：上传/读取链的兜底域名、操作线默认域名、签名 region 的唯一推导源——CNAME/CDN 域名不配这里（见 FAQ） |
| `AccessDomain` | 可选：CNAME 直达/CDN 域名，为完整 host 不拼 bucket 前缀；上传链第一优先、读取链第二优先 |
| `AccelerateDomain` | 可选：读取链最高优先域名（CDN 等），完整 host 不拼 bucket；只进读取线不进上传线 |
| `Protocol` | 产物 URL 协议，唯一协议事实源（空默认 https）：Endpoint 与各域名自带的 `http(s)://` 前缀一律剥掉以此为准 |
| `InternalEndpoint` | 可选：内网操作端点（完整 host），仅 `Internal()` 视图的操作线使用，不参与 region 推导 |
| `AddressingStyle`（仅 s3） | 寻址形态：`""` 或 `"path-style"`（默认，bucket 在路径，MinIO 原生）/ `"virtual-hosted"`（bucket 进 host，腾讯 COS 等需要） |

## 2. 快速接入

### ① 构造与基本用法（MinIO 示例）

```go
package main

import (
	"context"
	"strings"

	"github.com/shyun-cc/shw-gf-lib/storage"
)

func main() {
	// endpoint 形如 http://127.0.0.1:9000；region 无需配置（由 endpoint 推导 + us-east-1 兜底，见契约节）
	var s storage.Storage = storage.NewS3("<AK>", "<SK>", "bucket", "http://127.0.0.1:9000")
	ctx := context.Background()

	// 前端直传票据（POST policy 表单直传：目录/文件名/有效期秒/最小最大字节数）
	ticket, err := s.UploadTicket(ctx, "temp", "a1b2c3d4e5f6.png", 3600, 1, 10<<20)
	// ticket.UploadURL（表单提交目标）/ ticket.Method（POST）/ ticket.FormData（表单字段全集）
	// 前端按 FormData 逐字段拼 multipart form-data，file 字段放最后，POST 到 UploadURL
	_ = ticket

	// PUT 预签名直传（无大小限制形态）
	putURL, err := s.UploadURL(ctx, "temp/a1b2c3d4e5f6.png", 3600)
	_ = putURL // 前端直接 PUT 文件到该 URL

	// 操作线：元数据
	meta, err := s.Head(ctx, "temp/a1b2c3d4e5f6.png") // 不存在返回 nil, nil
	_ = meta // meta.Size / meta.ETag / meta.ContentType
	exists, err := s.Exists(ctx, "temp/a1b2c3d4e5f6.png")
	_ = exists
	err = s.Copy(ctx, "temp/a1b2c3d4e5f6.png", "avatars/2026-08/a1b2c3d4e5f6.png")
	err = s.Delete(ctx, "temp/a1b2c3d4e5f6.png") // 幂等

	// 操作线：服务端传输与列举
	err = s.Put(ctx, "exports/report.csv", "text/csv", strings.NewReader("id,name\n1,foo\n"))
	getMeta, body, err := s.Get(ctx, "exports/report.csv") // 不存在返回 nil, nil, nil；body 由调用方关闭
	_ = getMeta
	if body != nil {
		defer body.Close()
	}
	listing, err := s.List(ctx, "exports/", "", 100)
	_ = listing // listing.Objects / listing.IsTruncated / listing.NextMarker

	// 读访问 URL（读取链域名：AccelerateDomain > AccessDomain > Endpoint；公有桶直链见 attachment 的 PublicBase 用法）
	getURL, err := s.AccessURL(ctx, "avatars/2026-08/a1b2c3d4e5f6.png", 3600, "")
	_ = getURL
	_ = err // 示例聚焦调用形态，错误处理由项目自定
}
```

### ② 域名配置（CNAME / CDN）

```go
o := storage.NewOss("<AK>", "<SK>", "bucket", "https://oss-cn-hangzhou.aliyuncs.com")
o.AccessDomain = "https://up-cdn.example.com"      // 上传链优先：票据 UploadURL 与 PUT 预签名走它
o.AccelerateDomain = "https://img-cdn.example.com" // 读取链最高优先：AccessURL 与 PublicBase 走它
// 只配一个 CDN 域名兼做上传与读取加速时配 AccessDomain 即可（读取链无 AccelerateDomain 时落到它）

// 腾讯 COS（s3 adapter 需显式 virtual-hosted 寻址）
c := storage.NewS3("<AK>", "<SK>", "bucket-1250000000", "https://cos.ap-guangzhou.myqcloud.com")
c.AddressingStyle = "virtual-hosted"
```

### ③ Internal() 内网操作视图（导出 Excel 场景：内网写、公网读）

```go
// 装配（同桶双端点）：Endpoint 出签名线产物，InternalEndpoint 承载服务端读写
o := storage.NewOss("<AK>", "<SK>", "bucket", "https://oss-cn-hangzhou.aliyuncs.com")
o.InternalEndpoint = "https://oss-cn-hangzhou-internal.aliyuncs.com"

// 导出 Excel：服务端内网写入（不经公网带宽），用户拿公网预签名下载
func exportReport(ctx context.Context, o *storage.OssStorage) (string, error) {
	in := o.Internal() // 内网视图：操作线七方法走 InternalEndpoint，签名线产物与原实例完全一致

	var xlsx []byte // = 项目侧导出逻辑生成的 Excel 内容
	if err := in.Put(ctx, "exports/2026-08/report.xlsx",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		bytes.NewReader(xlsx)); err != nil {
		return "", err
	}

	// 下载 URL 下发给前端：AccessURL 是签名线（读取链域名），不受内网视图影响
	return o.AccessURL(ctx, "exports/2026-08/report.xlsx", 600,
		`attachment; filename="2026-08-report.xlsx"`)
}
```

attachment 组件（docs/attachment.md）在本接口之上编排附件全生命周期。

## 3. 契约

**接口 12 方法（三线）**：

| 线 | 方法（签名） |
| --- | --- |
| 签名线（4） | `UploadTicket(ctx, directory, filename string, expireSeconds int, minSize, maxSize int64) (*UploadTicket, error)` / `UploadURL(ctx, key string, expireSeconds int) (string, error)` / `AccessURL(ctx, key string, expireSeconds int, contentDisposition string) (string, error)`（contentDisposition 非空时作为 response-content-disposition 进签名 query，服务端下发下载文件名等响应头；值为完整透传，RFC 5987 编码归调用方） / `PublicBase() string` |
| 操作线（7） | `Put(ctx, key, contentType string, body io.Reader) error` / `Get(ctx, key string) (*ObjectMeta, io.ReadCloser, error)` / `Head(ctx, key string) (*ObjectMeta, error)` / `Copy(ctx, srcKey, dstKey string) error` / `Exists(ctx, key string) (bool, error)` / `Delete(ctx, key string) error` / `List(ctx, prefix, after string, max int) (*ObjectListing, error)` |
| 视图（1） | `Internal() Storage` |

- 两个 adapter 的 `UploadTicket` 共用文件名扩展名映射：`.png`→`image/png`、`.jpg`/`.jpeg`→`image/jpeg`、`.gif`→`image/gif`、`.webp`→`image/webp`、`.mp4`→`video/mp4`、`.mov`→`video/quicktime`、`.m4v`→`video/x-m4v`、`.webm`→`video/webm`、`.mp3`→`audio/mpeg`、`.pdf`→`application/pdf`、`.zip`→`application/zip`；其余返回 `application/octet-stream`。OSS 票据的 `ContentType`、`x-oss-content-type` 表单字段和 policy 的同名条件使用同一值；S3 票据在 `ContentType` 返回该值。
- Head 不存在返回 `nil, nil`；Get 不存在返回 `nil, nil, nil`（与 Head 语义对齐）；Get 的 body 由调用方关闭。
- Delete 幂等（404/204/200 都算成功）；List 的 `max<=0 或 >1000` 取 1000（单页上限），`IsTruncated` 时以 `NextMarker` 传回下一轮 `after` 翻页。

**域名三线优先级链**（域名均为完整 host、不拼 bucket 前缀——CNAME 形态；`Endpoint` 兜底形态 oss 为 `bucket.endpoint`，s3 按 AddressingStyle 展开）：

| 线 | 覆盖方法 | 优先级 |
| --- | --- | --- |
| 上传链 | UploadTicket 的 UploadURL、UploadURL | `AccessDomain > Endpoint` |
| 读取链 | AccessURL、PublicBase | `AccelerateDomain > AccessDomain > Endpoint` |
| 操作线 | Put/Get/Head/Copy/Exists/Delete/List | 默认 Endpoint（oss 固定 `bucket.endpoint`，管理面不走 CNAME/CDN；s3 按 AddressingStyle 展开）；`Internal()` 视图切到 InternalEndpoint（s3 同按 AddressingStyle 展开） |

- Internal 视图未配置 InternalEndpoint 时操作线返回错误不发请求（不静默回落公网）：oss 报「oss: InternalEndpoint 未配置，内网操作视图不可用」、s3 报「s3: InternalEndpoint 未配置」；视图上再调 `Internal()` 幂等返回自身。
- 签名线四方法（UploadTicket/UploadURL/AccessURL/PublicBase）不受 Internal 视图影响——视图上调用与原实例产物完全一致，永不使用内网域名。
- `Protocol` 是唯一协议事实源：各域名自带 `http(s)://` 前缀一律剥掉。oss 未配置默认 https；s3 未配置时先看当前链域名自带前缀，再看 Endpoint 是否带 `http://` 前缀（存量 MinIO http 端点兼容），否则默认 https。
- OSS `AccessURL` 一律按公网 `bucket.endpoint` 签名，不使用 InternalEndpoint；最终 URL 仍按读取链，保留转义路径与完整签名 query（含 contentDisposition 与有效期）。上传票据、PUT、公有直链及操作线保持各自原契约；attachment 临时/私有读取通过同一 `AccessURL` 生效。
- `PublicBase` 随读取链：配置 AccelerateDomain/AccessDomain 后 attachment 公有直链自动获得 CDN 域名。

**region 推导（deriveRegion，双 adapter 同源）**：region 不再是配置项，一律从 Endpoint 推导（host 先剥协议前缀/路径/端口），推导不出 `us-east-1` 兜底；推导结果同时用于四层密钥派生与 scope 构造：

| Endpoint 形态 | 推导结果 |
| --- | --- |
| `oss-{region}.aliyuncs.com` / `oss-{region}-internal.aliyuncs.com` | region（剥 `oss-` 前缀与 `-internal` 尾缀） |
| `s3.{region}.*` / `cos.{region}.*` | region（取第二段） |
| `s3-{region}.*` | region（首段剥 `s3-` 前缀） |
| `s3.amazonaws.com`（AWS 全局端点） | `us-east-1` |
| 其余（IP、带端口、未知域名） | `us-east-1` 兜底 |

**oss adapter（OSS4-HMAC-SHA256 V4）**：

- 预签名 URL 五参数（`x-oss-credential` / `x-oss-date` / `x-oss-expires` / `x-oss-signature-version` / `x-oss-additional-headers=host`），payload 值 `UNSIGNED-PAYLOAD`，host 单头签名；
- PostObject 票据：policy JSON（expiration + conditions，含 `eq $key = directory/filename` 与 `eq $x-oss-content-type`）base64 后 HMAC 签名；FormData 字段全集 `key` / `policy` / `x-oss-signature-version` / `x-oss-credential` / `x-oss-date` / `x-oss-signature` / `success_action_status` / `x-oss-content-type`；
- 操作线头签名（Authorization 头），签名头集基础三头 host / x-oss-content-sha256 / x-oss-date，Put 的 `Content-Type`、Copy 的 `x-oss-copy-source` 按需并入；
- **CanonicalURI 恒为 `/{bucket}/{key}`**（key 为空的 List 即 `/{bucket}/`），与请求实际走虚拟主机风格还是 CNAME 域名无关——OSS 服务端一律按带 bucket 的路径验签；请求 URL 路径段仍只有 key（bucket 在 host 里）。签名线与操作线同规则；
- **Authorization 的签名头列表字段名为 `AdditionalHeaders`**（不是 AWS 风格的 `SignedHeaders`）——OSS V4 header 签名只认该字段。

**s3 adapter（AWS SigV4）**：预签名 PUT/GET（`X-Amz-*` 参数族，UNSIGNED-PAYLOAD、host 单头）；操作线头签名（Authorization 头，签名头集固定三头）；POST policy 票据与 OSS 票据同构（字段族映射 `x-oss-*`→`x-amz-*`、`OSS4`→`AWS4`、`aliyun_v4_request`→`aws4_request`）：

- FormData 六字段：`key` / `policy` / `x-amz-algorithm` / `x-amz-credential` / `x-amz-date` / `x-amz-signature`（固定字段形态；完整 key 拒绝与重传行为由集成回归验证）；
- `key = {directory}/{filename}`（含目录前缀），conditions 必含 `eq $key = directory/filename`；表单提交目标是桶根（path-style 即 `host/{bucket}`，virtual-hosted/CNAME 即域名根）；
- `minSize`/`maxSize` 任一大于 0 时加 `content-length-range` 条件（min>0 且 max<=0 时上限 5GB 兜底，oss 版同逻辑）。

**s3 寻址双形态（AddressingStyle，寻址进签名）**：URL 构造与 CanonicalRequest 共用同一寻址产物——形态选错两处仍一致，但与服务端期望不符即 SignatureDoesNotMatch：

| 形态 | host | 路径（key 为空即桶根） |
| --- | --- | --- |
| path-style（默认，`""` 或 `"path-style"`） | Endpoint host（保留非标端口，MinIO `:9000` 属签名 host 一部分） | `/{bucket}[/{key}]` |
| virtual-hosted（`"virtual-hosted"`） | `{bucket}.{Endpoint host}` | `/{key}` |

AccessDomain/AccelerateDomain 命中时恒为 CNAME 形态：host 为域名本体、路径不拼 bucket（等同 virtual-hosted 形态，与 AddressingStyle 无关）；InternalEndpoint 与 Endpoint 同按 AddressingStyle 展开。

**ObjectMeta**：Size / ETag（非分片上传时为内容 md5 hex——attachment 归档去重依赖此语义）/ ContentType。

**四层密钥派生**（共享骨架）：`prefix+SK → date → region → service → request`（oss 前缀 `aliyun_v4`/service `oss`，s3 前缀 `AWS4`/service `s3`）。

## 4. FAQ

**Q：CDN 怎么接？**

CDN 域名配到 `AccessDomain` / `AccelerateDomain`：上传链优先 AccessDomain，读取链优先 AccelerateDomain；PublicBase 随读取链。OSS `AccessURL` 始终按公网 `bucket.endpoint` 签名，最终链接按读取链访问。CNAME/CDN 私有 GET 链路必须配置桶域名回源 Host，且完整透传对象路径、签名 query 和响应头参数；仅保留自定义域名 Host 的旧回源策略不能继续用于该预签名链路。

库不修改或自动识别 CDN/OSS 配置。上传域名若也经 CDN，仍按原上传签名契约单独确认。私有 URL 的鉴权与缓存 key、query 转发、签名过期后的缓存行为由 CDN 策略决定，不能从库侧推断缓存收益或凭 URL 过期推断边缘缓存已失效。

v0.15.0 迁移：删除 `ReadSignWithBucketHost` 字段配置及消费方临时签名包装；原 `AccessURL` 直接提供桶域名签名。曾依赖自定义域名签名的消费方须先把私有 GET 链路改为桶域名回源，并在目标环境验证完整 query 转发。未配置读取域名时仍生成桶直连 URL。

**Q：自定义域名为什么是事实必需？**

阿里云 OSS 默认域名（`bucket.oss-{region}.aliyuncs.com`）对网页文件强制 `Content-Disposition: attachment` 下载头——浏览器里图片/PDF 无法在线预览、一律触发下载。在线预览依赖自定义域名（CNAME 绑定到桶）配到 AccessDomain/AccelerateDomain，含在线预览诉求的桶在规划期就要安排自定义域名。

**Q：region 去哪了？**

构造不再收 region 参数：一律 `deriveRegion(endpoint)` 推导（规则表见契约节），推导不出 `us-east-1` 兜底（MinIO 实测不校验 region，任意合法值均可）。配套契约：**Endpoint 必须配服务商标准端点，CNAME/CDN 域名走 AccessDomain/AccelerateDomain**——把 CNAME/CDN 域名误填进 Endpoint 后，真云桶会推导出错误 region（us-east-1 兜底）产出 SignatureDoesNotMatch，难排查。

**Q：兼容矩阵——各家服务怎么配？**

| 服务 | adapter | 寻址配置 | 说明 |
| --- | --- | --- | --- |
| MinIO | s3 | path-style（默认，原生形态） | endpoint 传 `http://host:port`；region 由兜底取 us-east-1 |
| AWS S3 | s3 | virtual-hosted | endpoint 形如 `s3.{region}.amazonaws.com`（中国区 `.com.cn`） |
| 腾讯 COS | s3 | **需显式 `AddressingStyle = "virtual-hosted"`** | endpoint 形如 `cos.{region}.myqcloud.com`；默认 path-style 形态与 COS 请求格式不符（早期文档宣称 COS 直接可用的表述按寻址实况修正） |
| CNAME/CDN 域名 | oss / s3 | 域名为完整 host，等同 virtual-hosted 形态 | 配 AccessDomain/AccelerateDomain，不配进 Endpoint |

**Q：S3 UploadTicket 前端对接有什么变化？（v0.8.0 BREAKING）**

S3 票据从「PUT 预签名 URL」升级为「POST policy 表单」（与 OSS 票据同构，并修复了旧形态丢 directory 前缀的缺陷）：前端从 PUT 文件到预签名 URL，改为按 `ticket.FormData` 六字段（key/policy/x-amz-algorithm/x-amz-credential/x-amz-date/x-amz-signature）逐字段拼 multipart form-data、`file` 字段放最后、POST 到 `ticket.UploadURL`。原对接 PUT 形态的前端代码需要改造；OSS 侧表单形态不变。

**Q：UploadTicket 与 UploadURL 选哪个？**

Ticket（POST policy）能带 content-length-range 大小限制与 完整 key 约束，浏览器表单提交形态；UploadURL（PUT 预签名）简单但无服务端大小约束。附件场景用 Ticket（attachment 组件的 RequestUploadURL 就是它）。

**Q：ETag 什么情况下不是 md5？**

分片上传时 ETag 是分片组合哈希——本库链路不做分片（直传单文件），attachment 去重安全；若未来引入分片上传需重新评估去重哈希来源。

## 5. 维护者注记

**签名自主复刻的取证方式**：oss/s3 的签名全部**固定时间注入独立重算取证**——单测把 Now 固定后手工重算签名逐字段比对（对齐官方文档示例向量），域名优先级矩阵、票据向量、deriveRegion 表驱动各自独立单测。tests/storage_minio_test.go 再用真 MinIO 全链验证（MINIO_ENDPOINT/MINIO_AK/MINIO_SK/MINIO_BUCKET 环境变量注入、缺失 Skip），覆盖：POST policy 票据正例（上传成功 + 内容回读）与负例（篡改完整 key 被拒绝）、操作线链（Put→Get→List 分页→Head→Copy→Delete→Exists）、双端点内网视图（同桶双可达地址，断言视图操作线走内网端点、签名线产物与原实例一致、未配置 InternalEndpoint 的视图操作报错）。

**OSS 真实云验证边界**：oss adapter 有固定时间签名向量单测，但本库不再维护或要求真实阿里云 OSS 冒烟（issue #92）；这些单测不证明真实 OSS 的默认域名、CNAME/CDN 域名或操作线可用性。s3 adapter 已过真 MinIO 集成。s3 侧另有单点观察：List 翻页用 `start-after`（MinIO 实测通过；AWS 真云的 ListObjectsV2 标准翻页参数是 `continuation-token`，接 AWS 真云时验证 start-after 兼容性，不兼容则改 token 形态）。

**CDN 场景 cache key 观察**：response-content-disposition 等签名 query 参与 CDN 缓存 key 时，不同参数值的同对象会分叉缓存——CDN 侧 cache key 规则按业务取舍。

**共享签名骨架**：uriEncode/canonicalQuery/deriveSigningKey/deriveRegion 等在 storage.go 共享——两个 adapter 的 V4 派生同源，新增 adapter（如 cos 原生）复用骨架只实现差异段。

**改动规矩**：预签名 URL 参数形态与表单字段名是对外契约（前端 JS 按此对接），改动属破坏性变更须走 change 流程。

### 固定完整 key 与重传（v0.15.0，#156）

原 `UploadTicket(ctx, directory, filename, expireSeconds, minSize, maxSize)` 在 OSS 与 S3（含 silo）均直接签入 `["eq","$key","directory/filename"]`。客户端按原有 POST multipart 流程提交全部 FormData，`key` 不得替换为同目录其他对象。完整 key 使用 `strings.TrimSuffix(directory, "/") + "/" + filename` 组装；空 filename、含 `${` 的 key 模板和无效 UTF-8 返回错误。文件名不由浏览器 `${filename}` 展开。

同一未过期票据允许多次写入其固定 key，在存储端允许覆盖的配置下最后一次写入生效。票据不包含 forbid-overwrite，也不查询桶版本控制。签名的过期时间、原 content-length-range 行为与 MIME、上传域名/寻址保持不变；`UploadURL` 不变。

v0.15.0 删除 v0.14.6 新增的 `StrictUploadTicket`、`StrictTicketStorage`、`ErrStrictUploadUnsupported` 与 attachment 的 `RequestStrictUploadURL`，不保留模式开关或兼容入口。所有项目统一使用原入口。旧版本已签票据不能由升级撤销：如需立即消除目录写授权，停止旧签发并等待最长有效期结束，或由运维按项目策略撤销相应授权；不能只更新服务代码就声称旧票据失效。

此约束保证授权范围是指定 key，不保证内容一次写入。附件归档使用当前对象；项目如在归档前校验内容，应避免校验后又覆盖导致校验对象与归档对象不同。本轮未提供版本绑定或跨写入/归档的原子快照。

离线单测检查两个 adapter 的 eq key、模板拒绝、原大小/过期约束及独立签名；attachment 原入口用真实 adapter 生成票据。`tests/storage_minio_test.go` 覆盖同目录篡改不改变已有目标、同 key 重传取最后内容与大小/过期拒绝，环境缺失 Skip。未执行真实 OSS、silo 或客户端上传验收，不能以离线或 MinIO 结果代替。
