package main

import (
	"flag"
	"fmt"
	"go/ast"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"
	"unicode"

	"github.com/rhydori/logs"
	"golang.org/x/tools/go/packages"
)

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

type Generated struct {
	Package string
	Imports []string
	Packets []Packet
}

type Field struct {
	Name         string
	Kind         string // Primitive type: uint8, uint16, uint32, uint64, string, bool
	KindStrong   string // Strong type: character.ID, character.Level, class.ID (empty if primitive)
	BufferMethod string // Buffer method suffix: Uint8, Uint16, Uint32, Uint64, String, Bool

	IsSlice       bool
	SliceElemKind string
}

type Packet struct {
	Name   string
	Flow   Flow
	Opcode string // e.g. "Lobby" -> OpcodeLobby
	Action string // e.g. "LobbyCharList" -> LobbyCharList.Action()
	Fields []Field
}

// FuncName strips the "Packet" suffix, if any (e.g. LobbyCharListPacket -> LobbyCharList).
func (p Packet) FuncName() string {
	return strings.TrimSuffix(p.Name, "Packet")
}

//// WriteArg generates the code for the value being sent to the buffer.
//// It automatically applies a cast from the Strong Type to the Primitive Type.
//func (f Field) WriteArg(target string) string {
//	// If no Strong Type is defined, we use the field directly (e.g., p.Age).
//	if f.KindStrong == "" {
//		return target
//	}
//
//	// If a Strong Type exists, we "strip" it back to its primitive Kind.
//	// Example: Kind="uint32", KindStrong="character.ID" -> returns "uint32(p.ID)"
//	// Example: Kind="string", KindStrong="character.Name" -> returns "string(p.Name)"
//	return f.Kind + "(" + target + ")"
//}
//
//// ReadAssign generates the assignment logic for the Decode function.
//// It "wraps" the raw value 'v' from the buffer into your Strong Type.
//func (f Field) ReadAssign(target string) string {
//	// If no Strong Type is defined, we perform a direct assignment (e.g., p.Age = v).
//	if f.KindStrong == "" {
//		return target + " = v"
//	}
//
//	// If a Strong Type exists, we cast the raw value 'v' into it.
//	// Example: KindStrong="character.ID" -> returns "p.ID = character.ID(v)"
//	return target + " = " + f.KindStrong + "(v)"
//}

// WriteMethod returns the PacketWriter method name for this field's primitive kind.
func (f Field) WriteMethod() string {
	m := map[string]string{
		"uint8":  "WriteUint8",
		"uint16": "WriteUint16",
		"uint32": "WriteUint32",
		"uint64": "WriteUint64",
		"string": "WriteUint8String",
		"bool":   "WriteBool",
	}
	if v, ok := m[f.Kind]; ok {
		return v
	}

	return "WriteUnknown_" + f.Kind
}

func (f Field) ReadMethod() string {
	m := map[string]string{
		"uint8":  "ReadUint8",
		"uint16": "ReadUint16",
		"uint32": "ReadUint32",
		"uint64": "ReadUint64",
		"string": "ReadUint8String",
		"bool":   "ReadBool",
	}
	if v, ok := m[f.Kind]; ok {
		return v
	}

	return "ReadUnknown_" + f.Kind
}

// WriteArg produces the expression passed to the Write method.
// For numeric strong types it casts back to the primitive: uint8(p.Reason).
// For strings and primitive-typed fields it passes the value directly.
func (f Field) WriteArg(target string) string {
	if f.KindStrong == "" || f.Kind == "string" || f.Kind == "bool" {
		return target
	}

	return f.Kind + "(" + target + ")"
}

// ReadAssign produces the assignment in Decode.
// For strong types it wraps the raw value: p.Reason = Reason(v).
// For primitives it assigns directly: p.Health = v.
func (f Field) ReadAssign(target string) string {
	if f.KindStrong == "" {
		return target + " = v"
	}

	return target + " = " + f.KindStrong + "(v)"
}

// GetPackageName returns "character" when KindStrong is "character.ID".
// Returns "" for primitives or types in the same package.
func (f Field) GetPackageName() string {
	if !strings.Contains(f.KindStrong, ".") {
		return ""
	}

	// Split "character.ID" and take "character"
	return strings.Split(f.KindStrong, ".")[0]
}

