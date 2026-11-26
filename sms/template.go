package sms

import (
	"fmt"
	"regexp"
	"sort"
)

var placeholderRE = regexp.MustCompile(`\$\{([A-Za-z][A-Za-z0-9_]*)\}`)

// Placeholders 返回 ${name} 形式占位符的去重、排序名称。
// name 必须以 ASCII 字母开头，其余字符为字母、数字或下划线。
func Placeholders(content string) []string {
	seen := map[string]struct{}{}
	for _, m := range placeholderRE.FindAllStringSubmatch(content, -1) {
		seen[m[1]] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// ResolveParams 将项目上下文变量映射为厂商模板参数。
// mappings 的键必须恰好覆盖占位符，值须在 allowed 中且存在于 values。
// values 不得包含未声明的变量；允许声明但本模板未使用的变量。
// 不修改输入、不执行表达式、不验证厂商的变量值格式；错误只包含变量名。
func ResolveParams(content string, mappings map[string]string, allowed []string, values map[string]any) (map[string]any, error) {
	names := Placeholders(content)
	if len(names) != len(mappings) {
		return nil, fmt.Errorf("模板占位符映射不完整")
	}
	allow := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		allow[name] = struct{}{}
	}
	result := make(map[string]any, len(names))
	for _, name := range names {
		source, ok := mappings[name]
		if !ok || source == "" {
			return nil, fmt.Errorf("模板占位符 %s 未配置映射", name)
		}
		if _, ok := allow[source]; !ok {
			return nil, fmt.Errorf("模板变量 %s 未声明", source)
		}
		value, ok := values[source]
		if !ok {
			return nil, fmt.Errorf("发送上下文缺少变量 %s", source)
		}
		result[name] = value
	}
	keys := make([]string, 0, len(values))
	for name := range values {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, name := range keys {
		if _, ok := allow[name]; !ok {
			return nil, fmt.Errorf("发送上下文含未声明变量 %s", name)
		}
	}
	return result, nil
}
