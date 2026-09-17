/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package pcmcore

import "math"

// forceMuteFadeMs is the fixed fade-to-silence duration a force-mute uses,
// independent of a Long note's own envelope/ReleaseRate or a OneShot's
// natural length (see runningNote.forceMute). Mirrors fmcore's identical
// constant/behavior (see this package's doc comment for why it's
// duplicated rather than shared).
const forceMuteFadeMs = 3.0

// runningNote is one currently-sounding PCM sample instance assigned to a
// part: either a OneShot trigger (plays once at its authored pitch, no
// envelope) or a Long note (pitch-shifted from BaseNote, FM-style envelope,
// optionally looped).
type runningNote struct {
	kind VoiceType
	wave *WAVData

	pos        float64 // fractional playback position, in source sample frames
	pitchRatio float64 // Long: 2^((note-baseNote)/12); OneShot: always 1

	gain      float64 // static per-note gain: part volume * setting volume/127 * velocity/127
	leftGain  float64
	rightGain float64

	env                envelope // Long only
	looping            bool
	loopStart, loopEnd int

	// lfo/sampleRate implement Phase 8's optional vibrato LFO for a Long
	// note (see lfoOffsetSemitones) - meaningless, and left at their zero
	// value, for a OneShot note. sampleRate is fixed for the engine's
	// lifetime.
	lfo        LFOParams
	sampleRate int

	releaseAtSample int64 // Long only; -1 = never automatic
	released        bool
	ended           bool // true once playback has nothing left to output

	muted           bool
	muteStartSample int64
	muteFadeSamples int64

	triggerNote     uint8 // the note number this instance was triggered by, for retrigger/steal matching
	startedAtSample int64 // Long only; when this note began, for activeNoteForPortamento's "most recent" pick

	// reverbSend is this note's own reverb send level, [0,1]. OneShot only
	// (see Engine.TriggerOneShot) - a Long note's part-level send is
	// instead applied uniformly by Engine.mixSample, since a Long part's
	// reverbSend doesn't vary per note.
	reverbSend float64

	// attackGlide/releaseGlide implement Phase 5's portamento/pitch-bend
	// (see NoteBend) for a Long note; unused for OneShot. Mirrors
	// fmcore.runningNote's identical fields/reasoning.
	attackGlide  pitchGlide
	releaseGlide pitchGlide
}

func newOneShotNote(wave *WAVData, gain float64, pan float64, triggerNote uint8, reverbSend float64) *runningNote {
	lg, rg := panGains(pan)
	return &runningNote{
		kind:            OneShot,
		wave:            wave,
		pitchRatio:      1,
		gain:            gain,
		leftGain:        lg,
		rightGain:       rg,
		releaseAtSample: -1,
		triggerNote:     triggerNote,
		reverbSend:      reverbSend,
	}
}

func newLongNote(wave *WAVData, envParams EnvelopeParams, looping bool, gain, pan float64, pitchRatio float64, lfo LFOParams, sampleRate int, startedAtSample, releaseAtSample int64, bend resolvedBend) *runningNote {
	lg, rg := panGains(pan)
	n := &runningNote{
		kind:            Long,
		wave:            wave,
		pitchRatio:      pitchRatio,
		gain:            gain,
		leftGain:        lg,
		rightGain:       rg,
		env:             newEnvelope(envParams),
		looping:         looping,
		lfo:             lfo,
		sampleRate:      sampleRate,
		releaseAtSample: releaseAtSample,
		startedAtSample: startedAtSample,
	}
	if bend.attackSamples > 0 {
		n.attackGlide = pitchGlide{
			active: true, startSample: startedAtSample, durSamples: bend.attackSamples,
			startSemitones: bend.attackOffsetSemitones, endSemitones: 0,
		}
	}
	if bend.releaseSamples > 0 && releaseAtSample >= 0 {
		n.releaseGlide = pitchGlide{
			active: true, startSample: releaseAtSample - bend.releaseSamples, durSamples: bend.releaseSamples,
			startSemitones: 0, endSemitones: bend.releaseOffsetSemitones,
		}
	}
	if looping {
		if wave.HasLoop {
			n.loopStart, n.loopEnd = wave.LoopStart, wave.LoopEnd
		} else {
			n.loopStart, n.loopEnd = 0, len(wave.Samples)
		}
	}
	return n
}

func (n *runningNote) release() {
	if n.released {
		return
	}
	n.released = true
	n.env.noteOff()
}

// forceMute begins an immediate, fixed-duration (forceMuteFadeMs) fade to
// silence, applied as a gain multiplier on top of whatever this note would
// otherwise render - a OneShot's natural playback or a Long note's own
// envelope. Used both for a Long part's non-overlap between consecutive
// notes and to silence every part cleanly on playback Stop (see
// go-fmml/player), so a note is never simply cut off mid-waveform (which
// would otherwise produce an audible click). A no-op if already muted.
func (n *runningNote) forceMute(nowSample int64, sampleRate int) {
	if n.muted {
		return
	}
	n.muted = true
	n.muteStartSample = nowSample
	n.muteFadeSamples = int64(forceMuteFadeMs / 1000 * float64(sampleRate))
	if n.muteFadeSamples < 1 {
		n.muteFadeSamples = 1
	}
	n.release()
}

