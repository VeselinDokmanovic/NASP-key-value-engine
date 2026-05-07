package wal

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

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
		payload = append(payload, binary.LittleEndian.AppendUint64(nil, uint64(time.Now().UnixNano()))...)
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
