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

	"github.com/megaak-soft/go-fmml/common"
	"github.com/megaak-soft/go-fmml/fmcore"
	"github.com/megaak-soft/go-fmml/memory"
)

var noteLetterSemitone = map[byte]int{
	'C': 0, 'D': 2, 'E': 4, 'F': 5, 'G': 7, 'A': 9, 'B': 11,
}

// semitoneNoteName spells every pitch class with sharps, matching the
// note-name format fmcore.Engine.NoteOn and go-fmml/const use.
var semitoneNoteName = [12]string{"C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B"}

// validNoteLengths is every length digit a note/rest/'L' may use: the
// power-of-2 note lengths plus 6/12/24, CLAUDE.md's triplet lengths (a
// quarter/eighth/sixteenth-note triplet respectively - see
// common.baseNoteBeats's doc comment for the duration math).
var validNoteLengths = map[int]bool{1: true, 2: true, 4: true, 6: true, 8: true, 12: true, 16: true, 24: true, 32: true}

// mmlState tracks the running defaults that MML commands (O, L, Q, V, [@, P
// mid-stream]) mutate as a part's command string is scanned left to right.
type mmlState struct {
	octave        int
	defaultLength int
	defaultDotted bool
	gatePercent   int // Q: 1=25%,2=50%,3=75%,4=100%, expressed already as a percent
	velocity      uint8
	pan           float64
	voiceID       fmcore.VoiceID
}

// bendSpec is a parsed (but not yet applied) pitch-bend marker: CLAUDE.md's
// "_N/" / "_N\" attack notation or "/N_" / "\N_" release notation. direction
// is +1 for '/' (up) and -1 for '\' (down); semitones is the (always
// positive) magnitude, defaulting to 1 when unspecified. hasLen/length/
// dotted mirror readLength's own optional-explicit-note-length shape for
// the bend's own duration, resolved by resolveBendTicks once the note
// it's attached to is known.
type bendSpec struct {
	direction float64
	semitones float64
	hasLen    bool
	length    int
	dotted    bool
}

// portSpec is a parsed (but not yet applied) portamento marker: CLAUDE.md's
// "&&" notation, with an optional explicit glide-length ("&&4").
type portSpec struct {
	hasLen bool
	length int
	dotted bool
}

// resolveBendTicks picks a pitch-glide's (portamento's or an explicit
// pitch-bend's) duration in ticks: an explicit note-length digit, if given
// and it doesn't exceed the target note's own length; otherwise CLAUDE.md's
// documented default of 50% of it.
func resolveBendTicks(hasLen bool, length int, dotted bool, noteTicks int) int {
	if hasLen && validNoteLengths[length] {
		if t, err := common.NoteTypeTicks(noteTypeString(length, dotted)); err == nil && t > 0 && t <= noteTicks {
			return t
		}
	}
	return noteTicks / 2
}

// tryReadPortamentoMarker recognizes a leading "&&" plus an optional
// note-length (e.g. "&&4."). ok is false (consumed 0) if runes doesn't
// start with "&&" at all.
func tryReadPortamentoMarker(runes []rune) (spec portSpec, consumed int, ok bool) {
	if len(runes) < 2 || runes[0] != '&' || runes[1] != '&' {
		return portSpec{}, 0, false
	}
	length, dotted, hasLen, lenConsumed := readLength(runes[2:])
	return portSpec{hasLen: hasLen, length: length, dotted: dotted}, 2 + lenConsumed, true
}

// tryReadPrefixBend recognizes a leading attack pitch-bend marker: "_"
// [digits] ("/"|"\") [digits]. ok is false if runes doesn't start with "_"
// followed by a valid "/"/"\" direction.
func tryReadPrefixBend(runes []rune) (spec bendSpec, consumed int, ok bool) {
	if len(runes) == 0 || runes[0] != '_' {
		return bendSpec{}, 0, false
	}
	pos := 1
	n, nConsumed, hasN := readInt(runes[pos:])
	pos += nConsumed
	if pos >= len(runes) || (runes[pos] != '/' && runes[pos] != '\\') {
		return bendSpec{}, 0, false
	}
	dir := 1.0
	if runes[pos] == '\\' {
		dir = -1.0
	}
	pos++
	semitones := 1.0
	if hasN {
		semitones = float64(n)
	}
	length, dotted, hasLen, lenConsumed := readLength(runes[pos:])
	pos += lenConsumed
	return bendSpec{direction: dir, semitones: semitones, hasLen: hasLen, length: length, dotted: dotted}, pos, true
}

