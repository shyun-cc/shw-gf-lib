// Package tests 是 gf-lib 的集成测试目录（独立 go module，学 gorm 的 tests/ 组织方式）。
//
// 与主库的关系：本目录自带 go.mod（module github.com/shyun-cc/shw-gf-lib/tests），
// 通过 replace 引用本地主库，不参与主 module 的任何 go build / go test / go vet。
//
// 定位：存放需要真实 MySQL / Redis 等外部服务的集成测试（如 seqnum 的 DB 恢复路径）。
// 无外部依赖的单元测试一律放在主库各包目录下，不放这里。
//
// 运行方式：
//
//	cd tests
//	go mod tidy
//	MYSQL_DSN='用户:密码@tcp(127.0.0.1:3306)/库名' \
//	  GOWORK=off go test -tags integration ./...
//
// verifycode 原子操作集成 TestVerifyCodeAtomicRedis 要求 REDIS_ADDR，缺失时 Skip（不降级 miniredis）。
// Redis 连接可选：设置 REDIS_ADDR（如 127.0.0.1:6379）用真实 Redis；
// 未设置时自动降级为进程内 miniredis，无需外部 Redis 也能跑集成测试。
//
// RabbitMQ 集成（rabbitmq / task-amqp 全链路）：设置 AMQP_DSN
// （URL 形式 amqp://user:pass@host:port/vhost，如 amqp://guest:guest@127.0.0.1:5672/），
// 未设置时跳过；task / task-amqp 集成另需 PG_DSN（URL 形式
// postgres://user:pass@127.0.0.1:5432/dbname），task-amqp 全链路用例
// 需 AMQP_DSN 与 PG_DSN 两者齐备，任一缺失即跳过。
//
// MinIO 集成（storage 组件）：设置 MINIO_ENDPOINT（URL 形式如 http://127.0.0.1:9000，
// 未设置时跳过）、MINIO_AK/MINIO_SK（缺省 minioadmin）、MINIO_BUCKET（缺省 gf-lib-test）；
// 可选 MINIO_INTERNAL_ENDPOINT（同桶另一可达地址，如 http://localhost:10002，与
// MINIO_ENDPOINT 同实例不同 host 字符串），未设置时跳过双端点内网视图测试。
// 桶不存在时测试自动创建，测试自建的桶结束时清空对象并删除，不触碰实例内其他桶。
//
// 其中 GOWORK=off 用于隔离上层目录可能存在的工作区配置（go.work 的 use
// 列表不含本 module，不关闭会报 "directory prefix . does not contain
// modules listed in go.work"，且该 go.work 属于本机其他项目，不应改动）。
//
// 以下 L3 工具仅供消费方按需诊断；本库维护 agent 不主动执行，也不作为发布门禁。
// 消费方升级正式 tag 后验证业务并反馈 Bug，本库修复后按 CI 发版。
// L3 冒烟测试：验证各厂商通道连通性与请求契约（签名、加密、报文组装）
// 不被厂商拒绝；冒烟框架 helper（smokeLevel / smokeWant / assertAccepted /
// retryOnceOnTransient / knownDefect）的完整契约见 smoke_helper_test.go 包注释。
//
// 冒烟约定：
//   - 构建标签 `//go:build smoke`，与 integration 正交；用例文件按
//     smoke_<组件>_test.go 命名（如 smoke_sms_test.go）；
//   - 不进 PR CI（真凭证 + 可能计费）。由维护者按需手工运行：
//     自备凭证环境变量后执行 cd tests && GOWORK=off go test -tags smoke ./...；
//   - 档位 SMOKE_LEVEL 三档：read（默认，纯读）/ write（轻写：素材传删、
//     测试群消息）/ costly（真金写：短信发送、wxpay 下单）；
//     用例按自身副作用声明所需档位，当前档位不足时自动 Skip；
//   - 厂商凭证一律经环境变量注入，真实值不入 git、不写入任何仓库文件；
//     缺失对应变量的用例自动 Skip。变量清单（按组件）：
//     wecom：WECOM_CORP_ID / WECOM_AGENT_SECRET / WECOM_TEST_CHATID
//     wxoa：WXOA_APPID / WXOA_SECRET
//     wxapp：WXAPP_APPID / WXAPP_SECRET / WXAPP_LOGIN_CODE（人工门）
//     wxpay：WXPAY_MCHID / WXPAY_APPID / WXPAY_APIKEY_V3 / WXPAY_APIKEY_V2（可空）
//     / WXPAY_SERIAL_NO / WXPAY_PRIVATE_KEY_PATH（apiclient_key.pem 绝对路径）
//     sms：SMS_AK / SMS_SK / SMS_SIGN / SMS_TEMPLATE_CODE / SMS_TEST_PHONE
//     region / tencent：TENCENT_MAP_KEY
//     express：KD100_CUSTOMER / KD100_KEY
//     aliyun：ALIYUN_AK / ALIYUN_SK
//   - storage 的阿里云 OSS 冒烟已永久移除；S3/MinIO 集成测试仍按上文的
//     MINIO_* 环境变量约定执行，不属于 L3 冒烟。
//   - 断言纪律：只断言「厂商接受请求形态」（HTTP 成功 + 业务码 0 +
//     结构可解析），不断言业务数据；
//   - wxapp code2session 用例设人工门：需 SMOKE_LEVEL=costly 且手工注入
//     WXAPP_LOGIN_CODE（真人扫码一次性 code）方会执行。
//
// 约定：
//   - 所有集成测试文件必须带 `//go:build integration` 构建标签，
//     默认（不带标签）任何 go build / go test 均不触发；
//   - 数据库 / Redis 连接信息一律经环境变量注入（如 MYSQL_DSN），
//     不硬编码任何主机、账号或密码；
//   - 环境缺失时测试应 t.Skip 跳过并给出提示，不阻塞 CI 与本机开发。
package tests
