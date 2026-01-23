package sstable

import (
	"encoding/binary"
	"hash/crc32"
	"os"
)

type Entry struct {
	Timestamp int64
	Tombstone byte
	Key       []byte
	Value     []byte
}

func (e *Entry) Serialize() []byte {
	keySize := uint64(len(e.Key))
	valueSize := uint64(len(e.Value))

	buf := make([]byte, 0)

	tmp := make([]byte, 8)
	binary.LittleEndian.PutUint64(tmp, uint64(e.Timestamp))
	buf = append(buf, tmp...)

	buf = append(buf, e.Tombstone)

	binary.LittleEndian.PutUint64(tmp, keySize)
	buf = append(buf, tmp...)

	binary.LittleEndian.PutUint64(tmp, valueSize)
	buf = append(buf, tmp...)

	buf = append(buf, e.Key...)
	buf = append(buf, e.Value...)

	crc := crc32.ChecksumIEEE(buf)
	crcBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(crcBytes, crc)

	return append(crcBytes, buf...)
}

func DeserializeEntry(file *os.File) (*Entry, error) {
	header := make([]byte, 4+8+1+8+8)
	_, err := file.Read(header)
	if err != nil {
		return nil, err
	}

	e := &Entry{}
	e.Timestamp = int64(binary.LittleEndian.Uint64(header[4:12]))
	e.Tombstone = header[12]

	keySize := binary.LittleEndian.Uint64(header[13:21])
	valueSize := binary.LittleEndian.Uint64(header[21:29])

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