// tryReadPostfixBend recognizes a trailing release pitch-bend marker
// immediately after a note/chord: ("/"|"\") [digits] "_" [digits]. ok is
// false if runes doesn't match that shape at all (in particular, a bare
// "/"/"\ " not followed eventually by "_" is left untouched for the caller
// to report as unrecognized, rather than partially consumed).
func tryReadPostfixBend(runes []rune) (spec bendSpec, consumed int, ok bool) {
	if len(runes) == 0 || (runes[0] != '/' && runes[0] != '\\') {
		return bendSpec{}, 0, false
	}
	dir := 1.0
	if runes[0] == '\\' {
		dir = -1.0
	}
	pos := 1
	n, nConsumed, hasN := readInt(runes[pos:])
	pos += nConsumed
	if pos >= len(runes) || runes[pos] != '_' {
		return bendSpec{}, 0, false
	}
	pos++
	semitones := 1.0
	if hasN {
		semitones = float64(n)
	}
	length, dotted, hasLen, lenConsumed := readLength(runes[pos:])
	pos += lenConsumed
	return bendSpec{direction: dir, semitones: semitones, hasLen: hasLen, length: length, dotted: dotted}, pos, true
}

// noteSpecMIDI parses a NoteSpec's already-resolved NoteName (e.g. "C#4",
// always sharp-spelled - see buildNoteName) back into a MIDI note number,
// for portamento's pitch-difference calculation between the previous and
// new lead note.
func noteSpecMIDI(ns memory.NoteSpec) (int, error) {
	name := ns.NoteName
	if len(name) < 2 {
		return 0, fmt.Errorf("invalid note name %q", name)
	}
	semitone, ok := noteLetterSemitone[name[0]]
	if !ok {
		return 0, fmt.Errorf("invalid note name %q", name)
	}
	rest := name[1:]
	if strings.HasPrefix(rest, "#") {
		semitone++
		rest = rest[1:]
	}
	octave, err := strconv.Atoi(rest)
	if err != nil {
		return 0, fmt.Errorf("invalid note name %q", name)
	}
	return (octave+1)*12 + semitone, nil
}

// repeatMaxCount caps a "|: ... :|*n" pattern-repeat block's count, guarding
// against a mistyped huge multiplier (e.g. "*99999999") producing an
// absurdly long expanded string - no real musical passage plausibly
// repeats a single block more than a few hundred times.
const repeatMaxCount = 1000

// repeatTokenKind classifies what one left-to-right scan for repeat-block
// delimiters (see scanRepeatTokens) finds.
type repeatTokenKind int

const (
	repeatNone repeatTokenKind = iota
	repeatMatched
	repeatDanglingOpens
	repeatDanglingClose
)

// scanRepeatTokens performs one left-to-right, non-overlapping tokenizing
// pass over mml recognizing "|:"/":|" delimiters - advancing two characters
// on a match (never one), so two adjacent tokens like "|:|:" are correctly
// read as two separate opens rather than a raw substring search
// misreading the middle ":"+"|" pair as its own spurious close. It returns
// the first complete pair it finds via a stack, which - because stack
// (LIFO) matching always resolves the most-recently-opened block first -
// is always an innermost pair relative to any block still open around it.
func scanRepeatTokens(mml string) (kind repeatTokenKind, openIdx, closeIdx, danglingCount int) {
	var stack []int
	i := 0
	for i+1 < len(mml) {
		switch {
		case mml[i] == '|' && mml[i+1] == ':':
			stack = append(stack, i)
			i += 2
		case mml[i] == ':' && mml[i+1] == '|':
			if len(stack) == 0 {
				return repeatDanglingClose, 0, i, 0
			}
			return repeatMatched, stack[len(stack)-1], i, 0
		default:
			i++
		}
	}
	if len(stack) > 0 {
		return repeatDanglingOpens, 0, 0, len(stack)
	}
	return repeatNone, 0, 0, 0
}

