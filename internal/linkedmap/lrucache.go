package linkedmap

import "math"

// LRUCache corresponds to VS Code's LRUCache from src/vs/base/common/map.ts:
// a LinkedMap with a bounded size whose Get treats the entry as recently used.
type LRUCache[K comparable, V any] struct {
	LinkedMap[K, V]
	limit int
	ratio float64
}

// NewLRUCache corresponds to `new LRUCache(limit, ratio)`. The ratio is
// clamped to 0..1 as in VS Code and defaults to 1 when omitted.
func NewLRUCache[K comparable, V any](limit int, ratio ...float64) *LRUCache[K, V] {
	r := 1.0
	if len(ratio) > 0 {
		r = ratio[0]
	}
	return &LRUCache[K, V]{
		LinkedMap: *NewLinkedMap[K, V](),
		limit:     limit,
		ratio:     max(0, min(1, r)),
	}
}

// Limit corresponds to the Cache.limit getter.
func (c *LRUCache[K, V]) Limit() int {
	return c.limit
}

// SetLimit corresponds to the Cache.limit setter, including its immediate
// trim when the cache is over the new limit.
func (c *LRUCache[K, V]) SetLimit(limit int) {
	c.limit = limit
	c.checkTrim()
}

// Ratio corresponds to the Cache.ratio getter.
func (c *LRUCache[K, V]) Ratio() float64 {
	return c.ratio
}

// SetRatio corresponds to the Cache.ratio setter, including its immediate
// trim when the cache is over the limit.
func (c *LRUCache[K, V]) SetRatio(ratio float64) {
	c.ratio = max(0, min(1, ratio))
	c.checkTrim()
}

// Get corresponds to Cache.get / LRUCache's Touch.AsNew default.
func (c *LRUCache[K, V]) Get(key K, touch ...Touch) (V, bool) {
	mode := TouchAsNew
	if len(touch) > 0 {
		mode = touch[0]
	}
	return c.LinkedMap.Get(key, mode)
}

// Peek corresponds to Cache.peek: it reads without changing the order.
func (c *LRUCache[K, V]) Peek(key K) (V, bool) {
	return c.LinkedMap.Get(key, TouchNone)
}

// Set corresponds to LRUCache.set: the new entry becomes most recent and
// the cache is trimmed immediately.
func (c *LRUCache[K, V]) Set(key K, value V) *LRUCache[K, V] {
	c.LinkedMap.Set(key, value, TouchAsNew)
	c.checkTrim()
	return c
}

func (c *LRUCache[K, V]) checkTrim() {
	if c.Size() > c.limit {
		c.trimOld(int(math.Round(float64(c.limit) * c.ratio)))
	}
}
