/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package analyze

import (
	"fmt"
	"sort"
	"strings"

	"github.com/megaak-soft/go-fmml/common"
	"github.com/megaak-soft/go-fmml/memory"
	"github.com/megaak-soft/go-fmml/pcmcore"
)

// parsePCMLongPartCommands parses a Long-type PCM part's MML command
// string. Per CLAUDE.md's spec, a Long part "既存のFM音源パートMMLと同じ
// コマンド記載で演奏" (plays using the exact same command notation as an FM
// part), so this just reuses parsePartCommands and retags the result's
// event kinds/voice ID type for the PCM domain. transpose is the part's
// [pcmPartSetting] transpose setting, in semitones (see parsePartCommands).
func parsePCMLongPartCommands(mml string, transpose int) ([]memory.PCMSeqEvent, int, []string) {
	fmEvents, ticks, warnings := parsePartCommands(mml, transpose)
	events := make([]memory.PCMSeqEvent, len(fmEvents))
	for i, ev := range fmEvents {
		pe := memory.PCMSeqEvent{AtTick: ev.AtTick, Notes: ev.Notes, Pan: ev.Pan, VoiceID: pcmcore.VoiceID(ev.VoiceID)}
		switch ev.Kind {
		case memory.EventSetPan:
			pe.Kind = memory.PCMEventSetPan
		case memory.EventSetVoice:
			pe.Kind = memory.PCMEventSetVoice
		default:
			pe.Kind = memory.PCMEventNote
		}
		events[i] = pe
	}
	return events, ticks, warnings
}

// parsePCMOneShotPartCommands parses a OneShot-type PCM part's MML text
// (see CLAUDE.md's "ワンショットタイプ" grammar): one or more
// "<note>{<commands>}" blocks, each describing its own X/R rhythm for the
// sample mapped to that note. Every block's timeline starts at tick 0 (they
// all sound concurrently, like independent drum-kit voices layered over the
// same measure), and the part's total length is the longest block.
//
// Unrecognized tokens, malformed blocks, and out-of-range values are minor
// errors: reported as warnings, the offending block/token is skipped, and
// scanning continues.
func parsePCMOneShotPartCommands(mml string) ([]memory.PCMSeqEvent, int, []string) {
	var warnings []string
	var events []memory.PCMSeqEvent
	maxTicks := 0

	runes := []rune(strings.ToUpper(mml))
	i := 0
	for i < len(runes) {
		c := runes[i]
		if !isNoteLetter(byte(c)) {
			warnings = append(warnings, fmt.Sprintf("unexpected token %q outside a note block, skipping", string(c)))
			i++
			continue
		}
		letter := byte(c)
		i++
		accidental, consumed := readAccidental(runes[i:])
		i += consumed
		octave, consumed, ok := readInt(runes[i:])
		i += consumed
		if !ok {
			warnings = append(warnings, fmt.Sprintf("note block starting %q is missing its octave, skipping to next block", string(letter)))
			continue
		}
		if i >= len(runes) || runes[i] != '{' {
			warnings = append(warnings, fmt.Sprintf("note block %s%d is missing '{', skipping", string(letter), octave))
			continue
		}
		i++ // skip '{'

		end := indexRune(runes[i:], '}')
		if end < 0 {
			warnings = append(warnings, "unterminated note block '{' (missing '}'), ignoring rest of part")
			break
		}
		blockRunes := runes[i : i+end]
		i += end + 1 // skip past '}'

		noteNumber, err := noteMIDI(letter, accidental, octave)
		if err != nil {
			warnings = append(warnings, err.Error())
			continue
		}

		blockEvents, blockTicks, blockWarnings := parsePCMOneShotBlock(blockRunes, noteNumber)
		for _, w := range blockWarnings {
			warnings = append(warnings, fmt.Sprintf("note block %s%d: %s", string(letter), octave, w))
		}
		events = append(events, blockEvents...)
		if blockTicks > maxTicks {
			maxTicks = blockTicks
		}
	}

	sort.SliceStable(events, func(a, b int) bool { return events[a].AtTick < events[b].AtTick })
	return events, maxTicks, warnings
}

// parsePCMOneShotBlock parses one "{...}" block's X/R/L/V command stream,
// triggering noteNumber on every 'X'.
func parsePCMOneShotBlock(runes []rune, noteNumber uint8) ([]memory.PCMSeqEvent, int, []string) {
	var warnings []string
	var events []memory.PCMSeqEvent
	cursor := 0

	defaultLength, defaultDotted := 4, false
	velocity := uint8(64)

	resolveLength := func(hasLen bool, length int, dotted bool) (int, bool) {
		if !hasLen {
			return defaultLength, defaultDotted
		}
		if !validNoteLengths[length] {
			warnings = append(warnings, fmt.Sprintf("note length %d is not valid, defaulting to %d", length, defaultLength))
			return defaultLength, defaultDotted
		}
		return length, dotted
	}

	i := 0
	for i < len(runes) {
		c := runes[i]
		switch c {
		case 'X':
			i++
			length, dotted, hasLen, consumed := readLength(runes[i:])
			i += consumed
			length, dotted = resolveLength(hasLen, length, dotted)
			ticks, err := common.NoteTypeTicks(noteTypeString(length, dotted))
			if err != nil {
				warnings = append(warnings, err.Error())
				continue
			}
			events = append(events, memory.PCMSeqEvent{
				AtTick: cursor,
				Kind:   memory.PCMEventOneShotTrigger,
				OneShot: memory.PCMOneShotTrigger{
					NoteNumber: noteNumber,
					Velocity:   velocity,
				},
			})
			cursor += ticks

		case 'R':
			i++
			length, dotted, hasLen, consumed := readLength(runes[i:])
			i += consumed
			length, dotted = resolveLength(hasLen, length, dotted)
			ticks, err := common.NoteTypeTicks(noteTypeString(length, dotted))
			if err != nil {
				warnings = append(warnings, err.Error())
				continue
			}
			cursor += ticks

		case 'L':
			i++
			length, dotted, hasLen, consumed := readLength(runes[i:])
			i += consumed
			if !hasLen {
				length, dotted = 4, false
			}
			if !validNoteLengths[length] {
				warnings = append(warnings, fmt.Sprintf("'L%d' is not a valid note length, defaulting to L4", length))
				length = 4
			}
			defaultLength, defaultDotted = length, dotted

		case 'V':
			i++
			n, consumed, ok := readInt(runes[i:])
			i += consumed
			if !ok {
				n = 64
			}
			if n < 0 {
				n = 0
			}
			if n > 127 {
				n = 127
			}
			velocity = uint8(n)

		default:
			warnings = append(warnings, fmt.Sprintf("unrecognized oneShot MML command %q, skipping", string(c)))
			i++
		}
	}
	return events, cursor, warnings
}

// noteMIDI converts a letter/accidental/octave triple into a MIDI note
// number, matching the same grammar buildNoteName/parseNoteName use.
func noteMIDI(letter byte, accidental int, octave int) (uint8, error) {
	semitone, ok := noteLetterSemitone[letter]
	if !ok {
		return 0, fmt.Errorf("invalid note letter %q", string(letter))
	}
	midi := (octave+1)*12 + semitone + accidental
	if midi < 0 || midi > 127 {
		return 0, fmt.Errorf("note %s octave %d is out of MIDI range", string(letter), octave)
	}
	return uint8(midi), nil
}
