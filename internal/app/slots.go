package app

import "sync"

// slotPool 是服务器同时执行的生成任务槽位池。容量来自后台配置的「服务端并行生成数」
// （settings.serverSlots，默认 defaultServerSlots），可以在管理后台改完即时生效：
//
//   - 扩容：立即唤醒调度器补位，排队任务马上开工；
//   - 缩容：不打断正在执行的任务，只是不再启动新任务，等它们自然结束后收敛到新容量。
//
// 占位与释放都是非阻塞的 tryAcquire/release：占不到位的任务留在 queued 状态排队，
// 由调度器在有空位时按创建顺序启动。
type slotPool struct {
	mu    sync.Mutex
	limit int
	inUse int
}

func newSlotPool(limit int) *slotPool {
	if limit < 1 {
		limit = defaultServerSlots
	}
	return &slotPool{limit: limit}
}

// tryAcquire 尝试占用一个槽位；占不到时返回 false，调用方转入排队。
func (p *slotPool) tryAcquire() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.inUse >= p.limit {
		return false
	}
	p.inUse++
	return true
}

// release 归还一个槽位。只在确实占用过时调用；重复释放不会把占用数压成负数。
func (p *slotPool) release() {
	p.mu.Lock()
	if p.inUse > 0 {
		p.inUse--
	}
	p.mu.Unlock()
}

func (p *slotPool) limitValue() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.limit
}

func (p *slotPool) used() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.inUse
}

// setLimit 热调整容量，返回容量是否变化；小于 1 的取值回落到默认值。
func (p *slotPool) setLimit(limit int) bool {
	if limit < 1 {
		limit = defaultServerSlots
	}
	p.mu.Lock()
	changed := p.limit != limit
	p.limit = limit
	p.mu.Unlock()
	return changed
}

// applyServerSlots 让后台保存的「服务端并行生成数」即时作用于调度器。
func (a *App) applyServerSlots(limit int) {
	if a.slots.setLimit(limit) {
		a.signalDispatch()
	}
}
