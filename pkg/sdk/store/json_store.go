package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// JSONStore 实现基于本地 JSON 文件和跨进程文件锁的 TokenStore。
// 必须同时具备进程内 sync.Mutex 与跨进程 FileLock，因为同一个进程内的多个
// goroutine 竞争同一文件锁在部分 OS 实现中可能未定义，双层锁确保进程内与多进程间均完全互斥。
//
// 契约约束：
// Go 互斥锁设计不支持重入。在 Mutate(fn) 或持有 Lock() 的临界区内，如需写入磁盘，
// 调用方必须使用 SaveUnlocked(source)；禁止在同一 Goroutine 持锁期间调用 Save(source)，
// 否则将引发预期的互斥自死锁。
type JSONStore struct {
	path     string
	lockPath string
	inProcMu sync.Mutex
}

// NewJSONStore 创建一个 JSONStore 实例。
// 若未显式传入 lockPath，则默认在同一目录下创建同名 .lock 文件，
// 确保锁文件与数据文件位于同一文件系统挂载点上。
func NewJSONStore(path string, customLockPath ...string) *JSONStore {
	lp := path + ".lock"
	if len(customLockPath) > 0 && customLockPath[0] != "" {
		lp = customLockPath[0]
	}
	return &JSONStore{
		path:     path,
		lockPath: lp,
	}
}

// Path 返回数据存储文件的绝对/相对路径。
func (s *JSONStore) Path() string {
	return s.path
}

// LockPath 返回锁文件的绝对/相对路径。
func (s *JSONStore) LockPath() string {
	return s.lockPath
}

// Load 从磁盘读取 JSON 文件内容并反序列化至 target 结构体指针中。
func (s *JSONStore) Load(target any) error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("解析存储文件 %s 失败: %w", s.path, err)
	}
	return nil
}

// Save 在跨进程排他锁保护下将 source 序列化为 JSON 并原子化写入磁盘。
// 采用标准无条件互斥锁语义，确保所有并发 Goroutine 和跨进程写操作严格排队，杜绝数据竞争。
// 注意：若当前调用方已在 Mutate 闭包或已持有 Lock()，请直接调用 SaveUnlocked 写入，避免自死锁。
func (s *JSONStore) Save(source any) error {
	unlock, err := s.Lock()
	if err != nil {
		return err
	}
	defer unlock()

	return s.SaveUnlocked(source)
}

// SaveUnlocked 在调用方已持有排他锁（例如在 Mutate 事务或 Lock 保护范围）时直接执行原子写入。
// 之所以提供此方法，是因为 Go 互斥锁不支持可重入，分离加锁入口与无锁写入能向调用者提供明确的事务契约。
func (s *JSONStore) SaveUnlocked(source any) error {
	data, err := json.MarshalIndent(source, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化数据失败: %w", err)
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("创建存储目录 %s 失败: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".store-*.tmp")
	if err != nil {
		return fmt.Errorf("创建临时写入文件失败: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		if _, statErr := os.Stat(tmpPath); statErr == nil {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("写入临时文件失败: %w", err)
	}
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("修改临时文件权限失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭临时文件失败: %w", err)
	}

	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("原子替换存储文件失败: %w", err)
	}
	return nil
}

// Lock 获取进程内互斥锁与跨进程文件锁，返回释放闭包。
// 必须严格先获取进程内 inProcMu 再获取操作系统文件锁，确保多 Goroutine 调度时
// 不会因操作系统内核文件锁句柄重入或状态冲突引发未定义行为。
func (s *JSONStore) Lock() (func(), error) {
	s.inProcMu.Lock()

	fl, err := AcquireLock(s.lockPath)
	if err != nil {
		s.inProcMu.Unlock()
		return nil, fmt.Errorf("获取跨进程文件锁 %s 失败: %w", s.lockPath, err)
	}

	var once sync.Once
	unlock := func() {
		once.Do(func() {
			_ = fl.Close()
			s.inProcMu.Unlock()
		})
	}
	return unlock, nil
}

// Mutate 在排他锁保护下执行读取、业务修改与保存事务。
// 闭包 fn 内部已经处于临界区保护，若闭包内需要写盘保存，必须调用 SaveUnlocked；
// 严禁在 fn 内部调用 Save，否则将导致死锁。
func (s *JSONStore) Mutate(fn func() error) error {
	unlock, err := s.Lock()
	if err != nil {
		return err
	}
	defer unlock()

	return fn()
}
