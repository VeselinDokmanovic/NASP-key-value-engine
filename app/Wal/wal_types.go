package wal

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"key-value-engine/app/block"
)

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
