/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package fmcore

import (
	"math"
	"testing"
)

func TestLookupWaveW1IsUnchangedSine(t *testing.T) {
	for _, ph := range []float64{0, 0.1, 0.25, 0.5, 0.75, 0.9} {
		got := lookupWave(1, ph)
		want := math.Sin(2 * math.Pi * ph)
		if math.Abs(got-want) > 0.01 {
			t.Fatalf("lookupWave(1, %v) = %v, want ~%v (W1 must stay the original sine)", ph, got, want)
		}
	}
}

func TestLookupWaveInvalidSelectorDefaultsToW1(t *testing.T) {
	if got, want := lookupWave(0, 0.25), lookupWave(1, 0.25); got != want {
		t.Fatalf("wave 0 (unset) = %v, want same as W1 %v", got, want)
	}
	if got, want := lookupWave(9, 0.25), lookupWave(1, 0.25); got != want {
		t.Fatalf("wave 9 (out of range) = %v, want same as W1 %v", got, want)
	}
}

func TestLookupWaveW2IsHalfRectified(t *testing.T) {
	if v := lookupWave(2, 0.75); math.Abs(v) > 0.02 {
		t.Fatalf("W2 at phase 0.75 (negative half of sine) should be ~0, got %v", v)
	}
	if got, want := lookupWave(2, 0.25), lookupWave(1, 0.25); math.Abs(got-want) > 0.02 {
		t.Fatalf("W2 at phase 0.25 (positive half of sine) should match the sine, got %v want ~%v", got, want)
	}
}

func TestPitchGlideOffsetAtLinearRamp(t *testing.T) {
	g := pitchGlide{active: true, startSample: 100, durSamples: 100, startSemitones: -12, endSemitones: 0}
	if v := g.offsetAt(50); v != -12 {
		t.Fatalf("before the glide starts, expected the start value -12, got %v", v)
	}
	if v := g.offsetAt(150); v != -6 {
		t.Fatalf("halfway through the glide, expected -6, got %v", v)
	}
	if v := g.offsetAt(300); v != 0 {
		t.Fatalf("after the glide ends, expected the end value 0, got %v", v)
	}
}

func TestPitchGlideInactiveOffsetIsZero(t *testing.T) {
	var g pitchGlide
	if v := g.offsetAt(1000); v != 0 {
		t.Fatalf("an inactive glide should contribute no offset, got %v", v)
	}
}

// TestTriggerNoteAttackBendGlidesFromOffsetToWrittenPitch covers Phase 5's
// pitch-bend-up/down and portamento notation, both of which are resolved
// into the same attack-glide mechanism (see NoteBend's doc comment): the
// note should start bend.attackOffsetSemitones away from its own written
// pitch and glide linearly back to it over attackSamples.
func TestTriggerNoteAttackBendGlidesFromOffsetToWrittenPitch(t *testing.T) {
	e := newTestEngine(44100)
	e.RegisterVoice(1, testVoice())
	_ = e.SetPart(0, 1, 1.0, 0.0)

	bend := resolvedBend{attackOffsetSemitones: -12, attackSamples: 100}
	if _, err := e.triggerNote(0, 69, 0, 100, bend); err != nil { // A4, sustained
		t.Fatalf("triggerNote failed: %v", err)
	}

	n := e.parts[0].notes[0]
	wantTarget := noteToFreq(69)
	wantStart := wantTarget / 2 // one octave (12 semitones) below

	if got := n.freqAt(0); math.Abs(got-wantStart) > 0.01 {
		t.Fatalf("at note start, expected freq %v (one octave below target), got %v", wantStart, got)
	}
	mid := n.freqAt(50)
	if mid <= wantStart || mid >= wantTarget {
		t.Fatalf("halfway through the glide, expected a freq strictly between %v and %v, got %v", wantStart, wantTarget, mid)
	}
	if got := n.freqAt(100); math.Abs(got-wantTarget) > 0.01 {
		t.Fatalf("once the glide finishes, expected freq %v (the written pitch), got %v", wantTarget, got)
	}
	if got := n.freqAt(500); math.Abs(got-wantTarget) > 0.01 {
		t.Fatalf("well after the glide finishes, expected it to hold at %v, got %v", wantTarget, got)
	}
}

// TestTriggerNoteReleaseBendGlidesAwayEndingAtRelease covers the '/_'/'\_'
// release pitch-bend notation: the note holds its written pitch, then
// ramps to bend.releaseOffsetSemitones away from it, finishing exactly at
// the note's own natural release point.
func TestTriggerNoteReleaseBendGlidesAwayEndingAtRelease(t *testing.T) {
	e := newTestEngine(44100)
	e.RegisterVoice(1, testVoice())
	_ = e.SetPart(0, 1, 1.0, 0.0)

	bend := resolvedBend{releaseOffsetSemitones: 12, releaseSamples: 50}
	if _, err := e.triggerNote(0, 60, 1000, 100, bend); err != nil {
		t.Fatalf("triggerNote failed: %v", err)
	}

	n := e.parts[0].notes[0]
	wantBase := noteToFreq(60)
	wantAtRelease := wantBase * 2 // 12 semitones up = one octave

	if got := n.freqAt(n.releaseAtSample - 60); math.Abs(got-wantBase) > 0.01 {
		t.Fatalf("before the release-glide window starts, expected the written pitch %v, got %v", wantBase, got)
	}
	if got := n.freqAt(n.releaseAtSample); math.Abs(got-wantAtRelease) > 0.01 {
		t.Fatalf("exactly at the note's release point, expected the bent pitch %v, got %v", wantAtRelease, got)
	}
}