// expandRepeats textually expands every "|: ... :|*n" pattern-repeat block
// (CLAUDE.md's repeat notation) into n literal copies of its contents,
// before parsePartCommands's main scan ever runs - so a repeated block
// behaves in every respect exactly as if the user had typed it out n times
// by hand (CLAUDE.md's own worked example: "|: L4 C D E :|*2" ==
// "L4 C D E L4 C D E"), including any state a repeat spans across (octave,
// tempo commands, a tie/portamento reaching across the boundary, etc.) -
// there is nothing repeat-specific left for the rest of the parser to
// special-case. Blocks may nest; each pass resolves one innermost pair
// (see scanRepeatTokens), so nesting comes out correct automatically.
func expandRepeats(mml string) (string, []string) {
	var warnings []string
	for {
		kind, openIdx, closeIdx, danglingCount := scanRepeatTokens(mml)
		switch kind {
		case repeatNone:
			return mml, warnings

		case repeatDanglingClose:
			warnings = append(warnings, "':|' with no matching '|:', ignoring")
			mml = mml[:closeIdx] + mml[closeIdx+2:]

		case repeatDanglingOpens:
			warnings = append(warnings, fmt.Sprintf("%d '|:' with no matching ':|', ignoring", danglingCount))
			return strings.ReplaceAll(mml, "|:", ""), warnings

		case repeatMatched:
			inner := mml[openIdx+2 : closeIdx]
			rest := mml[closeIdx+2:]
			count, consumed, warn := readRepeatCount(rest)
			if warn != "" {
				warnings = append(warnings, warn)
			}
			mml = mml[:openIdx] + strings.Repeat(inner, count) + rest[consumed:]
		}
	}
}

// readRepeatCount parses an optional "*n" repeat-count suffix immediately
// following a repeat block's closing ":|". No leading '*' at all (or one
// with no digits after it) means "repeat once" (n=1), matching CLAUDE.md's
// "[* n]の記載を省略した場合は1回のみの演奏と同義" rule. An out-of-range
// count (less than 1, or above repeatMaxCount) is a minor error: clamped
// with a warning rather than taken literally.
func readRepeatCount(rest string) (count int, consumed int, warning string) {
	runes := []rune(rest)
	if len(runes) == 0 || runes[0] != '*' {
		return 1, 0, ""
	}
	n, digitsConsumed, ok := readInt(runes[1:])
	if !ok {
		return 1, 0, ""
	}
	consumed = 1 + digitsConsumed
	if n < 1 {
		return 1, consumed, fmt.Sprintf("'*%d' repeat count must be at least 1, defaulting to *1", n)
	}
	if n > repeatMaxCount {
		return repeatMaxCount, consumed, fmt.Sprintf("'*%d' repeat count exceeds the %d-repeat safety cap, using *%d", n, repeatMaxCount, repeatMaxCount)
	}
	return n, consumed, ""
}

