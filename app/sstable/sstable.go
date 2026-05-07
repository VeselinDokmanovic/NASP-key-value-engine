package sstable

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"key-value-engine/app/block"
)

type SSTable struct {
	ID           int64
	Dir          string
	DataPath     string
	IndexPath    string
	SummaryPath  string
	FilterPath   string
	MetadataPath string

	BlockManager *block.BlockManager

	MinKey []byte
	MaxKey []byte
	Size   int64

	summaryStep     int
	indexDataSize   int64
	summaryDataSize int64
	bloomFilter     *BloomFilter
	merkleHashes    [][]byte
}

type SSTableConfig struct {
	ID           int64
	Dir          string
	BlockManager *block.BlockManager
	SummaryStep  int
}

func NewSSTable(cfg SSTableConfig) *SSTable {
	tableID := cfg.ID
	dir := cfg.Dir

	dataPath := filepath.Join(dir, fmt.Sprintf("table-%d.data", tableID))
	indexPath := filepath.Join(dir, fmt.Sprintf("table-%d.index", tableID))
	summaryPath := filepath.Join(dir, fmt.Sprintf("table-%d.summary", tableID))
	filterPath := filepath.Join(dir, fmt.Sprintf("table-%d.filter", tableID))
	metadataPath := filepath.Join(dir, fmt.Sprintf("table-%d.metadata", tableID))

	summaryStep := cfg.SummaryStep
	if summaryStep <= 0 {
		summaryStep = SUMMARY_STEP
	}

	return &SSTable{
		ID:           tableID,
		Dir:          dir,
		DataPath:     dataPath,
		IndexPath:    indexPath,
		SummaryPath:  summaryPath,
		FilterPath:   filterPath,
		MetadataPath: metadataPath,
		BlockManager: cfg.BlockManager,
		summaryStep:  summaryStep,
	}
}

type blockWriter struct {
	bm       *block.BlockManager
	path     string
	blockNum int
	buf      []byte
	blockSz  int
	total    int
}

func newBlockWriter(bm *block.BlockManager, path string) *blockWriter {
	_ = os.Remove(path)
	return &blockWriter{
		bm:      bm,
		path:    path,
		blockSz: bm.GetBlockSize(),
		buf:     make([]byte, 0, bm.GetBlockSize()),
	}
}

func (bw *blockWriter) append(data []byte) error {
	bw.total += len(data)
	bw.buf = append(bw.buf, data...)
	for len(bw.buf) >= bw.blockSz {
		if err := bw.bm.WriteBlock(bw.path, bw.blockNum, bw.buf[:bw.blockSz]); err != nil {
			return fmt.Errorf("WriteBlock %s blok %d: %w", bw.path, bw.blockNum, err)
		}
		bw.blockNum++
		bw.buf = bw.buf[bw.blockSz:]
	}
	return nil
}

func (bw *blockWriter) flush() error {
	if len(bw.buf) == 0 {
		return nil
	}
	if err := bw.bm.WriteBlock(bw.path, bw.blockNum, bw.buf); err != nil {
		return fmt.Errorf("WriteBlock (flush) %s blok %d: %w", bw.path, bw.blockNum, err)
	}
	bw.blockNum++
	bw.buf = bw.buf[:0]
	return nil
}

func (bw *blockWriter) written() int {
	return bw.total
}

func (st *SSTable) Write(entries []*Entry) error {
	if len(entries) == 0 {
		return errors.New("SSTable ne moze biti prazan")
	}

	sort.Slice(entries, func(i, j int) bool {
		return bytes.Compare(entries[i].Key, entries[j].Key) < 0
	})

	st.MinKey = entries[0].Key
	st.MaxKey = entries[len(entries)-1].Key

	dataOffsets, merkleLeaves, err := st.writeDataFile(entries)
	if err != nil {
		return fmt.Errorf("greska pri pisanju Data fajla: %w", err)
	}

	if err = st.writeIndexFile(entries, dataOffsets); err != nil {
		return fmt.Errorf("greska pri pisanju Index fajla: %w", err)
	}

	if err = st.writeSummaryFile(entries); err != nil {
		return fmt.Errorf("greska pri pisanju Summary fajla: %w", err)
	}

	if err = st.writeBloomFilterFile(entries); err != nil {
		return fmt.Errorf("greska pri pisanju Bloom Filter fajla: %w", err)
	}

	if err = st.writeMetadataFile(merkleLeaves); err != nil {
		return fmt.Errorf("greska pri pisanju Metadata fajla: %w", err)
	}

	return nil
}

