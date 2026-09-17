/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package fmcore

import "github.com/megaak-soft/go-fmml/common"

// resolveBend converts a NoteBend's tick durations into a resolvedBend's
// sample counts against the engine's sample rate and common.Tempo's
// current value - done at fire time (see fireScheduled), the same moment
// a note's own durationMs is resolved, so a mid-playback tempo change
// affects both consistently.
func (e *Engine) resolveBend(b NoteBend) resolvedBend {
	var rb resolvedBend
	rb.attackOffsetSemitones = b.AttackOffsetSemitones
	rb.releaseOffsetSemitones = b.ReleaseOffsetSemitones
	rb.portamento = b.Portamento
	if b.AttackTicks > 0 {
		rb.attackSamples = common.TicksToSamples(b.AttackTicks, common.Tempo, e.sampleRate)
	}
	if b.ReleaseTicks > 0 {
		rb.releaseSamples = common.TicksToSamples(b.ReleaseTicks, common.Tempo, e.sampleRate)
	}
	return rb
}

// NoteBend describes a linear pitch-glide envelope applied to a single
// note-on (Phase 5's portamento '&&' and pitch-bend '_/','_\','/_','\_' MML
// notation, resolved by go-fmml/analyze/go-fmml/player into this
// shape). Both halves are optional and independent:
//
//   - Attack: the note starts AttackOffsetSemitones away from its own
//     written pitch and glides linearly (in semitones) back to it over
//     AttackTicks, measured from the note's own start. Portamento is
//     represented the same way, with AttackOffsetSemitones computed from
//     the pitch difference to the previous note/chord instead of an
//     explicit MML pitch-bend amount.
//   - Release: the note holds its written pitch, then over the final
//     ReleaseTicks before its own natural release it glides linearly to
//     ReleaseOffsetSemitones away from that pitch.
//
// Ticks (not samples or milliseconds) so the glide's duration is resolved
// from common.Tempo at the same fire-time moment as the note's own
// duration (see Engine.ScheduleNoteOnWithBend), never at schedule time.
// AttackTicks/ReleaseTicks <= 0 disables that half entirely.
//
// Portamento is true when this note-on is a portamento ('&&') glide-in
// from the previous still-sounding note on the same part: rather than a
// fresh attack, fireScheduled retargets that note's pitch in place (true
// legato - its envelope is never retriggered), using its actual live
// pitch as the glide's start. AttackOffsetSemitones/AttackTicks are still
// carried and used as a fresh-attack fallback for the (rare) case where
// there's no longer a note to glide from - e.g. it already finished
// before this fired.
type NoteBend struct {
	AttackOffsetSemitones float64
	AttackTicks           int
	Portamento            bool

	ReleaseOffsetSemitones float64
	ReleaseTicks           int

	// TieTicks is Phase 5.1's single-'&' tie (see memory.NoteBend.TieTicks
	// for the full semantics): when > 0, fireScheduled uses this note's
	// total held duration (ticks, resolved to ms via common.Tempo at fire
	// time like the note's own duration always is) instead of computing it
	// from noteType+sustain.
	TieTicks int
}

// resolvedBend is NoteBend with its tick durations already converted to
// sample counts against a concrete tempo/sample rate - the shape
// runningNote actually renders from. See fireScheduled for where that
// conversion happens.
type resolvedBend struct {
	attackOffsetSemitones float64
	attackSamples         int64 // <= 0: no attack glide
	portamento            bool

	releaseOffsetSemitones float64
	releaseSamples         int64 // <= 0: no release glide
}

// pitchGlide is one linear pitch-glide segment: the semitone offset from a
// note's own written pitch moves linearly from startSemitones (at
// startSample) to endSemitones (at startSample+durSamples), holding at
// endSemitones before/after that window.
type pitchGlide struct {
	active         bool
	startSample    int64
	durSamples     int64
	startSemitones float64
	endSemitones   float64
}

// offsetAt returns this glide's semitone offset at atSample; 0 if inactive.
func (g pitchGlide) offsetAt(atSample int64) float64 {
	if !g.active {
		return 0
	}
	if g.durSamples <= 0 {
		if atSample >= g.startSample {
			return g.endSemitones
		}
		return g.startSemitones
	}
	elapsed := atSample - g.startSample
	if elapsed <= 0 {
		return g.startSemitones
	}
	if elapsed >= g.durSamples {
		return g.endSemitones
	}
	t := float64(elapsed) / float64(g.durSamples)
	return g.startSemitones + (g.endSemitones-g.startSemitones)*t
}