// parsePartCommands tokenizes one part's concatenated MML command string
// into a timed event list plus the part's total length, both expressed in
// integer ticks (common.TicksPerQuarterNote per quarter note) rather than
// milliseconds: ticks are tempo-independent and exact for every note
// length this grammar supports, so a long sequence can't accumulate
// rounding drift. Real-time positions are derived from these tick counts
// only once, at playback time (see go-fmml/player), from each event's
// absolute tick offset - never by summing already-rounded durations.
//
// Unrecognized commands, out-of-range lengths, and missing numeric
// arguments are minor errors: reported as warnings, the offending token is
// skipped or defaulted, and scanning continues.
//
// transpose is the part's [partSetting]/[pcmPartSetting] transpose setting
// (semitones, CLAUDE.md's -36..36 range - 0 for no shift): it's folded
// directly into every resolved note's pitch as it's built (see
// buildNoteName's calls below), so everything downstream of this function
// - memory.SeqEvent, go-fmml/player, fmcore/pcmcore - only ever sees
// already-transposed note names and never needs to know transpose exists.
func parsePartCommands(mml string, transpose int) ([]memory.SeqEvent, int, []string) {
	mml, warnings := expandRepeats(mml)

	var events []memory.SeqEvent
	cursor := 0

	st := mmlState{octave: 4, defaultLength: 4, gatePercent: 100, velocity: 64}

	// Phase 5 portamento/pitch-bend tracking state: pendingAttackBend/
	// pendingPortamento hold a marker seen before the note/chord it
	// applies to (cleared once consumed by the next note/chord); the
	// last* fields describe the most recently emitted note/chord event,
	// needed to validate and compute a following "&&" portamento (see
	// CLAUDE.md's rules: no rest in between, matching single/chord shape).
	var pendingAttackBend *bendSpec
	var pendingPortamento *portSpec
	// pendingTie mirrors pendingPortamento for a single '&' (tie): merges a
	// following same-pitch note/chord onto the previous event in place
	// (single attack, combined held duration - see applyTie) instead of
	// gliding to a new pitch.
	pendingTie := false
	lastNoteEventIdx := -1
	lastEventWasChord := false
	restSinceLastNote := true

	runes := []rune(strings.ToUpper(mml))
	i := 0
	for i < len(runes) {
		c := runes[i]
		switch {
		case c == '&':
			if spec, consumed, ok := tryReadPortamentoMarker(runes[i:]); ok {
				pendingPortamento = &spec
				i += consumed
			} else {
				// A single '&' (not '&&') ties: it merges the following
				// same-pitch note/chord onto the previous one in place
				// instead of gliding to a new pitch - see pendingTie's
				// handling in the note-letter/chord cases below.
				pendingTie = true
				i++
			}

		case c == '_':
			if spec, consumed, ok := tryReadPrefixBend(runes[i:]); ok {
				pendingAttackBend = &spec
				i += consumed
			} else {
				warnings = append(warnings, "'_' pitch-bend marker missing '/' or '\\', skipping")
				i++
			}

		case isNoteLetter(byte(c)):
			letter := byte(c)
			i++
			accidental, consumed := readAccidental(runes[i:])
			i += consumed
			length, dotted, hasLen, consumed := readLength(runes[i:])
			i += consumed

			noteType, warn := resolveNoteType(length, dotted, hasLen, st)
			if warn != "" {
				warnings = append(warnings, warn)
			}
			ticks, err := common.NoteTypeTicks(noteType)
			if err != nil {
				warnings = append(warnings, err.Error())
				continue
			}

			if pendingTie {
				pendingTie = false
				merged, tieWarn := tryTieSingleNote(events, lastNoteEventIdx, lastEventWasChord, restSinceLastNote, letter, accidental+transpose, st.octave, ticks, st.gatePercent)
				if tieWarn != "" {
					warnings = append(warnings, tieWarn)
				}
				if merged {
					if pendingAttackBend != nil {
						warnings = append(warnings, "pitch-bend marker before a tied note has no effect, ignoring")
						pendingAttackBend = nil
					}
					cursor += ticks
					continue
				}
			}

			spec := memory.NoteSpec{
				NoteName: buildNoteName(letter, accidental+transpose, st.octave),
				NoteType: noteType,
				Sustain:  st.gatePercent,
				Velocity: st.velocity,
			}

			if pendingAttackBend != nil {
				// "_N/" (bend up) must START below the target and rise to
				// it, so the start-of-glide offset is negative; "_N\"
				// (bend down) starts above and falls, so positive - the
				// opposite sign from direction's own "/up=+1,\down=-1"
				// convention (see tryReadPrefixBend/tryReadPostfixBend).
				spec.Bend.AttackOffsetSemitones = -pendingAttackBend.direction * pendingAttackBend.semitones
				spec.Bend.AttackTicks = resolveBendTicks(pendingAttackBend.hasLen, pendingAttackBend.length, pendingAttackBend.dotted, ticks)
				pendingAttackBend = nil
			}
			if pendingPortamento != nil {
				switch {
				case lastNoteEventIdx < 0 || restSinceLastNote:
					warnings = append(warnings, "portamento '&&' with no preceding note (or a rest in between), ignoring")
				case lastEventWasChord:
					warnings = append(warnings, "portamento '&&' from a chord to a single note is invalid, ignoring")
				default:
					prevNotes := events[lastNoteEventIdx].Notes
					newMIDI, errNew := noteMIDI(letter, accidental+transpose, st.octave)
					prevMIDI, errPrev := noteSpecMIDI(prevNotes[0])
					if errNew == nil && errPrev == nil {
						// Single-note-to-single-note is the common case:
						// true legato (Portamento=true tells the engine to
						// glide the still-sounding note in place rather
						// than retriggering it - see fmcore.NoteBend's doc
						// comment). AttackOffsetSemitones is still the
						// written-pitch difference, kept only as a
						// fresh-attack fallback if that note has already
						// finished by the time this fires.
						spec.Bend.AttackOffsetSemitones = float64(prevMIDI - int(newMIDI))
						spec.Bend.AttackTicks = resolveBendTicks(pendingPortamento.hasLen, pendingPortamento.length, pendingPortamento.dotted, ticks)
						spec.Bend.Portamento = true
						for k := range prevNotes {
							prevNotes[k].Sustain = 100
						}
					}
				}
				pendingPortamento = nil
			}
			if rspec, rconsumed, ok := tryReadPostfixBend(runes[i:]); ok {
				spec.Bend.ReleaseOffsetSemitones = rspec.direction * rspec.semitones
				spec.Bend.ReleaseTicks = resolveBendTicks(rspec.hasLen, rspec.length, rspec.dotted, ticks)
				i += rconsumed
			}

			events = append(events, memory.SeqEvent{AtTick: cursor, Kind: memory.EventNote, Notes: []memory.NoteSpec{spec}})
			lastNoteEventIdx = len(events) - 1
			lastEventWasChord = false
			restSinceLastNote = false
			cursor += ticks

		case c == 'R':
			i++
			length, dotted, hasLen, consumed := readLength(runes[i:])
			i += consumed
			noteType, warn := resolveNoteType(length, dotted, hasLen, st)
			if warn != "" {
				warnings = append(warnings, warn)
			}
			ticks, err := common.NoteTypeTicks(noteType)
			if err != nil {
				warnings = append(warnings, err.Error())
				continue
			}
			cursor += ticks
			restSinceLastNote = true
			if pendingAttackBend != nil {
				warnings = append(warnings, "pitch-bend marker before a rest has no note to apply to, ignoring")
				pendingAttackBend = nil
			}
			if pendingPortamento != nil {
				warnings = append(warnings, "portamento '&&' before a rest is invalid, ignoring")
				pendingPortamento = nil
			}
			if pendingTie {
				warnings = append(warnings, "tie '&' before a rest is invalid, ignoring")
				pendingTie = false
			}

		case c == '[':
			end := indexRune(runes[i+1:], ']')
			if end < 0 {
				warnings = append(warnings, "unterminated chord '[' (missing ']'), ignoring rest of part")
				i = len(runes)
				break
			}
			chordRunes := runes[i+1 : i+1+end]
			i += end + 2 // skip past ']'

			var notes []memory.NoteSpec
			maxTicks := 0
			// chordOctave lets a chord mix octaves (e.g. "[O2 G O3 C]"): it
			// starts at the part's current octave and an "O" inside the
			// brackets retargets only the notes that follow it, local to
			// this chord - it never changes st.octave itself, so octave
			// commands after the chord are unaffected.
			chordOctave := st.octave
			j := 0
			for j < len(chordRunes) {
				cc := chordRunes[j]
				if cc == 'O' {
					j++
					n, consumed, ok := readInt(chordRunes[j:])
					j += consumed
					if !ok {
						warnings = append(warnings, "'O' inside a chord is missing an octave number, ignoring")
						continue
					}
					chordOctave = n
					continue
				}
				if !isNoteLetter(byte(cc)) {
					j++ // per spec, non-note/octave tokens inside a chord are ignored
					continue
				}
				letter := byte(cc)
				j++
				accidental, consumed := readAccidental(chordRunes[j:])
				j += consumed
				length, dotted, hasLen, consumed := readLength(chordRunes[j:])
				j += consumed

				noteType, warn := resolveNoteType(length, dotted, hasLen, st)
				if warn != "" {
					warnings = append(warnings, warn)
				}
				ticks, err := common.NoteTypeTicks(noteType)
				if err != nil {
					warnings = append(warnings, err.Error())
					continue
				}
				notes = append(notes, memory.NoteSpec{
					NoteName: buildNoteName(letter, accidental+transpose, chordOctave),
					NoteType: noteType,
					Sustain:  st.gatePercent,
					Velocity: st.velocity,
				})
				if ticks > maxTicks {
					maxTicks = ticks
				}
			}
			if len(notes) == 0 {
				warnings = append(warnings, "chord '[]' contained no notes, ignoring")
				break
			}

			if pendingTie {
				pendingTie = false
				merged, tieWarn := tryTieChord(events, lastNoteEventIdx, lastEventWasChord, restSinceLastNote, notes, maxTicks, st.gatePercent)
				if tieWarn != "" {
					warnings = append(warnings, tieWarn)
				}
				if merged {
					if pendingAttackBend != nil {
						warnings = append(warnings, "pitch-bend marker before a tied chord has no effect, ignoring")
						pendingAttackBend = nil
					}
					cursor += maxTicks
					continue
				}
			}

			if pendingAttackBend != nil {
				// See the single-note case's identical comment on the sign
				// flip here.
				offset := -pendingAttackBend.direction * pendingAttackBend.semitones
				attackTicks := resolveBendTicks(pendingAttackBend.hasLen, pendingAttackBend.length, pendingAttackBend.dotted, maxTicks)
				for k := range notes {
					notes[k].Bend.AttackOffsetSemitones = offset
					notes[k].Bend.AttackTicks = attackTicks
				}
				pendingAttackBend = nil
			}
			if pendingPortamento != nil {
				switch {
				case lastNoteEventIdx < 0 || restSinceLastNote:
					warnings = append(warnings, "portamento '&&' with no preceding note (or a rest in between), ignoring")
				case !lastEventWasChord:
					warnings = append(warnings, "portamento '&&' from a single note to a chord is invalid, ignoring")
				default:
					// Chord-to-chord intentionally stays a fresh attack
					// with a computed offset (Bend.Portamento left false)
					// rather than true legato: CLAUDE.md's own rule here
					// is "apply the lead notes' pitch difference uniformly
					// to every note," which doesn't define how each new
					// chord voice would individually continue a specific
					// old voice's envelope - see the single-note case
					// above for the true-legato path.
					prevNotes := events[lastNoteEventIdx].Notes
					prevMIDI, errPrev := noteSpecMIDI(prevNotes[0])
					newMIDI, errNew := noteSpecMIDI(notes[0])
					if errPrev == nil && errNew == nil {
						delta := float64(prevMIDI - newMIDI)
						attackTicks := resolveBendTicks(pendingPortamento.hasLen, pendingPortamento.length, pendingPortamento.dotted, maxTicks)
						for k := range notes {
							notes[k].Bend.AttackOffsetSemitones = delta
							notes[k].Bend.AttackTicks = attackTicks
						}
						for k := range prevNotes {
							prevNotes[k].Sustain = 100
						}
					}
				}
				pendingPortamento = nil
			}
			if rspec, rconsumed, ok := tryReadPostfixBend(runes[i:]); ok {
				releaseOffset := rspec.direction * rspec.semitones
				releaseTicks := resolveBendTicks(rspec.hasLen, rspec.length, rspec.dotted, maxTicks)
				for k := range notes {
					notes[k].Bend.ReleaseOffsetSemitones = releaseOffset
					notes[k].Bend.ReleaseTicks = releaseTicks
				}
				i += rconsumed
			}

			events = append(events, memory.SeqEvent{AtTick: cursor, Kind: memory.EventNote, Notes: notes})
			lastNoteEventIdx = len(events) - 1
			lastEventWasChord = true
			restSinceLastNote = false
			cursor += maxTicks

		case c == '>':
			st.octave--
			i++
		case c == '<':
			st.octave++
			i++

		case c == 'O':
			i++
			n, consumed, ok := readInt(runes[i:])
			i += consumed
			if !ok {
				warnings = append(warnings, "'O' missing octave number, ignoring")
				break
			}
			st.octave = n

		case c == 'L':
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
			st.defaultLength, st.defaultDotted = length, dotted

		case c == 'Q':
			i++
			n, consumed, ok := readInt(runes[i:])
			i += consumed
			if !ok {
				n = 4
			}
			if n < 1 || n > 4 {
				warnings = append(warnings, fmt.Sprintf("'Q%d' out of range 1-4, defaulting to Q4", n))
				n = 4
			}
			st.gatePercent = n * 25

		case c == 'V':
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
			st.velocity = uint8(n)

		case c == 'P':
			i++
			n, consumed, ok := readSignedFloat(runes[i:])
			i += consumed
			if !ok {
				warnings = append(warnings, "'P' missing pan value, ignoring")
				break
			}
			// 'P' is specified on CLAUDE.md's -16(left)..16(right) scale,
			// matching [partSetting]'s pan - normalize to [-1,1] here so
			// every downstream Pan field (including [partSetting]'s own,
			// see analyze/mml.go's normalizePan16) is on the same scale.
			st.pan = n / 16
			events = append(events, memory.SeqEvent{AtTick: cursor, Kind: memory.EventSetPan, Pan: st.pan})

		case c == '@':
			i++
			n, consumed, ok := readInt(runes[i:])
			i += consumed
			if !ok {
				warnings = append(warnings, "'@' missing voice id, ignoring")
				break
			}
			st.voiceID = fmcore.VoiceID(n)
			events = append(events, memory.SeqEvent{AtTick: cursor, Kind: memory.EventSetVoice, VoiceID: st.voiceID})

		default:
			warnings = append(warnings, fmt.Sprintf("unrecognized MML command %q, skipping", string(c)))
			i++
		}
	}

	if pendingAttackBend != nil {
		warnings = append(warnings, "pitch-bend marker at end of part with no following note, ignoring")
	}
	if pendingPortamento != nil {
		warnings = append(warnings, "portamento '&&' at end of part with no following note, ignoring")
	}
	if pendingTie {
		warnings = append(warnings, "tie '&' at end of part with no following note, ignoring")
	}

	return events, cursor, warnings
}

