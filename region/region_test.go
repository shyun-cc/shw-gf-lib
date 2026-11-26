package region

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shyun-cc/shw-gf-lib/tencent"
)

// fakeTencent 可编程腾讯 server：list 固定夹具、getchildren 按 id 返回、记录调用间隔。
// children 表缺某 id 时返回空 body → 客户端 JSON 解析失败重试耗尽 → Fetch 报错，
// 即「误调用即挂」的硬失败法（港澳/三沙不调用断言依赖此语义）。
type fakeTencent struct {
	listBody     string
	children     map[string]string // id → 响应 JSON
	getchildrenN int32
	failIds      map[string]bool // 这些 id 的 getchildren 返回 status≠0
	callTimes    []time.Time
}

func (f *fakeTencent) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case pathList:
			_, _ = w.Write([]byte(f.listBody))
		case pathGetchildren:
			atomic.AddInt32(&f.getchildrenN, 1)
			f.callTimes = append(f.callTimes, time.Now())
			id := r.URL.Query().Get("id")
			if f.failIds[id] {
				_, _ = w.Write([]byte(`{"status":120,"message":"查询频率超限"}`))
				return
			}
			_, _ = w.Write([]byte(f.children[id]))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// buildList 构造 list 接口二维分块响应
func buildList(l1, l2, l3 string) string {
	return fmt.Sprintf(`{"status":0,"result":[%s,%s,%s]}`, l1, l2, l3)
}

// ============================ 主夹具（腾讯 API 真实形态，实测取证） ============================
//
// list/getchildren 真实返回形态要点（夹具逐字对齐，勿改回平铺旧形态）：
//  1. 省块节点带数组 pinyin + 嵌套 location，如海南
//     {"pinyin":["hai","nan"],"location":{"lat":20.018639,"lng":110.348781}}
//  2. 区块（block[2]）节点普遍无 name 无 pinyin（仅 fullname + 嵌套 location）——
//     固有形态，Name/Pinyin 落空串不报错（固有形态容错用例覆盖）
//  3. getchildren 返回节点同样无 name 无 pinyin，result 结构为 [[节点...]]
//  4. 直筒市：东莞 441900 的 cidx 指向区块占位节点 441999（无 name、fullname 同市、
//     坐标同市），getchildren(441999) 才返回真镇街（按占位码二跳）
//  5. 省直辖县级市：五指山 469001 市块节点无 cidx，getchildren(469001) 一跳即真镇街
//
// 夹具布局（cidx 为目标块内局部下标范围，flatten 靠它建 ParentCode）：
//
//	block[0]（省）: 北京 110000（cidx 跨 block 跳级指向区块）、江苏 320000（→市块）、
//	  广东 440000（→市块东莞）、海南 460000（→市块五指山）
//	block[1]（市）: 南京 320100（cidx 指向区块玄武区）、东莞 441900（cidx 指向区块
//	  441999 占位）、五指山 469001（无 cidx——省直辖县级市）
//	block[2]（区）: 朝阳 110105（北京跳级而来）、玄武 320102（南京的区）、441999 占位
const fixtureList = `{"status":0,"result":[
  [
    {"id":"110000","name":"北京","fullname":"北京市","pinyin":["bei","jing"],"location":{"lat":39.9,"lng":116.4},"cidx":[0,0]},
    {"id":"320000","name":"江苏","fullname":"江苏省","pinyin":["jiang","su"],"location":{"lat":32.97,"lng":118.78},"cidx":[0,0]},
    {"id":"440000","name":"广东","fullname":"广东省","pinyin":["guang","dong"],"location":{"lat":23.13,"lng":113.27},"cidx":[1,1]},
    {"id":"460000","name":"海南","fullname":"海南省","pinyin":["hai","nan"],"location":{"lat":20.018639,"lng":110.348781},"cidx":[2,2]}
  ],
  [
    {"id":"320100","name":"南京","fullname":"南京市","pinyin":["nan","jing"],"location":{"lat":32.06,"lng":118.78},"cidx":[1,1]},
    {"id":"441900","name":"东莞","fullname":"东莞市","pinyin":["dong","guan"],"location":{"lat":23.02,"lng":113.75},"cidx":[2,2]},
    {"id":"469001","name":"五指山","fullname":"五指山市","pinyin":["wu","zhi","shan"],"location":{"lat":18.78,"lng":109.52}}
  ],
  [
    {"id":"110105","fullname":"朝阳区","location":{"lat":39.92,"lng":116.43}},
    {"id":"320102","fullname":"玄武区","location":{"lat":32.05,"lng":118.79}},
    {"id":"441999","fullname":"东莞市","location":{"lat":23.02,"lng":113.75}}
  ]
]}`

