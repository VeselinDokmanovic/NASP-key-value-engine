package sstable

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
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

	CompressionLevel int

	summaryStep     int
	indexFileSize   int64
	summaryFileSize int64
	bloomFilter     *BloomFilter
	merkleHashes    [][]byte
}

type SSTableConfig struct {
	ID               int64
	Dir              string
	BlockManager     *block.BlockManager
	CompressionLevel int
	SummaryStep      int // 1.3[DZ1]: svaki N-ti index entry se upisuje u Summary (default 4)
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
		summaryStep = SUMMARY_STEP // default iz constants.go
	}

	return &SSTable{
		ID:               tableID,
		Dir:              dir,
		DataPath:         dataPath,
		IndexPath:        indexPath,
		SummaryPath:      summaryPath,
		FilterPath:       filterPath,
		MetadataPath:     metadataPath,
		BlockManager:     cfg.BlockManager,
		CompressionLevel: cfg.CompressionLevel,
		summaryStep:      summaryStep,
	}
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

	err = st.writeIndexFile(entries, dataOffsets)
	if err != nil {
		return fmt.Errorf("greska pri pisanju Index fajla: %w", err)
	}

	err = st.writeSummaryFile(entries)
	if err != nil {
		return fmt.Errorf("greska pri pisanju Summary fajla: %w", err)
	}

	err = st.writeBloomFilterFile(entries)
	if err != nil {
		return fmt.Errorf("greska pri pisanju Bloom Filter fajla: %w", err)
	}

	err = st.writeMetadataFile(merkleLeaves)
	if err != nil {
		return fmt.Errorf("greska pri pisanju Metadata fajla: %w", err)
	}

	return nil
}

func (st *SSTable) writeDataFile(entries []*Entry) (map[int]uint64, [][]byte, error) {
	file, err := os.Create(st.DataPath)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()

	offsets := make(map[int]uint64)
	merkleLeaves := make([][]byte, 0, len(entries))
	currentOffset := uint64(0)

	for i, entry := range entries {
		var serialized []byte

		if st.CompressionLevel >= 2 {
			serialized = entry.SerializeV2()
		} else {
			serialized = entry.Serialize()
		}

		offsets[i] = currentOffset

		_, err := file.Write(serialized)
		if err != nil {
			return nil, nil, err
		}

		currentOffset += uint64(len(serialized))
		merkleLeaves = append(merkleLeaves, hashData(serialized))
	}

	return offsets, merkleLeaves, nil
}

func (st *SSTable) writeIndexFile(entries []*Entry, dataOffsets map[int]uint64) error {
	file, err := os.Create(st.IndexPath)
	if err != nil {
		return err
	}
	defer file.Close()

	var lastKey []byte

	for i, entry := range entries {
		offset := dataOffsets[i]

		var serialized []byte

		if st.CompressionLevel >= 2 {
			serialized = serializeIndexEntryV2(entry.Key, offset, lastKey)
			lastKey = entry.Key
		} else {
			serialized = SerializeIndexEntry(entry.Key, offset)
		}

		_, err := file.Write(serialized)
		if err != nil {
			return err
		}
	}

	return nil
}

func (st *SSTable) writeSummaryFile(entries []*Entry) error {
	file, err := os.Create(st.SummaryPath)
	if err != nil {
		return err
	}
	defer file.Close()

	err = WriteSummaryHeader(file, st.MinKey, st.MaxKey)
	if err != nil {
		return err
	}

	var lastKey []byte
	indexOffset := uint64(0)

	for i, entry := range entries {
		if (i % st.summaryStep) == 0 {
			var serialized []byte

			if st.CompressionLevel >= 2 {
				serialized = serializeSummaryEntryV2(entry.Key, indexOffset, lastKey)
				lastKey = entry.Key
			} else {
				serialized = SerializeSummaryEntry(entry.Key, indexOffset)
			}

			_, err := file.Write(serialized)
			if err != nil {
				return err
			}
		}

		if st.CompressionLevel >= 2 {
			indexOffset += uint64(len(serializeIndexEntryV2(entry.Key, indexOffset, lastKey)))
		} else {
			indexOffset += uint64(INDEX_ENTRY_SIZE)
		}
	}

	return nil
}

func (st *SSTable) writeBloomFilterFile(entries []*Entry) error {
	bf := NewBloomFilter(len(entries), 0.01)

	for _, entry := range entries {
		bf.Add(entry.Key)
	}

	return WriteBloomFilter(st.FilterPath, bf)
}

