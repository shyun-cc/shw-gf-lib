# settings — 配置字段处理核心

## 边界

纯字段注册、校验、补默认值、变更编码、敏感值编解码和管理展示。无数据库、Redis、HTTP、权限、审计表或全局状态。项目在自己的事务内应用 Change 并记录审计，提交成功后调用 cfgsource 刷新。settings→cryptox；不改变 cryptox 的 v1 密文格式或扩展短密钥路径。

## 定义

`New(fields []Field, codec Codec) (*Engine, error)` 注册字段；Key 不能空或重复，Kind 为 string/number/boolean/secret。number 表示 int64 整数。Field.Default 是存储字符串形式的默认值，由项目声明并在注册时校验；secret 禁止明文默认值。Min/Max 仅适用于 number，MaxBytes 限制字符串归一化后的字节数；Required 不允许清除，不能同时声明 Clearable。

Normalize 只适用于 string/secret，可做 origin/domain 等项目归一化；Validate 接收归一化后的 string/int64/bool。库不写死字段名或业务默认值。回调必须无持久化副作用、可并发使用。Engine 复制字段、默认值和边界指针；Fields 返回防御性副本，按 Key 排序。回调闭包与自定义 Codec 的线程安全由项目保证。

## 输入与变更

`EncodePatch(ctx, map[string]Input) ([]Change, error)`：

- 缺失 key：不修改。
- `Input{Value: ...}`：校验后生成存储字符串。
- `Input{Clear:true}`：必须字段允许 Clearable，且 Value 为 nil；产生 Change.Clear=true。项目决定删除行还是存空，不隐含 SQL 语义。
- Secret 的空字符串 Value 会报 explicit_clear_required；管理表单中未编辑的脱敏字段应省略，不能用空串冒充“不变”。旧项目空串清除契约在接口适配处翻译为 Clear=true。
- 未知字段、类型错误、范围越界、自定义校验失败时整批返回 nil；全部校验通过后才调用 Codec。Codec 失败也不返回部分变更；库不自行落库。
- number 接受 int/int8/int16/int32/int64、uint/uint32/uint64、十进制整数 json.Number、有限且无小数的安全范围 float64。超过 ±(2^53−1) 的 float64 被拒绝，HTTP 建议 UseNumber。字符串数字不作为输入 number 接受；存储解码则用 ParseInt(...,10,64)。boolean 输入只能是 bool，存储形式只能 true/false。

## 运行时与管理展示

`Resolve(ctx, stored map[string]string) map[string]Entry` 返回运行时值及逐字段状态；`View` 在此基础上移除所有 secret Value。View 可用于管理端，Resolve 的结果只应传运行时消费代码。

Entry.State：unset / defaulted / configured / invalid / decrypt_failed。Configured 只在成功解码的显式存储值上为 true，默认值不表示人工已配置。缺失使用声明默认值；损坏数据不静默回退默认。Required 缺失返回 unset + required 错误。未知存储 key 被忽略。存储中的空 secret 为 unset。

FieldError 只含 Key 与固定 Code，不包含输入、存储值和回调/Codec 原始错误；可 errors.Is 区分 ErrValidation 与 ErrCodec。项目决定启动失败、提示或禁用功能，库不决定启动策略。

## 敏感值 Codec

Codec 提供 Encrypt(context.Context,string) 和 Decrypt(context.Context,string)，密钥来源、轮换、明文存量兼容均由项目决定。

`NewCryptoxCodec(key,prefix string) (Codec,error)` 是可选封装：key 至少32字节，prefix 由项目传入（例如 enc:）；完整密文为 prefix + cryptox v1 密文。只走现有强密钥路径，不读取环境变量，不接受非对应前缀的明文。历史明文兼容应由项目显式实现 Codec，不能把解密失败当明文返回。

## 测试与接入

settings/settings_test.go 覆盖整批校验、Codec 调用边界、字段默认值/防御性复制、敏感投影、错误脱敏及 cryptox 格式。只消费正式 tag。示例（可编译的完整函数见 settings/example_test.go）：项目先创建 Engine，调用 EncodePatch，再在项目事务里循环应用 Change 和审计；事务成功后刷新 cfgsource。测试不访问数据库或真实厂商。
