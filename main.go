package main

import (
	"fmt"
	"key-value-engine/app/memtable"
)

func main() {

	// =========== AI Test primer ne zameri ============

	fmt.Println("=== Memtable Test ===")

	// Kreiraj memtable konfiguraciju
	config := memtable.MemtableConfig{
		Type:        "hashmap",
		MaxEntries:  5,
		MaxMemoryKB: 1024,
	}

	// Kreiraj memtable
	mt, ok := memtable.NewMemtable(config)
	if !ok {
		fmt.Println("Greška: Nije moguće kreirati memtable")
		return
	}

	fmt.Println("Memtable kreiran")

	// Test 1: PUT operacija
	fmt.Println("\n--- Test PUT ---")
	entry1 := memtable.NewMemtableEntry([]byte("korisnik1"), []byte("Petar Petrović"), false)
	entry2 := memtable.NewMemtableEntry([]byte("korisnik2"), []byte("Ana Anić"), false)
	entry3 := memtable.NewMemtableEntry([]byte("email"), []byte("test@example.com"), false)

	mt.Put([]byte("korisnik1"), entry1)
	mt.Put([]byte("korisnik2"), entry2)
	mt.Put([]byte("email"), entry3)

	fmt.Printf("Broj elemenata: %d\n", mt.Size())
	fmt.Printf("Memorijska upotreba: %d bajtova\n", mt.MemoryUsage())

	// Test 2: GET operacija
	fmt.Println("\n--- Test GET ---")
	if value, found := mt.Get([]byte("korisnik1")); found {
		fmt.Printf(" Pronađeno: korisnik1 = %s\n", string(value.Value))
	} else {
		fmt.Println("Ključ 'korisnik1' nije pronađen")
	}

	if value, found := mt.Get([]byte("email")); found {
		fmt.Printf("Pronađeno: email = %s\n", string(value.Value))
	} else {
		fmt.Println(" Ključ 'email' nije pronađen")
	}

	// Test nepostojećeg ključa
	if _, found := mt.Get([]byte("nepostojeci")); !found {
		fmt.Println(" Ključ 'nepostojeci' ispravno nije pronađen")
	}

	// Test 3: DELETE operacija
	fmt.Println("\n--- Test DELETE ---")
	mt.Delete([]byte("korisnik2"))
	fmt.Println("Obrisan ključ: korisnik2")

	if _, found := mt.Get([]byte("korisnik2")); !found {
		fmt.Println("✓ Ključ 'korisnik2' više nije dostupan (tombstone)")
	}

	// Test 4: GetAllSorted
	fmt.Println("\n--- Test GetAllSorted ---")
	sorted := mt.GetAllSorted()
	fmt.Printf("Sortirani unosi (%d):\n", len(sorted))
	for i, entry := range sorted {
		status := "aktivan"
		if entry.Tombstone {
			status = "obrisan"
		}
		fmt.Printf("  %d. %s = %s [%s]\n", i+1, string(entry.Key), string(entry.Value), status)
	}

	// Test 5: Popunjavanje do limita
	fmt.Println("\n--- Test IsFull ---")
	entry4 := memtable.NewMemtableEntry([]byte("key4"), []byte("value4"), false)
	entry5 := memtable.NewMemtableEntry([]byte("key5"), []byte("value5"), false)

	mt.Put([]byte("key4"), entry4)
	mt.Put([]byte("key5"), entry5)

	fmt.Printf("Broj elemenata: %d/%d\n", mt.Size(), config.MaxEntries)
	fmt.Printf("Memtable je pun: %v\n", mt.IsFull())

	// Pokušaj dodavanja kada je pun
	entry6 := memtable.NewMemtableEntry([]byte("key6"), []byte("value6"), false)
	if !mt.Put([]byte("key6"), entry6) {
		fmt.Println("✓ Put operacija odbačena - memtable je pun")
	}

	// Test 6: Clear
	fmt.Println("\n--- Test Clear ---")
	mt.Clear()
	fmt.Printf("Nakon Clear(): Broj elemenata = %d, Memorija = %d B\n", mt.Size(), mt.MemoryUsage())

	fmt.Println("\n=== Testiranje završeno ===")
}