var goTypeToBufferMethod = map[string]string{
	"uint8":  "Uint8",
	"uint16": "Uint16",
	"uint32": "Uint32",
	"uint64": "Uint64",
	"int32":  "Int32",
	"string": "String",
	"bool":   "Bool",
}

func main() {
	inFile := flag.String("in", "../../server/gameserver/internal/protocol/packets.go", "")
	outDir := flag.String("out", "./generated/", "")
	goFile := flag.String("go", "codec.go", "")
	gdFile := flag.String("gd", "codec.gd", "")
	flag.Parse()

	if *inFile == "" || *outDir == "" {
		logs.Warn("Missing required flags.")

		if *inFile == "" {
			logs.Error("Flag '-in' is required. Input file with packet definitions")
			logs.Info("Example: -in ../../gameserver/internal/protocol/packets.go")
		}
		if *outDir == "" {
			logs.Error("Flag '-out' is required. Output path for the generated codec")
			logs.Info("Example: -out ./generated/")
		}

		logs.Fatal("Fatal Error. See above for details.")
	}

	logs.Infof("Processing '%s' -> '%s%s'", *inFile, *outDir, *goFile)
	logs.Infof("Processing '%s' -> '%s%s'", *inFile, *outDir, *gdFile)

	pkg, packets, err := ParsePackets(*inFile)
	if err != nil {
		logs.Fatalf("Failed to parse packets '%v'", err)
	}

	importPaths := CollectImports(pkg, packets)

	if err := GenerateGoFile(*outDir, *goFile, packets, importPaths); err != nil {
		logs.Fatalf("Failed to generate Go file: %v", err)
	}

	if err := GenerateGodotFile(*outDir, *gdFile, packets); err != nil {
		logs.Fatalf("Failed to generate Godot file: %v", err)
	}

	logs.Info("Done! Protocol generated successfully.")
	time.Sleep(1 * time.Millisecond)
}

func ParsePackets(inFile string) (*packages.Package, []Packet, error) {
	pkg, err := LoadPackage(inFile)
	if err != nil {
		return nil, nil, err
	}

	packets := CollectPackets(pkg)

	return pkg, packets, nil
}

func LoadPackage(inFile string) (*packages.Package, error) {
	absIn, err := filepath.Abs(inFile)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(absIn)

	cfg := &packages.Config{
		Mode: packages.NeedName |
			packages.NeedFiles |
			packages.NeedSyntax |
			packages.NeedTypes |
			packages.NeedTypesInfo |
			packages.NeedImports,
		Dir: dir,
	}

	pkgs, err := packages.Load(cfg, ".")
	if err != nil {
		return nil, err
	}
	if packages.PrintErrors(pkgs) > 0 {
		return nil, fmt.Errorf("Packages loaded with errors.")
	}
	if len(pkgs) == 0 {
		return nil, fmt.Errorf("No Packages loaded from '%s'", dir)
	}

	return pkgs[0], nil
}

func CollectPackets(pkg *packages.Package) []Packet {
	var packets []Packet

	for _, file := range pkg.Syntax {
		for _, decl := range file.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok || genDecl.Doc == nil {
				continue
			}

			directive := GetGenProtocolDirective(genDecl.Doc)
			if directive == "" {
				continue
			}

			for _, spec := range genDecl.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				structType, ok := typeSpec.Type.(*ast.StructType)
				if !ok {
					continue
				}

				packet := ParseDirectives(typeSpec.Name.Name, directive)
				packet.Fields = ParseFields(pkg, structType)

				packets = append(packets, packet)
			}
		}
	}

	return packets
}

func CollectImports(pkg *packages.Package, packets []Packet) []string {
	uniquePaths := make(map[string]bool)
	var result []string

	for _, pkt := range packets {
		for _, field := range pkt.Fields {
			shortName := field.GetPackageName()
			if shortName == "" {
				continue
			}
			fullPath := ResolveFullImportPath(pkg, shortName)
			if fullPath != "" && !uniquePaths[fullPath] {
				uniquePaths[fullPath] = true

				result = append(result, fullPath)
			}
		}
	}

	return result
}

func GetGenProtocolDirective(doc *ast.CommentGroup) string {
	for _, comment := range doc.List {
		text := strings.TrimSpace(strings.TrimPrefix(comment.Text, "//"))
		if strings.HasPrefix(text, "GenProtocol:") {
			return strings.TrimPrefix(text, "GenProtocol:")
		}
	}

	return ""
}