// getchildren 响应常量（真实形态：节点无 name 无 pinyin、location 嵌套对象、result 外层套数组）
const streetsChaoyang = `{"status":0,"result":[[{"id":"110105001","fullname":"朝外街道","location":{"lat":39.9227,"lng":116.4384}},{"id":"110105002","fullname":"三里屯街道","location":{"lat":39.9334,"lng":116.4548}}]]}`

// streetsDongguan getchildren(441999)：直筒市按占位码二跳返回的真镇街
const streetsDongguan = `{"status":0,"result":[[{"id":"441900003","fullname":"东城街道","location":{"lat":23.0151,"lng":113.7598}},{"id":"441900004","fullname":"南城街道","location":{"lat":23.0208,"lng":113.7513}}]]}`

// streetsWuzhishan getchildren(469001)：省直辖县级市一跳返回的真镇街
const streetsWuzhishan = `{"status":0,"result":[[{"id":"469001100","fullname":"通什镇","location":{"lat":18.7766,"lng":109.5205}},{"id":"469001101","fullname":"南圣镇","location":{"lat":18.7497,"lng":109.5773}}]]}`

// defaultChildren 主夹具配套的 getchildren 表——恰好只含三个分支各自的请求码：
// 110105（直辖市区一跳）、441999（直筒市占位二跳）、469001（省直辖县级市一跳）。
// 表中无 441900（东莞市码）：若实现误走一跳而非占位二跳，误调用即空 body 报错。
func defaultChildren() map[string]string {
	return map[string]string{
		"110105": streetsChaoyang,
		"441999": streetsDongguan,
		"469001": streetsWuzhishan,
	}
}

// findByAdcode 输出集合按 9 位 adcode 建索引
func findByAdcode(regions []Region) map[string]Region {
	m := make(map[string]Region, len(regions))
	for _, r := range regions {
		m[r.Adcode] = r
	}
	return m
}

func newTestSource(t *testing.T, ft *fakeTencent) (*TencentSource, *Collector) {
	t.Helper()
	c := tencent.New("k", "")
	c.BaseURL = ft.server(t).URL
	c.Sleep = func(time.Duration) {}
	src := NewTencentSource(c)
	src.SetInterval(0, nil) // 测试不限速（间隔断言由独立用例覆盖）
	return src, NewCollector(src)
}

