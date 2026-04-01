package main

import "strings"

type Flow string

const (
	FlowServerEncode   Flow = "ServerEncode"
	FlowServerDecode   Flow = "ServerDecode"
	FlowClientEncode   Flow = "ClientEncode"
	FlowClientDecode   Flow = "ClientDecode"
	FlowServerToClient Flow = "Server2Client"
	FlowClientToServer Flow = "Client2Server"
	FlowBoth           Flow = "Both"
)

// flowByName maps the lowercase directive string to its Flow constant.
var flowByName = map[string]Flow{
	"serverencode":  FlowServerEncode,
	"serverdecode":  FlowServerDecode,
	"clientencode":  FlowClientEncode,
	"clientdecode":  FlowClientDecode,
	"server2client": FlowServerToClient,
	"client2server": FlowClientToServer,
	"both":          FlowBoth,
}

// ServerEncodes reports whether the server needs to encode this flow.
func (fl Flow) ServerEncodes() bool {
	switch fl {
	case FlowServerEncode, FlowServerToClient, FlowBoth:
		return true
	}
	return false
}

// ServerDecodes reports whether the server needs to decode this flow.
func (fl Flow) ServerDecodes() bool {
	switch fl {
	case FlowServerDecode, FlowClientToServer, FlowBoth:
		return true
	}
	return false
}

// ClientEncodes reports whether the client needs to encode this flow.
func (fl Flow) ClientEncodes() bool {
	switch fl {
	case FlowClientEncode, FlowClientToServer, FlowBoth:
		return true
	}
	return false
}

// ClientDecodes reports whether the client needs to decode this flow.
func (fl Flow) ClientDecodes() bool {
	switch fl {
	case FlowClientDecode, FlowServerToClient, FlowBoth:
		return true
	}
	return false
}

type Field struct {
	Name         string
	Kind         string // Primitive type: uint8, uint16, uint32, uint64, string, bool
	KindStrong   string // Strong type e.g. character.ID (empty if primitive)
	BufferMethod string // Buffer method suffix: Uint8, Uint16, Uint32, Uint64, String, Bool

	IsSlice       bool
	SliceElemKind string
	CountKind     string // Count kind tag for slices: "u8"(default), "u16", "u32"

	IsStruct     bool
	StructName   string
	StructFields []Field
}

type Packet struct {
	Name   string
	Flow   Flow
	Opcode string // e.g. "Lobby"         -> OpcodeLobby
	Action string // e.g. "LobbyCharList" -> LobbyCharList.Action()
	Fields []Field
}

type SharedStruct struct {
	Name   string
	Fields []Field
}

// FuncName strips the "Packet" suffix (e.g. LobbyCharListPacket -> LobbyCharList).
func (p Packet) FuncName() string {
	return strings.TrimSuffix(p.Name, "Packet")
}

// ---

var kindToWriteMethod = map[string]string{
	"uint8":  "WriteUint8",
	"uint16": "WriteUint16",
	"uint32": "WriteUint32",
	"uint64": "WriteUint64",
	"bool":   "WriteBool",
}

var kindToReadMethod = map[string]string{
	"uint8":  "ReadUint8",
	"uint16": "ReadUint16",
	"uint32": "ReadUint32",
	"uint64": "ReadUint64",
	"bool":   "ReadBool",
}

var countKindToWriteMethod = map[string]string{
	"u8": "WriteUint8", "uint8": "WriteUint8",
	"u16": "WriteUint16", "uint16": "WriteUint16",
	"u32": "WriteUint32", "uint32": "WriteUint32",
	"u64": "WriteUint64", "uint64": "WriteUint64",
}

var countKindToReadMethod = map[string]string{
	"u8": "ReadUint8", "uint8": "ReadUint8",
	"u16": "ReadUint16", "uint16": "ReadUint16",
	"u32": "ReadUint32", "uint32": "ReadUint32",
	"u64": "ReadUint64", "uint64": "ReadUint64",
}

var countKindToCastType = map[string]string{
	"u8": "uint8", "uint8": "uint8",
	"u16": "uint16", "uint16": "uint16",
	"u32": "uint32", "uint32": "uint32",
	"u64": "uint64", "uint64": "uint64",
}

func stringWriteMethod(countKind string) string {
	switch countKind {
	case "u8", "uint8":
		return "WriteUint8String"
	case "u16", "uint16":
		return "WriteUint16String"
	default:
		return "WriteUnknownString_" + countKind
	}
}

func stringReadMethod(countKind string) string {
	switch countKind {
	case "u8", "uint8":
		return "ReadUint8String"
	case "u16", "uint16":
		return "ReadUint16String"
	default:
		return "ReadUnknownString_" + countKind
	}
}

func (f Field) WriteMethod() string {
	if f.Kind == "string" {
		return stringWriteMethod(f.CountKind)
	}
	if m, ok := kindToWriteMethod[f.Kind]; ok {
		return m
	}

	return "WriteUnknown_" + f.Kind
}

func (f Field) ReadMethod() string {
	if f.Kind == "string" {
		return stringReadMethod(f.CountKind)
	}
	if m, ok := kindToReadMethod[f.Kind]; ok {
		return m
	}

	return "ReadUnknown_" + f.Kind
}

func (f Field) CountWriteMethod() string {
	if m, ok := countKindToWriteMethod[f.CountKind]; ok {
		return m
	}

	return "CountWriteUnknown_" + f.CountKind
}

func (f Field) CountReadMethod() string {
	if m, ok := countKindToReadMethod[f.CountKind]; ok {
		return m
	}

	return "CountReadUnknown_" + f.CountKind
}

func (f Field) CountCastType() string {
	if t, ok := countKindToCastType[f.CountKind]; ok {
		return t
	}

	return "CountCastUnknown_" + f.CountKind
}

// WriteArg returns the expression passed to the Write method.
// Numeric strong types are cast back to their primitive (e.g. uint8(p.Reason)).
func (f Field) WriteArg(target string) string {
	if f.KindStrong == "" || f.Kind == "bool" {
		return target
	}

	return f.Kind + "(" + target + ")"
}

// ReadAssign returns the assignment expression used inside Decode.
// Strong types are wrapped with their type name (e.g. p.Reason = Reason(v)).
func (f Field) ReadAssign(target string) string {
	if f.KindStrong == "" {
		return target + " = v"
	}

	return target + " = " + f.KindStrong + "(v)"
}

// GetPackageName returns the short package name from a qualified strong type.
// Returns "character" for "character.ID", or "" for primitives and same-package types.
func (f Field) GetPackageName() string {
	if !strings.Contains(f.KindStrong, ".") {
		return ""
	}

	return strings.Split(f.KindStrong, ".")[0]
}
