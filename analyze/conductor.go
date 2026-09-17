/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package analyze

import (
	"fmt"
	"strings"

	"github.com/megaak-soft/go-fmml/common"
)

// parseConductorCommands tokenizes a [conductorPart] MML command string
// (CLAUDE.md's Phase 7 conductor part) into a tempo map plus an optional
// loop jump point, both expressed in integer ticks - the same
// tempo-independent unit every other part's commands resolve into (see
// parsePartCommands's doc comment).
//
// Grammar: 'T' followed by a tempo value sets the tempo from this point in
// the timeline on; 'R' followed by a note length advances the timeline
// without sounding anything (its only purpose is to position a following
// 'T'/'J'); 'J' marks the current position as the loop restart point (only
// the first occurrence counts - a later one is a minor error, reported as
// a warning and ignored). Unrecognized commands and invalid values are
// likewise minor errors: reported as warnings, the offending token is
// skipped, and scanning continues.
func parseConductorCommands(mml string) (tempoMap []common.TempoPoint, jumpTick int, hasJump bool, warnings []string) {
	cursor := 0
	runes := []rune(strings.ToUpper(mml))
	i := 0
	for i < len(runes) {
		c := runes[i]
		switch {
		case c == 'T':
			i++
			n, consumed, ok := readInt(runes[i:])
			i += consumed
			if !ok {
				warnings = append(warnings, "conductor 'T' is missing a tempo value, ignoring")
				break
			}
			if n <= 0 {
				warnings = append(warnings, fmt.Sprintf("conductor 'T%d' is not a valid tempo, ignoring", n))
				break
			}
			tempoMap = append(tempoMap, common.TempoPoint{AtTick: cursor, BPM: float64(n)})

		case c == 'R':
			i++
			length, dotted, hasLen, consumed := readLength(runes[i:])
			i += consumed
			if !hasLen {
				length, dotted = 4, false
			} else if !validNoteLengths[length] {
				warnings = append(warnings, fmt.Sprintf("conductor rest length %d is not valid, defaulting to 4", length))
				length, dotted = 4, false
			}
			ticks, err := common.NoteTypeTicks(noteTypeString(length, dotted))
			if err != nil {
				warnings = append(warnings, err.Error())
				break
			}
			cursor += ticks

		case c == 'J':
			i++
			if hasJump {
				warnings = append(warnings, "multiple 'J' jump points in [conductorPart], only the first is used")
				break
			}
			jumpTick = cursor
			hasJump = true

		default:
			warnings = append(warnings, fmt.Sprintf("unrecognized conductor command %q, skipping", string(c)))
			i++
		}
	}
	return tempoMap, jumpTick, hasJump, warnings
}