func (st *SSTable) writeMetadataFile(merkleLeaves [][]byte) error {
	file, err := os.Create(st.MetadataPath)
	if err != nil {
		return err
	}
	defer file.Close()

	return WriteMerkleMetadata(file, merkleLeaves)
}

func (st *SSTable) Read() error {
	bf, err := LoadBloomFilter(st.FilterPath)
	if err != nil {
		return fmt.Errorf("greska pri ucitavanju Bloom Filter-a: %w", err)
	}
	st.bloomFilter = bf

	// Ucitavamo samo header Summary-ja (MinKey i MaxKey) - ostalo citamo blok po blok
	summaryHeader, err := st.readSummaryHeader()
	if err != nil {
		return fmt.Errorf("greska pri citanju Summary header-a: %w", err)
	}
	st.MinKey = summaryHeader.MinKey
	st.MaxKey = summaryHeader.MaxKey

	summaryInfo, err := os.Stat(st.SummaryPath)
	if err != nil {
		return fmt.Errorf("greska pri stat Summary fajla: %w", err)
	}
	st.summaryFileSize = summaryInfo.Size()

	indexInfo, err := os.Stat(st.IndexPath)
	if err != nil {
		return fmt.Errorf("greska pri stat Index fajla: %w", err)
	}
	st.indexFileSize = indexInfo.Size()

	metadataFile, err := os.Open(st.MetadataPath)
	if err != nil {
		return fmt.Errorf("greska pri otvaranju Metadata fajla: %w", err)
	}
	defer metadataFile.Close()

	hashes, err := ReadMerkleMetadata(metadataFile)
	if err != nil {
		return fmt.Errorf("greska pri citanju Metadata fajla: %w", err)
	}
	st.merkleHashes = hashes

	return nil
}

// Search trazi kljuc u SSTable-u.
// Vraca (vrednost, pronadjen, greska).
// pronadjen=true znaci da je kljuc u ovoj tabeli (i ako je obrisan tombstonom, vrednost je nil).
// pronadjen=false znaci da kljuc uopste nije u ovoj tabeli — trazi dalje u starijim tabelama.
func (st *SSTable) Search(key []byte) ([]byte, bool, error) {
	// Korak 1: Bloom Filter test
	if !st.bloomFilter.MightContain(key) {
		return nil, false, nil
	}

	// Korak 2: Provjera Min-Max opsega
	if bytes.Compare(key, st.MinKey) < 0 || bytes.Compare(key, st.MaxKey) > 0 {
		return nil, false, nil
	}

	// Korak 3: Citanje Summary blok po blok -> opseg u Index fajlu (byte offseti)
	startByteOffset, endByteOffset, err := st.searchSummaryBlocks(key)
	if err != nil {
		return nil, false, err
	}

	// Korak 4: Citanje Index fajla blok po blok u zadatom opsegu
	dataOffset, found, err := st.searchIndexBlocks(startByteOffset, endByteOffset, key)
	if err != nil {
		return nil, false, err
	}
	if !found {
		return nil, false, nil
	}

	// Korak 5: Citanje iz Data fajla preko BlockManager-a (LRU kes)
	entry, err := st.readEntryAt(dataOffset)
	if err != nil {
		return nil, false, err
	}

	if entry.Tombstone == 1 {
		// Kljuc je pronadjen ali je obrisan — vracamo found=true, value=nil
		// da signalizujemo: ne trazi dalje u starijim tabelama
		return nil, true, nil
	}

	return entry.Value, true, nil
}

// readSummaryHeader cita samo header Summary fajla (MinKey i MaxKey) blok po blok.
func (st *SSTable) readSummaryHeader() (*Summary, error) {
	blockData, err := st.BlockManager.ReadBlock(st.SummaryPath, 0)
	if err != nil {
		return nil, fmt.Errorf("greska pri citanju Summary bloka 0: %w", err)
	}

	if len(blockData) < SUMMARY_HEADER_SIZE {
		return nil, fmt.Errorf("Summary blok prekratak za header")
	}

	return &Summary{
		MinKey: bytes.TrimRight(blockData[:SUMMARY_KEY_SIZE], "\x00"),
		MaxKey: bytes.TrimRight(blockData[SUMMARY_KEY_SIZE:SUMMARY_HEADER_SIZE], "\x00"),
	}, nil
}

