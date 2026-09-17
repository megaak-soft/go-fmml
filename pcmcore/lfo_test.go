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

// TestLongNoteLFODisabledContributesNoOffset confirms a Long voice with no
// LFO configured (the zero value) never applies any vibrato.
func TestLongNoteLFODisabledContributesNoOffset(t *testing.T) {
	e := NewEngine(44100)
	e.RegisterVoice(2, longVoice(false))
	_ = e.SetPart('B', 2, 1.0, 0.0)
	if err := e.NoteOnLongRow('B', 69, 0, 127); err != nil {
		t.Fatalf("NoteOnLongRow failed: %v", err)
	}
	n := e.parts['B'].notes[0]
	if got := n.lfoOffsetSemitones(1000000); got != 0 {
		t.Fatalf("expected no LFO offset with no LFO configured, got %v", got)
	}
}

// TestLongNoteLFOSilentBeforeDelay confirms Phase 8's "lfoDelay" (発音して
// からLFOがかかるまでの秒数) is honored: no vibrato at all until it elapses.
func TestLongNoteLFOSilentBeforeDelay(t *testing.T) {
	e := NewEngine(44100)
	voice := longVoice(false)
	voice.LFO = LFOParams{Enabled: true, DelaySeconds: 0.4, FadeSeconds: 0.5, DepthCents: 30, RateHz: 6}
	e.RegisterVoice(2, voice)
	_ = e.SetPart('B', 2, 1.0, 0.0)
	if err := e.NoteOnLongRow('B', 69, 0, 127); err != nil {
		t.Fatalf("NoteOnLongRow failed: %v", err)
	}
	n := e.parts['B'].notes[0]
	if got := n.lfoOffsetSemitones(int64(0.3 * 44100)); got != 0 {
		t.Fatalf("expected 0 offset before lfoDelay (0.4s) has elapsed, got %v", got)
	}
}

// TestLongNoteLFODepthFadesInThenHoldsAtFullDepth confirms Phase 8's
// "lfoFade" ramps the vibrato's depth in linearly, reaching (and then
// holding at) the full "lfoDepth" once lfoFade has elapsed.
func TestLongNoteLFODepthFadesInThenHoldsAtFullDepth(t *testing.T) {
	sampleRate := 44100
	e := NewEngine(sampleRate)
	voice := longVoice(false)
	// RateHz=0.25 gives a 4s period, so t=1s lands exactly on the first
	// quarter-period peak (sin=1); FadeSeconds=1.0 has also just finished
	// ramping in by then, so that sample should read the full documented
	// depth (200 cents = 2 semitones) exactly.
	voice.LFO = LFOParams{Enabled: true, DelaySeconds: 0, FadeSeconds: 1.0, DepthCents: 200, RateHz: 0.25}
	e.RegisterVoice(2, voice)
	_ = e.SetPart('B', 2, 1.0, 0.0)
	if err := e.NoteOnLongRow('B', 69, 0, 127); err != nil {
		t.Fatalf("NoteOnLongRow failed: %v", err)
	}
	n := e.parts['B'].notes[0]

	atFullDepthPeak := n.lfoOffsetSemitones(int64(1.0 * float64(sampleRate)))
	if math.Abs(atFullDepthPeak-2.0) > 0.01 {
		t.Fatalf("expected full depth (200 cents = 2 semitones) at the post-fade peak, got %v", atFullDepthPeak)
	}

	partial := n.lfoOffsetSemitones(int64(0.25 * float64(sampleRate)))
	if math.Abs(partial) >= math.Abs(atFullDepthPeak) {
		t.Fatalf("expected a mid-fade offset smaller in magnitude than the full-depth peak, got %v vs %v", partial, atFullDepthPeak)
	}
}

// TestLongNoteLFOModulatesPitchRatio confirms the vibrato actually reaches
// pitchRatioAt (and so audibly modulates playback pitch), not just the raw
// semitone-offset calculation.
func TestLongNoteLFOModulatesPitchRatio(t *testing.T) {
	e := NewEngine(44100)
	voice := longVoice(false)
	voice.LFO = LFOParams{Enabled: true, DelaySeconds: 0, FadeSeconds: 0, DepthCents: 1200, RateHz: 1} // 1 octave depth, no fade
	e.RegisterVoice(2, voice)
	_ = e.SetPart('B', 2, 1.0, 0.0)
	if err := e.NoteOnLongRow('B', 69, 0, 127); err != nil {
		t.Fatalf("NoteOnLongRow failed: %v", err)
	}
	n := e.parts['B'].notes[0]

	// RateHz=1 -> 1s period; quarter period (0.25s) is the sine's peak, so
	// the full 1200-cent (1 octave) depth applies: pitchRatio should
	// double.
	got := n.pitchRatioAt(int64(0.25 * 44100))
	if math.Abs(got-2.0) > 0.01 {
		t.Fatalf("expected pitchRatio ~2.0 (one octave up) at the vibrato's peak, got %v", got)
	}
}

// TestOneShotNoteNeverAppliesLFO confirms CLAUDE.md's "PCM音色でLFOを掛け
// られるのはvoiceTypeがlongタイプのみとする(oneShotの場合は無視)": a
// OneShot note's lfoOffsetSemitones is always 0 - newOneShotNote never
// populates n.lfo, so even accidentally setting one on the Voice can't leak
// through.
func TestOneShotNoteNeverAppliesLFO(t *testing.T) {
	e := NewEngine(44100)
	voice := oneShotVoice()
	voice.LFO = LFOParams{Enabled: true, DelaySeconds: 0, FadeSeconds: 0, DepthCents: 1200, RateHz: 1}
	e.RegisterVoice(1, voice)
	_ = e.SetPart('A', 1, 1.0, 0.0)
	if err := e.TriggerOneShot('A', 36, 127); err != nil {
		t.Fatalf("TriggerOneShot failed: %v", err)
	}
	n := e.parts['A'].notes[0]
	if got := n.lfoOffsetSemitones(int64(0.25 * 44100)); got != 0 {
		t.Fatalf("expected a OneShot note to never apply LFO even if the voice sets one, got offset %v", got)
	}
}
