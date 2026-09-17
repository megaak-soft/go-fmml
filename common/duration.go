/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package common

import (
	"fmt"
	"math"
	"strings"
)

// baseNoteBeats maps a note type to its duration in quarter-note beats.
// NOTE6/NOTE12/NOTE24 are triplet lengths (CLAUDE.md's MML length digits
// 6/12/24): a quarter/eighth/sixteenth-note triplet, i.e. 3 of them exactly
// fill the duration 2 of the corresponding power-of-2 length would (a
// NOTE6 triplet is 2/3 of a NOTE4 beat, matching the general beats=4/length
// pattern every other entry here follows).
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

// NoteTypeBeats converts a note type name (e.g. "NOTE4", "NOTE8_DOT") into
// its duration in quarter-note beats; a "_DOT" suffix multiplies the base
// note's duration by 1.5.
func NoteTypeBeats(noteType string) (float64, error) {
	key := noteType
	dotted := strings.HasSuffix(noteType, "_DOT")
	if dotted {
		key = strings.TrimSuffix(noteType, "_DOT")
	}
	beats, ok := baseNoteBeats[key]
	if !ok {
		return 0, fmt.Errorf("common: unknown note type %q", noteType)
	}
	if dotted {
		beats *= 1.5
	}
	return beats, nil
}

// NoteTypeDurationMs derives a duration in milliseconds for noteType at the
// given tempo (beats per minute) and sustain percentage (100 = the note
// type's full length, 50 = half of it). Shared by fmcore.Engine.NoteOn and
// the MML sequencer (go-fmml/analyze, go-fmml/player) so both agree on
// timing from the same tempo value.
func NoteTypeDurationMs(tempo float64, noteType string, sustainPercent int) (int, error) {
	beats, err := NoteTypeBeats(noteType)
	if err != nil {
		return 0, err
	}
	if tempo <= 0 {
		tempo = 120
	}
	quarterNoteMs := 60000 / tempo
	full := beats * quarterNoteMs
	return int(full * float64(sustainPercent) / 100), nil
}

// TicksPerQuarterNote is the sequencer's timing resolution: how many
// integer "ticks" make up one quarter-note beat. 48 ticks/quarter note is
// 192 ticks per 4/4 measure, and divides every note length this engine
// supports (1,2,4,8,16,32, each optionally dotted, plus the 6,12,24 triplet
// lengths) with zero remainder, so a part's timeline can be built entirely
// in integer ticks with no per-note rounding error to accumulate over a
// long sequence. Real-time (millisecond/sample) positions are derived from
// ticks only once, at playback time, from each event's absolute tick count
// - never by adding up already-rounded increments - so they can't drift
// either.
const TicksPerQuarterNote = 48

// baseNoteTicks maps a note type to its duration in ticks, at
// TicksPerQuarterNote resolution. NOTE6/NOTE12/NOTE24 are triplet lengths
// (see baseNoteBeats's identical entries) - each divides
// TicksPerQuarterNote*4 (one whole note) with zero remainder, same as
// every power-of-2 length here.
var baseNoteTicks = map[string]int{
	"NOTE1":  TicksPerQuarterNote * 4,
	"NOTE2":  TicksPerQuarterNote * 2,
	"NOTE4":  TicksPerQuarterNote,
	"NOTE6":  TicksPerQuarterNote * 4 / 6,
	"NOTE8":  TicksPerQuarterNote / 2,
	"NOTE12": TicksPerQuarterNote * 4 / 12,
	"NOTE16": TicksPerQuarterNote / 4,
	"NOTE24": TicksPerQuarterNote * 4 / 24,
	"NOTE32": TicksPerQuarterNote / 8,
}

// NoteTypeTicks converts a note type name (e.g. "NOTE4", "NOTE8_DOT") into
// its exact integer duration in ticks; a "_DOT" suffix multiplies the base
// note's duration by 1.5 (exact for every supported note length at this
// resolution).
func NoteTypeTicks(noteType string) (int, error) {
	key := noteType
	dotted := strings.HasSuffix(noteType, "_DOT")
	if dotted {
		key = strings.TrimSuffix(noteType, "_DOT")
	}
	ticks, ok := baseNoteTicks[key]
	if !ok {
		return 0, fmt.Errorf("common: unknown note type %q", noteType)
	}
	if dotted {
		ticks = ticks * 3 / 2
	}
	return ticks, nil
}

// TicksToSamples converts an absolute tick count into an absolute sample
// count at the given tempo (BPM) and sample rate, computed directly from
// the tick count (never by summing already-rounded increments), so it
// carries no accumulated rounding error regardless of how large ticks
// grows.
func TicksToSamples(ticks int, tempo float64, sampleRate int) int64 {
	if tempo <= 0 {
		tempo = 120
	}
	samplesPerTick := 60.0 / tempo / float64(TicksPerQuarterNote) * float64(sampleRate)
	return int64(math.Round(float64(ticks) * samplesPerTick))
}

