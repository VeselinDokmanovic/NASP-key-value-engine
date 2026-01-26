package main

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

/*
   +---------------+-----------------+---------------+---------------+-----------------+----------+-...-+--...--+
   |    CRC (4B)   | Timestamp (8B) | Tombstone(1B) | Key Size (8B) | Value Size (8B) | Type (1B) | Key | Value |
   +---------------+-----------------+---------------+---------------+-----------------+----------+-...-+--...--+
   CRC = 32bit hash computed over the payload using CRC
   Key Size = Length of the Key data
   Tombstone = If this record was deleted and has a value
   Value Size = Length of the Value data
   Type = Type of record (0 - FULL, 1 - START, 2 - MID, 3 - END)
   Key = Key data
   Value = Value data
   Timestamp = Timestamp of the operation in seconds
*/

const (
	CRC_SIZE        = 4
	TIMESTAMP_SIZE  = 8
	TOMBSTONE_SIZE  = 1
	KEY_SIZE_SIZE   = 8
	VALUE_SIZE_SIZE = 8
	TYPE_SIZE       = 1
	// default segment config
	BLOCK_FACTOR = 5
	BLOCK_SIZE   = 2048 // 2KB

	HEADER_SIZE = TIMESTAMP_SIZE + TOMBSTONE_SIZE + KEY_SIZE_SIZE + VALUE_SIZE_SIZE + TYPE_SIZE

	CRC_START        = 0
	TIMESTAMP_START  = CRC_START + CRC_SIZE
	TOMBSTONE_START  = TIMESTAMP_START + TIMESTAMP_SIZE
	KEY_SIZE_START   = TOMBSTONE_START + TOMBSTONE_SIZE
	VALUE_SIZE_START = KEY_SIZE_START + KEY_SIZE_SIZE
	TYPE_START       = VALUE_SIZE_START + VALUE_SIZE_SIZE
	KEY_START        = TYPE_START + TYPE_SIZE

	RECORD_FULL  byte = 0
	RECORD_START byte = 1
	RECORD_MID   byte = 2
	RECORD_END   byte = 3
)

func CRC32(data []byte) uint32 {
	return crc32.ChecksumIEEE(data)
}

func boolToByte(b bool) byte {
	if b {
		return 1
	}
	return 0
}

type SegmentStats struct {
	SizeBytes        int64
	CapacityBytes    int64
	TotalBlocks      int64
	UsedBlocks       int64
	LastBlockBytes   int64
	SpaceInCurrBlock int64
	RemainingBytes   int64
}

type WAL struct {
	dir         string
	files       []string
	blockSize   int64
	blockFactor int64
}

func WALInit(dir string) (*WAL, error) {
	return WALInitConfig(dir, int64(BLOCK_SIZE), int64(BLOCK_FACTOR))
}

func WALInitConfig(dir string, blockSize, blockFactor int64) (*WAL, error) {
	w := &WAL{dir: dir, blockSize: blockSize, blockFactor: blockFactor}
	if err := w.loadLogFiles(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *WAL) computeSegmentStats(file *os.File) (SegmentStats, error) {
	fi, err := file.Stat()
	if err != nil {
		return SegmentStats{}, err
	}
	size := fi.Size()
	capacity := w.blockSize * w.blockFactor

	usedBlocks := int64(0)
	if size > 0 {
		usedBlocks = (size + w.blockSize - 1) / w.blockSize
	}
	if usedBlocks > w.blockFactor {
		usedBlocks = w.blockFactor // cap at max blocks
	}

	lastBlockBytes := size % w.blockSize
	spaceInCurrBlock := w.blockSize - lastBlockBytes

	if lastBlockBytes == 0 && size > 0 {
		spaceInCurrBlock = 0
	}

	if size == 0 {
		spaceInCurrBlock = w.blockSize
	}

	remaining := capacity - size

	return SegmentStats{
		SizeBytes:        size,
		CapacityBytes:    capacity,
		TotalBlocks:      w.blockFactor,
		UsedBlocks:       usedBlocks,
		LastBlockBytes:   lastBlockBytes,
		SpaceInCurrBlock: spaceInCurrBlock,
		RemainingBytes:   remaining,
	}, nil
}
func (w *WAL) readRecord(file *os.File, print bool) ([]byte, error) {
	crc := make([]byte, CRC_SIZE)
	_, err := file.Read(crc)
	if err == io.EOF {
		return nil, io.EOF // end of file reached
	}
	if err != nil {
		return nil, err
	}
	payload := make([]byte, 0)

	timestamp := make([]byte, TIMESTAMP_SIZE)
	file.Read(timestamp)
	payload = append(payload, timestamp...)

	tombstone := make([]byte, TOMBSTONE_SIZE)
	file.Read(tombstone)
	payload = append(payload, tombstone...)

	key_size := make([]byte, KEY_SIZE_SIZE)
	file.Read(key_size)
	payload = append(payload, key_size...)

	val_size := make([]byte, VALUE_SIZE_SIZE)
	file.Read(val_size)
	payload = append(payload, val_size...)

	typ := make([]byte, TYPE_SIZE)
	file.Read(typ)
	payload = append(payload, typ...)

	key := make([]byte, binary.LittleEndian.Uint64(key_size))
	file.Read(key)
	payload = append(payload, key...)

	value := make([]byte, binary.LittleEndian.Uint64(val_size))
	file.Read(value)
	payload = append(payload, value...)
	calculated_crc := CRC32(payload)
	stored_crc := binary.LittleEndian.Uint32(crc)
	if calculated_crc != stored_crc {
		return nil, fmt.Errorf("CRC mismatch")
	}
	if print {
		timestampVal := binary.LittleEndian.Uint64(timestamp)
		tombstoneVal := tombstone[0] == 1
		keyVal := string(key)
		valueVal := string(value)
		fmt.Printf("Record - Timestamp: %d, Tombstone: %t, Type: %d, Key: %s, Value: %s\n",
			timestampVal, tombstoneVal, int(typ[0]), keyVal, valueVal)
	}

	return payload, nil

}

func (w *WAL) readAllRecords(filepath string) {
	file, err := os.Open(filepath)
	if err != nil {
		return
	}
	defer file.Close()
	for {
		_, err := w.readRecord(file, true)
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Println("Error reading record:", err)
			break
		}
	}
}

