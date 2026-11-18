// Package linkedmap ports the LinkedMap and LRUCache collections from
// VS Code's src/vs/base/common/map.ts (commit
// 972b66fdc541e10689adfd90ade9549dea1b18bc).
//
// Nothing here is safe for concurrent use; like VS Code's maps, callers must
// keep a map on one goroutine. Iterators walk from the oldest entry to the
// newest and panic if the map is changed while they are in progress, matching
// VS Code's "LinkedMap got modified during iteration" check.
package linkedmap