// TestGlideNoteOnContinuesSameNoteWithoutRetriggering covers the user's
// explicit requirement that portamento be true legato: the previous
// note's envelope/operators must keep running completely undisturbed
// (same NoteID, same slice entry, same envelope stage/level) - only its
// target pitch and release timing change.
func TestGlideNoteOnContinuesSameNoteWithoutRetriggering(t *testing.T) {
	e := newTestEngine(44100)
	e.RegisterVoice(1, testVoice())
	_ = e.SetPart(0, 1, 1.0, 0.0)

	id, err := e.triggerNote(0, 60, 0, 100, resolvedBend{}) // C4, sustained
	if err != nil {
		t.Fatalf("triggerNote failed: %v", err)
	}

	// Let the envelope progress well past its attack stage before gliding.
	buf := make([]byte, bytesPerFrame*2000)
	if _, err := e.render(buf); err != nil {
		t.Fatalf("render failed: %v", err)
	}

	if len(e.parts[0].notes) != 1 {
		t.Fatalf("expected 1 note before the glide, got %d", len(e.parts[0].notes))
	}
	before := e.parts[0].notes[0]
	carrier := algorithms[before.voice.Algorithm].carriers[0]
	stageBefore, levelBefore := before.ops[carrier].eg.stage, before.ops[carrier].eg.level
	if stageBefore == stageAttack {
		t.Fatalf("test setup: expected the envelope to have left its attack stage by now")
	}

	gotID, err := e.glideNoteOn(0, 64, 0, 90, resolvedBend{attackOffsetSemitones: -4, attackSamples: 200}) // -> E4
	if err != nil {
		t.Fatalf("glideNoteOn failed: %v", err)
	}
	if gotID != id {
		t.Fatalf("expected glideNoteOn to continue the same NoteID %v, got %v", id, gotID)
	}
	if len(e.parts[0].notes) != 1 {
		t.Fatalf("expected the glide to still be exactly 1 note (no retrigger), got %d", len(e.parts[0].notes))
	}
	after := e.parts[0].notes[0]
	if after != before {
		t.Fatalf("expected glideNoteOn to mutate the existing *runningNote in place, got a different pointer")
	}
	if after.ops[carrier].eg.stage != stageBefore || after.ops[carrier].eg.level != levelBefore {
		t.Fatalf("expected the envelope to be completely undisturbed by the glide, stage %v->%v level %v->%v",
			stageBefore, after.ops[carrier].eg.stage, levelBefore, after.ops[carrier].eg.level)
	}

	wantTarget := noteToFreq(64)
	if got := after.freqAt(e.sampleClock); math.Abs(got-wantTarget/math.Pow(2, 4.0/12)) > 0.5 {
		t.Fatalf("expected the glide to start near the previous note's own pitch, got freq %v", got)
	}
	if got := after.freqAt(e.sampleClock + 200); math.Abs(got-wantTarget) > 0.01 {
		t.Fatalf("expected the glide to finish at the new target %v, got %v", wantTarget, got)
	}
}

// TestGlideNoteOnFallsBackToFreshAttackWhenNothingToGlideFrom covers the
// case where the previous note has already finished (e.g. a very short
// gap or a stolen voice) by the time the portamento note fires: there is
// nothing to continue, so it must fall back to an ordinary fresh attack.
func TestGlideNoteOnFallsBackToFreshAttackWhenNothingToGlideFrom(t *testing.T) {
	e := newTestEngine(44100)
	e.RegisterVoice(1, testVoice())
	_ = e.SetPart(0, 1, 1.0, 0.0)

	id, err := e.glideNoteOn(0, 60, 0, 100, resolvedBend{attackOffsetSemitones: -2, attackSamples: 100})
	if err != nil {
		t.Fatalf("glideNoteOn failed: %v", err)
	}
	if id == 0 {
		t.Fatalf("expected a valid NoteID from the fresh-attack fallback")
	}
	if len(e.parts[0].notes) != 1 {
		t.Fatalf("expected exactly 1 (freshly attacked) note, got %d", len(e.parts[0].notes))
	}
	n := e.parts[0].notes[0]
	carrier := algorithms[n.voice.Algorithm].carriers[0]
	if n.ops[carrier].eg.stage != stageAttack {
		t.Fatalf("expected the fallback note to start a fresh attack envelope, got stage %v", n.ops[carrier].eg.stage)
	}
}

func TestNoteOnRowHasNoBendByDefault(t *testing.T) {
	e := newTestEngine(44100)
	e.RegisterVoice(1, testVoice())
	_ = e.SetPart(0, 1, 1.0, 0.0)

	if _, err := e.NoteOnRow(0, 69, 0, 100); err != nil {
		t.Fatalf("NoteOnRow failed: %v", err)
	}
	n := e.parts[0].notes[0]
	want := noteToFreq(69)
	if got := n.freqAt(0); got != want {
		t.Fatalf("a plain NoteOnRow should have no pitch glide at all, got freq %v want %v", got, want)
	}
}