func (st *SSTable) writeDataFile(entries []*Entry) (map[int]uint64, [][]byte, error) {
	bw := newBlockWriter(st.BlockManager, st.DataPath)

	offsets := make(map[int]uint64, len(entries))
	merkleLeaves := make([][]byte, 0, len(entries))
	currentOffset := uint64(0)

	for i, entry := range entries {
		serialized := entry.Serialize()
		offsets[i] = currentOffset

		if err := bw.append(serialized); err != nil {
			return nil, nil, err
		}

		currentOffset += uint64(len(serialized))
		merkleLeaves = append(merkleLeaves, hashData(serialized))
	}

	return offsets, merkleLeaves, bw.flush()
}

func (st *SSTable) writeIndexFile(entries []*Entry, dataOffsets map[int]uint64) error {
	dataSize := uint64(len(entries)) * uint64(INDEX_ENTRY_SIZE)
	totalSize := 8 + dataSize

	bw := newBlockWriter(st.BlockManager, st.IndexPath)

	sizeHeader := make([]byte, 8)
	binary.LittleEndian.PutUint64(sizeHeader, totalSize)
	if err := bw.append(sizeHeader); err != nil {
		return err
	}

	for i, entry := range entries {
		serialized := SerializeIndexEntry(entry.Key, dataOffsets[i])
		if err := bw.append(serialized); err != nil {
			return err
		}
	}

	return bw.flush()
}

func (st *SSTable) writeSummaryFile(entries []*Entry) error {
	summaryCount := 0
	for i := range entries {
		if i%st.summaryStep == 0 {
			summaryCount++
		}
	}
	dataSize := uint64(8 + SUMMARY_HEADER_SIZE + summaryCount*SUMMARY_ENTRY_SIZE)

	bw := newBlockWriter(st.BlockManager, st.SummaryPath)

	sizeHeader := make([]byte, 8)
	binary.LittleEndian.PutUint64(sizeHeader, dataSize)
	if err := bw.append(sizeHeader); err != nil {
		return err
	}

	headerBuf := make([]byte, SUMMARY_HEADER_SIZE)
	copy(headerBuf[:SUMMARY_KEY_SIZE], st.MinKey)
	copy(headerBuf[SUMMARY_KEY_SIZE:], st.MaxKey)
	if err := bw.append(headerBuf); err != nil {
		return err
	}

	indexOffset := uint64(0)
	for i, entry := range entries {
		if (i % st.summaryStep) == 0 {
			serialized := SerializeSummaryEntry(entry.Key, indexOffset)
			if err := bw.append(serialized); err != nil {
				return err
			}
		}
		indexOffset += uint64(INDEX_ENTRY_SIZE)
	}

	return bw.flush()
}

func (st *SSTable) writeBloomFilterFile(entries []*Entry) error {
	bf := NewBloomFilter(len(entries), 0.01)
	for _, entry := range entries {
		bf.Add(entry.Key)
	}

	bw := newBlockWriter(st.BlockManager, st.FilterPath)
	if err := bw.append(bf.Serialize()); err != nil {
		return err
	}
	return bw.flush()
}

func (st *SSTable) writeMetadataFile(merkleLeaves [][]byte) error {
	header := make([]byte, 8)
	binary.LittleEndian.PutUint64(header, uint64(len(merkleLeaves)))

	bw := newBlockWriter(st.BlockManager, st.MetadataPath)
	if err := bw.append(header); err != nil {
		return err
	}
	for _, h := range merkleLeaves {
		if err := bw.append(h); err != nil {
			return err
		}
	}
	return bw.flush()
}

