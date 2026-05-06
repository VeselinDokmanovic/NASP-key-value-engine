package main

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"key-value-engine/app/block"
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
	BLOCK_HDR_SIZE  = 6
	// default segment config
	BLOCK_FACTOR = 5
	BLOCK_SIZE   = 2048 // 2KB
	CACHE_SIZE   = 16

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

var BLOCK_MAGIC = [4]byte{'W', 'A', 'L', 'B'}

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
	dir                string
	files              []string
	blockSize          int64
	blockFactor        int64
	bm                 *block.BlockManager
	currentFile        string
	currentBlk         []byte
	currentUsed        int64
	currentSegmentSize int64
}

func (w *WAL) blockPayloadSize() int64 {
	return w.blockSize - BLOCK_HDR_SIZE
}

func (w *WAL) newEmptyBlock() []byte {
	b := make([]byte, w.blockSize)
	copy(b[:4], BLOCK_MAGIC[:])
	binary.LittleEndian.PutUint16(b[4:6], 0)
	return b
}

func (w *WAL) blockUsedBytes(blockData []byte) (int, error) {
	if len(blockData) < int(w.blockSize) {
		return 0, fmt.Errorf("short block")
	}
	if string(blockData[:4]) != string(BLOCK_MAGIC[:]) {
		return 0, fmt.Errorf("invalid block magic")
	}
	used := int(binary.LittleEndian.Uint16(blockData[4:6]))
	if used < 0 || int64(used) > w.blockPayloadSize() {
		return 0, fmt.Errorf("invalid used bytes")
	}
	return used, nil
}

func (w *WAL) setBlockUsedBytes(blockData []byte, used int) {
	binary.LittleEndian.PutUint16(blockData[4:6], uint16(used))
}

func WALInit(dir string) (*WAL, error) {
	return WALInitConfig(dir, int64(BLOCK_SIZE), int64(BLOCK_FACTOR))
}

func WALInitConfig(dir string, blockSize, blockFactor int64) (*WAL, error) {
	bm, err := block.NewBlockManager(block.Config{
		PageSize:  int(blockSize),
		BlockSize: int(blockSize),
		CacheSize: CACHE_SIZE,
	})
	if err != nil {
		return nil, err
	}

	w := &WAL{dir: dir, blockSize: blockSize, blockFactor: blockFactor, bm: bm}
	if err := w.loadLogFiles(); err != nil {
		return nil, err
	}
	if last, ok := w.lastFile(); ok {
		w.currentFile = last
		if size, err := w.scanSegmentSize(last); err == nil {
			w.currentSegmentSize = size
		}
	}
	return w, nil
}

func (w *WAL) scanSegmentSize(filePath string) (int64, error) {
	total := int64(0)
	payloadCap := w.blockPayloadSize()

	for i := int64(0); i < w.blockFactor; i++ {
		blockData, err := w.bm.ReadBlock(filePath, int(i))
		if err != nil {
			break
		}

		buf := make([]byte, w.blockSize)
		copy(buf, blockData)

		used, err := w.blockUsedBytes(buf)
		if err != nil {
			fmt.Printf("Warning: stopping segment scan at %s block=%d: %v\n", filePath, i, err)
			break
		}

		total += int64(used)
		if int64(used) < payloadCap {
			break
		}
	}

	return total, nil
}

func (w *WAL) segmentSize(filePath string) (int64, error) {
	if filePath == w.currentFile {
		return w.currentSegmentSize, nil
	}
	return w.scanSegmentSize(filePath)
}

