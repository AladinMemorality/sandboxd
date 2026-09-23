package runtime

import (
	"encoding/binary"
	"errors"
	"io"
)

// ValidatePrivateArchiveIndex bounds ZIP metadata before archive/zip allocates
// its file table. A multi-gigabyte private transfer must not permit a similarly
// sized central directory or an unbounded declared entry count. Accept only
// standalone single-volume ZIP/ZIP64 archives, as our writers produce.
func ValidatePrivateArchiveIndex(reader io.ReaderAt, size int64, maxEntries int) error {
	invalid := errors.New("invalid or oversized private ZIP index")
	if size < 22 {
		return invalid
	}
	n := int64(65557)
	if size < n {
		n = size
	}
	tail := make([]byte, int(n))
	if _, err := reader.ReadAt(tail, size-n); err != nil {
		return err
	}
	end := -1
	for i := len(tail) - 22; i >= 0; i-- {
		if binary.LittleEndian.Uint32(tail[i:]) == 0x06054b50 && i+22+int(binary.LittleEndian.Uint16(tail[i+20:])) == len(tail) {
			end = i
			break
		}
	}
	if end < 0 {
		return invalid
	}
	e := tail[end:]
	boundary := size - n + int64(end)
	if binary.LittleEndian.Uint16(e[4:]) != 0 || binary.LittleEndian.Uint16(e[6:]) != 0 {
		return invalid
	}
	count := uint64(binary.LittleEndian.Uint16(e[10:]))
	directorySize := uint64(binary.LittleEndian.Uint32(e[12:]))
	offset := uint64(binary.LittleEndian.Uint32(e[16:]))
	if binary.LittleEndian.Uint16(e[8:]) != binary.LittleEndian.Uint16(e[10:]) {
		return invalid
	}
	if count == 0xffff || directorySize == 0xffffffff || offset == 0xffffffff {
		if boundary < 20 {
			return invalid
		}
		loc := make([]byte, 20)
		if _, err := reader.ReadAt(loc, boundary-20); err != nil {
			return err
		}
		if binary.LittleEndian.Uint32(loc) != 0x07064b50 || binary.LittleEndian.Uint32(loc[4:]) != 0 || binary.LittleEndian.Uint32(loc[16:]) != 1 {
			return invalid
		}
		zip64Offset := binary.LittleEndian.Uint64(loc[8:])
		if zip64Offset > uint64(boundary-20) || uint64(boundary-20)-zip64Offset < 56 {
			return invalid
		}
		record := make([]byte, 56)
		if _, err := reader.ReadAt(record, int64(zip64Offset)); err != nil {
			return err
		}
		length := binary.LittleEndian.Uint64(record[4:])
		if binary.LittleEndian.Uint32(record) != 0x06064b50 || length < 44 || length > 1<<20 || zip64Offset+12+length != uint64(boundary-20) || binary.LittleEndian.Uint32(record[16:]) != 0 || binary.LittleEndian.Uint32(record[20:]) != 0 {
			return invalid
		}
		count = binary.LittleEndian.Uint64(record[32:])
		if binary.LittleEndian.Uint64(record[24:]) != count {
			return invalid
		}
		directorySize = binary.LittleEndian.Uint64(record[40:])
		offset = binary.LittleEndian.Uint64(record[48:])
		boundary = int64(zip64Offset)
	}
	if count > uint64(maxEntries) || directorySize > 64<<20 || offset > uint64(boundary) || directorySize != uint64(boundary)-offset {
		return invalid
	}
	section := io.NewSectionReader(reader, int64(offset), int64(directorySize))
	var consumed uint64
	for i := uint64(0); i < count; i++ {
		var header [46]byte
		if _, err := io.ReadFull(section, header[:]); err != nil {
			return invalid
		}
		if binary.LittleEndian.Uint32(header[:]) != 0x02014b50 {
			return invalid
		}
		name := uint64(binary.LittleEndian.Uint16(header[28:]))
		extra := uint64(binary.LittleEndian.Uint16(header[30:]))
		comment := uint64(binary.LittleEndian.Uint16(header[32:]))
		length := uint64(46) + name + extra + comment
		if name == 0 || name > 4096 || consumed > directorySize || length > directorySize-consumed {
			return invalid
		}
		if _, err := section.Seek(int64(name+extra+comment), io.SeekCurrent); err != nil {
			return invalid
		}
		consumed += length
	}
	if consumed != directorySize {
		return invalid
	}
	return nil
}
