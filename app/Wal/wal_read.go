package wal

import (
	"encoding/binary"
	"fmt"
	"io"
)

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
