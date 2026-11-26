package task

import (
	"sync"
	"testing"
)

// TestRegistry_RegisteredIsQueue 注册后 IsQueue=true；未注册一律 false
func TestRegistry_RegisteredIsQueue(t *testing.T) {
	r := NewTypeRegistry()
	if r.IsQueue("job") {
		t.Fatal("未注册不应为 queue 模式")
	}
	r.Register("job")
	if !r.IsQueue("job") {
		t.Fatal("注册后应为 queue 模式")
	}
	if r.IsQueue("other") {
		t.Fatal("其余 type 仍为 pull 模式")
	}
	r.Register("job") // 重复注册幂等
	if !r.IsQueue("job") {
		t.Fatal("重复注册应幂等保持 queue 模式")
	}
}

// TestRegistry_ModeValues Mode 常量约定：ModePull=0（零值默认）ModeQueue=1
func TestRegistry_ModeValues(t *testing.T) {
	if ModePull != 0 || ModeQueue != 1 {
		t.Fatalf("Mode 常量漂移: ModePull=%d ModeQueue=%d", ModePull, ModeQueue)
	}
}

// TestRegistry_ZeroValueUsable 零值 TypeRegistry 不经构造函数可直接注册（懒初始化不 panic）
func TestRegistry_ZeroValueUsable(t *testing.T) {
	var r TypeRegistry
	if r.IsQueue("job") {
		t.Fatal("零值注册表查询不应为 queue 模式")
	}
	r.Register("job")
	if !r.IsQueue("job") {
		t.Fatal("零值注册后应可用")
	}
}

// TestRegistry_ConcurrentRegister 并发注册与查询不丢不串（-race 下验证锁正确）
func TestRegistry_ConcurrentRegister(t *testing.T) {
	r := NewTypeRegistry()
	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			typ := "t" + itoa(idx)
			r.Register(typ)
			if !r.IsQueue(typ) {
				t.Errorf("注册后立即可见: %s", typ)
			}
		}(i)
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		if !r.IsQueue("t" + itoa(i)) {
			t.Fatalf("并发注册后应全部可见: t%d", i)
		}
	}
}
