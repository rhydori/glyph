package main

import (
	_ "embed"
	"flag"
	"os"
	"path/filepath"
	"time"

	"github.com/rhydori/logs"
)

type Generated struct {
	Package string
	Imports []string
	Packets []Packet
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
	time.Sleep(5 * time.Millisecond)
}
