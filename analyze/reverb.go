/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package analyze

import (
	"fmt"
	"strconv"
	"strings"
)

// parseOneShotReverbSendMap parses a [pcmPartSetting] OneShot part's
// reverbSend string (see CLAUDE.md's Phase 4 spec), a comma-separated list
// of "note: level" pairs such as `"C1: 5, E1: 30, F1#: 15"`, into a MIDI
// note number -> [0,1] send level map. A malformed entry (bad note name,
// missing ':', non-numeric level) is a minor error: reported as a warning,
// that entry is skipped, and parsing continues.
func parseOneShotReverbSendMap(s string) (map[uint8]float64, []string) {
	var warnings []string
	result := make(map[uint8]float64)

	s = strings.TrimSpace(s)
	if s == "" {
		return result, warnings
	}

	for _, tok := range strings.Split(s, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		fields := strings.SplitN(tok, ":", 2)
		if len(fields) != 2 {
			warnings = append(warnings, fmt.Sprintf("reverbSend entry %q is missing ':', ignoring", tok))
			continue
		}

		note, err := parseNoteNameLenient(fields[0])
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("reverbSend entry %q: %s, ignoring", tok, err))
			continue
		}
		level, err := strconv.Atoi(strings.TrimSpace(fields[1]))
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("reverbSend entry %q: level %q is not a number, ignoring", tok, strings.TrimSpace(fields[1])))
			continue
		}
		result[note] = normalizeVolume127(clampVolume127Float(float64(level)))
	}
	return result, warnings
}

// parseNoteNameLenient converts a scientific-pitch-notation note name into
// a MIDI note number, tolerating the accidental appearing either right
// after the letter ("F#1", matching parseNoteName's usual grammar) or right
// after the octave digits ("F1#", the form CLAUDE.md's own Phase 4
// reverbSend example uses).
func parseNoteNameLenient(raw string) (uint8, error) {
	s := strings.ToUpper(strings.TrimSpace(raw))
	if len(s) < 2 {
		return 0, fmt.Errorf("invalid note name %q", raw)
	}
	semitone, ok := noteLetterSemitone[s[0]]
	if !ok {
		return 0, fmt.Errorf("invalid note name %q", raw)
	}

	accidental := 0
	var digits strings.Builder
	for _, r := range s[1:] {
		switch r {
		case '#', '+':
			accidental = 1
		case '-':
			accidental = -1
		default:
			digits.WriteRune(r)
		}
	}
	octave, err := strconv.Atoi(digits.String())
	if err != nil {
		return 0, fmt.Errorf("invalid note name %q", raw)
	}

	midi := (octave+1)*12 + semitone + accidental
	if midi < 0 || midi > 127 {
		return 0, fmt.Errorf("note name %q is out of MIDI range", raw)
	}
	return uint8(midi), nil
}
