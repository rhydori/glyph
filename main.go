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

	IsStruct     bool
	StructFields []Field
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
	if f.KindStrong == "" || f.Kind == "bool" {
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
	inDir := flag.String("in", "../../server/gameserver/internal/protocol/", "")
	inFile := flag.String("file", "packets.go", "")
	goOutDir := flag.String("goout", "../../server/gameserver/internal/protocol/", "")
	gdOutDir := flag.String("gdout", "../../client/scripts/backend/game/protocol/", "")
	goFile := flag.String("go", "codec.go", "")
	gdFile := flag.String("gd", "codec.gd", "")
	flag.Parse()

	if *inDir == "" || *inFile == "" || *goOutDir == "" || *gdOutDir == "" {
		logs.Warn("Missing required flags.")
		if *inDir == "" {
			logs.Error("Flag '-in' is required. Input directory for packet definitions")
			logs.Info("Example: -in ../../server/gameserver/internal/protocol/")
		}
		if *inFile == "" {
			logs.Error("Flag '-file' is required. Input file with packet definitions")
			logs.Info("Example: -file packets.go")
		}
		if *goOutDir == "" {
			logs.Error("Flag '-goout' is required. Output directory for the Go codec")
			logs.Info("Example: -goout ./generated/")
		}
		if *gdOutDir == "" {
			logs.Error("Flag '-gdout' is required. Output directory for the Godot codec")
			logs.Info("Example: -gdout ./generated/")
		}
		logs.Fatal("Fatal Error. See above for details.")
	}

	inFilePath := filepath.Join(*inDir, *inFile)
	goOutFilePath := filepath.Join(*goOutDir, *goFile)
	gdOutFilePath := filepath.Join(*gdOutDir, *gdFile)

	logs.Infof("Processing '%s' -> '%s'", inFilePath, goOutFilePath)
	logs.Infof("Processing '%s' -> '%s'", inFilePath, gdOutFilePath)

	// Remove the previously generated codec so stale code doesn't break package loading.
	os.Remove(goOutFilePath)

	rootPkg, _, packets, err := ParsePackets(inFilePath)
	if err != nil {
		logs.Fatalf("Failed to parse packets '%v'", err)
	}

	importPaths := CollectImports(rootPkg, packets)

	if err := GenerateGoFile(*goOutDir, *goFile, packets, importPaths); err != nil {
		logs.Fatalf("Failed to generate Go file: %v", err)
	}

	if err := GenerateGodotFile(*gdOutDir, *gdFile, packets); err != nil {
		logs.Fatalf("Failed to generate Godot file: %v", err)
	}

	logs.Info("Done! Protocol generated successfully.")
	time.Sleep(1 * time.Millisecond)
}

// ParsePackets loads all packages reachable from inFile and returns
// the root package, the full package map, and the parsed packet list.
func ParsePackets(inFile string) (*packages.Package, map[string]*packages.Package, []Packet, error) {
	rootPkg, pkgMap, err := LoadPackage(inFile)
	if err != nil {
		return nil, nil, nil, err
	}
	packets := CollectPackets(rootPkg, pkgMap)

	return rootPkg, pkgMap, packets, nil
}

// LoadPackages loads the package containing inFile together with all of its
// transitive dependencies (NeedDeps). It returns a flat map keyed by import
// path and the root package (the one whose directory matches inFile).
func LoadPackage(inFile string) (*packages.Package, map[string]*packages.Package, error) { // CHECK THISSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSS
	absIn, err := filepath.Abs(inFile)
	if err != nil {
		return nil, nil, err
	}
	dir := filepath.Dir(absIn)

	cfg := &packages.Config{
		Mode: packages.NeedName |
			packages.NeedFiles |
			packages.NeedSyntax |
			packages.NeedTypes |
			packages.NeedTypesInfo |
			packages.NeedImports |
			packages.NeedDeps,
		Dir: dir,
	}

	pkgs, err := packages.Load(cfg, ".")
	if err != nil {
		return nil, nil, err
	}
	if packages.PrintErrors(pkgs) > 0 {
		return nil, nil, fmt.Errorf("Packages loaded with errors.")
	}
	if len(pkgs) == 0 {
		return nil, nil, fmt.Errorf("No Packages loaded from '%s'", dir)
	}

	pkgMap := make(map[string]*packages.Package)
	var walk func(*packages.Package)
	walk = func(p *packages.Package) {
		if _, exists := pkgMap[p.PkgPath]; exists {
			return
		}
		pkgMap[p.PkgPath] = p
		for _, imp := range p.Imports {
			walk(imp)
		}
	}
	for _, pkg := range pkgs {
		walk(pkg)
	}

	var root *packages.Package
	for _, pkg := range pkgs {
		if len(pkg.GoFiles) > 0 && filepath.Dir(pkg.GoFiles[0]) == dir {
			root = pkg
			break
		}
	}
	if root == nil {
		root = pkgs[0]
	}

	return root, pkgMap, nil
}

