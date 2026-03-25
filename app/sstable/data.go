package sstable

import (
	"encoding/binary"
	"hash/crc32"
	"os"
)

type Entry struct {
	Timestamp int64
	Tombstone byte
	Type      byte
	Key       []byte
	Value     []byte
}

func (e *Entry) Serialize() []byte {
	keySize := uint64(len(e.Key))
	valueSize := uint64(len(e.Value))

	payload := make([]byte, 0)

	tmp8 := make([]byte, 8)

	binary.LittleEndian.PutUint64(tmp8, uint64(e.Timestamp))
	payload = append(payload, tmp8...)
	payload = append(payload, e.Tombstone)
	binary.LittleEndian.PutUint64(tmp8, keySize)
	payload = append(payload, tmp8...)
	binary.LittleEndian.PutUint64(tmp8, valueSize)
	payload = append(payload, tmp8...)
	payload = append(payload, e.Type)
	payload = append(payload, e.Key...)
	payload = append(payload, e.Value...)
	crc := crc32.ChecksumIEEE(payload)
	crcBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(crcBytes, crc)

	return append(crcBytes, payload...)
}

func DeserializeEntry(file *os.File) (*Entry, error) {
	header := make([]byte, 4+8+1+8+8+1)

	_, err := file.Read(header)
	if err != nil {
		return nil, err
	}

	e := &Entry{}

	e.Timestamp = int64(binary.LittleEndian.Uint64(header[4:12]))
	e.Tombstone = header[12]

	keySize := binary.LittleEndian.Uint64(header[13:21])
	valueSize := binary.LittleEndian.Uint64(header[21:29])

	e.Type = header[29]

	e.Key = make([]byte, keySize)
	_, err = file.Read(e.Key)
	if err != nil {
		return nil, err
	}

	e.Value = make([]byte, valueSize)
	_, err = file.Read(e.Value)
	if err != nil {
		return nil, err
	}

	return e, nil
}

// ==================== COMPRESSED FORMAT V2 ====================
// SerializeV2 enkodira Entry sa kompresijom:
// [Timestamp:varint] [Flags:1B] [KeySize:varint] [ValueSize:varint] [Key:var] [Value:var] [CRC32:4B]
// Flags: bit0=Tombstone, bit1=Type, bit2-7=reserved
func (e *Entry) SerializeV2() []byte {
	// Korak 1: Serijalizuj sve polja sa kompresijom
	payload := &[]byte{}

	// Timestamp kao varint
	tsVarint := EncodeVarint(uint64(e.Timestamp))
	*payload = append(*payload, tsVarint...)

	// Tombstone + Type kao 1 bajt (bit packing)
	flags := EncodeTombstoneAndType(e.Tombstone != 0, e.Type)
	*payload = append(*payload, flags)

	// KeySize kao varint
	keySizeVarint := EncodeVarint(uint64(len(e.Key)))
	*payload = append(*payload, keySizeVarint...)

	// ValueSize kao varint
	valueSizeVarint := EncodeVarint(uint64(len(e.Value)))
	*payload = append(*payload, valueSizeVarint...)

	// Key i Value kao raw
	*payload = append(*payload, e.Key...)
	*payload = append(*payload, e.Value...)

	// Korak 2: Izračunaj CRC32 i dodaj na početak
	crc := crc32.ChecksumIEEE(*payload)
	crcBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(crcBytes, crc)

	result := make([]byte, len(crcBytes)+len(*payload))
	copy(result, crcBytes)
	copy(result[len(crcBytes):], *payload)

	return result
}

// DeserializeEntryV2 dekodira komprimovani Entry
func DeserializeEntryV2(data []byte, offset int) (*Entry, int, error) {
	pos := offset

	// Pročitaj Timestamp kao varint
	timestamp, pos, err := DecodeVarint(data, pos)
	if err != nil {
		return nil, pos, err
	}

	// Pročitaj Flags
	if pos >= len(data) {
		return nil, pos, ErrInsufficientData
	}
	flags := data[pos]
	pos++

	tombstone, typeVal := DecodeTombstoneAndType(flags)

	// Pročitaj KeySize kao varint
	keySize, pos, err := DecodeVarint(data, pos)
	if err != nil {
		return nil, pos, err
	}

	// Pročitaj ValueSize kao varint
	valueSize, pos, err := DecodeVarint(data, pos)
	if err != nil {
		return nil, pos, err
	}

	// Pročitaj Key
	if pos+int(keySize) > len(data) {
		return nil, pos, ErrInsufficientData
	}
	key := data[pos : pos+int(keySize)]
	pos += int(keySize)

	// Pročitaj Value
	if pos+int(valueSize) > len(data) {
		return nil, pos, ErrInsufficientData
	}
	value := data[pos : pos+int(valueSize)]
	pos += int(valueSize)

	e := &Entry{
		Timestamp: int64(timestamp),
		Tombstone: boolToByte(tombstone),
		Type:      typeVal,
		Key:       key,
		Value:     value,
	}

	return e, pos, nil
}

// boolToByte konvertuje bool u byte (true=1, false=0)
func boolToByte(b bool) byte {
	if b {
		return 1
	}
	return 0
}