// muteGain returns the [0,1] gain multiplier a force-mute (if any) applies
// at atSample; 1 if the note isn't muted.
func (n *runningNote) muteGain(atSample int64) float64 {
	if !n.muted {
		return 1
	}
	elapsed := atSample - n.muteStartSample
	if elapsed >= n.muteFadeSamples {
		return 0
	}
	if elapsed < 0 {
		return 1
	}
	return 1 - float64(elapsed)/float64(n.muteFadeSamples)
}

// muteDone reports whether a force-mute (if any) has finished fading out.
func (n *runningNote) muteDone(atSample int64) bool {
	return n.muted && atSample-n.muteStartSample >= n.muteFadeSamples
}

// pitchRatioAt returns this note's instantaneous playback pitch ratio at
// atSample, applying any active attack/release pitch-glide (see NoteBend)
// on top of the note's own written pitch ratio. OneShot notes never glide
// (CLAUDE.md disallows the notation on a OneShot part) so this is only
// meaningful for Long notes.
func (n *runningNote) pitchRatioAt(atSample int64) float64 {
	offset := n.attackGlide.offsetAt(atSample) + n.releaseGlide.offsetAt(atSample) + n.lfoOffsetSemitones(atSample)
	if offset == 0 {
		return n.pitchRatio
	}
	return n.pitchRatio * math.Pow(2, offset/12)
}

// lfoOffsetSemitones mirrors fmcore.runningNote's identical method (see its
// doc comment); a OneShot note never has n.lfo.Enabled set (see
// Engine.TriggerOneShot's newOneShotNote, which never touches n.lfo), so
// this need not special-case n.kind.
func (n *runningNote) lfoOffsetSemitones(atSample int64) float64 {
	if !n.lfo.Enabled || n.lfo.RateHz <= 0 || n.sampleRate <= 0 {
		return 0
	}
	elapsedSeconds := float64(atSample-n.startedAtSample) / float64(n.sampleRate)
	if elapsedSeconds < n.lfo.DelaySeconds {
		return 0
	}
	t := elapsedSeconds - n.lfo.DelaySeconds
	depthGain := 1.0
	if n.lfo.FadeSeconds > 0 {
		depthGain = t / n.lfo.FadeSeconds
		if depthGain > 1 {
			depthGain = 1
		}
	}
	cents := n.lfo.DepthCents * depthGain * math.Sin(2*math.Pi*n.lfo.RateHz*t)
	return cents / 100
}

// render steps the note by one sample and returns its stereo output.
func (n *runningNote) render(sampleRate int, atSample int64) (float64, float64) {
	if n.ended || n.wave == nil || len(n.wave.Samples) == 0 {
		return 0, 0
	}

	sample := n.sampleAt(n.pos)
	if n.kind == Long {
		n.pos += n.pitchRatioAt(atSample)
	} else {
		n.pos += n.pitchRatio
	}

	switch n.kind {
	case OneShot:
		if n.pos >= float64(len(n.wave.Samples)) {
			n.ended = true
		}
		out := float64(sample) * n.gain
		return out * n.leftGain, out * n.rightGain

	default: // Long
		if n.looping && n.loopEnd > n.loopStart {
			for n.pos >= float64(n.loopEnd) {
				n.pos -= float64(n.loopEnd - n.loopStart)
			}
		} else if n.pos >= float64(len(n.wave.Samples)) {
			n.release() // no more source audio: fall into release so the envelope fades to true silence
		}
		level := n.env.advance(sampleRate)
		out := float64(sample) * n.gain * level
		return out * n.leftGain, out * n.rightGain
	}
}

// sampleAt linearly interpolates the wave's samples at fractional position
// pos (in source sample frames).
func (n *runningNote) sampleAt(pos float64) float32 {
	samples := n.wave.Samples
	i0 := int(pos)
	if i0 < 0 {
		i0 = 0
	}
	if i0 >= len(samples) {
		return 0
	}
	frac := float32(pos - float64(i0))
	s0 := samples[i0]
	i1 := i0 + 1
	if n.looping && n.loopEnd > n.loopStart && i1 >= n.loopEnd {
		i1 = n.loopStart
	}
	if i1 >= len(samples) {
		return s0
	}
	s1 := samples[i1]
	return s0 + (s1-s0)*frac
}

// finished reports whether this note can be dropped from its part.
func (n *runningNote) finished() bool {
	if n.kind == OneShot {
		return n.ended
	}
	return n.env.finished()
}

// panGains converts a pan in [-1,1] (-1 = full left, 0 = center, 1 = full
// right) into equal-power left/right gain multipliers. The conversion is
// continuous (a plain sin/cos curve over the whole range), so it renders
// every intermediate position CLAUDE.md's -16..16 MML pan scale can
// express - not just hard left/center/right. Duplicated from fmcore's
// identical helper (see this package's doc comment for why).
func panGains(pan float64) (float64, float64) {
	if pan < -1 {
		pan = -1
	} else if pan > 1 {
		pan = 1
	}
	angle := (pan + 1) / 2 * (math.Pi / 2)
	return math.Cos(angle), math.Sin(angle)
}
