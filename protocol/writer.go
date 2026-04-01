package protocol

import (
	"encoding/binary"
	"unicode/utf8"

	"github.com/rhydori/logs"
)

type PacketWriter struct {
	buf    []byte
	offset int
}

// NewWriter initialises the 3-byte header and positions the offset after it.
func NewWriter(buf []byte, opcode Opcode, action Action, status Status) PacketWriter {
	buf[0] = opcode.Raw()
	buf[1] = action.Raw()
	buf[2] = status.Raw()

	return PacketWriter{buf: buf, offset: HeaderSize}
}

func (w *PacketWriter) WriteUint8(v uint8) *PacketWriter {
	w.buf[w.offset] = v
	w.offset++

	return w
}

func (w *PacketWriter) WriteUint16(v uint16) *PacketWriter {
	binary.LittleEndian.PutUint16(w.buf[w.offset:], v)
	w.offset += 2

	return w
}

func (w *PacketWriter) WriteUint32(v uint32) *PacketWriter {
	binary.LittleEndian.PutUint32(w.buf[w.offset:], v)
	w.offset += 4

	return w
}

func (w *PacketWriter) WriteUint64(v uint64) *PacketWriter {
	binary.LittleEndian.PutUint64(w.buf[w.offset:], v)
	w.offset += 8

	return w
}

// WriteBool writes 0x00 or 0x01.
func (w *PacketWriter) WriteBool(v bool) *PacketWriter {
	if v {
		w.buf[w.offset] = 1
	} else {
		w.buf[w.offset] = 0
	}
	w.offset++
	return w
}

// WriteUint8String writes a u8-length-prefixed UTF-8 string.
// Maximum string length: 255 bytes. Longer strings are truncated with a warning.
func (w *PacketWriter) WriteUint8String(s string) *PacketWriter {
	l := len(s)
	if l > 255 {
		logs.Warnf("WriteUint8String truncating '%s' (%d bytes) to 255", s, l)
		l = 255
		for l > 0 && !utf8.RuneStart(s[l]) {
			l--
		}
	}

	w.buf[w.offset] = uint8(l)
	w.offset++
	copy(w.buf[w.offset:], s[:l])
	w.offset += l

	return w
}

// WriteUint16String writes a u16-length-prefixed UTF-8 string (max 65535 bytes).
// Use for chat messages, descriptions, etc.
func (w *PacketWriter) WriteUint16String(s string) *PacketWriter {
	binary.LittleEndian.PutUint16(w.buf[w.offset:], uint16(len(s)))
	w.offset += 2
	copy(w.buf[w.offset:], s)
	w.offset += len(s)
	return w
}

// WriteBytes copies raw bytes into the buffer. Use for blobs of known length.
func (w *PacketWriter) WriteBytes(b []byte) *PacketWriter {
	copy(w.buf[w.offset:], b)
	w.offset += len(b)

	return w
}

// Bytes returns the written slice (header + payload). The slice still points
// into the pooled buffer — copy it or send it before calling releaseBuffer.
func (w *PacketWriter) Bytes() []byte { return w.buf[:w.offset] }

// PayloadLen returns the number of bytes written after the header.
func (w *PacketWriter) PayloadLen() int { return w.offset - HeaderSize }
