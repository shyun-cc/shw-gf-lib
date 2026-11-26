package task

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// failProducer 恒败 fake（投递失败文案与重投失败取证）
type failProducer struct{ calls int }

func (p *failProducer) Publish(_ context.Context, _ string) error {
	p.calls++
	return errors.New("mq down")
}

// TestSubmit_PublishMatrix 判定矩阵四象限：ViaQueue×注册，任一为真即发消息
func TestSubmit_PublishMatrix(t *testing.T) {
	reg := NewTypeRegistry()
	reg.Register("reg")

	cases := []struct {
		name        string
		typ         string
		viaQueue    bool
		wantPublish int
	}{
		{"ViaQueue真+已注册", "reg", true, 1},
		{"ViaQueue真+未注册", "plain", true, 1},
		{"ViaQueue假+已注册", "reg", false, 1},
		{"ViaQueue假+未注册", "plain", false, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prod := &countingProducer{}
			d := NewDispatcher(newMemRepo(), prod)
			d.QueueTypes = reg
			if _, err := d.Submit(context.Background(), CreationOptions{Type: c.typ, ViaQueue: c.viaQueue}); err != nil {
				t.Fatal(err)
			}
			if len(prod.published) != c.wantPublish {
				t.Fatalf("发消息数=%d want %d: %v", len(prod.published), c.wantPublish, prod.published)
			}
		})
	}
}

// TestSubmit_QueueTypesNil 注册表 nil（未设字段）时行为不变：仅按 ViaQueue 决定
func TestSubmit_QueueTypesNil(t *testing.T) {
	prod := &countingProducer{}
	d := NewDispatcher(newMemRepo(), prod) // QueueTypes 未设
	ctx := context.Background()

	if _, err := d.Submit(ctx, CreationOptions{Type: "reg", ViaQueue: false}); err != nil {
		t.Fatal(err)
	}
	if len(prod.published) != 0 {
		t.Fatalf("nil 注册表时 pull 提交不应发消息: %v", prod.published)
	}
	if _, err := d.Submit(ctx, CreationOptions{Type: "reg", ViaQueue: true}); err != nil {
		t.Fatal(err)
	}
	if len(prod.published) != 1 {
		t.Fatalf("nil 注册表时 ViaQueue 提交应发消息: %v", prod.published)
	}
}

// TestSubmit_ViaQueueUnregisteredStillPublishes ViaQueue=true 但未注册：正常发布不报错
// （仅打 warning 提示重试不重投）
func TestSubmit_ViaQueueUnregisteredStillPublishes(t *testing.T) {
	prod := &countingProducer{}
	d := NewDispatcher(newMemRepo(), prod)
	d.QueueTypes = NewTypeRegistry() // 空注册表：type 未注册

	tk, err := d.Submit(context.Background(), CreationOptions{Type: "plain", ViaQueue: true})
	if err != nil {
		t.Fatalf("未注册不应导致提交失败: %v", err)
	}
	if len(prod.published) != 1 || prod.published[0] != tk.ID {
		t.Fatalf("ViaQueue=true 未注册仍应发恰一条消息: %v", prod.published)
	}
}

// TestSubmit_PublishFailWording 投递失败文案如实：说明已落表不回滚、注册 type 由
// 重试重投拉起；不得再宣称「RetryScan 会兜底拉起」的无条件承诺
func TestSubmit_PublishFailWording(t *testing.T) {
	// 未注册 type：仅 ViaQueue 触发
	unregistered := NewDispatcher(newMemRepo(), &failProducer{})
	_, err := unregistered.Submit(context.Background(), CreationOptions{Type: "plain", ViaQueue: true})
	if err == nil {
		t.Fatal("投递失败应返回错误")
	}
	if !strings.Contains(err.Error(), "任务已落表") {
		t.Fatalf("文案应说明已落表: %v", err)
	}
	if strings.Contains(err.Error(), "RetryScan 会兜底拉起") {
		t.Fatalf("文案不得宣称 RetryScan 无条件兜底: %v", err)
	}

	// 注册 type：注册表触发（ViaQueue=false）
	reg := NewTypeRegistry()
	reg.Register("reg")
	registered := NewDispatcher(newMemRepo(), &failProducer{})
	registered.QueueTypes = reg
	_, err = registered.Submit(context.Background(), CreationOptions{Type: "reg", ViaQueue: false})
	if err == nil {
		t.Fatal("注册 type 投递失败应返回错误")
	}
	if !strings.Contains(err.Error(), "任务已落表") {
		t.Fatalf("文案应说明已落表: %v", err)
	}
	if !strings.Contains(err.Error(), "重试重投") {
		t.Fatalf("文案应说明注册 type 由重试重投拉起: %v", err)
	}
	if strings.Contains(err.Error(), "RetryScan 会兜底拉起") {
		t.Fatalf("文案不得宣称 RetryScan 无条件兜底: %v", err)
	}
}

// TestSplitAndSubmit_RegistrySendsParent 注册 type 下 SplitAndSubmit 的父任务发布判定：
// 拆分提交强制 ViaQueue=false，但 type 已注册 queue 模式时 Submit 按注册表照发（行为契约）
func TestSplitAndSubmit_RegistrySendsParent(t *testing.T) {
	payloads := [][]byte{[]byte(`{"i":1}`), []byte(`{"i":2}`)}

	reg := NewTypeRegistry()
	reg.Register("split-reg")
	prod := &countingProducer{}
	d := NewDispatcher(newMemRepo(), prod)
	d.QueueTypes = reg
	parent, children, err := d.SplitAndSubmit(context.Background(),
		CreationOptions{Type: "split-reg"}, payloads)
	if err != nil {
		t.Fatal(err)
	}
	if len(prod.published) != 1 || prod.published[0] != parent.ID {
		t.Fatalf("注册 type 拆分提交父任务应发消息: published=%v parent=%s", prod.published, parent.ID)
	}
	if len(children) != 2 {
		t.Fatalf("子任务数应 2: %d", len(children))
	}

	// 未注册 type：父任务不发（保持 pull 语义）
	prod2 := &countingProducer{}
	d2 := NewDispatcher(newMemRepo(), prod2)
	if _, _, err := d2.SplitAndSubmit(context.Background(),
		CreationOptions{Type: "split-plain"}, payloads); err != nil {
		t.Fatal(err)
	}
	if len(prod2.published) != 0 {
		t.Fatalf("未注册 type 拆分提交父任务不应发消息: %v", prod2.published)
	}
}
