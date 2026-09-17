/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package pcmcore

import "github.com/megaak-soft/go-fmml/common"

// NoteBend mirrors fmcore.NoteBend for a Long PCM note (see that type's
// doc comment for the full semantics; duplicated here rather than shared
// for the same reason as this package's other fmcore-mirroring types - see
// the package doc comment). Unused for a OneShot note - CLAUDE.md disallows
// portamento/pitch-bend notation on a OneShot part.
type NoteBend struct {
	AttackOffsetSemitones float64
	AttackTicks           int
	Portamento            bool

	ReleaseOffsetSemitones float64
	ReleaseTicks           int

	// TieTicks mirrors fmcore.NoteBend.TieTicks (see memory.NoteBend.TieTicks
	// for the full semantics): when > 0, fireScheduled uses this note's
	// total held duration (ticks) instead of computing it from
	// noteType+sustain.
	TieTicks int
}

// resolvedBend is NoteBend with its tick durations already converted to
// sample counts - see Engine.resolveBend.
type resolvedBend struct {
	attackOffsetSemitones float64
	attackSamples         int64
	portamento            bool

	releaseOffsetSemitones float64
	releaseSamples         int64
}

// pitchGlide mirrors fmcore's identical type; see its doc comment.
type pitchGlide struct {
	active         bool
	startSample    int64
	durSamples     int64
	startSemitones float64
	endSemitones   float64
}

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

// resolveBend converts a NoteBend's tick durations into sample counts
// against the engine's sample rate and common.Tempo's current value at
// fire time - mirrors fmcore.Engine.resolveBend.
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
