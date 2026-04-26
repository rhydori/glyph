package main

import (
	"fmt"
	"go/ast"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/rhydori/logs"
	"golang.org/x/tools/go/packages"
)

var goTypeToBufferMethod = map[string]string{
	"uint8":  "Uint8",
	"uint16": "Uint16",
	"uint32": "Uint32",
	"uint64": "Uint64",
	"int32":  "Int32",
	"string": "String",
	"bool":   "Bool",
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

// LoadPackage loads the package containing inFile and all transitive dependencies.
// Returns the root package (whose directory matches inFile) and a flat map keyed by import path.
func LoadPackage(inFile string) (*packages.Package, map[string]*packages.Package, error) {
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
		return nil, nil, fmt.Errorf("No packages loaded from '%s'", dir)
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

// CollectPackets walks the root package's AST and returns all structs
// annotated with a Glyph directive.
func CollectPackets(pkg *packages.Package, pkgMap map[string]*packages.Package) []Packet {
	var packets []Packet

	for _, file := range pkg.Syntax {
		for _, decl := range file.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok || genDecl.Doc == nil {
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
				packet, ok := ParseDirectives(typeSpec.Name.Name, genDecl.Doc)
				if !ok {
					continue
				}

				packet.Fields = ParseFields(pkg, pkgMap, structType)
				packets = append(packets, packet)
			}
		}
	}

	return packets
}

// CollectImports resolves the full import paths for every external package
// referenced by packet fields, recursing into struct fields as needed.
func CollectImports(pkg *packages.Package, packets []Packet) []string {
	uniquePaths := make(map[string]bool)
	var result []string

	var collectField func(Field)
	collectField = func(field Field) {
		if !field.UsedInServerDecode() {
			return
		}

		if field.IsStruct {
			for _, sf := range field.StructFields {
				collectField(sf)
			}
			return
		}

		shortName := field.GetPackageName()
		if shortName == "" {
			return
		}

		fullPath := ResolveFullImportPath(pkg, shortName)
		if fullPath != "" && !uniquePaths[fullPath] {
			uniquePaths[fullPath] = true
			result = append(result, fullPath)
		}
	}

	for _, pkt := range packets {
		if !pkt.Flow.ServerDecodes() {
			continue
		}

		for _, field := range pkt.Fields {
			collectField(field)
		}
	}

	return result
}

// CollectSharedStructs returns all distinct struct types referenced across all packets.
func CollectSharedStructs(packets []Packet) []SharedStruct {
	seen := map[string]bool{}
	var result []SharedStruct

	for _, pkt := range packets {
		for _, f := range pkt.Fields {
			if f.IsStruct && !seen[f.StructName] {
				seen[f.StructName] = true
				result = append(result, SharedStruct{
					Name:   f.StructName,
					Fields: f.StructFields,
				})
			}
		}
	}

	return result
}

// ParseDirectives builds a Packet from its name and the raw Glyph directive string.
func ParseDirectives(name string, doc *ast.CommentGroup) (Packet, bool) {
	if doc == nil {
		return Packet{}, false
	}

	for _, c := range doc.List {
		text := strings.TrimSpace(strings.TrimPrefix(c.Text, "//"))

		if !strings.HasPrefix(text, "%Glyph") {
			continue
		}

		text = strings.ReplaceAll(text, "->", " ")
		text = strings.ReplaceAll(text, "--", " ")
		text = strings.Join(strings.Fields(text), " ")

		var flow, id string

		for part := range strings.FieldsSeq(text) {
			keyValue := strings.SplitN(part, ":", 2)
			if len(keyValue) != 2 {
				continue
			}

			key := strings.ToLower(keyValue[0])
			value := keyValue[1]

			switch key {
			case "flow":
				flow = value
			case "id":
				id = value
			}
		}

		if flow == "" || id == "" {
			return Packet{}, false
		}

		flowEnum, ok := flowByName[strings.ToLower(flow)]
		if !ok {
			return Packet{}, false
		}

		return Packet{
			Name: name,
			Flow: flowEnum,
			ID:   id,
		}, true
	}

	return Packet{}, false
}

func ExtractKeyValue(text, key string) string {
	prefix := key + ":"
	for part := range strings.FieldsSeq(text) {
		if after, ok := strings.CutPrefix(part, prefix); ok {
			return after
		}
	}
	return ""
}

// ParseFields parses a struct's field list into Fields.
// pkgMap is used to recursively resolve fields whose type is itself a struct. (e.g. []character.Character)
func ParseFields(pkg *packages.Package, pkgMap map[string]*packages.Package, structType *ast.StructType) []Field {
	var fields []Field

	for _, f := range structType.Fields.List {
		_, isSlice := parseType(f.Type)

		targetExpr := f.Type
		if isSlice {
			if array, ok := f.Type.(*ast.ArrayType); ok {
				targetExpr = array.Elt
			}
		}

		kind, strong, isStruct := resolveTypeInfo(pkg, targetExpr)

		countKind := ""
		if isSlice {
			countKind = "u8"
		}
		fieldFlow := ""
		if f.Tag != nil {
			tag := strings.Trim(f.Tag.Value, "`")
			rt := reflect.StructTag(tag)
			if v := rt.Get("count"); v != "" {
				countKind = v
			}
			if v := rt.Get("flow"); v != "" {
				fieldFlow = v
			}
		}

		var structName string
		var structFields []Field
		if isStruct {
			structName = strong
			if structName == "" {
				structName = kind
			}
			structFields = FindStructFields(pkgMap, structName)
		}

		for _, nameIdent := range f.Names {
			field := Field{
				Name:         nameIdent.Name,
				Kind:         kind,
				KindStrong:   strong,
				IsSlice:      isSlice,
				CountKind:    countKind,
				IsStruct:     isStruct,
				StructName:   structName,
				StructFields: structFields,
				FieldFlow:    fieldFlow,
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
	if len(parts) == 1 {
		for _, p := range pkgMap {
			for _, file := range p.Syntax {
				for _, decl := range file.Decls {
					genDecl, ok := decl.(*ast.GenDecl)
					if !ok {
						continue
					}
					for _, spec := range genDecl.Specs {
						ts, ok := spec.(*ast.TypeSpec)
						if !ok || ts.Name.Name != strongType {
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
		logs.Warnf("FindStructFields: type %q not found", strongType)
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

// parseType returns the string name and slice-ness of an AST expression.
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
//   - Primitive:    uint16              -> uint16
//   - Strong-typed: character.Level     -> uint8
//   - Struct:       character.Character -> struct
func resolveTypeInfo(pkg *packages.Package, expr ast.Expr) (kind, strong string, isStruct bool) {
	astName, _ := parseType(expr)

	tv, ok := pkg.TypesInfo.Types[expr]
	if !ok {
		return astName, "", false
	}

	underlying := tv.Type.Underlying().String()

	if strings.HasPrefix(underlying, "struct{") {
		return "struct", astName, true
	}

	if _, exists := goTypeToBufferMethod[underlying]; !exists {
		logs.Warnf("Unsupported underlying type '%q' for field '%q' — will be skipped", underlying, astName)
		return underlying, astName, false
	}

	if astName != underlying {
		return underlying, astName, false
	}

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

			if imp.Name != nil && imp.Name.Name != "" && imp.Name.Name != "_" && imp.Name.Name != "." {
				if imp.Name.Name == shortName {
					return fullPath
				}
				continue
			}

			parts := strings.Split(fullPath, "/")
			if len(parts) > 0 && parts[len(parts)-1] == shortName {
				return fullPath
			}
		}
	}
	return ""
}
