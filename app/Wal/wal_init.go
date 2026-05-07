package wal

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"key-value-engine/app/block"
)

func WALInit(dir string, bm *block.BlockManager) (*WAL, error) {
	return WALInitConfig(dir, int64(BLOCK_SIZE), int64(BLOCK_FACTOR), bm)
}

func WALInitConfig(dir string, blockSize, blockFactor int64, bm *block.BlockManager) (*WAL, error) {
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
