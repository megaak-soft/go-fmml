/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package fileio

import (
	"embed"
	"log"
	"os"

	"github.com/megaak-soft/go-fmml/analyze"
	"github.com/megaak-soft/go-fmml/memory"
)

// LoadMMLFile reads and parses the .mml sequence file at the given absolute
// path, storing the resulting sequence into go-fmml/memory under its
// SequenceID. Warnings (minor, recoverable parse issues) are logged; a
// fatal parse error is returned and nothing is stored.
func LoadMMLFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return loadMMLData(data)
}

// LoadMMLFileFS is LoadMMLFile's embed.FS counterpart, for MML files
// embedded into the host game binary via go:embed.
func LoadMMLFileFS(fsys embed.FS, path string) error {
	data, err := fsys.ReadFile(path)
	if err != nil {
		return err
	}
	return loadMMLData(data)
}

// SetMMLTextData parses MML sequence data supplied directly as a Go string
// (Phase 7's addendum) instead of read from a file/embed.FS - e.g. a
// backtick-quoted multi-line literal laid out exactly like a .mml file's
// own contents. Internal processing is otherwise identical to
// LoadMMLFile/LoadMMLFileFS: warnings are logged, a fatal parse error is
// returned and nothing is stored.
func SetMMLTextData(data string) error {
	return loadMMLData([]byte(data))
}

func loadMMLData(data []byte) error {
	seq, warnings, err := analyze.ParseMML(data)
	for _, w := range warnings {
		log.Printf("fileio: MML file warning: %s", w)
	}
	if err != nil {
		return err
	}
	memory.StoreSequence(seq)
	return nil
}