func (st *SSTable) Read() error {
	bf, err := st.loadBloomFilterBM()
	if err != nil {
		return fmt.Errorf("greska pri ucitavanju Bloom Filter-a: %w", err)
	}
	st.bloomFilter = bf

	indexBlock, err := st.BlockManager.ReadBlock(st.IndexPath, 0)
	if err != nil {
		return fmt.Errorf("greska pri citanju Index bloka 0: %w", err)
	}
	if len(indexBlock) < 8 {
		return errors.New("index fajl prekratak za size header")
	}
	st.indexDataSize = int64(binary.LittleEndian.Uint64(indexBlock[0:8]))

	summaryBlock, err := st.BlockManager.ReadBlock(st.SummaryPath, 0)
	if err != nil {
		return fmt.Errorf("greska pri citanju Summary bloka 0: %w", err)
	}
	if len(summaryBlock) < 8 {
		return errors.New("summary fajl prekratak za size header")
	}
	st.summaryDataSize = int64(binary.LittleEndian.Uint64(summaryBlock[0:8]))

	if len(summaryBlock) < 8+SUMMARY_HEADER_SIZE {
		return errors.New("summary blok prekratak za header")
	}
	st.MinKey = bytes.TrimRight(summaryBlock[8:8+SUMMARY_KEY_SIZE], "\x00")
	st.MaxKey = bytes.TrimRight(summaryBlock[8+SUMMARY_KEY_SIZE:8+SUMMARY_HEADER_SIZE], "\x00")

	hashes, err := st.readMerkleMetadataBM()
	if err != nil {
		return fmt.Errorf("greska pri citanju Metadata fajla: %w", err)
	}
	st.merkleHashes = hashes

	return nil
}

func (st *SSTable) loadBloomFilterBM() (*BloomFilter, error) {
	data, err := st.readAllBlocks(st.FilterPath)
	if err != nil {
		return nil, err
	}
	return DeserializeBloomFilter(data), nil
}

func (st *SSTable) readMerkleMetadataBM() ([][]byte, error) {
	data, err := st.readAllBlocks(st.MetadataPath)
	if err != nil {
		return nil, err
	}
	if len(data) < 8 {
		return nil, errors.New("metadata fajl prekratak")
	}

	count := binary.LittleEndian.Uint64(data[0:8])
	data = data[8:]

	hashes := make([][]byte, count)
	for i := uint64(0); i < count; i++ {
		if len(data) < MERKLE_HASH_SIZE {
			return nil, errors.New("metadata fajl skracen pri citanju heshova")
		}
		h := make([]byte, MERKLE_HASH_SIZE)
		copy(h, data[:MERKLE_HASH_SIZE])
		hashes[i] = h
		data = data[MERKLE_HASH_SIZE:]
	}
	return hashes, nil
}

func (st *SSTable) readAllBlocks(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	fileSize := int(info.Size())
	blockSz := st.BlockManager.GetBlockSize()

	var result []byte
	for blockNum := 0; blockNum*blockSz < fileSize; blockNum++ {
		blockData, err := st.BlockManager.ReadBlock(path, blockNum)
		if err != nil {
			return nil, fmt.Errorf("ReadBlock %s blok %d: %w", path, blockNum, err)
		}
		result = append(result, blockData...)
	}

	if len(result) > fileSize {
		result = result[:fileSize]
	}
	return result, nil
}

func (st *SSTable) MightContain(key []byte) bool {
	if st == nil || st.bloomFilter == nil {
		return true
	}
	return st.bloomFilter.MightContain(key)
}

func (st *SSTable) Search(key []byte) ([]byte, bool, error) {
	if !st.bloomFilter.MightContain(key) {
		return nil, false, nil
	}

	if bytes.Compare(key, st.MinKey) < 0 || bytes.Compare(key, st.MaxKey) > 0 {
		return nil, false, nil
	}

	startByteOffset, endByteOffset, err := st.searchSummaryBlocks(key)
	if err != nil {
		return nil, false, err
	}

	dataOffset, found, err := st.searchIndexBlocks(startByteOffset, endByteOffset, key)
	if err != nil {
		return nil, false, err
	}
	if !found {
		return nil, false, nil
	}

	entry, err := st.readEntryAt(dataOffset)
	if err != nil {
		return nil, false, err
	}

	if entry.Tombstone == 1 {
		return nil, true, nil
	}

	return entry.Value, true, nil
}

