package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"key-value-engine/app/sstable"
	"key-value-engine/block"
)

// Engine drzi sve SSTables i privremeni bafer za unose pre flushа.
type Engine struct {
	bm     *block.BlockManager
	tables []*sstable.SSTable
	buffer []*sstable.Entry
	nextID int64
	dir    string
	step   int // SummaryStep
}

func newEngine(dir string, summaryStep int) (*Engine, error) {
	os.MkdirAll(dir, 0755)
	bm, err := block.NewBlockManager(block.Config{
		PageSize:  4096,
		BlockSize: 8192,
		CacheSize: 16,
	})
	if err != nil {
		return nil, err
	}
	return &Engine{
		bm:     bm,
		nextID: 1,
		dir:    dir,
		step:   summaryStep,
		buffer: make([]*sstable.Entry, 0),
	}, nil
}

func (e *Engine) put(key, value []byte) {
	e.buffer = append(e.buffer, &sstable.Entry{
		Key:   key,
		Value: value,
	})
}

func (e *Engine) delete(key []byte) error {
	// Proveri bafer od najnovijeg ka najstarijem
	for i := len(e.buffer) - 1; i >= 0; i-- {
		if string(e.buffer[i].Key) == string(key) {
			if e.buffer[i].Tombstone == 1 {
				return fmt.Errorf("kljuc '%s' je vec obrisan", string(key))
			}
			// Kljuc postoji u baferu — dodaj tombstone
			e.buffer = append(e.buffer, &sstable.Entry{
				Key:       key,
				Tombstone: 1,
			})
			return nil
		}
	}

	// Proveri SSTables od najnovijeg ka najstarijem
	for i := len(e.tables) - 1; i >= 0; i-- {
		val, found, err := e.tables[i].Search(key)
		if err != nil {
			return err
		}
		if found {
			if val == nil {
				return fmt.Errorf("kljuc '%s' je vec obrisan", string(key))
			}
			e.buffer = append(e.buffer, &sstable.Entry{
				Key:       key,
				Tombstone: 1,
			})
			return nil
		}
	}

	return fmt.Errorf("kljuc '%s' ne postoji", string(key))
}

func (e *Engine) flush() error {
	if len(e.buffer) == 0 {
		return fmt.Errorf("bafer je prazan, nema sta da se flushuje")
	}
	ss := sstable.NewSSTable(sstable.SSTableConfig{
		ID:           e.nextID,
		Dir:          e.dir,
		BlockManager: e.bm,
		SummaryStep:  e.step,
	})
	if err := ss.Write(e.buffer); err != nil {
		return err
	}
	if err := ss.Read(); err != nil {
		return err
	}
	e.tables = append(e.tables, ss)
	e.nextID++
	e.buffer = e.buffer[:0]
	return nil
}

func (e *Engine) get(key []byte) ([]byte, bool, error) {
	for i := len(e.tables) - 1; i >= 0; i-- {
		val, found, err := e.tables[i].Search(key)
		if err != nil {
			return nil, false, err
		}
		if found {
			// found=true znaci da je kljuc u ovoj tabeli —
			// ako je val==nil, obrisan je tombstonom, ne trazimo dalje
			return val, val != nil, nil
		}
	}
	return nil, false, nil
}

func main() {
	fmt.Println("=== Key-Value Engine ===")
	fmt.Println("Komande: PUT <key> <value> | GET <key> | DELETE <key>")
	fmt.Println("         FLUSH | LIST | VALIDATE <id> | QUIT")
	fmt.Println()

	engine, err := newEngine("data", 4) // SummaryStep = 4 (default)
	if err != nil {
		fmt.Printf("Greska: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Engine pokrenut. (SummaryStep=%d)\n\n", engine.step)

	scanner := bufio.NewScanner(os.Stdin)

	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, " ", 3)
		cmd := strings.ToUpper(parts[0])

		switch cmd {

		case "PUT":
			if len(parts) < 3 {
				fmt.Println("Upotreba: PUT <key> <value>")
				continue
			}
			engine.put([]byte(parts[1]), []byte(parts[2]))
			fmt.Printf("OK  (u baferu, ukupno=%d)\n", len(engine.buffer))

		case "GET":
			if len(parts) < 2 {
				fmt.Println("Upotreba: GET <key>")
				continue
			}
			val, found, err := engine.get([]byte(parts[1]))
			if err != nil {
				fmt.Printf("Greska: %v\n", err)
			} else if found {
				fmt.Printf("OK  %s = %s\n", parts[1], string(val))
			} else {
				fmt.Printf("NOT FOUND  %s\n", parts[1])
			}

		case "DELETE":
			if len(parts) < 2 {
				fmt.Println("Upotreba: DELETE <key>")
				continue
			}
			if err := engine.delete([]byte(parts[1])); err != nil {
				fmt.Printf("Greska: %v\n", err)
			} else {
				fmt.Printf("OK  (tombstone u baferu)\n")
			}

		case "FLUSH":
			if err := engine.flush(); err != nil {
				fmt.Printf("Greska: %v\n", err)
			} else {
				fmt.Printf("OK  SSTable #%d kreiran  [ukupno=%d]\n",
					engine.nextID-1, len(engine.tables))
			}

		case "LIST":
			if len(engine.tables) == 0 {
				fmt.Println("Nema SSTables. Koristite FLUSH.")
				continue
			}
			fmt.Printf("%-6s  %-20s  %-20s\n", "ID", "MinKey", "MaxKey")
			fmt.Println(strings.Repeat("-", 50))
			for _, t := range engine.tables {
				fmt.Printf("%-6d  %-20s  %-20s\n", t.ID, string(t.MinKey), string(t.MaxKey))
			}

		case "VALIDATE":
			if len(parts) < 2 {
				fmt.Println("Upotreba: VALIDATE <id>")
				fmt.Println("Koristite LIST da vidite dostupne SSTables.")
				continue
			}
			id, err := strconv.ParseInt(parts[1], 10, 64)
			if err != nil {
				fmt.Printf("Neispravan ID: %s\n", parts[1])
				continue
			}
			var target *sstable.SSTable
			for _, t := range engine.tables {
				if t.ID == id {
					target = t
					break
				}
			}
			if target == nil {
				fmt.Printf("SSTable #%d nije pronadjen. Koristite LIST.\n", id)
				continue
			}
			valid, changed, err := target.ValidateIntegrity()
			if err != nil {
				fmt.Printf("Greska: %v\n", err)
			} else if valid {
				fmt.Printf("OK  SSTable #%d je integralan — nema izmena.\n", id)
			} else {
				fmt.Printf("UPOZORENJE  SSTable #%d ima izmene na %d mesta:\n", id, len(changed))
				for _, idx := range changed {
					fmt.Printf("  - zapis na indeksu %d\n", idx)
				}
			}

		case "QUIT", "EXIT", "Q":
			fmt.Println("Izlaz.")
			return

		default:
			fmt.Printf("Nepoznata komanda: %s\n", cmd)
		}
	}
}