// TestFlatten_MunicipalityAndDirectRuledCity 直辖市修正/直筒市街道/9 位补零
func TestFlatten_MunicipalityAndDirectRuledCity(t *testing.T) {
	ft := &fakeTencent{listBody: fixtureList, children: defaultChildren()}
	_, col := newTestSource(t, ft)

	regions, err := col.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byAdcode := findByAdcode(regions)

	// 直辖市的区 level=2（北京朝阳，跳级 flatten 为 3 后修正）
	if c, ok := byAdcode["110105000"]; !ok {
		t.Fatal("缺朝阳区（应为 9 位 110105000）")
	} else if c.Level != 2 {
		t.Fatalf("直辖市的区 level 应修正为 2，实际 %d", c.Level)
	}

	// 直筒市街道 level=3（东莞东城，占位 441999 二跳补拉）
	if s, ok := byAdcode["441900003"]; !ok {
		t.Fatal("缺东莞街道（9 位 441900003）")
	} else if s.Level != 3 {
		t.Fatalf("直筒市街道 level 应为 3，实际 %d", s.Level)
	}

	// 北京街道 level=3（朝阳区的 getchildren）
	if s, ok := byAdcode["110105001"]; !ok || s.Level != 3 {
		t.Fatalf("北京街道应存在且 level=3: %+v", s)
	}

	// 普通：江苏 level=1、南京 level=2、玄武区 level=3
	if byAdcode["320000000"].Level != 1 || byAdcode["320100000"].Level != 2 || byAdcode["320102000"].Level != 3 {
		t.Fatal("普通三级 level 不符")
	}

	// 省直辖县级市：五指山市 level=2（市块节点，无 cidx 无区块孩子）
	if w, ok := byAdcode["469001000"]; !ok || w.Level != 2 {
		t.Fatalf("五指山市应存在且 level=2: %+v", w)
	}

	// 省级 ParentCode 保持空契约（不被补零成 000000000）
	if p := byAdcode["320000000"].ParentCode; p != "" {
		t.Fatalf("省级 ParentCode 应为空，实际 %q", p)
	}

	// 全部 9 位
	for _, r := range regions {
		if len(r.Adcode) != 9 {
			t.Fatalf("adcode 应 9 位: %s", r.Adcode)
		}
	}
}

// TestFlatten_RealShapePinyinLocation 真实形态字段保真：数组 pinyin 空格 join、
// 嵌套 location 坐标精确解析（spec 场景「list 来源节点坐标与拼音」——海南样本）
func TestFlatten_RealShapePinyinLocation(t *testing.T) {
	ft := &fakeTencent{listBody: fixtureList, children: defaultChildren()}
	_, col := newTestSource(t, ft)

	regions, err := col.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byAdcode := findByAdcode(regions)

	// 海南：["hai","nan"] → "hai nan"，坐标取嵌套 location.lat/lng 非零精确值
	h := byAdcode["460000000"]
	if !exists(h) {
		t.Fatal("缺海南省")
	}
	if h.Pinyin != "hai nan" {
		t.Fatalf("海南 Pinyin 应为 %q，实际 %q", "hai nan", h.Pinyin)
	}
	if h.Lat != 20.018639 || h.Lng != 110.348781 {
		t.Fatalf("海南坐标应取嵌套 location (20.018639, 110.348781)，实际 (%v, %v)", h.Lat, h.Lng)
	}

	// 北京：双元素数组 join（39.9/116.4 字面量与 JSON 解析 float64 精确相等）
	b := byAdcode["110000000"]
	if !exists(b) {
		t.Fatal("缺北京市")
	}
	if b.Pinyin != "bei jing" || b.Lat != 39.9 || b.Lng != 116.4 {
		t.Fatalf("北京 Pinyin/坐标不符: %q (%v, %v)", b.Pinyin, b.Lat, b.Lng)
	}

	// 五指山：三元素数组 join "wu zhi shan"
	if w := byAdcode["469001000"]; w.Pinyin != "wu zhi shan" {
		t.Fatalf("五指山 Pinyin 应为 %q，实际 %q", "wu zhi shan", w.Pinyin)
	}
}