func (w *WAL) readLatestSegmentRecords() error {
	last, ok := w.lastFile()
	if !ok {
		return fmt.Errorf("no log files found")
	}
	fmt.Println("Reading log file:", last)
	w.readAllRecords(last)
	return nil
}

// scans dir and fills w.files sorted by numeric wal index.
func (w *WAL) loadLogFiles() error {
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return err
	}

	type item struct {
		path string
		idx  int
	}
	var items []item
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		base := entry.Name()
		if !strings.HasPrefix(base, "wal_") || !strings.HasSuffix(base, ".log") {
			continue
		}
		trimmed := base[len("wal_") : len(base)-len(".log")]
		idx, err := strconv.Atoi(trimmed)
		if err != nil {
			continue
		}
		items = append(items, item{path: filepath.Join(w.dir, base), idx: idx})
	}

	sort.Slice(items, func(i, j int) bool { return items[i].idx < items[j].idx })

	w.files = make([]string, len(items))
	for i := range items {
		w.files[i] = items[i].path
	}
	return nil
}

func (w *WAL) lastFile() (string, bool) {
	if len(w.files) == 0 {
		return "", false
	}
	return w.files[len(w.files)-1], true
}

// writes a record to the latest WAL segment
// if no file exists or segment is full, creates a new one
func (w *WAL) append(key, value string, tombstone bool) error {
	if len(w.files) == 0 {
		// Create initial segment if none exist
		_, err := w.newSegment()
		if err != nil {
			return err
		}
	}
	lastFile, _ := w.lastFile()
	file, err := os.OpenFile(lastFile, os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer file.Close()
	w.writeRecordFragments(file, key, value, tombstone)
	return nil
}
func (w *WAL) deleteSegmentsByWatermark(watermark int64) error {
	var kept []string
	for _, filePath := range w.files {
		base := filepath.Base(filePath)
		trimmed := base[len("wal_") : len(base)-len(".log")]
		idx, err := strconv.Atoi(trimmed)
		if err != nil { // if parsing fails, keep the file
			kept = append(kept, filePath)
			continue
		}
		if int64(idx) < watermark {
			if err := os.Remove(filePath); err != nil {
				return err
			}
			fmt.Printf("Deleted segment: %s\n", filePath)
		} else {
			kept = append(kept, filePath)
		}
	}
	w.files = kept
	return nil
}

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

	fmt.Println("Deleting logs 1-5")
	wal.deleteSegmentsByWatermark(6)
}

