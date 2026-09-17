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

func testVoiceWithLFO(lfo LFOParams) Voice {
	v := testVoice()
	v.LFO = lfo
	return v
}

// TestLFODisabledContributesNoOffset confirms a voice with no LFO
// configured (the zero value) never applies any vibrato.
func TestLFODisabledContributesNoOffset(t *testing.T) {
	e := newTestEngine(44100)
	e.RegisterVoice(1, testVoice())
	_ = e.SetPart(0, 1, 1.0, 0.0)
	if _, err := e.triggerNote(0, 69, 0, 100, resolvedBend{}); err != nil {
		t.Fatalf("triggerNote failed: %v", err)
	}
	n := e.parts[0].notes[0]
	if got := n.lfoOffsetSemitones(1000000); got != 0 {
		t.Fatalf("expected no LFO offset with no LFO configured, got %v", got)
	}
}

// TestLFOSilentBeforeDelay confirms Phase 8's "lfoDelay" (発音してからLFO
// がかかるまでの秒数) is honored: no vibrato at all until it elapses.
func TestLFOSilentBeforeDelay(t *testing.T) {
	e := newTestEngine(44100)
	e.RegisterVoice(1, testVoiceWithLFO(LFOParams{Enabled: true, DelaySeconds: 0.4, FadeSeconds: 0.5, DepthCents: 30, RateHz: 6}))
	_ = e.SetPart(0, 1, 1.0, 0.0)
	if _, err := e.triggerNote(0, 69, 0, 100, resolvedBend{}); err != nil {
		t.Fatalf("triggerNote failed: %v", err)
	}
	n := e.parts[0].notes[0]
	if got := n.lfoOffsetSemitones(int64(0.3 * 44100)); got != 0 {
		t.Fatalf("expected 0 offset before lfoDelay (0.4s) has elapsed, got %v", got)
	}
}

// TestLFODepthFadesInThenHoldsAtFullDepth confirms Phase 8's "lfoFade"
// ramps the vibrato's depth in linearly, reaching (and then holding at)
// the full "lfoDepth" once lfoFade has elapsed.
func TestLFODepthFadesInThenHoldsAtFullDepth(t *testing.T) {
	sampleRate := 44100
	e := newTestEngine(sampleRate)
	// RateHz=0.25 gives a 4s period, so t=1s lands exactly on the first
	// quarter-period peak (sin=1); FadeSeconds=1.0 has also just finished
	// ramping in by then, so that sample should read the full documented
	// depth (200 cents = 2 semitones) exactly.
	e.RegisterVoice(1, testVoiceWithLFO(LFOParams{Enabled: true, DelaySeconds: 0, FadeSeconds: 1.0, DepthCents: 200, RateHz: 0.25}))
	_ = e.SetPart(0, 1, 1.0, 0.0)
	if _, err := e.triggerNote(0, 69, 0, 100, resolvedBend{}); err != nil {
		t.Fatalf("triggerNote failed: %v", err)
	}
	n := e.parts[0].notes[0]

	atFullDepthPeak := n.lfoOffsetSemitones(int64(1.0 * float64(sampleRate)))
	if math.Abs(atFullDepthPeak-2.0) > 0.01 {
		t.Fatalf("expected full depth (200 cents = 2 semitones) at the post-fade peak, got %v", atFullDepthPeak)
	}

	partial := n.lfoOffsetSemitones(int64(0.25 * float64(sampleRate)))
	if math.Abs(partial) >= math.Abs(atFullDepthPeak) {
		t.Fatalf("expected a mid-fade offset smaller in magnitude than the full-depth peak, got %v vs %v", partial, atFullDepthPeak)
	}
}

// TestLFOModulatesFrequency confirms the vibrato actually reaches freqAt
// (and so audibly modulates playback pitch), not just the raw
// semitone-offset calculation.
func TestLFOModulatesFrequency(t *testing.T) {
	e := newTestEngine(44100)
	e.RegisterVoice(1, testVoiceWithLFO(LFOParams{Enabled: true, DelaySeconds: 0, FadeSeconds: 0, DepthCents: 1200, RateHz: 1})) // 1 octave depth, no fade
	_ = e.SetPart(0, 1, 1.0, 0.0)
	if _, err := e.triggerNote(0, 69, 0, 100, resolvedBend{}); err != nil {
		t.Fatalf("triggerNote failed: %v", err)
	}
	n := e.parts[0].notes[0]

	base := noteToFreq(69)
	// RateHz=1 -> 1s period; quarter period (0.25s) is the sine's peak, so
	// the full 1200-cent (1 octave) depth applies: frequency should double.
	got := n.freqAt(int64(0.25 * 44100))
	want := base * 2
	if math.Abs(got-want) > 0.5 {
		t.Fatalf("expected freq ~%v (one octave up) at the vibrato's peak, got %v", want, got)
	}
}
