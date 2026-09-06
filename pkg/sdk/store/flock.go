package store

import (
	"os"
	"path/filepath"
)

// FileLock 封装持有的跨进程文件排他锁。
// 必须保存底层 *os.File 句柄，因为在 Unix 和 Windows 上，
// 文件锁的生命周期都直接与打开的文件描述符/句柄绑定，
// 在 Close 时必须显式释放内核锁结构并关闭描述符。
type FileLock struct {
	f    *os.File
	path string
}

// AcquireLock 获取指定路径的跨进程排他锁。
// 若锁已被其他进程持有，该调用将阻塞等待，以保证多进程写入（如 Token 轮换）的严格串行化。
func AcquireLock(path string) (*FileLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := lockFileFD(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &FileLock{f: f, path: path}, nil
}

// TryLock 尝试以非阻塞方式获取跨进程排他锁。
// 当其他进程正在持有该锁时，返回 ok == false，供需要快速跳过或做冲突检测的场景使用。
func TryLock(path string) (*FileLock, bool, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, false, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, false, err
	}
	if err := tryLockFileFD(f); err != nil {
		_ = f.Close()
		return nil, false, nil
	}
	return &FileLock{f: f, path: path}, true, nil
}

// Close 释放持有的跨进程文件锁并关闭句柄。
// 此处严禁删除磁盘上的锁文件：因为并发进程可能正在相同的 inode 上阻塞等待，
// 若在释放时删除文件会导致等待者与新进程获取到不同 inode，造成锁互斥失效。
func (l *FileLock) Close() error {
	if l == nil || l.f == nil {
		return nil
	}
	unlockFileFD(l.f)
	return l.f.Close()
}

// Path 返回该锁对应的磁盘文件路径。
func (l *FileLock) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

// WithLock 在持有指定文件锁的环境下执行业务函数，确保退出时锁一定被释放。
func WithLock(path string, fn func() error) error {
	lk, err := AcquireLock(path)
	if err != nil {
		return err
	}
	defer lk.Close()
	return fn()
}
