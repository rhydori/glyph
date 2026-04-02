package protocol

import (
	"encoding/binary"
	"io"
)

type PacketReader struct {
	buf    []byte
	offset int
}

// NewReader wraps payload (the bytes after the header — do NOT include the header).
func NewReader(payload []byte) PacketReader {
	return PacketReader{buf: payload}
}

func (r *PacketReader) ReadUint8() (uint8, error) {
	if r.offset >= len(r.buf) {
		return 0, io.ErrUnexpectedEOF
	}
	v := r.buf[r.offset]
	r.offset++

	return v, nil
}

func (r *PacketReader) ReadUint16() (uint16, error) {
	if r.offset+2 > len(r.buf) {
		return 0, io.ErrUnexpectedEOF
	}
	v := binary.LittleEndian.Uint16(r.buf[r.offset:])
	r.offset += 2

	return v, nil
}

func (r *PacketReader) ReadUint32() (uint32, error) {
	if r.offset+4 > len(r.buf) {
		return 0, io.ErrUnexpectedEOF
	}
	v := binary.LittleEndian.Uint32(r.buf[r.offset:])
	r.offset += 4

	return v, nil
}

func (r *PacketReader) ReadUint64() (uint64, error) {
	if r.offset+8 > len(r.buf) {
		return 0, io.ErrUnexpectedEOF
	}
	v := binary.LittleEndian.Uint64(r.buf[r.offset:])
	r.offset += 8

	return v, nil
}

// ReadBool reads a single byte and returns true if non-zero.
func (r *PacketReader) ReadBool() (bool, error) {
	v, err := r.ReadUint8()
	return v != 0, err
}

// ReadRawString reads all remaining bytes as a UTF-8 string.
// Use only when the string is the last field in the packet.
func (r *PacketReader) ReadRawString() (string, error) {
	if r.offset >= len(r.buf) {
		return "", io.ErrUnexpectedEOF
	}
	s := string(r.buf[r.offset:])
	r.offset = len(r.buf)
	return s, nil
}

// ReadString reads a u8-length-prefixed UTF-8 string.
// The returned string is a fresh allocation — safe to keep after the buffer
// is returned to sync.Pool.
func (r *PacketReader) ReadUint8String() (string, error) {
	l, err := r.ReadUint8()
	if err != nil {
		return "", err
	}

	if r.offset+int(l) > len(r.buf) {
		return "", io.ErrUnexpectedEOF
	}
	s := string(r.buf[r.offset : r.offset+int(l)])
	r.offset += int(l)

	return s, nil
}

// ReadUint16String reads a u16-length-prefixed UTF-8 string.
func (r *PacketReader) ReadUint16String() (string, error) {
	l, err := r.ReadUint16()
	if err != nil {
		return "", err
	}

	if r.offset+int(l) > len(r.buf) {
		return "", io.ErrUnexpectedEOF
	}
	s := string(r.buf[r.offset : r.offset+int(l)])
	r.offset += int(l)

	return s, nil
}

// Remaining returns how many bytes are left unread.
func (r *PacketReader) Remaining() int { return len(r.buf) - r.offset }
