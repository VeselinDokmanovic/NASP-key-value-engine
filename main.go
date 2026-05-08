package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"strings"
)

func main() {
	engine, err := NewEngine()
	if err != nil {
		log.Fatal(err)
	}
	defer engine.Close()

	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Println("\nChoose action:")
		fmt.Println("1) PUT")
		fmt.Println("2) GET")
		fmt.Println("3) DELETE")
		fmt.Println("4) Exit")
		fmt.Print("> ")

		choiceRaw, _ := reader.ReadString('\n')
		choice := strings.TrimSpace(choiceRaw)

		switch choice {
		case "1", "PUT", "put":
			fmt.Print("Key: ")
			keyRaw, _ := reader.ReadString('\n')
			key := strings.TrimSpace(keyRaw)

			fmt.Print("Value: ")
			valRaw, _ := reader.ReadString('\n')
			val := strings.TrimSpace(valRaw)

			if err := engine.RateLimitedStore.Put(key, []byte(val)); err != nil {
				fmt.Printf("PUT error: %v\n", err)
			} else {
				fmt.Println("OK")
			}

		case "2", "GET", "get":
			fmt.Print("Key: ")
			keyRaw, _ := reader.ReadString('\n')
			key := strings.TrimSpace(keyRaw)

			v, ok, err := engine.RateLimitedStore.Get(key)
			if err != nil {
				fmt.Printf("GET error: %v\n", err)
			} else if !ok {
				fmt.Println("GET error: key not found")
			} else {
				fmt.Printf("Value: %s\n", string(v))
			}

		case "3", "DELETE", "delete":
			fmt.Print("Key: ")
			keyRaw, _ := reader.ReadString('\n')
			key := strings.TrimSpace(keyRaw)

			if err := engine.RateLimitedStore.Delete(key); err != nil {
				fmt.Printf("DELETE error: %v\n", err)
			} else {
				fmt.Println("OK")
			}

		case "4", "EXIT", "exit":
			fmt.Println("bye")
			return

		default:
			fmt.Println("unknown choice")
		}
	}
}
