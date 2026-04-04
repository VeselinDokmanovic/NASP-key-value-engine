package main

import (
	"fmt"
	"strings"
)

func main() {
	wal, err := WALInit("./wal_logs")
	if err != nil {
		panic(err)
	}
	if err := wal.readLatestSegmentRecords(); err != nil {
		fmt.Println("Error reading latest records:", err)
	}
	// tests
	wal.append("key1", "value1", false)

	wal.append("key2", strings.Repeat("x", 1000), false)

	wal.append("key3", strings.Repeat("y", 8000), false)

	wal.append("key4", strings.Repeat("z", 25000), false)

	wal.append("k", strings.Repeat("a", 2000), false)
	if err := wal.Flush(); err != nil {
		fmt.Println("Flush failed:", err)
	}

	fmt.Println("Deleting logs 1-5")
	if err := wal.deleteSegmentsByWatermark(6); err != nil {
		fmt.Println("Delete by watermark failed:", err)
	}
}
