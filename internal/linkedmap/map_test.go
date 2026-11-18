package linkedmap

import (
	"iter"
	"slices"
	"testing"
)

// The expectations in this file are ported from
// src/vs/base/test/common/map.test.ts (VS Code commit
// 972b66fdc541e10689adfd90ade9549dea1b18bc), suite "Map".

func assertKeys[K comparable, V any](t *testing.T, m *LinkedMap[K, V], want []K) {
	t.Helper()
	if got := slices.Collect(m.Keys()); !slices.Equal(got, want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
}

func assertValues[K comparable, V comparable](t *testing.T, m *LinkedMap[K, V], want []V) {
	t.Helper()
	if got := slices.Collect(m.Values()); !slices.Equal(got, want) {
		t.Fatalf("values = %v, want %v", got, want)
	}
}

func TestLinkedMapSimple(t *testing.T) {
	m := NewLinkedMap[string, string]()
	m.Set("ak", "av")
	m.Set("bk", "bv")
	assertKeys(t, m, []string{"ak", "bk"})
	assertValues(t, m, []string{"av", "bv"})
	if first, ok := m.First(); !ok || first != "av" {
		t.Fatalf("First() = %q, %v", first, ok)
	}
	if last, ok := m.Last(); !ok || last != "bv" {
		t.Fatalf("Last() = %q, %v", last, ok)
	}
}

func TestLinkedMapTouchOldOne(t *testing.T) {
	m := NewLinkedMap[string, string]()
	m.Set("ak", "av")
	m.Set("ak", "av", TouchAsOld)
	assertKeys(t, m, []string{"ak"})
	assertValues(t, m, []string{"av"})
}

func TestLinkedMapTouchNewOne(t *testing.T) {
	m := NewLinkedMap[string, string]()
	m.Set("ak", "av")
	m.Set("ak", "av", TouchAsNew)
	assertKeys(t, m, []string{"ak"})
	assertValues(t, m, []string{"av"})
}

func TestLinkedMapTouchOldTwo(t *testing.T) {
	m := NewLinkedMap[string, string]()
	m.Set("ak", "av")
	m.Set("bk", "bv")
	m.Set("bk", "bv", TouchAsOld)
	assertKeys(t, m, []string{"bk", "ak"})
	assertValues(t, m, []string{"bv", "av"})
}

func TestLinkedMapTouchNewTwo(t *testing.T) {
	m := NewLinkedMap[string, string]()
	m.Set("ak", "av")
	m.Set("bk", "bv")
	m.Set("ak", "av", TouchAsNew)
	assertKeys(t, m, []string{"bk", "ak"})
	assertValues(t, m, []string{"bv", "av"})
}

func TestLinkedMapTouchOldFromMiddle(t *testing.T) {
	m := NewLinkedMap[string, string]()
	m.Set("ak", "av")
	m.Set("bk", "bv")
	m.Set("ck", "cv")
	m.Set("bk", "bv", TouchAsOld)
	assertKeys(t, m, []string{"bk", "ak", "ck"})
	assertValues(t, m, []string{"bv", "av", "cv"})
}

func TestLinkedMapTouchNewFromMiddle(t *testing.T) {
	m := NewLinkedMap[string, string]()
	m.Set("ak", "av")
	m.Set("bk", "bv")
	m.Set("ck", "cv")
	m.Set("bk", "bv", TouchAsNew)
	assertKeys(t, m, []string{"ak", "ck", "bk"})
	assertValues(t, m, []string{"av", "cv", "bv"})
}

func TestLinkedMapBasics(t *testing.T) {
	m := NewLinkedMap[string, int]()
	if m.Size() != 0 {
		t.Fatal("new map is not empty")
	}
	for i, key := range []string{"1", "2", "3", "4", "5"} {
		m.Set(key, i+1)
	}
	if m.Size() != 5 {
		t.Fatalf("size = %d, want 5", m.Size())
	}
	for i, key := range []string{"1", "2", "3", "4", "5"} {
		if got, ok := m.Get(key); !ok || got != i+1 {
			t.Fatalf("Get(%q) = %d, %v", key, got, ok)
		}
	}
	if _, ok := m.Get("6"); ok {
		t.Fatal("missing key reported as present")
	}
	m.Delete("6")
	if m.Size() != 5 {
		t.Fatalf("size after missing delete = %d, want 5", m.Size())
	}
	for _, key := range []string{"1", "2", "3", "4", "5"} {
		if !m.Delete(key) {
			t.Fatalf("Delete(%q) reported false", key)
		}
	}
	if m.Size() != 0 {
		t.Fatalf("size after deletes = %d, want 0", m.Size())
	}
	for _, key := range []string{"1", "2", "3", "4", "5"} {
		if _, ok := m.Get(key); ok {
			t.Fatalf("Get(%q) after delete reported present", key)
		}
	}
	m.Set("1", 1)
	m.Set("2", 2)
	m.Set("3", 3)
	for i, key := range []string{"1", "2", "3"} {
		if !m.Has(key) {
			t.Fatalf("Has(%q) = false", key)
		}
		if got, ok := m.Get(key); !ok || got != i+1 {
			t.Fatalf("Get(%q) = %d, %v", key, got, ok)
		}
	}
	m.Clear()
	if m.Size() != 0 {
		t.Fatalf("size after Clear = %d", m.Size())
	}
	for _, key := range []string{"1", "2", "3"} {
		if _, ok := m.Get(key); ok || m.Has(key) {
			t.Fatalf("key %q survived Clear", key)
		}
	}
}

func panicked(fn func()) (panicked bool) {
	defer func() {
		if recover() != nil {
			panicked = true
		}
	}()
	fn()
	return false
}

func consumeOne[K comparable, V any](seq iter.Seq2[K, V]) {
	for range seq {
		return
	}
}

func consumeOneKey[K any](seq iter.Seq[K]) {
	for range seq {
		return
	}
}

func TestLinkedMapIterators(t *testing.T) {
	m := NewLinkedMap[int, int]()
	for i := 1; i <= 3; i++ {
		m.Set(i, i)
	}
	for key := range m.Keys() {
		if key == 0 {
			t.Fatal("unexpected key")
		}
	}
	for value := range m.Values() {
		if value == 0 {
			t.Fatal("unexpected value")
		}
	}
	for range m.All() {
	}

	{
		keys, values, entries := m.Keys(), m.Values(), m.All()
		m.Get(1)
		consumeOneKey(keys)
		consumeOneKey(values)
		consumeOne(entries)
	}

	{
		keys, values, entries := m.Keys(), m.Values(), m.All()
		m.Get(1, TouchAsNew)
		exceptions := 0
		if panicked(func() { consumeOneKey(keys) }) {
			exceptions++
		}
		if panicked(func() { consumeOneKey(values) }) {
			exceptions++
		}
		if panicked(func() { consumeOne(entries) }) {
			exceptions++
		}
		if exceptions != 3 {
			t.Fatalf("exceptions = %d, want 3", exceptions)
		}
	}
}

func TestLinkedMapLRUCacheSimple(t *testing.T) {
	cache := NewLRUCache[int, int](5)
	for i := 1; i <= 5; i++ {
		cache.Set(i, i)
	}
	if cache.Size() != 5 {
		t.Fatalf("size = %d, want 5", cache.Size())
	}
	cache.Set(6, 6)
	if cache.Size() != 5 {
		t.Fatalf("size = %d, want 5", cache.Size())
	}
	assertKeys(t, &cache.LinkedMap, []int{2, 3, 4, 5, 6})
	cache.Set(7, 7)
	if cache.Size() != 5 {
		t.Fatalf("size = %d, want 5", cache.Size())
	}
	assertKeys(t, &cache.LinkedMap, []int{3, 4, 5, 6, 7})
	for _, key := range []int{3, 4, 5, 6, 7} {
		if got, ok := cache.Get(key); !ok || got != key {
			t.Fatalf("Get(%d) = %d, %v", key, got, ok)
		}
	}
}

func TestLinkedMapLRUCacheGet(t *testing.T) {
	cache := NewLRUCache[int, int](5)
	for i := 1; i <= 5; i++ {
		cache.Set(i, i)
	}
	if cache.Size() != 5 {
		t.Fatalf("size = %d, want 5", cache.Size())
	}
	assertKeys(t, &cache.LinkedMap, []int{1, 2, 3, 4, 5})
	cache.Get(3)
	assertKeys(t, &cache.LinkedMap, []int{1, 2, 4, 5, 3})
	cache.Peek(4)
	assertKeys(t, &cache.LinkedMap, []int{1, 2, 4, 5, 3})
	for _, key := range []int{1, 2, 3, 4, 5} {
		if got, ok := cache.Get(key); !ok || got != key {
			t.Fatalf("Get(%d) = %d, %v", key, got, ok)
		}
	}
}

func TestLinkedMapLRUCacheLimit(t *testing.T) {
	cache := NewLRUCache[int, int](10)
	for i := 1; i <= 10; i++ {
		cache.Set(i, i)
	}
	if cache.Size() != 10 {
		t.Fatalf("size = %d, want 10", cache.Size())
	}
	cache.SetLimit(5)
	if cache.Size() != 5 {
		t.Fatalf("size after smaller limit = %d, want 5", cache.Size())
	}
	assertKeys(t, &cache.LinkedMap, []int{6, 7, 8, 9, 10})
	cache.SetLimit(20)
	if cache.Size() != 5 {
		t.Fatalf("size after larger limit = %d, want 5", cache.Size())
	}
	for i := 11; i <= 20; i++ {
		cache.Set(i, i)
	}
	if cache.Size() != 15 {
		t.Fatalf("size = %d, want 15", cache.Size())
	}
	values := make([]int, 0, 15)
	for i := 6; i <= 20; i++ {
		got, ok := cache.Get(i)
		if !ok || got != i {
			t.Fatalf("Get(%d) = %d, %v", i, got, ok)
		}
		values = append(values, got)
	}
	assertValues(t, &cache.LinkedMap, values)
}

func TestLinkedMapLRUCacheLimitWithRatio(t *testing.T) {
	cache := NewLRUCache[int, int](10, 0.5)
	for i := 1; i <= 10; i++ {
		cache.Set(i, i)
	}
	if cache.Size() != 10 {
		t.Fatalf("size = %d, want 10", cache.Size())
	}
	cache.Set(11, 11)
	if cache.Size() != 5 {
		t.Fatalf("size after trim = %d, want 5", cache.Size())
	}
	assertKeys(t, &cache.LinkedMap, []int{7, 8, 9, 10, 11})
	values := make([]int, 0, 5)
	for _, key := range slices.Collect(cache.Keys()) {
		if got, ok := cache.Get(key); ok {
			values = append(values, got)
		}
	}
	assertValues(t, &cache.LinkedMap, values)
}

func TestLinkedMapToJSONFromJSON(t *testing.T) {
	m := NewLinkedMap[string, string]()
	m.Set("ak", "av")
	m.Set("bk", "bv")
	m.Set("ck", "cv")
	json := m.ToJSON()

	m = NewLinkedMap[string, string]()
	m.FromJSON(json)
	assertKeys(t, m, []string{"ak", "bk", "ck"})
	assertValues(t, m, []string{"av", "bv", "cv"})
}

func TestLinkedMapDeleteHeadAndTail(t *testing.T) {
	m := NewLinkedMap[string, int]()
	if m.Size() != 0 {
		t.Fatal("new map is not empty")
	}
	m.Set("1", 1)
	if m.Size() != 1 {
		t.Fatalf("size = %d, want 1", m.Size())
	}
	m.Delete("1")
	if _, ok := m.Get("1"); ok || m.Size() != 0 || len(slices.Collect(m.Keys())) != 0 {
		t.Fatal("entry survived delete")
	}
}

func TestLinkedMapDeleteHead(t *testing.T) {
	m := NewLinkedMap[string, int]()
	m.Set("1", 1)
	m.Set("2", 2)
	if m.Size() != 2 {
		t.Fatalf("size = %d, want 2", m.Size())
	}
	m.Delete("1")
	if got, ok := m.Get("2"); !ok || got != 2 {
		t.Fatalf("Get(2) = %d, %v", got, ok)
	}
	if m.Size() != 1 {
		t.Fatalf("size = %d, want 1", m.Size())
	}
	assertKeys(t, m, []string{"2"})
}

func TestLinkedMapDeleteTail(t *testing.T) {
	m := NewLinkedMap[string, int]()
	m.Set("1", 1)
	m.Set("2", 2)
	if m.Size() != 2 {
		t.Fatalf("size = %d, want 2", m.Size())
	}
	m.Delete("2")
	if got, ok := m.Get("1"); !ok || got != 1 {
		t.Fatalf("Get(1) = %d, %v", got, ok)
	}
	if m.Size() != 1 {
		t.Fatalf("size = %d, want 1", m.Size())
	}
	assertKeys(t, m, []string{"1"})
}
