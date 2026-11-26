# region — 行政区划采集管道

> 面向项目侧接入者（第 1–3 节）与库维护者（加读第 4 节）。事实来源是 `region/` 源码，本文的代码示例、签名与采集行为均与其逐字一致；若发现不一致，以源码为准并修正本文档。

## 1. 组件定位

行政区划采集管道——**只采集不存储**。两个角色：

- **`Source` 接口**：数据源适配器（拉取并标准化为 3 级 `Region[]`）；包内现成腾讯数据源 `TencentSource`；
- **`Collector`**：透明管道——取数后递归补全 `path`（`/祖先码…/自身码/` 形式），无任何存储副作用，结果怎么落库是项目的事。

依赖方向：region → tencent（HTTP 客户端复用公开包）。

## 2. 快速接入

```go
package main

import (
	"context"

	"github.com/shyun-cc/shw-gf-lib/region"
	"github.com/shyun-cc/shw-gf-lib/tencent"
)

func main() {
	client := tencent.New("<KEY>", "<SK>")
	src := region.NewTencentSource(client)
	regions, err := region.NewCollector(src).Fetch(context.Background())
	if err != nil {
		return
	}
	for _, r := range regions {
		// r.Adcode（9 位）/ r.Name / r.Fullname / r.Level(1省2市3区县街道)
		// r.Path（/祖先…/自身/，9 位码）/ r.ParentCode / r.Lat / r.Lng / r.Pinyin
		saveRegion(r) // 项目自己的落库
	}
}
```

## 3. 腾讯数据源采集行为

`TencentSource.Fetch` 的拉平与修正规则（四类区域形态按腾讯返回形态**数据驱动**分类，无任何城市码硬编码名单）：

- **普通省**：district/v1/list 三层结构直接拉平；
- **直辖市**（北京/上海/天津/重庆）：cidx 跨 block 直指区级（跳过市级 block），区 flatten 出来 level=3 → **修正为 2**，再 getchildren 补街道 level=3；
- **直筒市**（东莞/中山/嘉峪关/儋州等不设区县的地级市）：腾讯 list 在市下返回**占位节点**（id = 市码前 4 位 + "99"，fullname 与市相同），采集器对占位节点发起**第二跳** getchildren(占位码) 取真镇街（parent=市码、level=3），占位节点本身从输出中**剔除**；
- **省直辖县级市**（济源/仙桃/五指山/新疆兵团市等，list 市块中无 cidx）：getchildren(市码) **一跳**直接返回真镇街（parent=市码、level=3）；
- **港澳台**：港澳的区（ParentCode 810000/820000）腾讯数据到区为止（getchildren 合法空），不发起调用；台湾（710000）各市现行形态有 cidx 指向真实区划、本就无需补拉，与港澳共用同一拦截集合，是腾讯侧数据形态变化时的防线（某市失去 cidx 也不试探，避免撞空 result 触发整体报错）；
- **三沙等有 cidx 指向真实区划的市**：标准形态，不补街；
- **补零**：全部完成后 6 位 adcode 右侧补零到 9 位（getchildren 传参仍用原始 6 位）；省级 `ParentCode` 为空是契约，不参与补零；
- **限速**：相邻 getchildren 间隔 ≥200ms（QPS=5 腾讯平台硬限；SetInterval 可注入加速测试，生产保持默认）；
- **采集完整性（fail-fast）**：任一**会发起调用的分支** getchildren 失败（重试耗尽，或 status=0 但 result / 子级列表为空）时 `Fetch` 整体返回 error（错误信息含失败 adcode），**不返回部分数据**——地址库入库依赖全量镇街，静默缺口会污染数据；调用方按错误修复后整体重跑（港澳的区等明确不发起调用的分支不受此约束）。

**字段解析**：腾讯返回的 `location` 为嵌套对象、`pinyin` 为数组，组件解析为 `Lat`/`Lng`（float64）与空格 join 的拼音串（如 "hai nan"，兼容业务方 `pinyin LIKE '{letter}%'` 首词首字母检索契约）；list 区块节点无 `name`/`pinyin` 属数据源固有形态，`Name`/`Pinyin` 落空串，由业务方按 `Fullname` 回退。

## 4. FAQ

**Q：为什么只采集不存储？**

落库形态（单表/父子表/物化路径列）与更新策略（全量重灌/增量）是项目决策；管道只保证输出标准化 Region[]。path 字段已是物化路径形态，单表存够用。

**Q：adcode 为什么统一 9 位？**

腾讯 API 返回 6 位；补零到 9 位对齐国家统计局 9 位行政区划标准（后 3 位为基层扩展位），避免与其他系统对接时二次处理。

**Q：采集要多久？**

getchildren 发生在直辖市的区（约 86 个）、直筒市占位二跳（约 4 个）与省直辖县级市一跳（约 34 个），合计约 117 次 × 200ms 限速间隔 ≈ 24 秒，加网络与重试开销为半分钟量级。普通地级市停在区县级，不拉街道。

**Q：父节点缺失的数据会怎样？**

resolvePath 对缺失父级退化为根路径（`/自身码/`）不报错——异常数据不阻塞整批，事后按 ParentCode 校验即可发现。

**Q：能换其他数据源（民政部/高德）吗？**

实现 `region.Source` 接口（Fetch 返回标准化 Region[]，path 留空由 Collector 补）即可，Collector 与存储侧不动。

## 5. 维护者注记

**城市分类：直辖市保留硬编码、直筒市名单硬编码已废**：municipalitySet（直辖市 4 码）是国家行政区划标准（非项目参数），属公共事实保留硬编码——与「库不做项目决策」不冲突；直筒市/省直辖县级市的分类已改为**数据驱动**（占位节点指纹 + cidx 孩子反查），不再硬编码名单——原 directRuledCitySet 存在注释码名写反与三沙误伤问题，随删除消除。

**SetInterval 双参设计**：注入间隔与 sleep 函数两个参数——单测注入 0 间隔 + 假 sleep，全量采集测试不真等 1 分钟。

**失败语义 2026-08-18 变更**：原为「单点失败隔离」（失败返回空数组不中断），因地址库入库场景静默缺口会污染数据，改为 fail-fast 整体报错（BREAKING，见 CHANGELOG）。

**直筒市采集 2026-08-24 修正**：旧实现的夹具假设了错误的 API 形态（假设 getchildren(市码) 直接返回镇街），实际真镇街在占位节点的第二跳上、从未被采集，此前的「已修复并有测试覆盖」说法建立在错误夹具上；本次按腾讯真实返回形态（带 SN 签名实测取证）修正字段解析与层级结构，夹具全部重写为真实形态。

**测试**：包内单测（拉平算法用脱机 fixture、直辖市 level 修正、直筒市占位二跳与省直辖县级市一跳、港澳区不发起调用、补零与省级父码空、path 递归与缓存、完整性 fail-fast 与空 result 报错）。
