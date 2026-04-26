package protocol

const HeaderSize = 3

type PacketID uint16
type Kind uint8

const (
	AuthLogin PacketID = 0x0101

	SystemNotice PacketID = 0x0203
)

const (
	Request Kind = iota
	OK
	Error
)

func (v PacketID) Raw() uint16 { return uint16(v) }
func (v Kind) Raw() uint8      { return uint8(v) }
