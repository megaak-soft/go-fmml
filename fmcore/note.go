/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package fmcore

import "math"

// forceMuteFadeMs is the fixed fade-to-silence duration a force-mute uses,
// deliberately independent of any voice's own envelope/ReleaseRate (see
// runningNote.forceMute).
const forceMuteFadeMs = 3.0

// runningNote is one currently-sounding voice instance assigned to a part.
type runningNote struct {
	id              NoteID
	noteNumber      uint8
	velocity        uint8
	freq            float64
	voice           Voice
	ops             [4]operator
	sampleRate      int // fixed for the engine's lifetime; see lfoOffsetSemitones
	startedAtSample int64
	releaseAtSample int64 // sample clock value to trigger release at, -1 = never automatic
	released        bool

	muted           bool
	muteStartSample int64
	muteFadeSamples int64

	// attackGlide/releaseGlide implement Phase 5's portamento/pitch-bend
	// (see NoteBend): freqAt applies whichever is active on top of the
	// note's own written freq. The two are expected not to overlap in time
	// (attack settles to 0 before release begins ramping away from it) -
	// on a very short note where they would, their offsets simply sum.
	attackGlide  pitchGlide
	releaseGlide pitchGlide
}

func noteToFreq(note uint8) float64 {
	return 440 * math.Pow(2, (float64(note)-69)/12)
}

func newRunningNote(id NoteID, note, velocity uint8, voice Voice, sampleRate int, startedAtSample, releaseAtSample int64, bend resolvedBend) *runningNote {
	n := &runningNote{
		id:              id,
		noteNumber:      note,
		velocity:        velocity,
		freq:            noteToFreq(note),
		voice:           voice,
		sampleRate:      sampleRate,
		startedAtSample: startedAtSample,
		releaseAtSample: releaseAtSample,
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
	for i, p := range voice.Operators {
		n.ops[i] = newOperator(p)
	}
	return n
}

// freqAt returns this note's instantaneous frequency at atSample, applying
// any active attack/release pitch-glide (see NoteBend) and the voice's
// optional vibrato LFO (see lfoOffsetSemitones) on top of the note's own
// written pitch.
func (n *runningNote) freqAt(atSample int64) float64 {
	offset := n.attackGlide.offsetAt(atSample) + n.releaseGlide.offsetAt(atSample) + n.lfoOffsetSemitones(atSample)
	if offset == 0 {
		return n.freq
	}
	return n.freq * math.Pow(2, offset/12)
}

// lfoOffsetSemitones returns the voice's vibrato LFO's pitch offset (in
// semitones) at atSample (Phase 8's "lfo"/"lfoDelay"/"lfoFade"/"lfoDepth"/
// "lfoHz" voice parameters): 0 until DelaySeconds has elapsed since the
// note started, then a sine oscillation at RateHz whose depth (DepthCents)
// ramps linearly in over FadeSeconds before holding at full depth.
func (n *runningNote) lfoOffsetSemitones(atSample int64) float64 {
	lfo := n.voice.LFO
	if !lfo.Enabled || lfo.RateHz <= 0 || n.sampleRate <= 0 {
		return 0
	}
	elapsedSeconds := float64(atSample-n.startedAtSample) / float64(n.sampleRate)
	if elapsedSeconds < lfo.DelaySeconds {
		return 0
	}
	t := elapsedSeconds - lfo.DelaySeconds
	depthGain := 1.0
	if lfo.FadeSeconds > 0 {
		depthGain = t / lfo.FadeSeconds
		if depthGain > 1 {
			depthGain = 1
		}
	}
	cents := lfo.DepthCents * depthGain * math.Sin(2*math.Pi*lfo.RateHz*t)
	return cents / 100
}

// render steps the note by one sample and returns its output, scaled by
// note-on velocity.
func (n *runningNote) render(sampleRate int, atSample int64) float64 {
	alg := algorithms[n.voice.Algorithm]
	sample := renderOperators(&n.ops, alg, sampleRate, n.freqAt(atSample), n.voice.Feedback)
	return sample * (float64(n.velocity) / 127)
}

// release begins this note's envelope release stage immediately (as if its
// held duration had just ended). A no-op if the note is already releasing.
func (n *runningNote) release() {
	if n.released {
		return
	}
	n.released = true
	for i := range n.ops {
		n.ops[i].eg.noteOff()
	}
}

// forceMute begins an immediate, fixed-duration (forceMuteFadeMs) fade to
// silence, applied as a gain multiplier independent of the voice's own
// envelope/ReleaseRate. This is how the sample-accurate sequencer (see
// Engine.ScheduleForceStopPart) enforces monophonic non-overlap between
// consecutive notes/chords on a part: however long a voice's own release
// tail is configured to ring for musically, a force-mute always silences
// it within the same few milliseconds, so timing correctness never depends
// on tone-color settings. A no-op if already muted.
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

// finished reports whether every carrier operator has decayed to silence
// after release, meaning this note can be dropped from its part.
func (n *runningNote) finished() bool {
	alg := algorithms[n.voice.Algorithm]
	for _, c := range alg.carriers {
		if !n.ops[c].eg.finished() {
			return false
		}
	}
	return true
}