func (w *WAL) writeRecordFragments(file *os.File, key, value string, tombstone bool) {
	keyBytes := []byte(key)
	valueBytes := []byte(value)

	remainingKey := len(keyBytes)
	remainingValue := len(valueBytes)
	keyOffset := 0
	valueOffset := 0
	first := true
	newSegmentCreated := false

	for remainingKey+remainingValue > 0 {
		stats, err := w.computeSegmentStats(file)
		if err != nil {
			panic(err)
		}

		// if block 95% full add padding
		if stats.SpaceInCurrBlock > 0 {
			blockUtilization := float64(stats.LastBlockBytes) / float64(w.blockSize)
			if blockUtilization > 0.95 {
				padding := make([]byte, stats.SpaceInCurrBlock)
				_, err = file.Write(padding)
				if err != nil {
					panic(err)
				}
				fmt.Printf("Block 95%% full, added %dB padding.\n", stats.SpaceInCurrBlock)
				// recompute stats after padding
				stats, err = w.computeSegmentStats(file)
				if err != nil {
					panic(err)
				}
			}
		}

		space := stats.SpaceInCurrBlock
		if space == 0 {
			// current block is full, check if we're at the last block
			if stats.UsedBlocks >= stats.TotalBlocks {
				// segment is full
				fmt.Println("Segment full, creating new segment.")
				file.Close()
				newFile, err := w.newSegment()
				if err != nil {
					panic(err)
				}
				file = newFile
				newSegmentCreated = true
				continue
			}
			space = w.blockSize
		}

		maxData := space - int64(CRC_SIZE+HEADER_SIZE)

		takeKey := int64(remainingKey)
		if takeKey > maxData {
			takeKey = maxData
		}
		remForValue := maxData - takeKey
		takeValue := int64(remainingValue)
		if takeValue > remForValue {
			takeValue = remForValue
		}

		var typ byte
		if first && int(takeKey) == remainingKey && int(takeValue) == remainingValue {
			typ = RECORD_FULL
		} else if first {
			typ = RECORD_START
		} else if int(takeKey)+int(takeValue) == remainingKey+remainingValue {
			typ = RECORD_END
		} else {
			typ = RECORD_MID
		}

		payload := make([]byte, 0, HEADER_SIZE+int(takeKey)+int(takeValue))
		payload = append(payload, binary.LittleEndian.AppendUint64(nil, uint64(time.Now().Unix()))...)
		payload = append(payload, boolToByte(tombstone))
		payload = append(payload, binary.LittleEndian.AppendUint64(nil, uint64(takeKey))...)
		payload = append(payload, binary.LittleEndian.AppendUint64(nil, uint64(takeValue))...)
		payload = append(payload, typ)
		if takeKey > 0 {
			payload = append(payload, keyBytes[keyOffset:keyOffset+int(takeKey)]...)
		}
		if takeValue > 0 {
			payload = append(payload, valueBytes[valueOffset:valueOffset+int(takeValue)]...)
		}

		crc := CRC32(payload)
		_, err = file.Write(binary.LittleEndian.AppendUint32(nil, crc))
		if err != nil {
			panic(err)
		}
		_, err = file.Write(payload)
		if err != nil {
			panic(err)
		}

		keyOffset += int(takeKey)
		valueOffset += int(takeValue)
		remainingKey -= int(takeKey)
		remainingValue -= int(takeValue)
		first = false

		// stats after write

		typeStr := ""
		switch typ {
		case RECORD_FULL:
			typeStr = "FULL"
		case RECORD_START:
			typeStr = "START"
		case RECORD_MID:
			typeStr = "MID"
		case RECORD_END:
			typeStr = "END"
		}
		fmt.Printf("  [Fragment] Type: %s, Key: %dB, Value: %dB, Total: %dB, Remaining: Key=%dB Value=%dB\n",
			typeStr, takeKey, takeValue, int64(CRC_SIZE)+int64(len(payload)), remainingKey, remainingValue)
		fmt.Printf("    File Stats: Size=%dB, Used=%d/%d blocks, Space in curr block=%dB, Remaining in segment=%dB\n",
			stats.SizeBytes, stats.UsedBlocks, stats.TotalBlocks, stats.SpaceInCurrBlock, stats.RemainingBytes)

	}

	if newSegmentCreated {
		file.Close()
	}
}

func (w *WAL) newSegment() (*os.File, error) {
	nextIndex := 0
	if last, ok := w.lastFile(); ok {
		base := filepath.Base(last)
		trimmed := base[len("wal_") : len(base)-len(".log")]
		if idx, err := strconv.Atoi(trimmed); err == nil {
			nextIndex = idx + 1
		}
	}

	filename := filepath.Join(w.dir, fmt.Sprintf("wal_%04d.log", nextIndex))

	file, err := os.Create(filename)
	if err != nil {
		return nil, err
	}

	fmt.Printf("Created new segment: %s\n", filename)
	w.files = append(w.files, filename)
	return file, nil
}