// searchSummaryBlocks cita Summary fajl blok po blok i vraca [startByteOffset, endByteOffset)
// opseg u Index fajlu u kome treba traziti dati kljuc.
func (st *SSTable) searchSummaryBlocks(key []byte) (uint64, uint64, error) {
	blockSize := uint64(st.BlockManager.GetBlockSize())
	startByteOffset := uint64(0)
	endByteOffset := uint64(st.indexFileSize)

	// Preskacemo header (MinKey + MaxKey)
	currentOffset := uint64(SUMMARY_HEADER_SIZE)
	totalSize := uint64(st.summaryFileSize)

	for currentOffset < totalSize {
		blockNum := int(currentOffset / blockSize)
		offsetInBlock := int(currentOffset % blockSize)

		blockData, err := st.BlockManager.ReadBlock(st.SummaryPath, blockNum)
		if err != nil {
			return 0, 0, fmt.Errorf("greska pri citanju Summary bloka %d: %w", blockNum, err)
		}

		// Prolazimo kroz entrie koji pocinju u ovom bloku
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

		// Entry prelazi granicu bloka
		if currentOffset < totalSize && offsetInBlock < len(blockData) {
			remaining := blockData[offsetInBlock:]
			nextBlock, err := st.BlockManager.ReadBlock(st.SummaryPath, blockNum+1)
			if err != nil {
				return 0, 0, fmt.Errorf("greska pri citanju Summary bloka %d: %w", blockNum+1, err)
			}

			combined := make([]byte, len(remaining)+len(nextBlock))
			copy(combined, remaining)
			copy(combined[len(remaining):], nextBlock)

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

// searchIndexBlocks cita Index fajl blok po blok kroz BlockManager i trazi dati kljuc
// u opsegu [startByteOffset, endByteOffset). Vraca data offset i true ako je kljuc nadjen.
func (st *SSTable) searchIndexBlocks(startByteOffset, endByteOffset uint64, key []byte) (uint64, bool, error) {
	blockSize := uint64(st.BlockManager.GetBlockSize())
	currentOffset := startByteOffset

	for currentOffset < endByteOffset {
		blockNum := int(currentOffset / blockSize)
		offsetInBlock := int(currentOffset % blockSize)

		blockData, err := st.BlockManager.ReadBlock(st.IndexPath, blockNum)
		if err != nil {
			return 0, false, fmt.Errorf("greska pri citanju Index bloka %d: %w", blockNum, err)
		}

		// Prolazimo kroz sve entrie koji pocinju u ovom bloku
		for offsetInBlock+INDEX_ENTRY_SIZE <= len(blockData) && currentOffset < endByteOffset {
			entryData := blockData[offsetInBlock : offsetInBlock+INDEX_ENTRY_SIZE]

			parsedKey := bytes.TrimRight(entryData[:INDEX_KEY_SIZE], "\x00")
			dataOffset := binary.LittleEndian.Uint64(entryData[INDEX_KEY_SIZE : INDEX_KEY_SIZE+8])

			if bytes.Equal(parsedKey, key) {
				return dataOffset, true, nil
			}

			offsetInBlock += INDEX_ENTRY_SIZE
			currentOffset += INDEX_ENTRY_SIZE
		}

		// Ako entry prelazi granicu bloka, ucitajmo sledeci blok i procitamo entry
		if currentOffset < endByteOffset && offsetInBlock < len(blockData) {
			remaining := blockData[offsetInBlock:]
			nextBlock, err := st.BlockManager.ReadBlock(st.IndexPath, blockNum+1)
			if err != nil {
				return 0, false, fmt.Errorf("greska pri citanju Index bloka %d: %w", blockNum+1, err)
			}

			combined := make([]byte, len(remaining)+len(nextBlock))
			copy(combined, remaining)
			copy(combined[len(remaining):], nextBlock)

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

// readEntryAt cita Entry iz data fajla koristeci BlockManager za kesiranje.
// Ako zapis prelazi granicu bloka, automatski se ucitava i sledeci blok.
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

	r := bytes.NewReader(data)
	if st.CompressionLevel >= 2 {
		return DeserializeEntryV2(r)
	}
	return DeserializeEntry(r)
}

func (st *SSTable) ValidateIntegrity() (bool, []int, error) {
	dataFile, err := os.Open(st.DataPath)
	if err != nil {
		return false, nil, err
	}
	defer dataFile.Close()

	var values [][]byte

	for {
		var entry *Entry
		var err error

		if st.CompressionLevel >= 2 {
			entry, err = DeserializeEntryV2(dataFile)
		} else {
			entry, err = DeserializeEntry(dataFile)
		}

		if err != nil {
			break
		}

		// Koristi isti format koji je koristen pri pisanju
		var serialized []byte
		if st.CompressionLevel >= 2 {
			serialized = entry.SerializeV2()
		} else {
			serialized = entry.Serialize()
		}
		values = append(values, serialized)
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
		"compressionLevel":  st.CompressionLevel,
		"indexFileSize":     st.indexFileSize,
		"merkleHashesCount": len(st.merkleHashes),
	}
}

func serializeIndexEntryV2(key []byte, offset uint64, lastKey []byte) []byte {
	deltaKey := encodeDeltaKey(key, lastKey)
	offsetBytes := encodeVarint(offset)

	result := make([]byte, 0, len(deltaKey)+len(offsetBytes))
	result = append(result, deltaKey...)
	result = append(result, offsetBytes...)
	return result
}

func serializeSummaryEntryV2(key []byte, indexOffset uint64, lastKey []byte) []byte {
	deltaKey := encodeDeltaKey(key, lastKey)
	offsetBytes := encodeVarint(indexOffset)

	result := make([]byte, 0, len(deltaKey)+len(offsetBytes))
	result = append(result, deltaKey...)
	result = append(result, offsetBytes...)
	return result
}

func encodeDeltaKey(key []byte, lastKey []byte) []byte {
	if len(lastKey) == 0 {
		result := make([]byte, 0, 2+len(key))
		result = append(result, encodeVarint(uint64(len(key)))...)
		result = append(result, key...)
		return result
	}

	commonLen := 0
	minLen := len(lastKey)
	if len(key) < minLen {
		minLen = len(key)
	}

	for i := 0; i < minLen; i++ {
		if lastKey[i] == key[i] {
			commonLen++
		} else {
			break
		}
	}

	diffLen := len(key) - commonLen
	diff := key[commonLen:]

	result := make([]byte, 0, 2+len(diff))
	result = append(result, encodeVarint(uint64(commonLen))...)
	result = append(result, encodeVarint(uint64(diffLen))...)
	result = append(result, diff...)
	return result
}

func encodeVarint(value uint64) []byte {
	var result []byte

	for value >= 128 {
		result = append(result, byte((value&0x7F)|0x80))
		value >>= 7
	}

	result = append(result, byte(value&0x7F))
	return result
}

func decodeVarint(data []byte, offset int) (uint64, int) {
	var result uint64
	var shift uint

	for i := offset; i < len(data); i++ {
		b := data[i]
		result |= uint64(b&0x7F) << shift

		if b&0x80 == 0 {
			return result, i + 1 - offset
		}

		shift += 7
	}

	return result, 0
}

func DeserializeEntryV2(r io.Reader) (*Entry, error) {
	crc := make([]byte, 4)
	_, err := io.ReadFull(r, crc)
	if err != nil {
		return nil, err
	}

	buf := make([]byte, 10)
	n, _ := r.Read(buf)
	if n == 0 {
		return nil, errors.New("defektna datoteka")
	}

	timestamp, _ := decodeVarint(buf, 0)

	flags := make([]byte, 1)
	_, err = io.ReadFull(r, flags)
	if err != nil {
		return nil, err
	}

	tombstone := flags[0] >> 7
	typeFlag := (flags[0] >> 6) & 0x01

	buf = make([]byte, 20)
	n, _ = r.Read(buf)

	keySize, keyConsumed := decodeVarint(buf, 0)
	valueSize, _ := decodeVarint(buf, keyConsumed)

	key := make([]byte, keySize)
	_, err = io.ReadFull(r, key)
	if err != nil {
		return nil, err
	}

	value := make([]byte, valueSize)
	_, err = io.ReadFull(r, value)
	if err != nil {
		return nil, err
	}

	return &Entry{
		Timestamp: int64(timestamp),
		Tombstone: tombstone,
		Type:      typeFlag,
		Key:       key,
		Value:     value,
	}, nil
}

func (e *Entry) SerializeV2() []byte {
	payload := make([]byte, 0)

	payload = append(payload, encodeVarint(uint64(e.Timestamp))...)

	flags := (e.Tombstone << 7) | (e.Type << 6)
	payload = append(payload, flags)

	payload = append(payload, encodeVarint(uint64(len(e.Key)))...)
	payload = append(payload, encodeVarint(uint64(len(e.Value)))...)

	payload = append(payload, e.Key...)
	payload = append(payload, e.Value...)

	crc := binary.LittleEndian.AppendUint32(nil, calculateCRC(payload))
	return append(crc, payload...)
}

func calculateCRC(data []byte) uint32 {
	return crc32.ChecksumIEEE(data)
}
