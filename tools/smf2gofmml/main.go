/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

// Command Smf2GoFMML reads a Standard MIDI File (Format 1) and converts it
// into a go-fmml .mml file, per CLAUDE.md's Phase 6 spec. The "smf"/
// "convert" folders it resolves are relative to wherever its own binary
// ends up, so build it straight into cmd/ (its intended home), e.g. from
// this directory:
//
//	go build -o cmd/Smf2GoFMML.exe .
//
// Usage (run from inside cmd/, or wherever the built exe lives):
//
//	Smf2GoFMML <path-to.mid>            (an absolute path, used as-is)
//	Smf2GoFMML <filename.mid>           (looked up under <exeDir>/smf)
//
// The converted file is written to <exeDir>/convert/<song name>.mml,
// creating that folder if it doesn't already exist.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/megaak-soft/go-fmml/tools/smf2gofmml/convert"
	"github.com/megaak-soft/go-fmml/tools/smf2gofmml/midiparse"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: Smf2GoFMML <path-or-filename.mid>")
		os.Exit(1)
	}
	arg := os.Args[1]

	exePath, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: cannot resolve the executable's own path:", err)
		os.Exit(1)
	}
	exeDir := filepath.Dir(exePath)

	inputPath := arg
	if !filepath.IsAbs(arg) {
		inputPath = filepath.Join(exeDir, "smf", arg)
	}

	data, err := os.ReadFile(inputPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: failed to read SMF file:", err)
		os.Exit(1)
	}

	result, err := midiparse.ParseSMF(data)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: failed to parse SMF file:", err)
		os.Exit(1)
	}

	mml, warnings, err := convert.ConvertToMML(result)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}

	outDir := filepath.Join(exeDir, "convert")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "error: failed to create the convert output folder:", err)
		os.Exit(1)
	}

	outPath := filepath.Join(outDir, sanitizeFileName(result.SongName)+".mml")
	if err := os.WriteFile(outPath, []byte(mml), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "error: failed to write the output MML file:", err)
		os.Exit(1)
	}

	fmt.Println("wrote", outPath)
}

// sanitizeFileName replaces characters invalid in a Windows file name (plus
// any control character) with '_', so an arbitrary SMF sequence-name meta
// event can always be used as an output file's base name.
func sanitizeFileName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "untitled"
	}
	const invalid = "\\/:*?\"<>|"
	var b strings.Builder
	for _, r := range name {
		if r < 0x20 || strings.ContainsRune(invalid, r) {
			b.WriteRune('_')
		} else {
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return "untitled"
	}
	return out
}
