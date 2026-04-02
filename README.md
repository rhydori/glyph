# Glyph

A custom binary serialization protocol that reads annotated Go structs and outputs ready-to-use encode/decode code for both the Go Server and the Godot Client (GDScript).

## How it works

You annotate your Packet structs with a `%Glyph` directive. Glyph parses those annotations, and generates two files:

- A codec.go for the Go Server
- A codec.gd for the Godot Client

## Defining packets

### 1. Shared data types

Define reusable structs normally — no annotation needed:

```go
// data.go

type CharacterData struct {
    Name       character.Name
    Class      class.ID
    Level      character.Level
    Experience character.Experience
}
```

### 2. Annotate packets

Use the `Glyph` directive on Packet structs and declare their Flow, Opcode, and Action:

```go
// packets.go

// %Glyph: Flow:Server2Client -- Opcode:Lobby -- Action:CharList
type LobbyCharListPacket struct {
    Characters []CharacterData
}

// %Glyph -> Flow:Client2Server -- Opcode:Lobby -- Action:EnterWorld
type LobbyEnterWorldPacket struct {
    CharName   character.Name
    ClassID    class.ID
    Level      character.Level
    Experience character.Experience
}
```

### Directive fields

| Field    | Description                              | Example          |
|----------|------------------------------------------|------------------|
| `Flow`   | Direction of the packet (see below)      | `Server2Client`  |
| `Opcode` | Opcode Name                              | `Lobby`          |
| `Action` | Opcode Name Prefix + Action Name		  | `LobbyCharList`  |

### Available flows

| Flow            | Server encodes | Server decodes | Client encodes | Client decodes |
|-----------------|:-:|:-:|:-:|:-:|
| `Server2Client` | ✓ | | | ✓ |
| `Client2Server` | | ✓ | ✓ | |
| `ServerEncode`  | ✓ | | | |
| `ServerDecode`  | | ✓ | | |
| `ClientEncode`  | | | ✓ | |
| `ClientDecode`  | | | | ✓ |
| `Both`          | ✓ | ✓ | ✓ | ✓ |

### Supported field types

| Go type  | Notes                                    |
|----------|------------------------------------------|
| `uint8`  |                                          |
| `uint16` |                                          |
| `uint32` |                                          |
| `uint64` |                                          |
| `string` | Length-prefixed. Default prefix: `u8`    |
| `bool`   |                                          |
| Slices   | Any of the above, or a struct            |
| Structs  | Cross-package types are resolved         |

#### String length prefix

Use the `count` struct tag to change the length prefix of a string field:

```go
type ExamplePacket struct {
    ShortName string              // u8 prefix (default)
    LongDesc  string `count:"u16"` // u16 prefix
}
```

#### Slice count prefix

The same `count` tag controls the element count prefix for slices:

```go
type ExamplePacket struct {
    Items []uint32 `count:"u16"` // u16 element count
}
```

## Running the generator

```bash
go run . -in ./path/to/packets.go -out ./generated
```

This produces:

```
generated/
├── codec.go    # Go server codec
└── codec.gd    # GDScript Godot codec
```

## Generated output example

```go
func EncodeLobbyCharList(buf []byte, p *LobbyCharListPacket) []byte { ... }

func EncodeLobbyCharList(payload []byte, p *LobbyCharListPacket) error { ... }
```

```gdscript
class LobbyCharCreatePacket:
	static func encode(char_name:String)-> PackedByteArray:
		var stream := Packet.encode(Opcode.LOBBY, LobbyAction.CHAR_CREATE)
		var buffer_char_name := char_name.to_utf8_buffer()
		stream.put_u8(buffer_char_name.size())
		stream.put_data(buffer_char_name)
		return stream.data_array

class SystemNoticePacket:
	var message:String
	static func decode(payload:PackedByteArray) -> SystemNoticePacket:
		if payload.is_empty():
			push_error("SystemNoticePacket.decode: empty payload")
			return null

		var packet := SystemNoticePacket.new()
		var stream := Helper._make_stream(payload)
		packet.message = stream.get_utf8_string(stream.get_u8())
		return packet
```