// TestFlatten_DistrictNodeMissingFieldsTolerated 固有形态容错：区块节点与
// getchildren 节点普遍无 name 无 pinyin——落空串不报错，嵌套 location 仍可解析
func TestFlatten_DistrictNodeMissingFieldsTolerated(t *testing.T) {
	ft := &fakeTencent{listBody: fixtureList, children: defaultChildren()}
	_, col := newTestSource(t, ft)

	regions, err := col.Fetch(context.Background()) // 走到断言即 Fetch 正常完成不报错
	if err != nil {
		t.Fatal(err)
	}
	byAdcode := findByAdcode(regions)

	// 区块节点（北京朝阳，无 name 无 pinyin 字段）：Name/Pinyin 空串、坐标仍解析
	c := byAdcode["110105000"]
	if !exists(c) {
		t.Fatal("缺朝阳区")
	}
	if c.Name != "" || c.Pinyin != "" {
		t.Fatalf("无 name/pinyin 的区块节点应落空串，实际 Name=%q Pinyin=%q", c.Name, c.Pinyin)
	}
	if c.Lat != 39.92 || c.Lng != 116.43 {
		t.Fatalf("朝阳坐标应取嵌套 location (39.92, 116.43)，实际 (%v, %v)", c.Lat, c.Lng)
	}

	// 常规地级市的区（南京玄武）同形态
	if x := byAdcode["320102000"]; x.Name != "" || x.Pinyin != "" {
		t.Fatalf("玄武区应落空串，实际 Name=%q Pinyin=%q", x.Name, x.Pinyin)
	}

	// getchildren 补拉街道同形态（无 name 无 pinyin）
	s := byAdcode["110105001"]
	if !exists(s) {
		t.Fatal("缺朝阳街道")
	}
	if s.Name != "" || s.Pinyin != "" {
		t.Fatalf("getchildren 节点应落空串，实际 Name=%q Pinyin=%q", s.Name, s.Pinyin)
	}
	if s.Lat != 39.9227 || s.Lng != 116.4384 {
		t.Fatalf("街道坐标应取嵌套 location (39.9227, 116.4384)，实际 (%v, %v)", s.Lat, s.Lng)
	}
}

// TestCollector_PathChain path 递归链（/110000000/110105000/街道9位/）
func TestCollector_PathChain(t *testing.T) {
	ft := &fakeTencent{listBody: fixtureList, children: defaultChildren()}
	_, col := newTestSource(t, ft)

	regions, err := col.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range regions {
		switch r.Adcode {
		case "110000000":
			if r.Path != "/110000000/" {
				t.Fatalf("省级 path 不符: %s", r.Path)
			}
		case "110105000":
			if r.Path != "/110000000/110105000/" {
				t.Fatalf("直辖市区 path 不符: %s", r.Path)
			}
		case "110105001":
			if r.Path != "/110000000/110105000/110105001/" {
				t.Fatalf("街道 path 不符: %s", r.Path)
			}
		case "441900003":
			// 直筒市街道：父级是市码 441900000（而非占位 441999000）
			if r.Path != "/440000000/441900000/441900003/" {
				t.Fatalf("直筒市街道 path 不符: %s", r.Path)
			}
		case "469001100":
			// 省直辖县级市街道：/海南省/五指山市/通什镇/
			if r.Path != "/460000000/469001000/469001100/" {
				t.Fatalf("省直辖县级市街道 path 不符: %s", r.Path)
			}
		}
	}
}

// TestFetch_DirectRuledTwoHop 直筒市占位二跳：东莞 getchildren(市码) 返回的是
// 占位节点（441999「东莞市」），须按占位码二跳取真镇街，且占位节点从输出剔除
func TestFetch_DirectRuledTwoHop(t *testing.T) {
	// children 表只放 441999 不放 441900：若实现误按市码一跳，误调用即空 body 报错
	ft := &fakeTencent{listBody: fixtureList, children: defaultChildren()}
	_, col := newTestSource(t, ft)

	regions, err := col.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byAdcode := findByAdcode(regions)

	// 真镇街：level=3、ParentCode=市码 441900000
	for _, code := range []string{"441900003", "441900004"} {
		s, ok := byAdcode[code]
		if !ok {
			t.Fatalf("缺直筒市真镇街 %s", code)
		}
		if s.Level != 3 || s.ParentCode != "441900000" {
			t.Fatalf("%s 应 level=3 且 ParentCode=441900000，实际 level=%d ParentCode=%s", code, s.Level, s.ParentCode)
		}
	}
	if p := byAdcode["441900003"].Path; p != "/440000000/441900000/441900003/" {
		t.Fatalf("直筒市街道 path 不符: %s", p)
	}

	// 占位节点 MUST NOT 残留（9 位补零后 441999000）
	if _, exists := byAdcode["441999000"]; exists {
		t.Fatal("占位节点 441999000 应从输出剔除")
	}
}

