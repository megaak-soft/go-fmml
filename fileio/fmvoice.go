/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

// Package fileio reads FM voice parameter files and MML sequence files from
// disk (or an embed.FS), hands their contents to go-fmml/analyze for
// parsing, and stores the result into go-fmml/memory.
package fileio

import (
	"embed"
	"log"
	"os"

	"github.com/megaak-soft/go-fmml/analyze"
	"github.com/megaak-soft/go-fmml/memory"
)

// LoadFMVoiceFile reads and parses the FM voice parameter YAML file at the
// given absolute path, storing every voice it defines into go-fmml/memory.
// Warnings (minor, recoverable parse issues) are logged; a fatal parse
// error is returned and nothing is stored.
func LoadFMVoiceFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return loadFMVoiceData(data)
}

// LoadFMVoiceFileFS is LoadFMVoiceFile's embed.FS counterpart, for voice
// files embedded into the host game binary via go:embed.
func LoadFMVoiceFileFS(fsys embed.FS, path string) error {
	data, err := fsys.ReadFile(path)
	if err != nil {
		return err
	}
	return loadFMVoiceData(data)
}

// SetFMVoiceData parses FM voice parameter data supplied directly as a Go
// string (Phase 7's addendum) instead of read from a file/embed.FS - e.g. a
// backtick-quoted multi-line literal laid out exactly like a voice YAML
// file's own contents. Internal processing is otherwise identical to
// LoadFMVoiceFile/LoadFMVoiceFileFS: warnings are logged, a fatal parse
// error is returned and nothing is stored.
func SetFMVoiceData(data string) error {
	return loadFMVoiceData([]byte(data))
}

func loadFMVoiceData(data []byte) error {
	defs, warnings, err := analyze.ParseFMVoiceFile(data)
	for _, w := range warnings {
		log.Printf("fileio: FM voice file warning: %s", w)
	}
	if err != nil {
		return err
	}
	for _, def := range defs {
		memory.StoreVoice(def.VoiceID, def.Voice)
	}
	return nil
}