func (st *SSTable) readSummaryHeader() (*Summary, error) {
	blockData, err := st.BlockManager.ReadBlock(st.SummaryPath, 0)
	if err != nil {
		return nil, fmt.Errorf("greska pri citanju Summary bloka 0: %w", err)
	}
	if len(blockData) < 8+SUMMARY_HEADER_SIZE {
		return nil, errors.New("Summary blok prekratak za header")
	}
	return &Summary{
		MinKey: bytes.TrimRight(blockData[8:8+SUMMARY_KEY_SIZE], "\x00"),
		MaxKey: bytes.TrimRight(blockData[8+SUMMARY_KEY_SIZE:8+SUMMARY_HEADER_SIZE], "\x00"),
	}, nil
}

func (st *SSTable) searchSummaryBlocks(key []byte) (uint64, uint64, error) {
	blockSize := uint64(st.BlockManager.GetBlockSize())
	indexContentSize := uint64(st.indexDataSize) - 8

	startByteOffset := uint64(0)
	endByteOffset := indexContentSize

	currentOffset := uint64(8 + SUMMARY_HEADER_SIZE)
	totalSize := uint64(st.summaryDataSize)

	for currentOffset < totalSize {
		blockNum := int(currentOffset / blockSize)
		offsetInBlock := int(currentOffset % blockSize)

		blockData, err := st.BlockManager.ReadBlock(st.SummaryPath, blockNum)
		if err != nil {
			return 0, 0, fmt.Errorf("greska pri citanju Summary bloka %d: %w", blockNum, err)
		}

		for offsetInBlock+SUMMARY_ENTRY_SIZE <= len(blockData) && currentOffset < totalSize {
			entryData := blockData[offsetInBlock : offsetInBlock+SUMMARY_ENTRY_SIZE]
			parsedKey := bytes.TrimRight(entryData[:SUMMARY_KEY_SIZE], "\x00")
			indexOffset := binary.LittleEndian.Uint64(entryData[SUMMARY_KEY_SIZE : SUMMARY_KEY_SIZE+8])

			if bytes.Compare(key, parsedKey) < 0 {
				endByteOffset = indexOffset
				return startByteOffset, endByteOffset, nil
			}
			startByteOffset = indexOffset

			offsetInBlock += SUMMARY_ENTRY_SIZE
			currentOffset += SUMMARY_ENTRY_SIZE
		}

		if currentOffset < totalSize && offsetInBlock < len(blockData) {
			remaining := blockData[offsetInBlock:]
			nextBlock, err := st.BlockManager.ReadBlock(st.SummaryPath, blockNum+1)
			if err != nil {
				return 0, 0, fmt.Errorf("greska pri citanju Summary bloka %d: %w", blockNum+1, err)
			}
			combined := append(remaining, nextBlock...)
			if len(combined) >= SUMMARY_ENTRY_SIZE {
				entryData := combined[:SUMMARY_ENTRY_SIZE]
				parsedKey := bytes.TrimRight(entryData[:SUMMARY_KEY_SIZE], "\x00")
				indexOffset := binary.LittleEndian.Uint64(entryData[SUMMARY_KEY_SIZE : SUMMARY_KEY_SIZE+8])
				if bytes.Compare(key, parsedKey) < 0 {
					endByteOffset = indexOffset
					return startByteOffset, endByteOffset, nil
				}
				startByteOffset = indexOffset
			}
			currentOffset += SUMMARY_ENTRY_SIZE
		}
	}

	return startByteOffset, endByteOffset, nil
}