// TestFetch_ProvinceDirectOneHop 省直辖县级市一跳：五指山市块无 cidx 无孩子，
// getchildren(市码) 一跳即真镇街（children 表无 469001 则误跳过会缺失报错侧未覆盖，
// 此处正向断言镇街形态与 path）
func TestFetch_ProvinceDirectOneHop(t *testing.T) {
	ft := &fakeTencent{listBody: fixtureList, children: defaultChildren()}
	_, col := newTestSource(t, ft)

	regions, err := col.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byAdcode := findByAdcode(regions)

	// 市块节点本体：level=2、挂在海南省下
	w := byAdcode["469001000"]
	if !exists(w) {
		t.Fatal("缺五指山市")
	}
	if w.Level != 2 || w.ParentCode != "460000000" {
		t.Fatalf("五指山应 level=2 且 ParentCode=460000000，实际 level=%d ParentCode=%s", w.Level, w.ParentCode)
	}

	// 一跳补拉的真镇街：level=3、ParentCode=市码 469001000
	for _, code := range []string{"469001100", "469001101"} {
		s, ok := byAdcode[code]
		if !ok {
			t.Fatalf("缺省直辖县级市镇街 %s", code)
		}
		if s.Level != 3 || s.ParentCode != "469001000" {
			t.Fatalf("%s 应 level=3 且 ParentCode=469001000，实际 level=%d ParentCode=%s", code, s.Level, s.ParentCode)
		}
	}
	if p := byAdcode["469001100"].Path; p != "/460000000/469001000/469001100/" {
		t.Fatalf("省直辖县级市镇街 path 不符: %s", p)
	}
}

// TestFetch_HongkongNoCall 港澳区不发起 getchildren：香港的区挂在市块
// （Level=2、ParentCode=810000），getchildren 返回合法空，MUST NOT 发起调用。
// children 表不放 811100——误调用则空 body 解析失败重试耗尽 → Fetch 报错即挂
func TestFetch_HongkongNoCall(t *testing.T) {
	ft := &fakeTencent{
		listBody: buildList(
			`[{"id":"810000","name":"香港","fullname":"香港特别行政区","pinyin":["xiang","gang"],"location":{"lat":22.2849,"lng":114.1577},"cidx":[0,0]}]`,
			`[{"id":"811100","fullname":"中西区","location":{"lat":22.2836,"lng":114.1545}}]`,
			`[]`,
		),
		children: map[string]string{}, // 不含 811100（硬失败法）
	}
	_, col := newTestSource(t, ft)

	regions, err := col.Fetch(context.Background())
	if err != nil {
		t.Fatalf("港澳区不补街，Fetch 不应报错: %v", err)
	}
	byAdcode := findByAdcode(regions)

	d := byAdcode["811100000"]
	if !exists(d) {
		t.Fatal("缺香港中西区（应来自 list、9 位 811100000）")
	}
	if d.Level != 2 || d.ParentCode != "810000000" {
		t.Fatalf("中西区应 level=2 且 ParentCode=810000000，实际 level=%d ParentCode=%s", d.Level, d.ParentCode)
	}
	if d.Fullname != "中西区" {
		t.Fatalf("中西区 Fullname 不符: %q", d.Fullname)
	}

	// 未发起任何 getchildren（列表数据即完整，无冗余调用）
	if n := atomic.LoadInt32(&ft.getchildrenN); n != 0 {
		t.Fatalf("港澳区不应发起 getchildren，实际调用 %d 次", n)
	}
}