// applyTie merges a same-pitch note/chord tied by a single '&' onto ns (the
// previous note/chord's already-emitted NoteSpec) in place: rather than a
// new event, the whole chain fires as a single attack (ns's own Velocity is
// left untouched - MML's per-tied-segment "V" has no effect, since there's
// no second attack to apply it to) held for the chain's combined length.
// segmentTicks is this new segment's own full (un-gated) length; the
// running total is recovered from ns.NoteType on the first tie in a chain,
// or from ns.Bend.TieTicks itself on a later one. gatePercent is this
// segment's own gate ('Q'): the last segment tied onto a note governs when
// the combined note releases, so ns.Sustain is simply overwritten with it.
func applyTie(ns *memory.NoteSpec, segmentTicks int, gatePercent int) {
	total := ns.Bend.TieTicks
	if total <= 0 {
		if t, err := common.NoteTypeTicks(ns.NoteType); err == nil {
			total = t
		}
	}
	ns.Bend.TieTicks = total + segmentTicks
	ns.Sustain = gatePercent
}

// tryTieSingleNote merges a single-note tie ('&') onto the previous event's
// note in place, if valid: a preceding single note (not a chord, no rest in
// between) at the exact same pitch. Anything else - nothing to tie onto, a
// chord on either side, or a different pitch - leaves merged false: per
// CLAUDE.md's addendum, a tie between different notes is simply ignored, so
// the caller plays the note fresh instead (its own separate attack).
func tryTieSingleNote(events []memory.SeqEvent, lastIdx int, lastWasChord bool, restSince bool, letter byte, accidental int, octave int, ticks int, gatePercent int) (merged bool, warning string) {
	switch {
	case lastIdx < 0 || restSince:
		return false, "tie '&' with no preceding note (or a rest in between), ignoring"
	case lastWasChord:
		return false, "tie '&' between a chord and a single note is invalid, ignoring"
	}
	prevNotes := events[lastIdx].Notes
	newMIDI, errNew := noteMIDI(letter, accidental, octave)
	prevMIDI, errPrev := noteSpecMIDI(prevNotes[0])
	if errNew != nil || errPrev != nil || prevMIDI != int(newMIDI) {
		return false, "tie '&' between different notes is invalid, ignoring (played as a separate note)"
	}
	applyTie(&prevNotes[0], ticks, gatePercent)
	return true, ""
}

