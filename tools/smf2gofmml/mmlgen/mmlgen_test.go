/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package mmlgen

import (
	"testing"

	"github.com/megaak-soft/go-fmml/common"
)

func TestDecomposeTicksExactAndRemainder(t *testing.T) {
	toks := decomposeTicks(common.TicksPerQuarterNote * 4)
	if len(toks) != 1 || toks[0].length != 1 || toks[0].dotted {
		t.Errorf("decomposeTicks(whole note) = %+v, want single non-dotted whole note", toks)
	}

	toks = decomposeTicks(0)
	if toks != nil {
		t.Errorf("decomposeTicks(0) = %+v, want nil", toks)
	}
}

func TestNoteNameParts(t *testing.T) {
	letter, octave := NoteNameParts(60) // C4
	if letter != "C" || octave != 4 {
		t.Errorf("NoteNameParts(60) = (%q, %d), want (\"C\", 4)", letter, octave)
	}
	letter, octave = NoteNameParts(61) // C#4
	if letter != "C#" || octave != 4 {
		t.Errorf("NoteNameParts(61) = (%q, %d), want (\"C#\", 4)", letter, octave)
	}
}