// TestFetch_SanshaNoCall 三沙标准形态不冗余采集：市块 cidx 指向区块 3 个真实
// 区划（无一命中占位指纹），children 表不放 460300——误调用即空 body 报错
func TestFetch_SanshaNoCall(t *testing.T) {
	ft := &fakeTencent{
		listBody: buildList(
			`[{"id":"460000","name":"海南","fullname":"海南省","pinyin":["hai","nan"],"location":{"lat":20.018639,"lng":110.348781},"cidx":[0,0]}]`,
			`[{"id":"460300","name":"三沙","fullname":"三沙市","pinyin":["san","sha"],"location":{"lat":16.831,"lng":112.338},"cidx":[0,2]}]`,
			`[{"id":"460321","fullname":"西沙群岛","location":{"lat":16.83,"lng":112.34}},{"id":"460322","fullname":"南沙群岛","location":{"lat":10.28,"lng":114.26}},{"id":"460323","fullname":"中沙群岛","location":{"lat":15.78,"lng":114.3}}]`,
		),
		children: map[string]string{}, // 不含 460300（硬失败法）
	}
	_, col := newTestSource(t, ft)

	regions, err := col.Fetch(context.Background())
	if err != nil {
		t.Fatalf("三沙有真实区划不补街，Fetch 不应报错: %v", err)
	}
	byAdcode := findByAdcode(regions)

	for _, code := range []string{"460321000", "460322000", "460323000"} {
		d, ok := byAdcode[code]
		if !ok {
			t.Fatalf("缺三沙区划 %s", code)
		}
		if d.Level != 3 || d.ParentCode != "460300000" {
			t.Fatalf("%s 应 level=3 且 ParentCode=460300000，实际 level=%d ParentCode=%s", code, d.Level, d.ParentCode)
		}
	}

	// 不发起冗余 getchildren
	if n := atomic.LoadInt32(&ft.getchildrenN); n != 0 {
		t.Fatalf("三沙标准形态不应发起 getchildren，实际调用 %d 次", n)
	}
}

// TestFetch_TaiwanGuardNoCall 台湾省码守卫：现行形态台湾各市有 cidx 指向真实区划、
// 本就无需补拉；本夹具构造形态变化场景（台北市无 cidx 无孩子）验证守卫不对其
// 发起 getchildren——children 表不放 710100，误调用即空 body 解析失败即挂
func TestFetch_TaiwanGuardNoCall(t *testing.T) {
	ft := &fakeTencent{
		listBody: buildList(
			`[{"id":"710000","name":"台湾","fullname":"台湾省","pinyin":["tai","wan"],"location":{"lat":23.7,"lng":120.96},"cidx":[0,0]}]`,
			`[{"id":"710100","name":"台北","fullname":"台北市","pinyin":["tai","bei"],"location":{"lat":25.03,"lng":121.57}}]`,
			`[]`,
		),
		children: map[string]string{}, // 不含 710100（硬失败法）
	}
	_, col := newTestSource(t, ft)

	regions, err := col.Fetch(context.Background())
	if err != nil {
		t.Fatalf("台湾市无 cidx 时守卫应拦截补拉，Fetch 不应报错: %v", err)
	}
	byAdcode := findByAdcode(regions)

	c := byAdcode["710100000"]
	if !exists(c) {
		t.Fatal("缺台北市（应来自 list、9 位 710100000）")
	}
	if c.Level != 2 || c.ParentCode != "710000000" {
		t.Fatalf("台北市应 level=2 且 ParentCode=710000000，实际 level=%d ParentCode=%s", c.Level, c.ParentCode)
	}
	if n := atomic.LoadInt32(&ft.getchildrenN); n != 0 {
		t.Fatalf("台湾市无 cidx 不应发起 getchildren，实际调用 %d 次", n)
	}
}