// tryTieChord is tryTieSingleNote's chord counterpart: valid only when the
// previous event is also a chord of the exact same size with every note at
// the exact same pitch (position by position) - CLAUDE.md's addendum only
// defines a tie between identical notes, and a chord has no single "pitch"
// of its own to compare, so an exact match is required rather than (as
// portamento does) just the lead notes.
func tryTieChord(events []memory.SeqEvent, lastIdx int, lastWasChord bool, restSince bool, newNotes []memory.NoteSpec, maxTicks int, gatePercent int) (merged bool, warning string) {
	switch {
	case lastIdx < 0 || restSince:
		return false, "tie '&' with no preceding note (or a rest in between), ignoring"
	case !lastWasChord:
		return false, "tie '&' between a single note and a chord is invalid, ignoring"
	}
	prevNotes := events[lastIdx].Notes
	if len(prevNotes) != len(newNotes) {
		return false, "tie '&' between chords of different sizes is invalid, ignoring (played as a separate chord)"
	}
	for k := range newNotes {
		newMIDI, errNew := noteSpecMIDI(newNotes[k])
		prevMIDI, errPrev := noteSpecMIDI(prevNotes[k])
		if errNew != nil || errPrev != nil || newMIDI != prevMIDI {
			return false, "tie '&' between different chords is invalid, ignoring (played as a separate chord)"
		}
	}
	for k := range prevNotes {
		applyTie(&prevNotes[k], maxTicks, gatePercent)
	}
	return true, ""
}

