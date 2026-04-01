package protocol

const HeaderSize = 3

type Opcode uint8
type Action uint8
type Status uint8

func (v Opcode) Raw() uint8 { return uint8(v) }
func (v Action) Raw() uint8 { return uint8(v) }
func (v Status) Raw() uint8 { return uint8(v) }
