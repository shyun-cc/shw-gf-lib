// Package region 行政区划采集管道——只采集不存储。
//
// 形态：Source 接口（数据源适配器）+ Collector（透明管道：取标准化 Region[] 后
// 递归补 path）。存储下沉业务方。腾讯数据源实现在本包 tencent.go；
// 腾讯 HTTP 客户端在独立的 tencent 公开包。
package region

import "context"

// Region 标准化行政区划节点（adcode/parentCode 统一 9 位，右侧补零）
type Region struct {
	Adcode     string // 9 位行政区划码
	Name       string
	Fullname   string
	Level      int    // 1 省 2 市 3 区县/街道
	Path       string // /祖先…/自身/ 形式，9 位码（Collector 补全）
	ParentCode string // 父级 9 位码；省级为空
	Lat        float64
	Lng        float64
	Pinyin     string
}

// Source 行政区划数据源接口（适配器实现：拉取并标准化为 3 级 Region[]，path 留空）
type Source interface {
	Fetch(ctx context.Context) ([]Region, error)
}

// Collector 采集管道：调 Source 取数，补全 path 后返回。无任何存储副作用。
type Collector struct {
	src Source
}

// NewCollector 构造采集器
func NewCollector(src Source) *Collector {
	return &Collector{src: src}
}

// Fetch 采集并补 path
func (c *Collector) Fetch(ctx context.Context) ([]Region, error) {
	regions, err := c.src.Fetch(ctx)
	if err != nil {
		return nil, err
	}
	if len(regions) == 0 {
		return []Region{}, nil
	}

	byAdcode := make(map[string]*Region, len(regions))
	for i := range regions {
		byAdcode[regions[i].Adcode] = &regions[i]
	}

	pathCache := make(map[string]string, len(regions))
	result := make([]Region, len(regions))
	for i := range regions {
		result[i] = regions[i]
		result[i].Path = resolvePath(&regions[i], byAdcode, pathCache)
	}
	return result, nil
}

// resolvePath 递归计算 path（带缓存）：省级（无父）/{adcode}/；子级为父 path + 自身；
// 父节点缺失（异常数据）退化根路径。
func resolvePath(r *Region, byAdcode map[string]*Region, cache map[string]string) string {
	if p, ok := cache[r.Adcode]; ok {
		return p
	}
	var path string
	if r.ParentCode == "" {
		path = "/" + r.Adcode + "/"
	} else if parent, ok := byAdcode[r.ParentCode]; ok {
		path = resolvePath(parent, byAdcode, cache) + r.Adcode + "/"
	} else {
		path = "/" + r.Adcode + "/"
	}
	cache[r.Adcode] = path
	return path
}
