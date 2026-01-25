package main

import (
	"fmt"
	"key-value-engine/block"
)

func main() {
	cfg := block.Config{
		PageSize:  4096,
		BlockSize: 4096,
		CacheSize: 2,
	}

	bm, err := block.NewBlockManager(cfg)
	if err != nil {
		panic(err)
	}

	// blok 0
	data0 := make([]byte, 4096)
	copy(data0, []byte("HELLO BLOCK 0"))

	err = bm.WriteBlock("test.dat", 0, data0)
	if err != nil {
		panic(err)
	}

	// citanje bloka 0
	read0, err := bm.ReadBlock("test.dat", 0)
	if err != nil {
		panic(err)
	}

	fmt.Println(string(read0[:13]))

	// blok 1
	data1 := make([]byte, 4096)
	copy(data1, []byte("HELLO BLOCK 1"))
	_ = bm.WriteBlock("test.dat", 1, data1)

	// opet citanje bloka 0 iz cachea
	read0Again, _ := bm.ReadBlock("test.dat", 0)
	fmt.Println(string(read0Again[:13]))
}
