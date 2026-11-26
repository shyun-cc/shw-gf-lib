package region

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/shyun-cc/shw-gf-lib/tencent"
)

// TencentSource 腾讯地图行政区划数据源（list 拉平 + getchildren 补街）。
//
// 街道补拉按市分型数据驱动（依据 flatten 后的孩子数据分类，无城市码名单）：
//   - 直辖市（北京/上海/天津/重庆）：cidx 跨 block 指向区级（跳过市级 block），
//     区 flatten 为 level=3 → 修正为 2，并 getchildren(区码) 补街道 level=3
//   - 直筒市（东莞/中山等）：市块孩子全是占位节点（尾码 99 且与市同名，如
//     441999「东莞市」），剔除占位后按占位码二跳 getchildren 补真镇街
//   - 省直辖县级市（济源/五指山等，市块无 cidx 无孩子）：getchildren(市码) 一跳即真镇街
//   - 港澳台（710000/810000/820000）：getchildren 返回合法空，不发起补拉
//
// 补零：全部完成后 6 位 adcode 右侧补零到 9 位（getchildren 传参仍用原始 6 位）；
// 省级 ParentCode 为空是契约，不参与补零。
// 限速：相邻 getchildren 间隔 ≥200ms（QPS=5 腾讯平台硬限，间隔函数可注入加速测试）。
// 完整性：任一 getchildren 失败（重试耗尽或空 result）fetch 即整体报错——
// 采集结果不完整时 MUST NOT 返回部分数据（调用方按错误修复后整体重跑）。
type TencentSource struct {
	client   *tencent.Client
	interval time.Duration // getchildren 间隔（默认 200ms；测试可注入 0）
	sleep    func(time.Duration)
}

// 直辖市 adcode 是国家行政区划标准编码（非项目参数），硬编码
var municipalitySet = map[string]bool{
	"110000": true, // 北京
	"310000": true, // 上海
	"120000": true, // 天津
	"500000": true, // 重庆
}

// 港澳台省级行政区划码（国家标准编码，非项目参数）：港澳（810000/820000）的区
// 挂在市块，getchildren 返回合法空，不发起补拉；台湾（710000）现行数据形态各市
// 有 cidx 指向真实区划、本就无需补拉，列入集合是数据形态变化时的防线（防止某市
// 失去 cidx 后误发 getchildren 撞空 result 触发整体报错）。
var noStreetProvinceSet = map[string]bool{
	"710000": true, // 台湾
	"810000": true, // 香港
	"820000": true, // 澳门
}

const (
	pathList        = "/ws/district/v1/list"
	pathGetchildren = "/ws/district/v1/getchildren"
	qpsInterval     = 200 * time.Millisecond
)

// NewTencentSource 构造腾讯数据源
func NewTencentSource(client *tencent.Client) *TencentSource {
	return &TencentSource{client: client, interval: qpsInterval, sleep: time.Sleep}
}

// SetInterval 注入 getchildren 间隔（仅测试加速用；生产保持 200ms QPS 限速）
func (s *TencentSource) SetInterval(d time.Duration, sleep func(time.Duration)) {
	s.interval = d
	if sleep != nil {
		s.sleep = sleep
	}
}