func ParseDirectives(name, directive string) Packet {
	pkt := Packet{Name: name}

	parts := strings.Fields(directive)
	if len(parts) == 0 {
		return pkt
	}

	flow := strings.ToLower(parts[0])
	switch flow {
	case "serverencode":
		pkt.Flow = FlowServerEncode
	case "serverdecode":
		pkt.Flow = FlowServerDecode
	case "clientencode":
		pkt.Flow = FlowClientEncode
	case "clientdecode":
		pkt.Flow = FlowClientDecode
	case "server2client":
		pkt.Flow = FlowServerToClient
	case "client2server":
		pkt.Flow = FlowClientToServer
	case "both":
		pkt.Flow = FlowBoth
	}

	for _, part := range parts[1:] {
		keyValue := strings.SplitN(part, ":", 2)
		if len(keyValue) != 2 {
			continue
		}

		key := strings.ToLower(keyValue[0])
		value := keyValue[1]
		switch key {
		case "opcode":
			pkt.Opcode = value // e.g. "Lobby"
		case "action":
			pkt.Action = value // e.g. "LobbyCharList"
		}
	}

	return pkt
}

func ParseFields(pkg *packages.Package, structType *ast.StructType) []Field {
	var fields []Field

	for _, f := range structType.Fields.List {
		// Identify if it's a slice
		_, isSlice := parseType(f.Type)

		// If it's a slice, we need to look at the element INSIDE []Type
		targetExpr := f.Type
		if isSlice {
			if array, ok := f.Type.(*ast.ArrayType); ok {
				targetExpr = array.Elt
			}
		}

		kind, strong, bufferMethod := resolveTypeInfo(pkg, targetExpr)

		for _, nameIdent := range f.Names {
			field := Field{
				Name:         nameIdent.Name,
				Kind:         kind,
				KindStrong:   strong,
				BufferMethod: bufferMethod,
				IsSlice:      isSlice,
			}

			if isSlice {
				field.SliceElemKind = kind
			}

			fields = append(fields, field)
		}
	}

	return fields
}

func parseType(e ast.Expr) (kind string, isSlice bool) {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name, false

	case *ast.ArrayType:
		sliceElemKind, _ := parseType(t.Elt)
		return sliceElemKind, true

	case *ast.SelectorExpr:
		pkgName, _ := parseType(t.X)
		return pkgName + "." + t.Sel.Name, false

	default:
		return "Unknown", false
	}
}

// resolveTypeInfo breaks down the AST expression into our 3 categories.
func resolveTypeInfo(pkg *packages.Package, expr ast.Expr) (kind, strong, bufferMethod string) {
	// Get the actual string representation from AST (e.g., "character.ID" or "uint32")
	astName, _ := parseType(expr)

	// Use TypeInfo to find the underlying primitive type
	// This handles: type ID uint32 -> underlying is uint32
	tv, ok := pkg.TypesInfo.Types[expr]
	if !ok {
		return astName, "", "Unknown"
	}

	underlying := tv.Type.Underlying().String()
	// Map the underlying type to our Raw buffer name
	bufferMethodName, exists := goTypeToBufferMethod[underlying]
	if !exists {
		bufferMethodName = "Unknown"
	}

	// Strong type: e.g. astName="character.Level", underlying="uint8"
	if astName != underlying {
		return underlying, astName, bufferMethodName
	}

	// Primitive type
	return underlying, "", bufferMethodName
}

func ResolveFullImportPath(pkg *packages.Package, shortName string) string {
	for _, file := range pkg.Syntax {
		for _, imp := range file.Imports {
			if imp.Path == nil {
				continue
			}
			fullPath := strings.Trim(imp.Path.Value, `"`)

			// Explicit alias: import foo "pkg/path/bar"
			if imp.Path != nil && imp.Name.Name == shortName {
				return fullPath
			}

			// No alias: match last path segment against shortName
			if imp.Name == nil {
				parts := strings.Split(fullPath, "/")
				if parts[len(parts)-1] == shortName {
					return fullPath

				}
			}
		}
	}

	return ""
}

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

	return tmpl.Execute(f, packets)
}

