package main

import (
	"fmt"

	"kv-engine/engine"
)

func main() {
	e := engine.NewEngine(2)

	e.Put("a", []byte("1"))
	e.Put("b", []byte("2"))

	val, err := e.Get("a")
	fmt.Println("GET a:", string(val), err)

	e.Put("c", []byte("3"))

	val, err = e.Get("b")
	fmt.Println("GET b:", string(val), err)

	val, err = e.Get("c")
	fmt.Println("GET c:", string(val), err)
}
