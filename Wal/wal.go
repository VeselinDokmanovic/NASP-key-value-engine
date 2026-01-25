package wal

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

/*
   +---------------+-----------------+---------------+---------------+----------------+-...-+--...--+
   |    CRC (4B)   | Timestamp (8B) | Tombstone(1B) | Key Size (8B) | Value Size (8B) | Key | Value |
   +---------------+-----------------+---------------+---------------+----------------+-...-+--...--+
   CRC = 32bit hash computed over the payload using CRC
   Key Size = Length of the Key data
   Tombstone = If this record was deleted and has a value
   Value Size = Length of the Value data
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

	CRC_START        = 0
	TIMESTAMP_START  = CRC_START + CRC_SIZE
	TOMBSTONE_START  = TIMESTAMP_START + TIMESTAMP_SIZE
	KEY_SIZE_START   = TOMBSTONE_START + TOMBSTONE_SIZE
	VALUE_SIZE_START = KEY_SIZE_START + KEY_SIZE_SIZE
	KEY_START        = VALUE_SIZE_START + VALUE_SIZE_SIZE
)

func CRC32(data []byte) uint32 {
	return crc32.ChecksumIEEE(data)
}

func ReadRecord(file *os.File, print bool) ([]byte, error) {
	crc := make([]byte, CRC_SIZE)
	_, err := file.Read(crc)
	if err == io.EOF {
		return nil, io.EOF
	}
	if err != nil {
		return nil, err
	}
	payload := make([]byte, 0)

	timestamp := make([]byte, TIMESTAMP_SIZE)
	file.Read(timestamp)
	payload = append(payload, timestamp...)

	tombstone := make([]byte, TOMBSTONE_SIZE)
	file.Read(tombstone)
	payload = append(payload, tombstone...)

	key_size := make([]byte, KEY_SIZE_SIZE)
	file.Read(key_size)
	payload = append(payload, key_size...)

	val_size := make([]byte, VALUE_SIZE_SIZE)
	file.Read(val_size)
	payload = append(payload, val_size...)

	key := make([]byte, binary.LittleEndian.Uint64(key_size))
	file.Read(key)
	payload = append(payload, key...)

	value := make([]byte, binary.LittleEndian.Uint64(val_size))
	file.Read(value)
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
		fmt.Printf("Record - Timestamp: %d, Tombstone: %t, Key: %s, Value: %s\n",
			timestampVal, tombstoneVal, keyVal, valueVal)
	}

	return payload, nil

}

func readAllRecords(filepath string) {
	file, err := os.Open(filepath)
	if err != nil {

	}
	defer file.Close()
	for {
		_, err := ReadRecord(file, true)
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Println("Error reading record:", err)
			break
		}
	}
}
func LoadLogFiles(dirPath string) ([]string, error) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, err
	}

	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".log") {
			files = append(files, filepath.Join(dirPath, entry.Name()))
		}
	}

	return files, nil
}
func main() {
	files, err := LoadLogFiles("./wal_logs")
	if err != nil {
		panic(err)
	}
	if len(files) == 0 {
		fmt.Println("No log files found.")
		return
	}
	last_file := files[len(files)-1]
	fmt.Println("Reading log file:", last_file)
	readAllRecords(last_file)
	file, err := os.Open(last_file)
	if err != nil {
		panic(err)
	}
	defer file.Close()
	writeRecord(file, "exampleKey", "exampleValue", false)

}
func writeRecord(file *os.File, key, value string, tombstone bool) {
	keyBytes := []byte(key)
	valueBytes := []byte(value)

	// build payload
	payload := make([]byte, 0)
	payload = append(payload, binary.LittleEndian.AppendUint64(nil, uint64(time.Now().Unix()))...)
	payload = append(payload, boolToByte(tombstone))
	payload = append(payload, binary.LittleEndian.AppendUint64(nil, uint64(len(keyBytes)))...)
	payload = append(payload, binary.LittleEndian.AppendUint64(nil, uint64(len(valueBytes)))...)
	payload = append(payload, keyBytes...)
	payload = append(payload, valueBytes...)

	crc := CRC32(payload)

	file.Write(binary.LittleEndian.AppendUint32(nil, crc))
	file.Write(payload)

}

func newSegment(file *os.File, payload []byte) {
	panic("unimplemented")
}

func boolToByte(b bool) byte {
	if b {
		return 1
	}
	return 0
}