// TestFetch_FourBranchSmoke 四分支合成冒烟：一个夹具同时含直辖市区补街 /
// 直筒市占位二跳 / 省直辖县级市一跳 / 港澳区不调用，一次 Fetch 跑通并断言各分支特征
func TestFetch_FourBranchSmoke(t *testing.T) {
	// 布局：北京 cidx 跳级→区块朝阳；广东→市块东莞（cidx→区块占位 441999）；
	// 海南→市块五指山（无 cidx）；香港→市块区 811100（不调用）。
	// children 放三个分支请求码 110105/441999/469001，不放 811100（硬失败法）
	ft := &fakeTencent{
		listBody: buildList(
			`[{"id":"110000","name":"北京","fullname":"北京市","pinyin":["bei","jing"],"location":{"lat":39.9,"lng":116.4},"cidx":[0,0]},{"id":"440000","name":"广东","fullname":"广东省","pinyin":["guang","dong"],"location":{"lat":23.13,"lng":113.27},"cidx":[0,0]},{"id":"460000","name":"海南","fullname":"海南省","pinyin":["hai","nan"],"location":{"lat":20.018639,"lng":110.348781},"cidx":[1,1]},{"id":"810000","name":"香港","fullname":"香港特别行政区","pinyin":["xiang","gang"],"location":{"lat":22.2849,"lng":114.1577},"cidx":[2,2]}]`,
			`[{"id":"441900","name":"东莞","fullname":"东莞市","pinyin":["dong","guan"],"location":{"lat":23.02,"lng":113.75},"cidx":[1,1]},{"id":"469001","name":"五指山","fullname":"五指山市","pinyin":["wu","zhi","shan"],"location":{"lat":18.78,"lng":109.52}},{"id":"811100","fullname":"中西区","location":{"lat":22.2836,"lng":114.1545}}]`,
			`[{"id":"110105","fullname":"朝阳区","location":{"lat":39.92,"lng":116.43}},{"id":"441999","fullname":"东莞市","location":{"lat":23.02,"lng":113.75}}]`,
		),
		children: map[string]string{
			"110105": streetsChaoyang,
			"441999": streetsDongguan,
			"469001": streetsWuzhishan,
		},
	}
	_, col := newTestSource(t, ft)

	regions, err := col.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byAdcode := findByAdcode(regions)

	// 分支一（直辖市区）：朝阳 level 修正 2，街道 level=3
	if c := byAdcode["110105000"]; !exists(c) || c.Level != 2 {
		t.Fatalf("直辖市区应 level=2: %+v", byAdcode["110105000"])
	}
	if s := byAdcode["110105001"]; !exists(s) || s.Level != 3 || s.ParentCode != "110105000" {
		t.Fatalf("直辖市区街道应 level=3 且 ParentCode=110105000: %+v", byAdcode["110105001"])
	}

	// 分支二（直筒市占位二跳）：真镇街在、占位无残留
	if s := byAdcode["441900003"]; !exists(s) || s.Level != 3 || s.ParentCode != "441900000" {
		t.Fatalf("直筒市镇街应 level=3 且 ParentCode=441900000: %+v", byAdcode["441900003"])
	}
	if _, exists := byAdcode["441999000"]; exists {
		t.Fatal("占位节点 441999000 应从输出剔除")
	}

	// 分支三（省直辖县级市一跳）：镇街 parent=市码
	if s := byAdcode["469001100"]; !exists(s) || s.Level != 3 || s.ParentCode != "469001000" {
		t.Fatalf("省直辖县级市镇街应 level=3 且 ParentCode=469001000: %+v", byAdcode["469001100"])
	}

	// 分支四（港澳区不调用）：区仍在输出、level=2
	if d := byAdcode["811100000"]; !exists(d) || d.Level != 2 || d.ParentCode != "810000000" {
		t.Fatalf("港澳区应 level=2 且 ParentCode=810000000: %+v", byAdcode["811100000"])
	}

	// 总账：4 省 + 3 市块节点（东莞/五指山/中西区）+ 区块 2 剔占位余 1 + 街道 2×3 = 14
	if len(regions) != 14 {
		t.Fatalf("输出节点数应为 14（4+3+1+6），实际 %d", len(regions))
	}

	// 恰好 3 次 getchildren（朝阳 + 占位 441999 + 五指山），港澳未触发
	if n := atomic.LoadInt32(&ft.getchildrenN); n != 3 {
		t.Fatalf("getchildren 应恰好调用 3 次，实际 %d 次", n)
	}
}

