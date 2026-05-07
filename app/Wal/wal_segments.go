package wal

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

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

func (w *WAL) maxTimestampForFile(filePath string) (int64, error) {
	var offset int64
	var maxTs int64
	size, err := w.segmentSize(filePath)
	if err != nil {
		return 0, err
	}
	// safety margin to avoid reading partial records
	readLimit := size - 100
	for offset < readLimit {
		// check CRC to detect padding
		peekData, err := w.readAt(filePath, offset, 4)
		if err != nil {
			break
		}
		// if CRC = 0 padding reached
		if len(peekData) == 4 && peekData[0] == 0 && peekData[1] == 0 && peekData[2] == 0 && peekData[3] == 0 {
			break
		}

		payload, err := w.readRecord(filePath, &offset, false)
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		if len(payload) < TIMESTAMP_SIZE {
			continue
		}
		ts := int64(binary.LittleEndian.Uint64(payload[:TIMESTAMP_SIZE]))
		if ts > maxTs {
			maxTs = ts
		}
	}
	return maxTs, nil
}

func (w *WAL) DeleteSegmentsBeforeTimestamp(flushTs int64) error {
	if len(w.files) == 0 {
		return nil
	}

	highestIdxToDelete := -1
	for i, filePath := range w.files {
		maxTs, err := w.maxTimestampForFile(filePath)
		if err != nil {
			return fmt.Errorf("failed to scan wal file %s: %w", filePath, err)
		}
		// empty file (maxTs==0) safe to delete
		if maxTs == 0 || maxTs < flushTs {
			highestIdxToDelete = i
			continue
		}
		// found a segment that contains newer records
		break
	}

	if highestIdxToDelete < 0 {
		return nil
	}

	base := filepath.Base(w.files[highestIdxToDelete])
	trimmed := base[len("wal_") : len(base)-len(".log")]
	idx, err := strconv.Atoi(trimmed)
	if err != nil {
		return fmt.Errorf("invalid wal filename: %s", base)
	}
	watermark := int64(idx + 1)
	return w.deleteSegmentsByWatermark(watermark)
}
