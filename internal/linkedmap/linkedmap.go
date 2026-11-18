package linkedmap

import "iter"

// Touch corresponds to VS Code's Touch enum. AsOld moves an entry to the
// front (oldest) of the order; AsNew moves it to the back (newest).
type Touch uint8

const (
	// TouchNone corresponds to Touch.None.
	TouchNone Touch = iota
	// TouchAsOld corresponds to Touch.AsOld.
	TouchAsOld
	// TouchAsNew corresponds to Touch.AsNew.
	TouchAsNew
)

type item[K comparable, V any] struct {
	key      K
	value    V
	previous *item[K, V]
	next     *item[K, V]
}

// LinkedMap corresponds to VS Code's LinkedMap: a map that also keeps its
// entries in insertion/recency order. The zero value is ready to use.
type LinkedMap[K comparable, V any] struct {
	items map[K]*item[K, V]
	head  *item[K, V]
	tail  *item[K, V]
	size  int
	state uint64
}

// NewLinkedMap corresponds to `new LinkedMap()`.
func NewLinkedMap[K comparable, V any]() *LinkedMap[K, V] {
	return &LinkedMap[K, V]{items: make(map[K]*item[K, V])}
}

// Clear corresponds to LinkedMap.clear.
func (m *LinkedMap[K, V]) Clear() {
	clear(m.items)
	m.head = nil
	m.tail = nil
	m.size = 0
	m.state++
}

// IsEmpty corresponds to LinkedMap.isEmpty.
func (m *LinkedMap[K, V]) IsEmpty() bool {
	return m.head == nil && m.tail == nil
}

// Size corresponds to the LinkedMap.size getter.
func (m *LinkedMap[K, V]) Size() int {
	return m.size
}

// First corresponds to the LinkedMap.first getter. The bool reports whether
// an entry exists, replacing VS Code's undefined value.
func (m *LinkedMap[K, V]) First() (V, bool) {
	if m.head == nil {
		var zero V
		return zero, false
	}
	return m.head.value, true
}

// Last corresponds to the LinkedMap.last getter. The bool reports whether
// an entry exists, replacing VS Code's undefined value.
func (m *LinkedMap[K, V]) Last() (V, bool) {
	if m.tail == nil {
		var zero V
		return zero, false
	}
	return m.tail.value, true
}

// Has corresponds to LinkedMap.has.
func (m *LinkedMap[K, V]) Has(key K) bool {
	_, ok := m.items[key]
	return ok
}

// Get corresponds to LinkedMap.get. The bool reports whether the key exists;
// TS returns undefined instead. Touch defaults to TouchNone.
func (m *LinkedMap[K, V]) Get(key K, touch ...Touch) (V, bool) {
	entry, ok := m.items[key]
	if !ok {
		var zero V
		return zero, false
	}
	if len(touch) > 0 {
		m.touchItem(entry, touch[0])
	}
	return entry.value, true
}

// Set corresponds to LinkedMap.set. Touch defaults to TouchNone, which
// appends a new entry and leaves an existing entry's position alone.
func (m *LinkedMap[K, V]) Set(key K, value V, touch ...Touch) *LinkedMap[K, V] {
	if m.items == nil {
		m.items = make(map[K]*item[K, V])
	}
	mode := TouchNone
	if len(touch) > 0 {
		mode = touch[0]
	}

	if entry, ok := m.items[key]; ok {
		entry.value = value
		m.touchItem(entry, mode)
		return m
	}

	entry := &item[K, V]{key: key, value: value}
	switch mode {
	case TouchAsOld:
		m.addFirst(entry)
	default:
		// TouchNone, TouchAsNew and unknown values all append in VS Code.
		m.addLast(entry)
	}
	m.items[key] = entry
	m.size++
	return m
}

// Delete corresponds to LinkedMap.delete.
func (m *LinkedMap[K, V]) Delete(key K) bool {
	_, ok := m.Remove(key)
	return ok
}

// Remove corresponds to LinkedMap.remove. The bool reports whether the key
// existed, replacing VS Code's undefined value.
func (m *LinkedMap[K, V]) Remove(key K) (V, bool) {
	entry, ok := m.items[key]
	if !ok {
		var zero V
		return zero, false
	}
	delete(m.items, key)
	m.removeItem(entry)
	m.size--
	return entry.value, true
}

// Shift corresponds to LinkedMap.shift.
func (m *LinkedMap[K, V]) Shift() (V, bool) {
	if m.head == nil {
		var zero V
		return zero, false
	}
	entry := m.head
	delete(m.items, entry.key)
	m.removeItem(entry)
	m.size--
	return entry.value, true
}

