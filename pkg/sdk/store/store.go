package store

// TokenStore 定义凭据与配置的持久化及跨进程同步接口。
// 之所以将跨进程锁与持久化操作绑定在同一接口中，是为了保证
// 在多进程并发轮换 Token 或写入配置时，事务临界区能够严格闭环，
// 避免展示层直接感知底层的 OS 锁细节。
//
// 并发与锁契约：
// Go 互斥锁不支持可重入。在 Mutate(fn) 或已显式调用 Lock() 的临界区内，
// 若需要将变更落盘，必须调用 SaveUnlocked；禁止在持锁期间再次调用 Save，避免互斥自死锁。
type TokenStore interface {
	// Load 读取存储内容并反序列化至 target 结构体指针中。
	Load(target any) error

	// Save 在独占锁保护下将 source 数据原子化写入存储媒介。无锁上下文调用此方法会自动加锁并在返回前释放。
	Save(source any) error

	// SaveUnlocked 在调用者已持有 Lock() 或处于 Mutate 事务上下文时直接原子化写入存储媒介。
	SaveUnlocked(source any) error

	// Mutate 执行受锁保护的读取-修改-保存事务，避免多进程与多协程读取到脏数据。闭包内部落盘必须使用 SaveUnlocked。
	Mutate(fn func() error) error

	// Lock 获取跨进程与进程内排他锁，返回用于释放锁的闭包函数。
	Lock() (unlock func(), err error)
}