func isNoteLetter(b byte) bool {
	_, ok := noteLetterSemitone[b]
	return ok
}

// readAccidental consumes a leading '#'/'+' (sharp) or '-' (flat).
func readAccidental(runes []rune) (accidental int, consumed int) {
	if len(runes) == 0 {
		return 0, 0
	}
	switch runes[0] {
	case '#', '+':
		return 1, 1
	case '-':
		return -1, 1
	default:
		return 0, 0
	}
}

// readLength consumes an optional length digits + optional '.' (e.g. "16",
// "4.").
func readLength(runes []rune) (length int, dotted bool, hasLength bool, consumed int) {
	n, digitsConsumed, ok := readInt(runes)
	if ok {
		length = n
		hasLength = true
		consumed = digitsConsumed
	}
	if consumed < len(runes) && runes[consumed] == '.' {
		dotted = true
		consumed++
	}
	return length, dotted, hasLength, consumed
}

func readInt(runes []rune) (value int, consumed int, ok bool) {
	start := 0
	for consumed < len(runes) && runes[consumed] >= '0' && runes[consumed] <= '9' {
		consumed++
	}
	if consumed == start {
		return 0, 0, false
	}
	n, err := strconv.Atoi(string(runes[start:consumed]))
	if err != nil {
		return 0, consumed, false
	}
	return n, consumed, true
}