// Fetch 拉取并标准化：list 拉平 → 按市分型补街道（直辖市区/直筒市二跳/省直辖县级市一跳）
// + 剔除占位节点 → 补零 + 直辖市区 level 修正
func (s *TencentSource) Fetch(ctx context.Context) ([]Region, error) {
	data, err := s.client.Get(ctx, pathList, nil)
	if err != nil {
		return nil, err
	}
	result, _ := data["result"].([]any)
	if len(result) == 0 {
		return []Region{}, nil
	}

	regions := flattenChunked(result)

	// 孩子 ParentCode(6位) → regions 下标切片（一次遍历建好，供补街分类与占位剔除共用）
	byParent := make(map[string][]int, len(regions))
	for i := range regions {
		if code := regions[i].ParentCode; code != "" {
			byParent[code] = append(byParent[code], i)
		}
	}

	// 占位指纹命中的孩子下标集合：无论市落哪个分支，命中者一律从最终输出剔除
	placeholderIdx := make(map[int]bool)

	// fetchAndAppend 补拉街道并追加（含限速 sleep），失败即中断整体采集
	fetchAndAppend := func(id6, parent6 string) error {
		streets, err := s.fetchStreets(ctx, id6, parent6)
		if err != nil {
			return err
		}
		regions = append(regions, streets...)
		if s.interval > 0 {
			s.sleep(s.interval)
		}
		return nil
	}

	// 补街分类循环（数据驱动四分支）：先取快照长度，循环中 append 镇街不影响遍历
	snapshot := len(regions)
	for i := 0; i < snapshot; i++ {
		r := regions[i]
		// 直辖市的区（来自区块 level=3）：getchildren(区码) 一跳即街道
		if r.ParentCode != "" && municipalitySet[r.ParentCode] {
			if err := fetchAndAppend(r.Adcode, r.Adcode); err != nil {
				return nil, err
			}
			continue
		}
		// 港澳台省码下的市级层节点（港澳的区、台湾的市）：不发起补拉——港澳
		// getchildren 为合法空；台湾的市有真实区划孩子本就无需补拉，无孩子时
		// 也不试探（数据形态变化防线）
		if r.Level == 2 && noStreetProvinceSet[r.ParentCode] {
			continue
		}
		if r.Level != 2 {
			continue
		}
		// 以下为 Level==2 的市节点，按已有孩子数据分型
		children := byParent[r.Adcode]
		if len(children) == 0 {
			// 无孩子：大陆省直辖县级市（市块无 cidx）getchildren(市码) 一跳即真镇街。
			// 港澳台省码已被上方分支拦截，此处守卫为纵深防御（防上方条件被改动后误发补拉）
			if !noStreetProvinceSet[r.ParentCode] {
				if err := fetchAndAppend(r.Adcode, r.Adcode); err != nil {
					return nil, err
				}
			}
			continue
		}
		// 有孩子：收集命中占位指纹的（无论市落哪个分支，命中者一律剔除）
		allPlaceholder := true
		var placeholderCodes []string
		for _, ci := range children {
			c := regions[ci]
			if isPlaceholderNode(c.Adcode, c.Fullname, r.Adcode, r.Fullname) {
				placeholderIdx[ci] = true
				placeholderCodes = append(placeholderCodes, c.Adcode)
			} else {
				allPlaceholder = false
			}
		}
		if !allPlaceholder {
			continue // 存在真实区县：常规地级市不补（镇街由区块 cidx 覆盖）
		}
		// 直筒市：getchildren(市码) 返回的是占位节点，须按占位码二跳补真镇街
		// （理论范围内占位仅一条，按码去重是防御）
		seen := make(map[string]bool, len(placeholderCodes))
		for _, pc := range placeholderCodes {
			if seen[pc] {
				continue
			}
			seen[pc] = true
			if err := fetchAndAppend(pc, r.Adcode); err != nil {
				return nil, err
			}
		}
	}

	// 剔除占位节点（新 append 的镇街下标不小于快照长度，不在剔除集合内）
	if len(placeholderIdx) > 0 {
		filtered := make([]Region, 0, len(regions)-len(placeholderIdx))
		for i := range regions {
			if !placeholderIdx[i] {
				filtered = append(filtered, regions[i])
			}
		}
		regions = filtered
	}

	// 直辖市区 level 3→2 修正须在补零前（municipalitySet 存 6 位码），随后统一补零
	// （省级 ParentCode 为空是契约，不补零——补了会让 resolvePath 的「省级无父」
	// 判定失配，只能靠「父缺失退化根路径」巧合兜底）
	for i := range regions {
		if regions[i].Level == 3 && regions[i].ParentCode != "" && municipalitySet[regions[i].ParentCode] {
			regions[i].Level = 2
		}
		regions[i].Adcode = pad9(regions[i].Adcode)
		if regions[i].ParentCode != "" {
			regions[i].ParentCode = pad9(regions[i].ParentCode)
		}
	}
	return regions, nil
}

// fetchStreets 拉取单个区/市的街道；失败或空 result 均报错（完整性契约：
// 业务方依赖全量镇街数据入库，静默缺口会污染地址库）。
// id6 为 getchildren 请求码（直筒市为占位码），parent6 为产出街道的 ParentCode
// （直筒市街道的父级是市码而非占位码）。
func (s *TencentSource) fetchStreets(ctx context.Context, id6, parent6 string) ([]Region, error) {
	data, err := s.client.Get(ctx, pathGetchildren, map[string]string{"id": id6})
	if err != nil {
		return nil, fmt.Errorf("街道补拉失败（adcode=%s，重试已耗尽）: %w", id6, err)
	}
	result, _ := data["result"].([]any)
	if len(result) == 0 {
		return nil, fmt.Errorf("街道补拉异常（adcode=%s）：getchildren 返回空 result", id6)
	}
	children, _ := result[0].([]any)
	if len(children) == 0 {
		return nil, fmt.Errorf("街道补拉异常（adcode=%s）：getchildren 子级列表为空", id6)
	}
	streets := make([]Region, 0, len(children))
	for _, node := range children {
		m, ok := node.(map[string]any)
		if !ok {
			continue
		}
		streets = append(streets, nodeToRegion(m, 3, parent6))
	}
	return streets, nil
}

