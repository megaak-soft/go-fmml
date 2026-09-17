/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package fmcore

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/megaak-soft/go-fmml/common"
)

// NoteOn is the abstracted, human-readable counterpart to NoteOnRow: it
// takes a note name (e.g. "A4", "C#5") and a note type (e.g. "NOTE4",
// "NOTE8_DOT", see go-fmml/const), converts them into a MIDI note number
// and a duration in milliseconds, and triggers the note.
//
// The note type's 100%-length duration is derived from common.Tempo (beats
// per minute, one beat = one quarter note); sustain then scales that length
// as a percentage (100 = full length, 50 = half length). It returns a
// NoteID immediately, same as NoteOnRow.
func (e *Engine) NoteOn(partID PartID, noteName string, noteType string, sustain int, velocity uint8) (NoteID, error) {
	noteNumber, err := parseNoteName(noteName)
	if err != nil {
		return 0, err
	}
	beats, err := noteTypeBeats(noteType)
	if err != nil {
		return 0, err
	}

	quarterNoteMs := 60000 / common.Tempo
	fullDurationMs := beats * quarterNoteMs
	durationMs := int(fullDurationMs * float64(sustain) / 100)

	return e.NoteOnRow(partID, noteNumber, durationMs, velocity)
}

var noteLetterSemitone = map[byte]int{
	'C': 0, 'D': 2, 'E': 4, 'F': 5, 'G': 7, 'A': 9, 'B': 11,
}

// parseNoteName converts a scientific-pitch-notation note name such as "A4"
// or "C#5" into a MIDI note number (C4 = 60).
func parseNoteName(name string) (uint8, error) {
	if len(name) < 2 {
		return 0, fmt.Errorf("fmcore: invalid note name %q", name)
	}
	semitone, ok := noteLetterSemitone[name[0]]
	if !ok {
		return 0, fmt.Errorf("fmcore: invalid note name %q", name)
	}

	rest := name[1:]
	if strings.HasPrefix(rest, "#") {
		semitone++
		rest = rest[1:]
	}

	octave, err := strconv.Atoi(rest)
	if err != nil {
		return 0, fmt.Errorf("fmcore: invalid note name %q", name)
	}

	midi := (octave+1)*12 + semitone
	if midi < 0 || midi > 127 {
		return 0, fmt.Errorf("fmcore: note name %q is out of MIDI range", name)
	}
	return uint8(midi), nil
}

// baseNoteBeats maps a note type to its duration in quarter-note beats.
// Mirrors common.baseNoteBeats (see this package's other fmcore-mirroring
// types' doc comments for why); NOTE6/NOTE12/NOTE24 are triplet lengths -
// see common.baseNoteBeats's identical entries for the full reasoning.
var baseNoteBeats = map[string]float64{
	"NOTE1":  4,
	"NOTE2":  2,
	"NOTE4":  1,
	"NOTE6":  4.0 / 6.0,
	"NOTE8":  0.5,
	"NOTE12": 4.0 / 12.0,
	"NOTE16": 0.25,
	"NOTE24": 4.0 / 24.0,
	"NOTE32": 0.125,
}

// noteTypeBeats converts a note type name (e.g. "NOTE4", "NOTE8_DOT") into
// its duration in quarter-note beats; a "_DOT" suffix multiplies the base
// note's duration by 1.5.
func noteTypeBeats(noteType string) (float64, error) {
	key := noteType
	dotted := strings.HasSuffix(noteType, "_DOT")
	if dotted {
		key = strings.TrimSuffix(noteType, "_DOT")
	}
	beats, ok := baseNoteBeats[key]
	if !ok {
		return 0, fmt.Errorf("fmcore: unknown note type %q", noteType)
	}
	if dotted {
		beats *= 1.5
	}
	return beats, nil
}