const goTemplate = `package protocol
{{if .Imports}}
import (
	{{- range .Imports}}
	"{{.}}"
	{{- end}}
)
{{- end}}
{{- range .Packets}}
{{- if or (eq .Flow "ServerEncode") (eq .Flow "Server2Client") (eq .Flow "Both")}}
// Code generated by GenProtocol; DO NOT EDIT.
func Encode{{.FuncName}}(buf []byte, p *{{.Name}}) []byte {
	w := NewWriter(buf, Opcode{{.Opcode}}, {{.FuncName}}.Action(), StatusOK)
	{{- range .Fields}}
	{{- if .IsSlice}}
	w.WriteUint16(uint16(len(p.{{.Name}})))
	for _, v := range p.{{.Name}} {
		w.{{.WriteMethod}}({{.WriteArg "v"}})
	}
	{{- else}}
	w.{{.WriteMethod}}({{.WriteArg (printf "p.%s" .Name)}})
	{{- end}}
	{{- end}}

	return w.Bytes()
}
{{end}}
{{- if or (eq .Flow "ServerDecode") (eq .Flow "Client2Server") (eq .Flow "Both")}}
// Code generated by GenProtocol; DO NOT EDIT.
func Decode{{.FuncName}}(payload []byte, p *{{.Name}}) error {
	r := NewReader(payload)
	{{- range .Fields}}
	{
		{{- if .IsSlice}}
		count, err := r.ReadUint16()
		if err != nil { return err }
		p.{{.Name}} = make([]{{if .KindStrong}}{{.KindStrong}}{{else}}{{.Kind}}{{end}}, count)
		for i := range p.{{.Name}} {
			v, err := r.{{.ReadMethod}}()
			if err != nil { return err }
			{{.ReadAssign (printf "p.%s[i]" .Name)}}
		}
		{{- else}}
		v, err := r.{{.ReadMethod}}()
		if err != nil { return err }
		{{.ReadAssign (printf "p.%s" .Name)}}
		{{- end}}
	}
	{{- end}}

	return nil
}
{{end}}
{{- end}}`

// ─── Godot template ───────────────────────────────────────────────────────────
//
// Design aligned with the existing GameProtocol.gd patterns:
//
//  Flow direction (critical — was inverted before):
//    S2C  → client DECODES  → generates  static func from_payload(...) -> T
//    C2S  → client ENCODES  → generates  func to_packet() -> PackedByteArray
//    Both → generates both
//
//  StreamPeerBuffer usage:
//    decode: create inside from_payload, assign data_array, read with _s.*
//    encode: create inside to_packet, write with _s.*, return _s.data_array
//    big_endian is NOT set — default is LE, matching Go's binary.LittleEndian.
//
//  Strings (u8-length-prefixed UTF-8):
//    decode → _s.get_utf8_string(_s.get_u8())   (clean Godot 4 API)
//    encode → var _b = v.to_utf8_buffer(); _s.put_u8(_b.size()); _s.put_data(_b)
//
//  Action enum reference:
//    "Lobby" + "LobbyCharList" → GameProtocol.LobbyAction.CHAR_LIST
//    resolved at generation time by to_gd_action(opcode, action).
//
//  The generated file preloads GameProtocol so enums don't need to be duplicated.