// SamplesToTicks is TicksToSamples's inverse: it converts an absolute
// sample count into the nearest absolute tick count at the given tempo and
// sample rate.
func SamplesToTicks(samples int64, tempo float64, sampleRate int) int {
	if tempo <= 0 {
		tempo = 120
	}
	samplesPerTick := 60.0 / tempo / float64(TicksPerQuarterNote) * float64(sampleRate)
	if samplesPerTick <= 0 {
		return 0
	}
	return int(math.Round(float64(samples) / samplesPerTick))
}

// SecondsToTicks converts a duration in seconds into the nearest integer
// tick count at the given tempo (BPM) - the sample-rate-independent
// counterpart to TicksToSamples, for a caller (see
// go-fmml/player.SkipPlay) that wants to seek to a position expressed in
// wall-clock seconds rather than samples.
func SecondsToTicks(seconds, tempo float64) int {
	if tempo <= 0 {
		tempo = 120
	}
	ticksPerSecond := float64(TicksPerQuarterNote) * tempo / 60
	return int(math.Round(seconds * ticksPerSecond))
}

// TempoPoint is one breakpoint in a sequence's tempo map (Phase 7's
// [conductor] tempo automation): the tempo (BPM) takes effect starting at
// AtTick and holds until the next breakpoint (or indefinitely, for the
// last one). A map's first entry is always at AtTick 0.
type TempoPoint struct {
	AtTick int
	BPM    float64
}

// samplesPerTick is TicksToSamples's inner per-tick conversion factor,
// shared by every tempo-map-aware conversion below so a single-breakpoint
// map reproduces TicksToSamples/SamplesToTicks exactly.
func samplesPerTick(tempo float64, sampleRate int) float64 {
	if tempo <= 0 {
		tempo = 120
	}
	return 60.0 / tempo / float64(TicksPerQuarterNote) * float64(sampleRate)
}

// TicksToSamplesFromMap is TicksToSamples's tempo-map-aware counterpart: it
// walks tempoMap's breakpoints so a tick count occurring after a tempo
// change is converted using every tempo segment it actually passed through,
// rather than a single fixed BPM. A one-entry (or empty) map degenerates to
// exactly the same arithmetic as TicksToSamples.
func TicksToSamplesFromMap(ticks int, tempoMap []TempoPoint, sampleRate int) int64 {
	if len(tempoMap) == 0 {
		return TicksToSamples(ticks, 120, sampleRate)
	}
	var samples float64
	prevTick := tempoMap[0].AtTick
	prevTempo := tempoMap[0].BPM
	for i := 1; i < len(tempoMap) && tempoMap[i].AtTick <= ticks; i++ {
		samples += float64(tempoMap[i].AtTick-prevTick) * samplesPerTick(prevTempo, sampleRate)
		prevTick = tempoMap[i].AtTick
		prevTempo = tempoMap[i].BPM
	}
	samples += float64(ticks-prevTick) * samplesPerTick(prevTempo, sampleRate)
	return int64(math.Round(samples))
}

// SamplesToTicksFromMap is SamplesToTicks's tempo-map-aware counterpart,
// the inverse of TicksToSamplesFromMap.
func SamplesToTicksFromMap(samples int64, tempoMap []TempoPoint, sampleRate int) int {
	if len(tempoMap) == 0 {
		return SamplesToTicks(samples, 120, sampleRate)
	}
	var elapsed int64
	prevTick := tempoMap[0].AtTick
	prevTempo := tempoMap[0].BPM
	for i := 1; i < len(tempoMap); i++ {
		segSamples := int64(math.Round(float64(tempoMap[i].AtTick-prevTick) * samplesPerTick(prevTempo, sampleRate)))
		if elapsed+segSamples > samples {
			break
		}
		elapsed += segSamples
		prevTick = tempoMap[i].AtTick
		prevTempo = tempoMap[i].BPM
	}
	spt := samplesPerTick(prevTempo, sampleRate)
	if spt <= 0 {
		return prevTick
	}
	return prevTick + int(math.Round(float64(samples-elapsed)/spt))
}

// TicksToMs converts a tick count into a duration in milliseconds at the
// given tempo (BPM), computed directly from the tick count (like
// TicksToSamples, never by summing already-rounded increments). Used for a
// tied note's ('&', see go-fmml/analyze's NoteBend.TieTicks) total held
// duration, which spans multiple note-length tokens and so has no single
// NoteType string to look up via NoteTypeDurationMs.
func TicksToMs(ticks int, tempo float64) float64 {
	if tempo <= 0 {
		tempo = 120
	}
	msPerTick := 60000 / tempo / float64(TicksPerQuarterNote)
	return float64(ticks) * msPerTick
}
