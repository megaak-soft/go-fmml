/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package pcmcore

import (
	"fmt"
	"strconv"
	"strings"
)

var noteLetterSemitone = map[byte]int{
	'C': 0, 'D': 2, 'E': 4, 'F': 5, 'G': 7, 'A': 9, 'B': 11,
}

// parseNoteName converts a scientific-pitch-notation note name such as "A4"
// or "C#5" into a MIDI note number (C4 = 60). Duplicated from fmcore's
// identical helper (see this package's doc comment for why) so
// ScheduleNoteOnLong can accept the same noteName string shape
// fmcore.Engine.ScheduleNoteOn does.
func parseNoteName(name string) (uint8, error) {
	if len(name) < 2 {
		return 0, fmt.Errorf("pcmcore: invalid note name %q", name)
	}
	semitone, ok := noteLetterSemitone[name[0]]
	if !ok {
		return 0, fmt.Errorf("pcmcore: invalid note name %q", name)
	}

	rest := name[1:]
	if strings.HasPrefix(rest, "#") {
		semitone++
		rest = rest[1:]
	}

	octave, err := strconv.Atoi(rest)
	if err != nil {
		return 0, fmt.Errorf("pcmcore: invalid note name %q", name)
	}

	midi := (octave+1)*12 + semitone
	if midi < 0 || midi > 127 {
		return 0, fmt.Errorf("pcmcore: note name %q is out of MIDI range", name)
	}
	return uint8(midi), nil
}
