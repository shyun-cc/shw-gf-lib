//go:build dev

// dev 构建命令分身的单元测试（外部测试包，fake runner，不触真库、
// 不 import goose）。
//
// 覆盖：命令元信息（Brief 标注 [--fresh]、声明 fresh 参数）、带
// --fresh 分派 Fresh（spec 场景「先清库后从头应用全部迁移」的命令
// 层分派）、不带参数与 parser 为 nil 走 Up（行为与生产版一致）、
// Up / Fresh 双路径错误原样透传给 gcmd。
package migrate_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gogf/gf/v2/os/gcmd"
	"github.com/shyun-cc/shw-gf-lib/migrate"
)

// fakeDevRunner dev 构建命令的假 runner：分别记录 Up / Fresh 调用
// 次数，可分别注入返回错误。
type fakeDevRunner struct {
	upCalls    int
	freshCalls int
	upErr      error
	freshErr   error
}

// Up 记录一次 Up 调用并返回注入的错误（默认 nil）。
func (f *fakeDevRunner) Up(ctx context.Context) error {
	f.upCalls++
	return f.upErr
}

// Fresh 记录一次 Fresh 调用并返回注入的错误（默认 nil）。
func (f *fakeDevRunner) Fresh(ctx context.Context) error {
	f.freshCalls++
	return f.freshErr
}

// newDevTestParser 用 gcmd 的解析构造器生成 parser：声明 fresh 为
// 无值开关选项（与命令 Arguments 的 Orphan 声明一致），按传入的
// args 决定是否带上 --fresh。
func newDevTestParser(t *testing.T, args []string) *gcmd.Parser {
	t.Helper()
	parser, err := gcmd.ParseArgs(args, map[string]bool{"fresh": false})
	if err != nil {
		t.Fatalf("构造测试 parser 失败: %v", err)
	}
	return parser
}

// TestNewCommandDevMeta dev 形态的命令元信息：名称 migrate、Brief
// 标注 [--fresh]、声明 fresh 参数。
func TestNewCommandDevMeta(t *testing.T) {
	cmd := migrate.NewCommand(&fakeDevRunner{})

	if cmd.Name != "migrate" {
		t.Fatalf("命令名称期望 %q，实际 %q", "migrate", cmd.Name)
	}
	if !strings.Contains(cmd.Brief, "[--fresh]") {
		t.Fatalf("dev 形态 Brief 应标注 [--fresh]，实际: %q", cmd.Brief)
	}
	found := false
	for _, arg := range cmd.Arguments {
		if arg.Name == "fresh" {
			found = true
		}
	}
	if !found {
		t.Fatal("dev 形态应声明 fresh 参数")
	}
}

// TestNewCommandDevFreshDispatch 带 --fresh：命令层分派 Fresh，
// 不直接调 Up（从头应用全部迁移由 Fresh 内部完成）。
func TestNewCommandDevFreshDispatch(t *testing.T) {
	fake := &fakeDevRunner{}
	cmd := migrate.NewCommand(fake)

	parser := newDevTestParser(t, []string{"--fresh"})
	if err := cmd.Func(context.Background(), parser); err != nil {
		t.Fatalf("Func() 不期望返回错误: %v", err)
	}
	if fake.freshCalls != 1 {
		t.Fatalf("期望 Fresh 被调用 1 次，实际 %d 次", fake.freshCalls)
	}
	if fake.upCalls != 0 {
		t.Fatalf("--fresh 时命令层不应直接调 Up，实际调了 %d 次", fake.upCalls)
	}
}

// TestNewCommandDevWithoutFresh 不带参数：走 Up，行为与生产版一致。
func TestNewCommandDevWithoutFresh(t *testing.T) {
	fake := &fakeDevRunner{}
	cmd := migrate.NewCommand(fake)

	parser := newDevTestParser(t, nil)
	if err := cmd.Func(context.Background(), parser); err != nil {
		t.Fatalf("Func() 不期望返回错误: %v", err)
	}
	if fake.upCalls != 1 {
		t.Fatalf("期望 Up 被调用 1 次，实际 %d 次", fake.upCalls)
	}
	if fake.freshCalls != 0 {
		t.Fatalf("不带 --fresh 不应触发 Fresh，实际调了 %d 次", fake.freshCalls)
	}
}

// TestNewCommandDevNilParser parser 为 nil（非命令行入口直接调用）：
// 判空保护走 Up，与生产版行为一致。
func TestNewCommandDevNilParser(t *testing.T) {
	fake := &fakeDevRunner{}
	cmd := migrate.NewCommand(fake)

	if err := cmd.Func(context.Background(), nil); err != nil {
		t.Fatalf("Func() 不期望返回错误: %v", err)
	}
	if fake.upCalls != 1 {
		t.Fatalf("parser 为 nil 期望走 Up 1 次，实际 %d 次", fake.upCalls)
	}
	if fake.freshCalls != 0 {
		t.Fatalf("parser 为 nil 不应触发 Fresh，实际调了 %d 次", fake.freshCalls)
	}
}

// TestNewCommandDevErrorPassthrough Up 与 Fresh 双路径返回的 error
// 均原样透传给 gcmd（不吞不改）。
func TestNewCommandDevErrorPassthrough(t *testing.T) {
	errUp := errors.New("增量迁移错误透传验证（单测注入）")
	errFresh := errors.New("清库重建错误透传验证（单测注入）")

	// Up 路径：不带参数。
	cmdUp := migrate.NewCommand(&fakeDevRunner{upErr: errUp})
	if err := cmdUp.Func(context.Background(), nil); !errors.Is(err, errUp) {
		t.Fatalf("Up 路径应原样透传错误，实际: %v", err)
	}

	// Fresh 路径：带 --fresh。
	cmdFresh := migrate.NewCommand(&fakeDevRunner{freshErr: errFresh})
	parser := newDevTestParser(t, []string{"--fresh"})
	if err := cmdFresh.Func(context.Background(), parser); !errors.Is(err, errFresh) {
		t.Fatalf("Fresh 路径应原样透传错误，实际: %v", err)
	}
}