// flattenChunked 展平 list 的二维分块：block 下标定级、cidx 局部下标建 parentCode、
// 直辖市跳级用 adcode 前缀探测目标 block。
func flattenChunked(blocks []any) []Region {
	// 先把 []any 转 [][]map[string]any
	parsed := make([][]map[string]any, 0, len(blocks))
	for _, b := range blocks {
		arr, _ := b.([]any)
		block := make([]map[string]any, 0, len(arr))
		for _, n := range arr {
			if m, ok := n.(map[string]any); ok {
				block = append(block, m)
			}
		}
		parsed = append(parsed, block)
	}

	// parentMap[blockIdx][localIdx] = parentAdcode
	parentMap := map[int]map[int]string{}
	for i := range parsed {
		for _, node := range parsed[i] {
			cidx, ok := node["cidx"].([]any)
			if !ok || len(cidx) < 2 {
				continue
			}
			start, end := toInt(cidx[0]), toInt(cidx[1])
			parentAdcode, _ := node["id"].(string)
			if parentAdcode == "" || end < start {
				continue
			}
			childBlock := resolveChildBlock(parsed, i, parentAdcode, start, end)
			if childBlock < 0 {
				continue
			}
			if parentMap[childBlock] == nil {
				parentMap[childBlock] = map[int]string{}
			}
			for l := start; l <= end; l++ {
				if l >= 0 && l < len(parsed[childBlock]) {
					parentMap[childBlock][l] = parentAdcode
				}
			}
		}
	}

	// 按 block 顺序构造（level = blockIdx+1）
	var out []Region
	for i := range parsed {
		for local, node := range parsed[i] {
			parent := ""
			if m, ok := parentMap[i]; ok {
				if p, ok := m[local]; ok {
					parent = p
				}
			}
			out = append(out, nodeToRegion(node, i+1, parent))
		}
	}
	return out
}

// resolveChildBlock 探测 cidx 指向的子级 block：候选 block[i+1]（标准）→ block[i+2]（直辖市跳级）。
// 前缀匹配长度按父级 level 递减（行政区划树形编码规则）：省 2 位、市 4 位、其他 6 位；
// 范围内全部存在节点都匹配才算命中。
func resolveChildBlock(blocks [][]map[string]any, nodeBlockIdx int, parentAdcode string, start, end int) int {
	parentLevel := nodeBlockIdx + 1
	prefixLen := 6
	switch parentLevel {
	case 1:
		prefixLen = 2
	case 2:
		prefixLen = 4
	}
	prefix := parentAdcode
	if len(prefix) > prefixLen {
		prefix = prefix[:prefixLen]
	}

	for offset := 1; offset <= 2; offset++ {
		cand := nodeBlockIdx + offset
		if cand >= len(blocks) {
			break
		}
		if start < 0 || start >= len(blocks[cand]) {
			continue
		}
		effectiveEnd := end
		if effectiveEnd > len(blocks[cand])-1 {
			effectiveEnd = len(blocks[cand]) - 1
		}
		if rangeMatchesParentPrefix(blocks[cand], prefix, start, effectiveEnd) {
			return cand
		}
	}
	return -1
}

// rangeMatchesParentPrefix 范围内全部存在节点的前缀都匹配且至少一个节点（空 id 视为不匹配）
func rangeMatchesParentPrefix(block []map[string]any, prefix string, start, end int) bool {
	checked := 0
	for i := start; i <= end; i++ {
		id, _ := block[i]["id"].(string)
		if id == "" {
			return false
		}
		if !strings.HasPrefix(id, prefix) {
			return false
		}
		checked++
	}
	return checked > 0
}

// nodeToRegion 标准化单个 API 节点：location 为嵌套对象（{"lat":..,"lng":..}，
// 非平铺 key）、pinyin 为数组（如 ["hai","nan"]，逐元素以空格 join）；
// 类型不符静默落零值/空串（采集容错，异常形态由完整性契约兜底）。
func nodeToRegion(node map[string]any, level int, parent string) Region {
	id, _ := node["id"].(string)
	name, _ := node["name"].(string)
	fullname, _ := node["fullname"].(string)
	var lat, lng float64
	if loc, ok := node["location"].(map[string]any); ok {
		lat, _ = loc["lat"].(float64)
		lng, _ = loc["lng"].(float64)
	}
	var pinyinParts []string
	if arr, ok := node["pinyin"].([]any); ok {
		for _, p := range arr {
			if s, ok := p.(string); ok {
				pinyinParts = append(pinyinParts, s)
			}
		}
	}
	return Region{
		Adcode:     id,
		Name:       name,
		Fullname:   fullname,
		Level:      level,
		ParentCode: parent,
		Lat:        lat,
		Lng:        lng,
		Pinyin:     strings.Join(pinyinParts, " "),
	}
}

// isPlaceholderNode 占位节点指纹（直筒市 getchildren(市码) 返回的占位，如东莞
// 441999「东莞市」）：三条同时满足——id 6 位且前 4 位与市码一致、id 尾 2 位为
// "99"、fullname 与市同名。
func isPlaceholderNode(id, fullname, cityCode, cityFullname string) bool {
	return len(id) == 6 && len(cityCode) >= 4 &&
		id[:4] == cityCode[:4] && id[4:] == "99" &&
		fullname == cityFullname
}

// pad9 6 位 adcode 右侧补零到 9 位（已是 9 位则原样）
func pad9(code string) string {
	for len(code) < 9 {
		code += "0"
	}
	return code
}

func toInt(v any) int {
	f, ok := v.(float64)
	if !ok {
		return 0
	}
	return int(f)
}