// All corresponds to LinkedMap.entries / [Symbol.iterator]: it yields key/value
// pairs from oldest to newest as an iter.Seq2. Mutating the map while the
// sequence is in progress panics, as it does in VS Code.
func (m *LinkedMap[K, V]) All() iter.Seq2[K, V] {
	state := m.state
	return func(yield func(K, V) bool) {
		for current := m.head; current != nil; current = current.next {
			if m.state != state {
				panic("LinkedMap got modified during iteration")
			}
			if !yield(current.key, current.value) {
				return
			}
		}
	}
}

// Keys corresponds to LinkedMap.keys and yields in order.
func (m *LinkedMap[K, V]) Keys() iter.Seq[K] {
	all := m.All()
	return func(yield func(K) bool) {
		for key := range all {
			if !yield(key) {
				return
			}
		}
	}
}

// Values corresponds to LinkedMap.values and yields in order.
func (m *LinkedMap[K, V]) Values() iter.Seq[V] {
	all := m.All()
	return func(yield func(V) bool) {
		for _, value := range all {
			if !yield(value) {
				return
			}
		}
	}
}

// Entry is the Go-native form of one [key, value] pair in VS Code's toJSON.
type Entry[K comparable, V any] struct {
	Key   K
	Value V
}

// ToJSON corresponds to LinkedMap.toJSON.
func (m *LinkedMap[K, V]) ToJSON() []Entry[K, V] {
	entries := make([]Entry[K, V], 0, m.size)
	for key, value := range m.All() {
		entries = append(entries, Entry[K, V]{Key: key, Value: value})
	}
	return entries
}

// FromJSON corresponds to LinkedMap.fromJSON.
func (m *LinkedMap[K, V]) FromJSON(entries []Entry[K, V]) {
	m.Clear()
	for _, entry := range entries {
		m.Set(entry.Key, entry.Value)
	}
}

// trimOld corresponds to LinkedMap.trimOld: it drops the oldest entries.
func (m *LinkedMap[K, V]) trimOld(newSize int) {
	if newSize >= m.size {
		return
	}
	if newSize == 0 {
		m.Clear()
		return
	}
	current := m.head
	currentSize := m.size
	for current != nil && currentSize > newSize {
		delete(m.items, current.key)
		current = current.next
		currentSize--
	}
	m.head = current
	if current != nil {
		current.previous = nil
	}
	m.size = currentSize
	m.state++
}

func (m *LinkedMap[K, V]) addFirst(entry *item[K, V]) {
	if m.head == nil && m.tail == nil {
		m.tail = entry
	} else {
		entry.next = m.head
		m.head.previous = entry
	}
	m.head = entry
	m.state++
}

func (m *LinkedMap[K, V]) addLast(entry *item[K, V]) {
	if m.head == nil && m.tail == nil {
		m.head = entry
	} else {
		entry.previous = m.tail
		m.tail.next = entry
	}
	m.tail = entry
	m.state++
}

func (m *LinkedMap[K, V]) removeItem(entry *item[K, V]) {
	switch {
	case entry == m.head && entry == m.tail:
		m.head = nil
		m.tail = nil
	case entry == m.head:
		m.head = entry.next
		m.head.previous = nil
	case entry == m.tail:
		m.tail = entry.previous
		m.tail.next = nil
	default:
		entry.previous.next = entry.next
		entry.next.previous = entry.previous
	}
	entry.previous = nil
	entry.next = nil
	m.state++
}

func (m *LinkedMap[K, V]) touchItem(entry *item[K, V], mode Touch) {
	switch mode {
	case TouchAsOld:
		if entry == m.head {
			return
		}
		m.unlink(entry)
		entry.previous = nil
		entry.next = m.head
		m.head.previous = entry
		m.head = entry
	case TouchAsNew:
		if entry == m.tail {
			return
		}
		m.unlink(entry)
		entry.previous = m.tail
		entry.next = nil
		m.tail.next = entry
		m.tail = entry
	default:
		return
	}
	m.state++
}

func (m *LinkedMap[K, V]) unlink(entry *item[K, V]) {
	switch {
	case entry == m.head:
		m.head = entry.next
		m.head.previous = nil
	case entry == m.tail:
		m.tail = entry.previous
		m.tail.next = nil
	default:
		entry.previous.next = entry.next
		entry.next.previous = entry.previous
	}
}
