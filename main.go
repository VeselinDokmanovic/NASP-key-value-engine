package main

import (
	"fmt"
	"key-value-engine/app/memtable"
)

func main() {

	impls := []string{"hashmap", "skiplist", "btree"}
	for _, impl := range impls {
		fmt.Printf("\n=== Testing memtable implementation: %s ===\n", impl)

		cfg := memtable.MemtableConfig{
			Type:        impl,
			MaxEntries:  5,
			MaxMemoryKB: 1024,
			BTreeOrder:  3,
		}

		pool, ok := memtable.NewMemtablePool(2, cfg)
		if !ok {
			fmt.Printf("Failed to create pool for %s\n", impl)
			continue
		}

		// PUT a few entries via pool
		pool.Put([]byte("a"), []byte("val_a"))
		pool.Put([]byte("b"), []byte("val_b"))
		pool.Put([]byte("c"), []byte("val_c"))

		fmt.Printf("Size after puts: %d, Memory: %d B\n", pool.Size(), pool.MemoryUsage())

		// GET existing and non-existing
		if e, found := pool.Get([]byte("b")); found {
			fmt.Printf("GET b -> %s\n", string(e.Value))
		}
		if _, found := pool.Get([]byte("z")); !found {
			fmt.Println("GET z -> not found (ok)")
		}

		// DELETE via pool
		pool.Delete([]byte("b"))
		if _, found := pool.Get([]byte("b")); !found {
			fmt.Println("b is tombstoned (ok)")
		}

		// Fill to force rotation and check ShouldFlush
		pool.Put([]byte("d"), []byte("val_d"))
		pool.Put([]byte("e"), []byte("val_e"))
		fmt.Printf("Pool Size: %d, ShouldFlush=%v\n", pool.Size(), pool.ShouldFlush())

		// Get all entries for flush (sorted)
		all := pool.GetAllForFlush()
		fmt.Printf("All entries for flush: %d\n", len(all))

		// Clear pool
		pool.Clear()
		fmt.Printf("After Clear: Size=%d, Memory=%d\n", pool.Size(), pool.MemoryUsage())
	}
}
