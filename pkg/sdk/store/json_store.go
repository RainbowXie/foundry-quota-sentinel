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
// goroutine 竞争同一文件锁在部分 OS 实现中可能重入或未定义，双层锁确保进程内与多进程间均完全互斥。
type JSONStore struct {
	path       string
	lockPath   string
	inProcMu   sync.Mutex
	inFlightMu sync.Mutex
	lockedBy   uintptr
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
// 若当前调用上下文已经通过 Mutate 或 Lock 获得互斥锁，Save 会自动复用已持有的锁上下文执行安全写入，
// 彻底消除在 Mutate 闭包内直接调用 Save(cur) 引发的进程内自死锁陷阱。
func (s *JSONStore) Save(source any) error {
	s.inFlightMu.Lock()
	alreadyLocked := s.lockedBy != 0
	s.inFlightMu.Unlock()

	if alreadyLocked {
		return s.SaveUnlocked(source)
	}

	unlock, err := s.Lock()
	if err != nil {
		return err
	}
	defer unlock()

	return s.SaveUnlocked(source)
}

// SaveUnlocked 在调用方已持有排他锁时直接执行原子写入，避免同一个 goroutine 重入获取互斥锁导致死锁。
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
func (s *JSONStore) Lock() (func(), error) {
	s.inProcMu.Lock()

	fl, err := AcquireLock(s.lockPath)
	if err != nil {
		s.inProcMu.Unlock()
		return nil, fmt.Errorf("获取跨进程文件锁 %s 失败: %w", s.lockPath, err)
	}

	s.inFlightMu.Lock()
	s.lockedBy = 1
	s.inFlightMu.Unlock()

	var once sync.Once
	unlock := func() {
		once.Do(func() {
			s.inFlightMu.Lock()
			s.lockedBy = 0
			s.inFlightMu.Unlock()

			_ = fl.Close()
			s.inProcMu.Unlock()
		})
	}
	return unlock, nil
}

// Mutate 在排他锁保护下执行读取、业务修改与保存事务。
func (s *JSONStore) Mutate(fn func() error) error {
	unlock, err := s.Lock()
	if err != nil {
		return err
	}
	defer unlock()

	return fn()
}
