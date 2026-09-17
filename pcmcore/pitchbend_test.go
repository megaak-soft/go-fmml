/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package pcmcore

import (
	"math"
	"testing"
)

// TestLongNoteAttackBendGlidesFromOffsetToWrittenPitch covers Phase 5's
// pitch-bend/portamento notation applied to a Long PCM note: the note's
// playback pitch ratio should start bend.attackOffsetSemitones away from
// its own written pitch and glide linearly back to it over attackSamples.
func TestLongNoteAttackBendGlidesFromOffsetToWrittenPitch(t *testing.T) {
	e := NewEngine(44100)
	e.RegisterVoice(1, longVoice(false))
	_ = e.SetPart('A', 1, 1.0, 0.0)

	bend := resolvedBend{attackOffsetSemitones: -12, attackSamples: 100}
	if err := e.noteOnLongRow('A', 69, 0, 100, bend); err != nil { // A4 == BaseNote, ratio 1
		t.Fatalf("noteOnLongRow failed: %v", err)
	}

	n := e.parts['A'].notes[0]
	wantTarget := 1.0
	wantStart := wantTarget / 2

	if got := n.pitchRatioAt(0); math.Abs(got-wantStart) > 0.001 {
		t.Fatalf("at note start, expected pitch ratio %v (one octave below target), got %v", wantStart, got)
	}
	mid := n.pitchRatioAt(50)
	if mid <= wantStart || mid >= wantTarget {
		t.Fatalf("halfway through the glide, expected a ratio strictly between %v and %v, got %v", wantStart, wantTarget, mid)
	}
	if got := n.pitchRatioAt(100); math.Abs(got-wantTarget) > 0.001 {
		t.Fatalf("once the glide finishes, expected ratio %v (the written pitch), got %v", wantTarget, got)
	}
}

// TestLongNoteReleaseBendGlidesAwayEndingAtRelease covers the release
// pitch-bend notation applied to a Long PCM note.
func TestLongNoteReleaseBendGlidesAwayEndingAtRelease(t *testing.T) {
	e := NewEngine(44100)
	e.RegisterVoice(1, longVoice(false))
	_ = e.SetPart('A', 1, 1.0, 0.0)

	bend := resolvedBend{releaseOffsetSemitones: 12, releaseSamples: 50}
	if err := e.noteOnLongRow('A', 69, 1000, 100, bend); err != nil {
		t.Fatalf("noteOnLongRow failed: %v", err)
	}

	n := e.parts['A'].notes[0]
	wantBase := 1.0
	wantAtRelease := 2.0

	if got := n.pitchRatioAt(n.releaseAtSample - 60); math.Abs(got-wantBase) > 0.001 {
		t.Fatalf("before the release-glide window starts, expected ratio %v, got %v", wantBase, got)
	}
	if got := n.pitchRatioAt(n.releaseAtSample); math.Abs(got-wantAtRelease) > 0.001 {
		t.Fatalf("exactly at the note's release point, expected ratio %v, got %v", wantAtRelease, got)
	}
}

// TestGlideNoteOnLongContinuesSameNoteWithoutRetriggering covers true
// legato for a Long PCM note: the previous note's envelope/playback
// position must keep running undisturbed - only its target pitch ratio
// and release timing change.
func TestGlideNoteOnLongContinuesSameNoteWithoutRetriggering(t *testing.T) {
	e := NewEngine(44100)
	e.RegisterVoice(1, longVoice(false))
	_ = e.SetPart('A', 1, 1.0, 0.0)

	if err := e.noteOnLongRow('A', 69, 0, 100, resolvedBend{}); err != nil { // A4 == BaseNote
		t.Fatalf("noteOnLongRow failed: %v", err)
	}
	before := e.parts['A'].notes[0]
	for i := 0; i < 2000; i++ {
		before.render(44100, int64(i))
	}
	stageBefore, levelBefore, posBefore := before.env.stage, before.env.level, before.pos

	if err := e.glideNoteOnLong('A', 74, 0, 90, resolvedBend{attackOffsetSemitones: -5, attackSamples: 200}); err != nil { // -> D5
		t.Fatalf("glideNoteOnLong failed: %v", err)
	}
	if len(e.parts['A'].notes) != 1 {
		t.Fatalf("expected the glide to still be exactly 1 note (no retrigger), got %d", len(e.parts['A'].notes))
	}
	after := e.parts['A'].notes[0]
	if after != before {
		t.Fatalf("expected glideNoteOnLong to mutate the existing *runningNote in place, got a different pointer")
	}
	if after.env.stage != stageBefore || after.env.level != levelBefore || after.pos != posBefore {
		t.Fatalf("expected the envelope/playback position to be undisturbed by the glide")
	}

	wantTarget := math.Pow(2, (74.0-69.0)/12)
	if got := after.pitchRatioAt(2000 + 200); math.Abs(got-wantTarget) > 0.001 {
		t.Fatalf("expected the glide to finish at the new target ratio %v, got %v", wantTarget, got)
	}
}

func TestGlideNoteOnLongFallsBackToFreshAttackWhenNothingToGlideFrom(t *testing.T) {
	e := NewEngine(44100)
	e.RegisterVoice(1, longVoice(false))
	_ = e.SetPart('A', 1, 1.0, 0.0)

	if err := e.glideNoteOnLong('A', 69, 0, 100, resolvedBend{attackOffsetSemitones: -2, attackSamples: 100}); err != nil {
		t.Fatalf("glideNoteOnLong failed: %v", err)
	}
	if len(e.parts['A'].notes) != 1 {
		t.Fatalf("expected exactly 1 (freshly attacked) note, got %d", len(e.parts['A'].notes))
	}
	n := e.parts['A'].notes[0]
	if n.env.stage != stageAttack {
		t.Fatalf("expected the fallback note to start a fresh attack envelope, got stage %v", n.env.stage)
	}
}

func TestOneShotIgnoresPitchGlide(t *testing.T) {
	e := NewEngine(44100)
	e.RegisterVoice(1, oneShotVoice())
	_ = e.SetPart('A', 1, 1.0, 0.0)
	if err := e.TriggerOneShot('A', 36, 127); err != nil {
		t.Fatalf("TriggerOneShot failed: %v", err)
	}
	n := e.parts['A'].notes[0]
	if got := n.pitchRatioAt(1000000); got != 1 {
		t.Fatalf("a oneShot note's pitch ratio should always be 1 regardless of glide state, got %v", got)
	}
}
