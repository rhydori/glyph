package main

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
	"unicode"
)

//go:embed templates/codec.go.tmpl
var goTemplate string

//go:embed templates/codec.gd.tmpl
var gdTemplate string

func GenerateGoFile(outDir, fileName string, packets []Packet, imports []string) error {
	tmpl, err := template.New("go").Funcs(funcMap).Parse(goTemplate)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(outDir, fileName))
	if err != nil {
		return err
	}
	defer f.Close()
	return tmpl.Execute(f, struct {
		Imports []string
		Packets []Packet
	}{imports, packets})
}

func GenerateGodotFile(outDir, fileName string, packets []Packet) error {
	tmpl, err := template.New("gd").Funcs(funcMap).Parse(gdTemplate)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	f, err := os.Create(filepath.Join(outDir, fileName))
	if err != nil {
		return err
	}
	defer f.Close()

	return tmpl.Execute(f, struct {
		Packets       []Packet
		SharedStructs []SharedStruct
	}{packets, CollectSharedStructs(packets)})
}

var (
	matchFirstCap = regexp.MustCompile(`(.)([A-Z][a-z]+)`)
	matchAllCap   = regexp.MustCompile(`([a-z0-9])([A-Z])`)
)

// toSnake converts a CamelCase name to snake_case.
func toSnake(s string) string {
	s = matchFirstCap.ReplaceAllString(s, `${1}_${2}`)
	s = matchAllCap.ReplaceAllString(s, `${1}_${2}`)
	return strings.ToLower(s)
}

// Converts CamelCase to UPPER_SNAKE
func camelToUpperSnake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && unicode.IsUpper(r) {
			b.WriteByte('_')
		}
		b.WriteRune(unicode.ToUpper(r))
	}

	return b.String()
}

// gdType maps a Go primitive kind to its GDScript static type annotation.
func gdType(kind string) string {
	switch kind {
	case "uint8", "uint16", "uint32", "uint64":
		return "int"
	case "string":
		return "String"
	case "bool":
		return "bool"
	default:
		return "# BUG: Unknown Kind " + kind
	}
}

// gdCountGet returns the StreamPeerBuffer getter call for a given count kind.
func gdCountGet(kind string) string {
	switch kind {
	case "u8", "uint8":
		return "get_u8()"
	case "u16", "uint16":
		return "get_u16()"
	case "u32", "uint32":
		return "get_u32()"
	case "u64", "uint64":
		return "get_u64()"
	default:
		return "get_u8()"
	}
}

// gdCountPut returns the StreamPeerBuffer putter method name for a given count kind.
func gdCountPut(kind string) string {
	switch kind {
	case "u8", "uint8":
		return "put_u8"
	case "u16", "uint16":
		return "put_u16"
	case "u32", "uint32":
		return "put_u32"
	case "u64", "uint64":
		return "put_u64"
	default:
		return "put_u8"
	}
}

// toGdPacketID converts a packet ID into a GDScript PacketID enum reference.
// e.g. "LobbyCharList" -> "PacketID.LOBBY_CHAR_LIST"
func toGdPacketID(id string) string {
	return "PacketID." + camelToUpperSnake(id)
}

// toGdRead returns the GDScript expression to read a field from a StreamPeerBuffer.
func toGdRead(f Field) string {
	switch f.Kind {
	case "uint8":
		return "stream.get_u8()"
	case "uint16":
		return "stream.get_u16()"
	case "uint32":
		return "stream.get_u32()"
	case "uint64":
		return "stream.get_u64()"
	case "bool":
		return "stream.get_u8() != 0"
	case "string":
		if f.CountKind == "" {
			return "stream.get_utf8_string(stream.get_available_bytes())"
		}
		return "stream.get_utf8_string(stream." + gdCountGet(f.CountKind) + ")"
	default:
		return "# BUG: Unknown Kind " + f.Kind
	}
}

// toGdWrite returns the GDScript statement(s) to write a field into a StreamPeerBuffer.
func toGdWrite(f Field, varName string) string {
	switch f.Kind {
	case "string":
		b := "buffer_" + varName
		if f.CountKind == "" {
			return fmt.Sprintf(
				"var %s := %s.to_utf8_buffer()\n\t\tstream.put_data(%s)",
				b, varName, b,
			)

		}
		return fmt.Sprintf(
			"var %s := %s.to_utf8_buffer()\n\t\tstream.%s(%s.size())\n\t\tstream.put_data(%s)",
			b, varName, gdCountPut(f.CountKind), b, b,
		)
	case "bool":
		return fmt.Sprintf("stream.put_u8(1 if %s else 0)", varName)
	default:
		put := map[string]string{
			"uint8": "put_u8", "uint16": "put_u16",
			"uint32": "put_u32", "uint64": "put_u64",
		}
		if m, ok := put[f.Kind]; ok {
			return fmt.Sprintf("stream.%s(%s)", m, varName)
		}
		return "# BUG: Unknown Kind " + f.Kind
	}
}

// toGdParams returns the full GDScript parameter list for an encode function.
// including only fields that are used in encoding (shared and client-only fields).
func toGdParams(fields []Field) string {
	var parts []string
	for _, f := range fields {
		if f.UsedInClientEncode() {
			parts = append(parts, gdParamName(f.Name)+":"+gdType(f.Kind))
		}
	}

	return strings.Join(parts, ", ")
}

func gdParamName(name string) string {
	return toSnake(name) + "_arg"
}

func filterServerEncodeFields(fields []Field) []Field {
	var result []Field
	for _, f := range fields {
		if f.UsedInServerEncode() {
			result = append(result, f)
		}
	}
	return result
}

func filterServerDecodeFields(fields []Field) []Field {
	var result []Field
	for _, f := range fields {
		if f.UsedInServerDecode() {
			result = append(result, f)
		}
	}
	return result
}

func filterClientEncodeFields(fields []Field) []Field {
	var result []Field
	for _, f := range fields {
		if f.UsedInClientEncode() {
			result = append(result, f)
		}
	}
	return result
}

func filterClientDecodeFields(fields []Field) []Field {
	var result []Field
	for _, f := range fields {
		if f.UsedInClientDecode() {
			result = append(result, f)
		}
	}
	return result
}

var funcMap = template.FuncMap{
	"to_snake":                    toSnake,
	"to_upper":                    strings.ToUpper,
	"to_gd_type":                  gdType,
	"to_gd_packet_id":             toGdPacketID,
	"to_gd_read":                  toGdRead,
	"to_gd_write":                 toGdWrite,
	"to_gd_count_get":             gdCountGet,
	"to_gd_count_put":             gdCountPut,
	"to_gd_params":                toGdParams,
	"to_gd_param_name":            gdParamName,
	"filter_server_encode_fields": filterServerEncodeFields,
	"filter_server_decode_fields": filterServerDecodeFields,
	"filter_client_encode_fields": filterClientEncodeFields,
	"filter_client_decode_fields": filterClientDecodeFields,
}