func CollectPackets(pkg *packages.Package, pkgMap map[string]*packages.Package) []Packet {
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
				packet.Fields = ParseFields(pkg, pkgMap, structType)

				packets = append(packets, packet)
			}
		}
	}

	return packets
}

// CollectImports walks all packet fields (recursing into StructFields) and
// resolves the full import path for every referenced external package.
func CollectImports(pkg *packages.Package, packets []Packet) []string {
	uniquePaths := make(map[string]bool)
	var result []string

	needsDeeps := func(flow Flow) bool {
		return flow == FlowServerDecode ||
			flow == FlowClientDecode ||
			flow == FlowClientToServer ||
			flow == FlowBoth
	}

	var collectField func(Field, Flow)
	collectField = func(field Field, flow Flow) {
		// Struct types only appear by name in make([]T, n) inside decode.
		// Skip adding their package import for encode-only flows.
		if field.IsStruct && !needsDeeps(flow) {
			for _, sf := range field.StructFields {
				collectField(sf, flow)
			}
			return
		}

		shortName := field.GetPackageName()
		if shortName != "" {
			fullPath := ResolveFullImportPath(pkg, shortName)

			if fullPath != "" && !uniquePaths[fullPath] {
				uniquePaths[fullPath] = true

				result = append(result, fullPath)
			}
		}

		for _, sf := range field.StructFields {
			collectField(sf, flow)
		}
	}

	for _, pkt := range packets {
		for _, field := range pkt.Fields {
			collectField(field, pkt.Flow)
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

// ParseFields parses a struct's field list.  pkgMap is needed so that fields
// whose type is itself a struct (e.g. []character.Character) can be resolved
// recursively via FindStructFields.
func ParseFields(pkg *packages.Package, pkgMap map[string]*packages.Package, structType *ast.StructType) []Field {
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

		kind, strong, isStruct := resolveTypeInfo(pkg, targetExpr)

		var structFields []Field
		if isStruct {
			typeStr := strong
			if typeStr == "" {
				typeStr = kind
			}
			structFields = FindStructFields(pkgMap, typeStr)
		}

		for _, nameIdent := range f.Names {
			field := Field{
				Name:         nameIdent.Name,
				Kind:         kind,
				KindStrong:   strong,
				IsSlice:      isSlice,
				IsStruct:     isStruct,
				StructFields: structFields,
			}
			if isSlice {
				field.SliceElemKind = kind
			}

			fields = append(fields, field)
		}
	}

	return fields
}

// FindStructFields looks up a named struct type (e.g. "character.Character")
// across all loaded packages and returns its parsed fields.
func FindStructFields(pkgMap map[string]*packages.Package, strongType string) []Field {
	parts := strings.SplitN(strongType, ".", 2)
	if len(parts) != 2 {
		return nil
	}
	pkgShort, typeName := parts[0], parts[1]

	for _, p := range pkgMap {
		if p.Name != pkgShort {
			continue
		}
		for _, file := range p.Syntax {
			for _, decl := range file.Decls {
				genDecl, ok := decl.(*ast.GenDecl)
				if !ok {
					continue
				}
				for _, spec := range genDecl.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok || ts.Name.Name != typeName {
						continue
					}
					st, ok := ts.Type.(*ast.StructType)
					if !ok {
						continue
					}
					return ParseFields(p, pkgMap, st)
				}
			}
		}
	}

	logs.Warnf("FindStructFields: type %q not found in loaded packages", strongType)
	return nil
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

// resolveTypeInfo breaks an AST expression into (kind, strong, isStruct).
//
//   - Primitive field  uint16          → ("uint16",  "",                false)
//   - Strong-typed     character.Level → ("uint8",   "character.Level", false)
//   - Struct           character.Character → ("struct", "character.Character", true)
func resolveTypeInfo(pkg *packages.Package, expr ast.Expr) (kind, strong string, isStruct bool) {
	// Get the actual string representation from AST (e.g., "character.ID" or "uint32")
	astName, _ := parseType(expr)

	// Use TypeInfo to find the underlying primitive type
	// This handles: type ID uint32 -> underlying is uint32
	tv, ok := pkg.TypesInfo.Types[expr]
	if !ok {
		return astName, "", false
	}

	underlying := tv.Type.Underlying().String()

	// Struct types — caller resolves fields via FindStructFields.
	if strings.HasPrefix(underlying, "struct{") {
		return "struct", astName, true
	}

	if _, exists := goTypeToBufferMethod[underlying]; !exists {
		logs.Warnf("Unsupported Underlying Type '%q' for '%q' field will be skipped", underlying, astName)
		return underlying, astName, false
	}

	// Strong type: e.g. astName="character.Level", underlying="uint8"
	if astName != underlying {
		return underlying, astName, false
	}

	// Primitive type
	return underlying, "", false
}

// ResolveFullImportPath finds the full import path for a short package name
// by scanning the import declarations in every syntax file of pkg.
func ResolveFullImportPath(pkg *packages.Package, shortName string) string {
	for _, file := range pkg.Syntax {
		for _, imp := range file.Imports {
			if imp == nil || imp.Path == nil {
				continue
			}
			fullPath := strings.Trim(imp.Path.Value, `"`)

			// Explicit alias: import foo "pkg/path/bar"
			if imp.Name != nil && imp.Name.Name != "" && imp.Name.Name != "_" && imp.Name.Name != "." {
				if imp.Name.Name == shortName {
					return fullPath
				}
				continue
			}

			// No alias: match last path segment against shortName
			parts := strings.Split(fullPath, "/")
			if len(parts) > 0 && parts[len(parts)-1] == shortName {
				return fullPath
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

// Struct fields are inlined field-by-field using a template variable ($f) to
// carry the outer field name into the inner range over StructFields.
//
// Encode slice-of-struct example (Characters []character.Character):
//
//	w.WriteUint16(uint16(len(p.Characters)))
//	for _, v := range p.Characters {
//	    w.WriteUint8String(v.Name)
//	    w.WriteUint32(uint32(v.ID))
//	    ...
//	}
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
	{{- $f := .}}
	{{- if .IsSlice}}
	w.WriteUint16(uint16(len(p.{{.Name}})))
	for _, v := range p.{{.Name}} {
		{{- if .IsStruct}}
		{{- range .StructFields}}
		w.{{.WriteMethod}}({{.WriteArg (printf "v.%s" .Name)}})
		{{- end}}
		{{- else}}
		w.{{.WriteMethod}}({{.WriteArg "v"}})
		{{- end}}
	}
	{{- else if .IsStruct}}
	{{- range .StructFields}}
	w.{{.WriteMethod}}({{.WriteArg (printf "p.%s.%s" $f.Name .Name)}})
	{{- end}}
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
	{{- $f := .}}
	{{- if .IsSlice}}
	{	
		if err != nil { return err }
		count, err := r.ReadUint16()
		if err != nil { return err }
		p.{{.Name}} = make([]{{if .KindStrong}}{{.KindStrong}}{{else}}{{.Kind}}{{end}}, count)
		for i := range p.{{.Name}} {
			{{- if .IsStruct}}
			{{- range .StructFields}}
			{
				v, err := r.{{.ReadMethod}}()
				if err != nil { return err }
				{{.ReadAssign (printf "p.%s[i].%s" $f.Name .Name)}}
			}
			{{- end}}
			{{- else}}
			{
				v, err := r.{{.ReadMethod}}()
				if err != nil { return err }
				{{.ReadAssign (printf "p.%s[i]" $f.Name)}}
			}
			{{- end}}
		}
	}
	{{- else if .IsStruct}}
	{{- range .StructFields}}
	{
		v, err := r.{{.ReadMethod}}()
		if err != nil { return err }
		{{.ReadAssign (printf "p.%s.%s" $f.Name .Name)}}
	}
	{{- end}}
	{{- else}}
	{
		v, err := r.{{.ReadMethod}}()
		if err != nil { return err }
		{{.ReadAssign (printf "p.%s" .Name)}}
	}
	{{- end}}
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