// TestFetch_IntegrityFailFast 完整性契约：任一 getchildren 失败 → 整体报错，
// 不返回部分数据（调用方修复后整体重跑，静默缺口会污染地址库）
func TestFetch_IntegrityFailFast(t *testing.T) {
	ft := &fakeTencent{
		listBody: fixtureList,
		children: defaultChildren(),
		failIds:  map[string]bool{"110105": true}, // 一跳分支（朝阳）失败
	}
	src, _ := newTestSource(t, ft)

	regions, err := src.Fetch(context.Background())
	if err == nil {
		t.Fatalf("单点 getchildren 失败应整体报错，实际返回 %d 个节点", len(regions))
	}
	if !strings.Contains(err.Error(), "110105") {
		t.Fatalf("错误信息应包含失败 adcode: %v", err)
	}
	if len(regions) != 0 {
		t.Fatalf("失败时不得返回部分数据，实际 %d 个节点", len(regions))
	}
}

// TestFetch_EmptyResultFails 完整性契约：getchildren status=0 但空 result 同样报错
func TestFetch_EmptyResultFails(t *testing.T) {
	children := defaultChildren()
	children["469001"] = `{"status":0,"result":[]}` // 省直辖一跳分支注入空 result
	ft := &fakeTencent{listBody: fixtureList, children: children}
	src, _ := newTestSource(t, ft)

	if _, err := src.Fetch(context.Background()); err == nil {
		t.Fatal("空 result 应报错（街道数据缺失即不完整）")
	}
}

// TestInterval_QPS 间隔下界：getchildren 间隔不小于注入值
func TestInterval_QPS(t *testing.T) {
	ft := &fakeTencent{listBody: fixtureList, children: defaultChildren()}
	c := tencent.New("k", "")
	c.BaseURL = ft.server(t).URL
	c.Sleep = func(time.Duration) {}
	src := NewTencentSource(c)
	var sleeps []time.Duration
	src.SetInterval(5*time.Millisecond, func(d time.Duration) { sleeps = append(sleeps, d) })

	if _, err := src.Fetch(context.Background()); err != nil {
		t.Fatal(err)
	}
	// 三次 getchildren（朝阳一跳 + 东莞占位二跳 441999 + 五指山一跳），每次后都应 sleep
	if len(sleeps) != 3 {
		t.Fatalf("三次 getchildren 应各 sleep 一次，实际 %d", len(sleeps))
	}
	for _, d := range sleeps {
		if d != 5*time.Millisecond {
			t.Fatalf("间隔值应透传: %v", d)
		}
	}
}

// TestCollector_EmptySource 空数据安全
func TestCollector_EmptySource(t *testing.T) {
	ft := &fakeTencent{listBody: `{"status":0,"result":[]}`}
	_, col := newTestSource(t, ft)
	regions, err := col.Fetch(context.Background())
	if err != nil || len(regions) != 0 {
		t.Fatalf("空数据应返回空切片: %v %v", regions, err)
	}
}

// exists Region 存在性判定辅助（map 取出的零值即缺失：Adcode 为空）
func exists(r Region) bool {
	return r.Adcode != ""
}
