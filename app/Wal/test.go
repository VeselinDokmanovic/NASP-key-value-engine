package Wal

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
	success, err := wal.append("key1", "value1", false)
	if err != nil {
		fmt.Printf("append key1 failed: %v\n", err)
	} else if success {
		fmt.Println("key1 appended successfully")
	}

	success, err = wal.append("key2", strings.Repeat("x", 1000), false)
	if err != nil {
		fmt.Printf("append key2 failed: %v\n", err)
	} else if success {
		fmt.Println("key2 appended successfully")
	}

	success, err = wal.append("key3", strings.Repeat("y", 8000), false)
	if err != nil {
		fmt.Printf("append key3 failed: %v\n", err)
	} else if success {
		fmt.Println("key3 appended successfully")
	}

	success, err = wal.append("key4", strings.Repeat("z", 25000), false)
	if err != nil {
		fmt.Printf("append key4 failed: %v\n", err)
	} else if success {
		fmt.Println("key4 appended successfully")
	}

	success, err = wal.append("k", strings.Repeat("a", 2000), false)
	if err != nil {
		fmt.Printf("append k failed: %v\n", err)
	} else if success {
		fmt.Println("k appended successfully")
	}
	if err := wal.Flush(); err != nil {
		fmt.Println("Flush failed:", err)
	}

	fmt.Println("Deleting logs 1-5")
	if err := wal.deleteSegmentsByWatermark(6); err != nil {
		fmt.Println("Delete by watermark failed:", err)
	}
}