func (w *WAL) computeSegmentStats(filePath string) (SegmentStats, error) {
	size, err := w.segmentSize(filePath)
	if err != nil {
		return SegmentStats{}, err
	}
	payloadCap := w.blockPayloadSize()
	capacity := payloadCap * w.blockFactor

	usedBlocks := int64(0)
	if size > 0 {
		usedBlocks = (size + payloadCap - 1) / payloadCap
	}
	if usedBlocks > w.blockFactor {
		usedBlocks = w.blockFactor // cap at max blocks
	}

	lastBlockBytes := size % payloadCap
	spaceInCurrBlock := payloadCap - lastBlockBytes

	if lastBlockBytes == 0 && size > 0 {
		spaceInCurrBlock = 0
	}

	if size == 0 {
		spaceInCurrBlock = payloadCap
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

func (w *WAL) readAt(filePath string, offset int64, length int) ([]byte, error) {
	if length == 0 {
		return []byte{}, nil
	}

	size, err := w.segmentSize(filePath)
	if err != nil {
		return nil, err
	}
	if offset >= size {
		return nil, io.EOF
	}
	if offset+int64(length) > size {
		return nil, io.ErrUnexpectedEOF
	}

	out := make([]byte, length)
	remaining := length
	dst := 0
	cur := offset

	for remaining > 0 {
		payloadCap := w.blockPayloadSize()
		blockNum := int(cur / payloadCap)
		payloadOffset := int(cur % payloadCap)

		blockData, err := w.bm.ReadBlock(filePath, blockNum)
		if err != nil {
			return nil, err
		}
		buf := make([]byte, w.blockSize)
		copy(buf, blockData)

		used, err := w.blockUsedBytes(buf)
		if err != nil {
			return nil, err
		}

		available := used - payloadOffset
		if available <= 0 {
			return nil, io.ErrUnexpectedEOF
		}

		take := remaining
		if take > available {
			take = available
		}

		start := int(BLOCK_HDR_SIZE) + payloadOffset
		copy(out[dst:dst+take], buf[start:start+take])
		remaining -= take
		dst += take
		cur += int64(take)
	}

	return out, nil
}

func (w *WAL) readField(filePath string, offset *int64, size int64) ([]byte, error) {
	data, err := w.readAt(filePath, *offset, int(size))
	if err != nil {
		return nil, err
	}
	*offset += size
	return data, nil
}

func (w *WAL) readRecord(filePath string, offset *int64, print bool) ([]byte, error) {
	crc, err := w.readAt(filePath, *offset, CRC_SIZE)
	if err == io.EOF {
		return nil, io.EOF // end of file reached
	}
	if err != nil {
		return nil, err
	}
	*offset += CRC_SIZE

	payload := make([]byte, 0)

	timestamp, err := w.readField(filePath, offset, TIMESTAMP_SIZE)
	if err != nil {
		return nil, err
	}
	payload = append(payload, timestamp...)

	tombstone, err := w.readField(filePath, offset, TOMBSTONE_SIZE)
	if err != nil {
		return nil, err
	}
	payload = append(payload, tombstone...)

	key_size, err := w.readField(filePath, offset, KEY_SIZE_SIZE)
	if err != nil {
		return nil, err
	}
	payload = append(payload, key_size...)

	val_size, err := w.readField(filePath, offset, VALUE_SIZE_SIZE)
	if err != nil {
		return nil, err
	}
	payload = append(payload, val_size...)

	typ, err := w.readField(filePath, offset, TYPE_SIZE)
	if err != nil {
		return nil, err
	}
	payload = append(payload, typ...)

	keyLen := int64(binary.LittleEndian.Uint64(key_size))
	key, err := w.readField(filePath, offset, keyLen)
	if err != nil {
		return nil, err
	}
	payload = append(payload, key...)

	valueLen := int64(binary.LittleEndian.Uint64(val_size))
	value, err := w.readField(filePath, offset, valueLen)
	if err != nil {
		return nil, err
	}
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

func (w *WAL) readAllRecords(filePath string) {
	var offset int64
	for {
		_, err := w.readRecord(filePath, &offset, true)
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
func (w *WAL) append(key, value string, tombstone bool) (bool, error) {
	if len(w.files) == 0 {
		// Create initial segment if none exist
		_, err := w.newSegment()
		if err != nil {
			return false, err
		}
	}
	lastFile, _ := w.lastFile()
	w.currentFile = lastFile
	err := w.writeRecordFragments(lastFile, key, value, tombstone)
	if err != nil {
		return false, err
	}
	return true, nil
}

func (w *WAL) ensureCurrentBlock(filePath string) error {
	if w.currentFile != filePath || w.currentBlk == nil {
		w.currentFile = filePath
		size, err := w.scanSegmentSize(filePath)
		if err != nil {
			return err
		}
		payloadCap := w.blockPayloadSize()
		inBlock := size % payloadCap
		w.currentUsed = size - inBlock

		w.currentBlk = w.newEmptyBlock()
		if inBlock > 0 {
			blockNum := int(size / payloadCap)
			blockData, err := w.bm.ReadBlock(filePath, blockNum)
			if err != nil {
				return err
			}
			copy(w.currentBlk, blockData)
			used, err := w.blockUsedBytes(w.currentBlk)
			if err != nil {
				return err
			}
			if int64(used) < inBlock {
				return fmt.Errorf("inconsistent block used bytes")
			}
		}
		w.currentSegmentSize = size
	}
	return nil
}

func (w *WAL) persistCurrentBlock() error {
	if w.currentFile == "" || w.currentBlk == nil {
		return nil
	}
	used, err := w.blockUsedBytes(w.currentBlk)
	if err != nil {
		return err
	}
	if used == 0 {
		return nil
	}

	blockNum := int(w.currentUsed / w.blockPayloadSize())
	if err := w.bm.WriteBlock(w.currentFile, blockNum, w.currentBlk); err != nil {
		return err
	}
	if int64(used) > w.blockPayloadSize() {
		return fmt.Errorf("block payload overflow")
	}
	w.currentSegmentSize = w.currentUsed + int64(used)
	return nil
}

func (w *WAL) Flush() error {
	return w.persistCurrentBlock()
}

func (w *WAL) rotateToNextBlock() error {
	if err := w.persistCurrentBlock(); err != nil {
		return err
	}
	w.currentUsed += w.blockPayloadSize()
	w.currentSegmentSize = w.currentUsed
	w.currentBlk = w.newEmptyBlock()
	return nil
}

func (w *WAL) appendToCurrentBlock(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	payloadCap := int(w.blockPayloadSize())

	for len(data) > 0 {
		used, err := w.blockUsedBytes(w.currentBlk)
		if err != nil {
			return err
		}

		space := payloadCap - used
		if space == 0 {
			if err := w.rotateToNextBlock(); err != nil {
				return err
			}
			space = payloadCap
		}

		take := len(data)
		if take > space {
			take = space
		}

		copy(w.currentBlk[int(BLOCK_HDR_SIZE)+used:int(BLOCK_HDR_SIZE)+used+take], data[:take])
		w.setBlockUsedBytes(w.currentBlk, used+take)
		w.currentSegmentSize = w.currentUsed + int64(used+take)
		data = data[take:]

		if used+take == payloadCap {
			if err := w.rotateToNextBlock(); err != nil {
				return err
			}
			continue
		}
	}

	return nil
}

func (w *WAL) padCurrentBlock() error {
	if w.currentBlk == nil {
		return nil
	}
	used, err := w.blockUsedBytes(w.currentBlk)
	if err != nil {
		return err
	}
	padding := make([]byte, int(w.blockPayloadSize())-used)
	return w.appendToCurrentBlock(padding)
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
	if w.currentFile != "" {
		if base := filepath.Base(w.currentFile); len(base) > 0 {
			trimmed := base[len("wal_") : len(base)-len(".log")]
			if idx, err := strconv.Atoi(trimmed); err == nil && int64(idx) < watermark {
				w.currentFile = ""
				w.currentBlk = nil
				w.currentUsed = 0
			}
		}
	}
	return nil
}

func computeFragmentParts(maxData int64, remainingKey, remainingValue int) (int64, int64) {
	takeKey := int64(remainingKey)
	if takeKey > maxData {
		takeKey = maxData
	}

	remForValue := maxData - takeKey
	takeValue := int64(remainingValue)
	if takeValue > remForValue {
		takeValue = remForValue
	}

	return takeKey, takeValue
}

func determineRecordType(first bool, takeKey, takeValue int64, remainingKey, remainingValue int) byte {
	if first && int(takeKey) == remainingKey && int(takeValue) == remainingValue {
		return RECORD_FULL
	}
	if first {
		return RECORD_START
	}
	if int(takeKey)+int(takeValue) == remainingKey+remainingValue {
		return RECORD_END
	}
	return RECORD_MID
}

func (w *WAL) prepareFragmentSpace(currentFile string) (string, int64, error) {
	stats, err := w.computeSegmentStats(currentFile)
	if err != nil {
		return currentFile, 0, err
	}

	if stats.SpaceInCurrBlock > 0 {
		blockUtilization := float64(stats.LastBlockBytes) / float64(w.blockPayloadSize())
		if blockUtilization > 0.95 {
			if err := w.padCurrentBlock(); err != nil {
				return currentFile, 0, err
			}
			fmt.Printf("Block 95%% full, added %dB padding.\n", stats.SpaceInCurrBlock)

			stats, err = w.computeSegmentStats(currentFile)
			if err != nil {
				return currentFile, 0, err
			}
		}
	}

	space := stats.SpaceInCurrBlock
	if space != 0 {
		return currentFile, space, nil
	}

	if stats.UsedBlocks < stats.TotalBlocks {
		return currentFile, w.blockPayloadSize(), nil
	}

	if err := w.rotateToNextBlock(); err != nil {
		return currentFile, 0, err
	}

	fmt.Println("Segment full, creating new segment.")
	newFile, err := w.newSegment()
	if err != nil {
		return currentFile, 0, err
	}
	if err := w.ensureCurrentBlock(newFile); err != nil {
		return currentFile, 0, err
	}

	stats, err = w.computeSegmentStats(newFile)
	if err != nil {
		return currentFile, 0, err
	}
	return newFile, stats.SpaceInCurrBlock, nil
}

func (w *WAL) writeRecordFragments(filePath string, key, value string, tombstone bool) error {
	keyBytes := []byte(key)
	valueBytes := []byte(value)

	remainingKey := len(keyBytes)
	remainingValue := len(valueBytes)
	keyOffset := 0
	valueOffset := 0
	first := true
	currentFile := filePath
	if err := w.ensureCurrentBlock(currentFile); err != nil {
		return err
	}

	for remainingKey+remainingValue > 0 {
		var err error
		var space int64
		currentFile, space, err = w.prepareFragmentSpace(currentFile)
		if err != nil {
			return err
		}

		maxData := space - int64(CRC_SIZE+HEADER_SIZE)
		if maxData <= 0 {
			if err := w.padCurrentBlock(); err != nil {
				return err
			}
			continue
		}

		takeKey, takeValue := computeFragmentParts(maxData, remainingKey, remainingValue)
		typ := determineRecordType(first, takeKey, takeValue, remainingKey, remainingValue)

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
		record := binary.LittleEndian.AppendUint32(nil, crc)
		record = append(record, payload...)

		if err := w.appendToCurrentBlock(record); err != nil {
			return err
		}

		keyOffset += int(takeKey)
		valueOffset += int(takeValue)
		remainingKey -= int(takeKey)
		remainingValue -= int(takeValue)
		first = false

	}

	return nil
}

func (w *WAL) newSegment() (string, error) {
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
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}

	fmt.Printf("Created new segment: %s\n", filename)
	w.files = append(w.files, filename)
	w.currentFile = filename
	w.currentBlk = w.newEmptyBlock()
	w.currentUsed = 0
	w.currentSegmentSize = 0
	return filename, nil
}
