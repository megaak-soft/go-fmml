/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

// Package mmlgen holds the low-level building blocks smf2gofmml's convert
// package uses to render MML command text: note-length quantization/tie
// decomposition against go-fmml's own MML length grammar, staccato (Q)
// snapping, and note-name spelling.
package mmlgen

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/megaak-soft/go-fmml/common"
)

// lenTok is one MML note-length token (e.g. "4" or "8.") together with its
// duration in go-fmml's internal ticks (see common.TicksPerQuarterNote).
type lenTok struct {
	length int
	dotted bool
	ticks  int
}

// lengthTable lists every MML note-length token this converter may emit,
// sorted longest-first, so decomposeTicks's greedy fit always tries the
// largest usable length before falling back to smaller ones. Built from
// common.NoteTypeTicks (rather than hardcoded tick counts) so it can never
// drift from the engine's own duration math; simpler/plainer tokens (a
// straight quarter note over an equivalent dotted-triplet spelling) are
// preferred by listing them first and skipping any later entry whose tick
// count a prior one already claimed.
var lengthTable = buildLengthTable()

func buildLengthTable() []lenTok {
	order := []struct {
		length int
		dotted bool
	}{
		{1, false}, {2, false}, {4, false}, {8, false}, {16, false}, {32, false},
		{1, true}, {2, true}, {4, true}, {8, true}, {16, true}, {32, true},
		{6, false}, {12, false}, {24, false},
	}
	seen := make(map[int]bool)
	var toks []lenTok
	for _, o := range order {
		noteType := fmt.Sprintf("NOTE%d", o.length)
		if o.dotted {
			noteType += "_DOT"
		}
		ticks, err := common.NoteTypeTicks(noteType)
		if err != nil || seen[ticks] {
			continue
		}
		seen[ticks] = true
		toks = append(toks, lenTok{length: o.length, dotted: o.dotted, ticks: ticks})
	}
	sort.Slice(toks, func(i, j int) bool { return toks[i].ticks > toks[j].ticks })
	return toks
}

// MinUnitTicks is the shortest duration (in ticks) any MML length token in
// lengthTable can represent - a convenient stand-in duration when a caller
// needs "a nominal short length" (e.g. a OneShot trigger's own placeholder
// length, since the sample plays to its own natural end regardless).
func MinUnitTicks() int {
	return lengthTable[len(lengthTable)-1].ticks
}

// decomposeTicks greedily splits a duration in ticks into the fewest MML
// length tokens (each drawn from lengthTable, largest-first) summing to at
// most ticks; a remainder smaller than the smallest representable length is
// simply dropped (sub-32nd-note rounding noise from the source MIDI file's
// own timing). A duration too small to represent at all still yields one
// minimal-length token, so every note/rest/trigger produces something.
func decomposeTicks(ticks int) []lenTok {
	if ticks <= 0 {
		return nil
	}
	var out []lenTok
	remaining := ticks
	for len(out) < 256 && remaining > 0 {
		picked := false
		for _, tok := range lengthTable {
			if tok.ticks <= remaining {
				out = append(out, tok)
				remaining -= tok.ticks
				picked = true
				break
			}
		}
		if !picked {
			break
		}
	}
	if len(out) == 0 {
		out = append(out, lengthTable[len(lengthTable)-1])
	}
	return out
}

func lenStr(length int, dotted bool) string {
	s := strconv.Itoa(length)
	if dotted {
		s += "."
	}
	return s
}

var pitchClassNames = [12]string{"C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B"}

// NoteNameParts splits a MIDI note number into its MML letter(+accidental)
// and octave, matching go-fmml's C4==MIDI 60 convention.
func NoteNameParts(midiNote uint8) (letter string, octave int) {
	letter = pitchClassNames[midiNote%12]
	octave = int(midiNote)/12 - 1
	return
}

// gateIndex snaps a note's actual sounding ratio (soundTicks of slotTicks)
// to one of MML's four staccato levels (Q1=25% .. Q4=100%).
func gateIndex(soundTicks, slotTicks int) int {
	if slotTicks <= 0 {
		return 4
	}
	ratio := float64(soundTicks) / float64(slotTicks)
	if ratio > 1 {
		ratio = 1
	}
	if ratio < 0 {
		ratio = 0
	}
	idx := int(math.Round(ratio * 4))
	if idx < 1 {
		idx = 1
	}
	if idx > 4 {
		idx = 4
	}
	return idx
}

func writeNoteToken(sb *strings.Builder, midiNote uint8, tok lenTok) {
	letter, oct := NoteNameParts(midiNote)
	fmt.Fprintf(sb, "O%d%s%s", oct, letter, lenStr(tok.length, tok.dotted))
}

// EmitRest writes ticks of silence as one or more "R<len>" tokens.
func EmitRest(sb *strings.Builder, ticks int) {
	for _, tok := range decomposeTicks(ticks) {
		fmt.Fprintf(sb, " R%s", lenStr(tok.length, tok.dotted))
	}
}

// EmitMelodicSlot writes one note or chord occupying slotTicks, tied ('&')
// across as many length tokens as decomposeTicks needs, with a single Q
// (staccato) setting placed immediately before the final tied segment -
// the segment whose gate actually governs the whole tied note's release.
func EmitMelodicSlot(sb *strings.Builder, pitches []uint8, slotTicks int, soundTicks int) {
	tokens := decomposeTicks(slotTicks)
	sumTicks := 0
	for _, t := range tokens {
		sumTicks += t.ticks
	}
	qIdx := gateIndex(soundTicks, sumTicks)
	isChord := len(pitches) > 1

	for i, tok := range tokens {
		last := i == len(tokens)-1
		if last {
			fmt.Fprintf(sb, " Q%d", qIdx)
		}
		sb.WriteString(" ")
		if isChord {
			sb.WriteString("[")
			for _, p := range pitches {
				writeNoteToken(sb, p, tok)
			}
			sb.WriteString("]")
		} else {
			writeNoteToken(sb, pitches[0], tok)
		}
		if !last {
			sb.WriteString(" &")
		}
	}
}

// EmitOnsetGap writes a OneShot PCM part's trigger-plus-silence span: the
// first decomposed length token is the 'X' trigger, any remaining tokens
// (the gap's leftover length once ticks exceeds the longest single MML
// length) are plain 'R' rests.
func EmitOnsetGap(sb *strings.Builder, ticks int) {
	for i, tok := range decomposeTicks(ticks) {
		cmd := "R"
		if i == 0 {
			cmd = "X"
		}
		fmt.Fprintf(sb, " %s%s", cmd, lenStr(tok.length, tok.dotted))
	}
}

// WrapMML re-flows an already space-separated MML token stream onto
// multiple lines no wider than width, purely for readability: sectionize
// (go-fmml/analyze) strips whitespace per line and concatenates lines
// directly, so replacing a token-separating space with a newline changes
// nothing about how the file parses.
func WrapMML(s string, width int) string {
	fields := strings.Fields(s)
	var sb strings.Builder
	lineLen := 0
	for i, f := range fields {
		if i > 0 {
			if lineLen+1+len(f) > width {
				sb.WriteString("\n  ")
				lineLen = 0
			} else {
				sb.WriteString(" ")
				lineLen++
			}
		}
		sb.WriteString(f)
		lineLen += len(f)
	}
	return sb.String()
}
