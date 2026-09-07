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

// TestJSONStore_ConcurrentSaveSerializes 验证多个 Goroutine 并发直接调用 Save(cur) 时，
// 互斥锁严格生效，数据写入不会发生破坏或丢失，所有并发调用均能安全串行化落盘。
func TestJSONStore_ConcurrentSaveSerializes(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "store_concurrent_save.json")
	s := NewJSONStore(storePath)

	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		workerID := i
		go func() {
			defer wg.Done()
			data := testData{
				Count: workerID,
				Items: map[string]string{"worker": "done"},
			}
			if err := s.Save(data); err != nil {
				t.Errorf("Save failed: %v", err)
			}
		}()
	}

	wg.Wait()

	var loaded testData
	if err := s.Load(&loaded); err != nil {
		t.Fatalf("Load after concurrent Save failed: %v", err)
	}
	if loaded.Items["worker"] != "done" {
		t.Fatal("expected items[worker] == done after concurrent saves")
	}
}

// TestJSONStore_MutateWithSaveUnlocked 验证契约规范：调用方在 Mutate 闭包中使用 SaveUnlocked
// 能正确完成读取-修改-保存事务，且不会发生死锁。
func TestJSONStore_MutateWithSaveUnlocked(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "store_mutate_unlocked.json")
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
		return s.SaveUnlocked(cur)
	})
	if err != nil {
		t.Fatalf("Mutate with SaveUnlocked failed: %v", err)
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
