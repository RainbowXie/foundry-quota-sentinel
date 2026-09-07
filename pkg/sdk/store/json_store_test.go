package store

import (
	"path/filepath"
	"sync"
	"testing"
)

type testData struct {
	Count int               `json:"count"`
	Items map[string]string `json:"items"`
}

func TestJSONStore_Basic(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "store.json")
	s := NewJSONStore(storePath)

	data := testData{
		Count: 1,
		Items: map[string]string{"foo": "bar"},
	}

	if err := s.Save(data); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	var loaded testData
	if err := s.Load(&loaded); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if loaded.Count != 1 || loaded.Items["foo"] != "bar" {
		t.Fatalf("unexpected loaded data: %+v", loaded)
	}
}

func TestJSONStore_ConcurrentMutate(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "store.json")
	s := NewJSONStore(storePath)

	initial := testData{
		Count: 0,
		Items: make(map[string]string),
	}
	if err := s.Save(initial); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	const workers = 10
	const incrementsPerWorker = 20

	var wg sync.WaitGroup
	wg.Add(workers)

	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < incrementsPerWorker; j++ {
				err := s.Mutate(func() error {
					var cur testData
					if err := s.Load(&cur); err != nil {
						return err
					}
					cur.Count++
					return s.SaveUnlocked(cur)
				})
				if err != nil {
					t.Errorf("Mutate failed: %v", err)
				}
			}
		}()
	}

	wg.Wait()

	var final testData
	if err := s.Load(&final); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	expected := workers * incrementsPerWorker
	if final.Count != expected {
		t.Fatalf("expected count %d, got %d", expected, final.Count)
	}
}

// TestJSONStore_MutateDirectSaveProvesNoDeadlock 验证调用方在 Mutate 闭包中直接调用
// s.Save(cur) 不会发生自死锁，证明内建重入防御逻辑有效。
func TestJSONStore_MutateDirectSaveProvesNoDeadlock(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "store_deadlock.json")
	s := NewJSONStore(storePath)

	initial := testData{Count: 42}
	if err := s.Save(initial); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	err := s.Mutate(func() error {
		var cur testData
		if err := s.Load(&cur); err != nil {
			return err
		}
		cur.Count += 8
		// 直接调用 Save 而非 SaveUnlocked，验证不自死锁
		return s.Save(cur)
	})
	if err != nil {
		t.Fatalf("Mutate with direct Save failed: %v", err)
	}

	var loaded testData
	if err := s.Load(&loaded); err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.Count != 50 {
		t.Fatalf("expected count 50, got %d", loaded.Count)
	}
}

func TestFlock_TryLock(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "test.lock")

	lk1, err := AcquireLock(lockPath)
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}

	// 此时同一文件的排他锁已被 lk1 持有，TryLock 应当返回 ok == false
	lk2, ok, err := TryLock(lockPath)
	if err != nil {
		t.Fatalf("TryLock error: %v", err)
	}
	if ok {
		_ = lk2.Close()
		t.Fatalf("expected TryLock to fail when lock is held, but got ok=true")
	}

	if err := lk1.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// 释放后 TryLock 应成功
	lk3, ok, err := TryLock(lockPath)
	if err != nil {
		t.Fatalf("TryLock after close error: %v", err)
	}
	if !ok {
		t.Fatalf("expected TryLock to succeed after lock released")
	}
	_ = lk3.Close()
}