const gdTemplate = `class_name Codec extends GameProtocol

# Code generated by GenProtocol; DO NOT EDIT.
class Packet:
	var opcode:int
	var action:int
	var status:int
	var payload:PackedByteArray

	static func encode(opcode_:Opcode, action_:int) -> StreamPeerBuffer:
		var stream := StreamPeerBuffer.new()
		stream.put_u8(opcode_)
		stream.put_u8(action_)
		stream.put_u8(Status.REQUEST)

		return stream

	static func decode(packet:PackedByteArray) -> Packet:
		if packet.size() < HEADER_SIZE:
			return null
	
		var new_packet := Packet.new()
		new_packet.opcode = packet[0]
		new_packet.action = packet[1]
		new_packet.status = packet[2]
		new_packet.payload = packet.slice(HEADER_SIZE, packet.size())

		return new_packet
{{range .}}
{{- if or (eq .Flow "ClientEncode") (eq .Flow "ClientDecode") (eq .Flow "Client2Server") (eq .Flow "Server2Client") (eq .Flow "Both")}}
# {{.Name}} — Flow: {{.Flow}}
# Code generated by GenProtocol; DO NOT EDIT.
class {{.Name}}:

	{{- range .Fields}}
	static var {{.Name | to_snake}}:{{.Kind | to_gd_type}}{{if .IsSlice}} = []{{end}}
	{{- end}}

	{{- if or (eq .Flow "ClientEncode") (eq .Flow "Client2Server") (eq .Flow "Both")}}
	static func encode(stream:StreamPeerBuffer) -> PackedByteArray:
		{{- range .Fields}}
		{{- if .IsSlice}}
		stream.put_u16({{.Name | to_snake}}.size())
		for _v in {{.Name | to_snake}}:
			{{.Kind | to_gd_put "_v"}}
		{{- else if eq .Kind "string"}}
		var {{.Name | to_snake}} = stream.to_utf8_buffer()
		stream.put_u8({{.Name | to_snake}}.size())
		stream.put_data({{.Name | to_snake}})
		{{- else}}
		{{.Kind | to_gd_put (.Name | to_snake)}}
		{{- end}}
		{{- end}}
		return stream.data_array
		{{- end}}

	{{- if or (eq .Flow "ClientDecode") (eq .Flow "Server2Client") (eq .Flow "Both")}}
	static func decode(payload:PackedByteArray) -> {{.Name}}:
		if payload.is_empty():
			push_error("{{.Name}}.decode: empty payload")
			return null
		var p := {{.Name}}.new()
		var stream := StreamPeerBuffer.new()
		stream.data_array = payload

		{{- range .Fields}}
		{{- if .IsSlice}}
		var _cnt_{{.Name | to_snake}} := stream.get_u16()
		p.{{.Name | to_snake}}.resize(_cnt_{{.Name | to_snake}})
		for _i in _cnt_{{.Name | to_snake}}:
			p.{{.Name | to_snake}}[_i] = {{.Kind | to_gd_get}}
		{{- else if eq .Kind "string"}}
		p.{{.Name | to_snake}} = stream.get_utf8_string(stream.get_u8())
		{{- else}}
		p.{{.Name | to_snake}} = {{.Kind | to_gd_get}}
		{{- end}}
		{{- end}}
		return p
	{{- end}}
{{- end}}
{{end}}`

// camelToUpperSnake converts "CharList" → "CHAR_LIST".
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

var funcMap = template.FuncMap{
	// camelCase → snake_case  (ClassID → class_id)
	"to_snake": func(s string) string {
		var b strings.Builder
		for i, r := range s {
			if i > 0 && unicode.IsUpper(r) {
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToLower(r))
		}
		return b.String()
	},

	// "Lobby" → "LOBBY"  (used for Opcode enum key)
	"to_upper": strings.ToUpper,

	// Go primitive kind → GDScript static type annotation.
	"to_gd_type": func(kind string) string {
		switch kind {
		case "string":
			return "String"
		case "bool":
			return "bool"
		default:
			return "int"
		}
	},

	// to_gd_action("Lobby", "LobbyCharList") → "GameProtocol.LobbyAction.CHAR_LIST"
	// to_gd_action("Auth",  "AuthLogin")      → "GameProtocol.AuthAction.LOGIN"
	//
	// Algorithm:
	//   1. Strip the opcode prefix from the action name.
	//   2. Convert remaining CamelCase to UPPER_SNAKE.
	//   3. Emit "GameProtocol.<Opcode>Action.<UPPER_SNAKE>".
	"to_gd_action": func(opcode, action string) string {
		actionSuffix := strings.TrimPrefix(action, opcode) // "LobbyCharList" → "CharList"
		return "GameProtocol." + opcode + "Action." + camelToUpperSnake(actionSuffix)
	},

	// to_gd_get returns the StreamPeerBuffer read expression for a primitive kind.
	// The stream variable is always named "_s" inside generated methods.
	// String is handled inline in the template (get_utf8_string), not here.
	"to_gd_get": func(kind string) string {
		switch kind {
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
		default:
			return "# BUG: Unknown Kind " + kind
		}
	},

	// to_gd_put(varName, kind) returns the StreamPeerBuffer write statement.
	// In Go templates: {{.Kind | to_gd_put "myVar"}} calls to_gd_put("myVar", .Kind).
	// String is handled inline in the template, not here.
	"to_gd_put": func(varName, kind string) string {
		switch kind {
		case "uint8":
			return "stream.put_u8(" + varName + ")"
		case "uint16":
			return "stream.put_u16(" + varName + ")"
		case "uint32":
			return "stream.put_u32(" + varName + ")"
		case "uint64":
			return "stream.put_u64(" + varName + ")"
		case "bool":
			return "stream.put_u8(1 if " + varName + " else 0)"
		default:
			return "# BUG: Unknown Kind " + kind
		}
	},
}
