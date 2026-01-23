package main

import (
	"fmt"
	"key-value-engine/app/sstable"
)

func main() {
	fmt.Println("Hello Go")

	bf := sstable.NewBloomFilter(1000, 0.01)

	bf.Add([]byte("key1"))
	bf.Add([]byte("key2"))

	err := sstable.WriteBloomFilter("data/sstable/table-1.filter", bf)
	if err != nil {
		fmt.Println("Error writing BloomFilter:", err)
		return
	}

	loaded, err := sstable.LoadBloomFilter("data/sstable/table-1.filter")
	if err != nil {
		fmt.Println("Error loading BloomFilter:", err)
		return
	}

	fmt.Println(loaded.MightContain([]byte("key1")))
	fmt.Println(loaded.MightContain([]byte("key2")))
}