func readSignedFloat(runes []rune) (value float64, consumed int, ok bool) {
	end := 0
	if end < len(runes) && (runes[end] == '-' || runes[end] == '+') {
		end++
	}
	digitsStart := end
	for end < len(runes) && ((runes[end] >= '0' && runes[end] <= '9') || runes[end] == '.') {
		end++
	}
	if end == digitsStart {
		return 0, 0, false
	}
	v, err := strconv.ParseFloat(string(runes[:end]), 64)
	if err != nil {
		return 0, end, false
	}
	return v, end, true
}

func indexRune(runes []rune, target rune) int {
	for i, r := range runes {
		if r == target {
			return i
		}
	}
	return -1
}

// resolveNoteType turns an (optionally explicit) length+dot pair into a
// NOTEn/NOTEn_DOT type string, falling back to the part's current default
// length (set by L) when no explicit length was given.
func resolveNoteType(length int, dotted bool, hasLength bool, st mmlState) (string, string) {
	if !hasLength {
		length, dotted = st.defaultLength, st.defaultDotted
	} else if !validNoteLengths[length] {
		warn := fmt.Sprintf("note length %d is not valid, defaulting to %d", length, st.defaultLength)
		length, dotted = st.defaultLength, st.defaultDotted
		return noteTypeString(length, dotted), warn
	}
	return noteTypeString(length, dotted), ""
}

func noteTypeString(length int, dotted bool) string {
	s := fmt.Sprintf("NOTE%d", length)
	if dotted {
		s += "_DOT"
	}
	return s
}

// buildNoteName resolves a letter/accidental/octave triple (e.g. 'D', -1, 4
// for a flat D in octave 4) into a sharp-spelled scientific-pitch-notation
// string such as "C#4", matching fmcore.Engine.NoteOn's expected format.
func buildNoteName(letter byte, accidental int, octave int) string {
	semitone := noteLetterSemitone[letter] + accidental
	octaveShift := 0
	for semitone < 0 {
		semitone += 12
		octaveShift--
	}
	for semitone > 11 {
		semitone -= 12
		octaveShift++
	}
	return fmt.Sprintf("%s%d", semitoneNoteName[semitone], octave+octaveShift)
}
