//go:build !dev

// 默认构建（生产形态）命令分身的单元测试（外部测试包，fake runner，
// 不触真库、不 import goose）。
//
// 覆盖：命令元信息（Brief 无 fresh 字样、未声明任何参数）、Func
// 执行走 Up、传入清库参数（--fresh）不被识别仍走普通增量 up
// （spec 场景「默认构建传 --fresh：参数不被识别，执行普通增量 up」；
// gcmd 非 strict 解析下未声明选项被静默忽略，这正是生产行为）、
// 错误原样透传给 gcmd。
package migrate_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gogf/gf/v2/os/gcmd"
	"github.com/shyun-cc/shw-gf-lib/migrate"
)

// fakeUpRunner 默认构建命令的假 runner：记录 Up 调用次数，可注入
// 返回错误。
type fakeUpRunner struct {
	upCalls int
	err     error
}

// Up 记录一次调用并返回注入的错误（默认 nil）。
func (f *fakeUpRunner) Up(ctx context.Context) error {
	f.upCalls++
	return f.err
}

// TestNewCommandDefaultMeta 生产形态的命令元信息：名称 migrate、
// Brief 不出现 fresh 字样（默认构建物理无清库能力的对外表现）、
// 未声明任何参数。
func TestNewCommandDefaultMeta(t *testing.T) {
	cmd := migrate.NewCommand(&fakeUpRunner{})

	if cmd.Name != "migrate" {
		t.Fatalf("命令名称期望 %q，实际 %q", "migrate", cmd.Name)
	}
	if strings.Contains(strings.ToLower(cmd.Brief), "fresh") {
		t.Fatalf("生产形态 Brief 不应出现 fresh 字样，实际: %q", cmd.Brief)
	}
	if len(cmd.Arguments) != 0 {
		t.Fatalf("生产形态不应声明任何参数，实际声明 %d 个", len(cmd.Arguments))
	}
}

// TestNewCommandDefaultUp Func 直接调用（parser 传 nil，默认版不
// 解析任何参数）：执行一次 Up 并返回 nil。
func TestNewCommandDefaultUp(t *testing.T) {
	fake := &fakeUpRunner{}
	cmd := migrate.NewCommand(fake)

	if err := cmd.Func(context.Background(), nil); err != nil {
		t.Fatalf("Func() 不期望返回错误: %v", err)
	}
	if fake.upCalls != 1 {
		t.Fatalf("期望 Up 被调用 1 次，实际 %d 次", fake.upCalls)
	}
}

// TestNewCommandDefaultIgnoresFreshOption 生产形态传 --fresh：
// 参数未被声明、不被识别（gcmd 非 strict 解析静默忽略未声明选项），
// 命令仍执行普通增量 up。
func TestNewCommandDefaultIgnoresFreshOption(t *testing.T) {
	fake := &fakeUpRunner{}
	cmd := migrate.NewCommand(fake)

	// 空 supportedOptions：--fresh 不在任何声明表内，非 strict 下被忽略。
	parser, err := gcmd.ParseArgs([]string{"--fresh"}, map[string]bool{})
	if err != nil {
		t.Fatalf("构造测试 parser 失败: %v", err)
	}

	if err := cmd.Func(context.Background(), parser); err != nil {
		t.Fatalf("Func() 不期望返回错误: %v", err)
	}
	if fake.upCalls != 1 {
		t.Fatalf("期望忽略未知参数后执行 1 次 Up，实际 %d 次", fake.upCalls)
	}
}

// TestNewCommandDefaultErrorPassthrough runner 返回的 error 必须
// 原样透传给 gcmd（不吞不改，由 gcmd 负责退出码与错误打印）。
func TestNewCommandDefaultErrorPassthrough(t *testing.T) {
	errFake := errors.New("命令错误透传验证（单测注入）")
	cmd := migrate.NewCommand(&fakeUpRunner{err: errFake})

	err := cmd.Func(context.Background(), nil)
	if !errors.Is(err, errFake) {
		t.Fatalf("Func() 应原样透传 runner 错误，实际: %v", err)
	}
}