func (st *SSTable) searchIndexBlocks(startByteOffset, endByteOffset uint64, key []byte) (uint64, bool, error) {
	blockSize := uint64(st.BlockManager.GetBlockSize())

	currentOffset := startByteOffset + 8

	absoluteEnd := endByteOffset + 8

	if absoluteEnd > uint64(st.indexDataSize) {
		absoluteEnd = uint64(st.indexDataSize)
	}

	for currentOffset < absoluteEnd {
		blockNum := int(currentOffset / blockSize)
		offsetInBlock := int(currentOffset % blockSize)

		blockData, err := st.BlockManager.ReadBlock(st.IndexPath, blockNum)
		if err != nil {
			return 0, false, fmt.Errorf("greska pri citanju Index bloka %d: %w", blockNum, err)
		}

		for offsetInBlock+INDEX_ENTRY_SIZE <= len(blockData) && currentOffset < absoluteEnd {
			entryData := blockData[offsetInBlock : offsetInBlock+INDEX_ENTRY_SIZE]
			parsedKey := bytes.TrimRight(entryData[:INDEX_KEY_SIZE], "\x00")
			dataOffset := binary.LittleEndian.Uint64(entryData[INDEX_KEY_SIZE : INDEX_KEY_SIZE+8])

			if bytes.Equal(parsedKey, key) {
				return dataOffset, true, nil
			}

			offsetInBlock += INDEX_ENTRY_SIZE
			currentOffset += INDEX_ENTRY_SIZE
		}

		if currentOffset < absoluteEnd && offsetInBlock < len(blockData) {
			remaining := blockData[offsetInBlock:]
			nextBlock, err := st.BlockManager.ReadBlock(st.IndexPath, blockNum+1)
			if err != nil {
				return 0, false, fmt.Errorf("greska pri citanju Index bloka %d: %w", blockNum+1, err)
			}
			combined := append(remaining, nextBlock...)
			if len(combined) >= INDEX_ENTRY_SIZE {
				entryData := combined[:INDEX_ENTRY_SIZE]
				parsedKey := bytes.TrimRight(entryData[:INDEX_KEY_SIZE], "\x00")
				dataOffset := binary.LittleEndian.Uint64(entryData[INDEX_KEY_SIZE : INDEX_KEY_SIZE+8])
				if bytes.Equal(parsedKey, key) {
					return dataOffset, true, nil
				}
			}
			currentOffset += INDEX_ENTRY_SIZE
		}
	}

	return 0, false, nil
}

func (st *SSTable) readEntryAt(offset uint64) (*Entry, error) {
	blockSize := st.BlockManager.GetBlockSize()
	blockNum := int(offset) / blockSize
	offsetInBlock := int(offset) % blockSize

	blockData, err := st.BlockManager.ReadBlock(st.DataPath, blockNum)
	if err != nil {
		return nil, err
	}

	data := blockData[offsetInBlock:]
	if nextBlock, err := st.BlockManager.ReadBlock(st.DataPath, blockNum+1); err == nil {
		combined := make([]byte, len(data)+len(nextBlock))
		copy(combined, data)
		copy(combined[len(data):], nextBlock)
		data = combined
	}

	return DeserializeEntry(bytes.NewReader(data))
}

func (st *SSTable) ValidateIntegrity() (bool, []int, error) {
	rawData, err := st.readAllBlocks(st.DataPath)
	if err != nil {
		return false, nil, fmt.Errorf("greska pri citanju data fajla za validaciju: %w", err)
	}

	r := bytes.NewReader(rawData)
	var values [][]byte

	for r.Len() > 0 {
		entry, err := DeserializeEntry(r)
		if err != nil {
			break
		}
		values = append(values, entry.Serialize())
	}

	valid, changed := ValidateMerkle(st.merkleHashes, values)
	return valid, changed, nil
}

func (st *SSTable) GetInfo() map[string]interface{} {
	return map[string]interface{}{
		"id":                st.ID,
		"minKey":            string(st.MinKey),
		"maxKey":            string(st.MaxKey),
		"dataPath":          st.DataPath,
		"indexPath":         st.IndexPath,
		"summaryPath":       st.SummaryPath,
		"filterPath":        st.FilterPath,
		"metadataPath":      st.MetadataPath,
		"indexDataSize":     st.indexDataSize,
		"merkleHashesCount": len(st.merkleHashes),
	}
}
