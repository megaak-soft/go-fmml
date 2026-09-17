/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package fmcore

import (
	"testing"

	"github.com/megaak-soft/go-fmml/common"
)

// slowReleaseVoice has a deliberately very slow, "musical" release
// (ReleaseRate 1, the slowest available) so tests can confirm force-mute
// silences a note quickly regardless of how the voice itself is tuned.
func slowReleaseVoice() Voice {
	return Voice{
		Algorithm: 7, // all four operators are carriers
		Operators: [4]OperatorParams{
			{Multiple: 1, TotalLevel: 0, Envelope: EnvelopeParams{AttackRate: 31, Decay1Rate: 0, SustainLevel: 1, Decay2Rate: 0, ReleaseRate: 1}},
			{Multiple: 1, TotalLevel: 127, Envelope: EnvelopeParams{AttackRate: 31, Decay1Rate: 0, SustainLevel: 1, Decay2Rate: 0, ReleaseRate: 1}},
			{Multiple: 1, TotalLevel: 127, Envelope: EnvelopeParams{AttackRate: 31, Decay1Rate: 0, SustainLevel: 1, Decay2Rate: 0, ReleaseRate: 1}},
			{Multiple: 1, TotalLevel: 127, Envelope: EnvelopeParams{AttackRate: 31, Decay1Rate: 0, SustainLevel: 1, Decay2Rate: 0, ReleaseRate: 1}},
		},
	}
}

func TestScheduleNoteOnFiresAtExactSample(t *testing.T) {
	e := newTestEngine(44100)
	e.RegisterVoice(1, testVoice())
	_ = e.SetPart(0, 1, 1.0, 0.0)

	const target = int64(1000)
	if err := e.ScheduleNoteOn(target, 0, "A4", "NOTE4", 100, 100); err != nil {
		t.Fatalf("ScheduleNoteOn failed: %v", err)
	}

	buf := make([]byte, bytesPerFrame*target) // render up to, but not including, the target sample
	if _, err := e.render(buf); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	e.mu.Lock()
	n := len(e.parts[0].notes)
	e.mu.Unlock()
	if n != 0 {
		t.Fatalf("expected the note not to have started yet, got %d sounding", n)
	}

	buf2 := make([]byte, bytesPerFrame*10)
	if _, err := e.render(buf2); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	e.mu.Lock()
	n = len(e.parts[0].notes)
	e.mu.Unlock()
	if n != 1 {
		t.Fatalf("expected the note to have started exactly at its scheduled sample, got %d sounding", n)
	}
}

// TestScheduleForceStopPartIgnoresVoiceReleaseRate confirms that a
// force-muted note goes silent within the fixed forceMuteFadeMs window
// even when its voice's own ReleaseRate is set to never really decay
// (rate 1, the slowest available) - i.e. release/cutoff timing must not
// depend on tone-color settings.
func TestScheduleForceStopPartIgnoresVoiceReleaseRate(t *testing.T) {
	e := newTestEngine(44100)
	e.RegisterVoice(1, slowReleaseVoice())
	_ = e.SetPart(0, 1, 1.0, 0.0)

	if _, err := e.NoteOnRow(0, 60, 0, 100); err != nil { // sustain indefinitely
		t.Fatalf("NoteOn failed: %v", err)
	}

	// Confirm the voice is, in fact, still at meaningful volume long after
	// a normal release would have started (proves the voice really is
	// slow-release, not that the test is vacuous).
	buf := make([]byte, bytesPerFrame*4410) // 100ms
	if _, err := e.render(buf); err != nil {
		t.Fatalf("render failed: %v", err)
	}

	e.mu.Lock()
	now := e.sampleClock
	e.mu.Unlock()
	e.ScheduleForceStopPart(now, 0)

	// forceMuteFadeMs=3ms; render well past it and confirm the note is gone.
	buf2 := make([]byte, bytesPerFrame*882) // 20ms
	if _, err := e.render(buf2); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	e.mu.Lock()
	n := len(e.parts[0].notes)
	e.mu.Unlock()
	if n != 0 {
		t.Fatalf("expected force-mute to have silenced and dropped the note within ~20ms regardless of its slow ReleaseRate, but %d notes remain", n)
	}
}

// TestScheduledCEGESequenceDoesNotOverlapEvenWithSlowRelease replicates the
// reported real-world scenario: an "O4 L8 C E G E" melody (four NOTE8s at
// 150bpm, 200ms apart) played on a voice with a deliberately extreme, slow
// release, scheduled sample-accurately exactly as go-fmml/player does
// (ScheduleForceStopPart then ScheduleNoteOn at each note's own onset
// sample). Each note is expected to keep sounding for the whole of its own
// 200ms window (that's correct legato behavior, not overlap) but must be
// gone - force-muted and cleaned up - within a few ms of the next note
// starting, regardless of the voice's own (here: deliberately near-
// infinite) ReleaseRate.
func TestScheduledCEGESequenceDoesNotOverlapEvenWithSlowRelease(t *testing.T) {
	e := newTestEngine(44100)
	e.RegisterVoice(0, slowReleaseVoice())
	_ = e.SetPart(1, 0, 1.0, 0.0)

	notes := []struct {
		name string
		num  uint8
	}{
		{"C4", 60}, {"E4", 64}, {"G4", 67}, {"E4", 64},
	}

	const windowSamples = int64(0.200 * 44100) // NOTE8 @ 150bpm
	for i, n := range notes {
		atSample := int64(i) * windowSamples
		e.ScheduleForceStopPart(atSample, 1)
		if err := e.ScheduleNoteOn(atSample, 1, n.name, "NOTE8", 100, 100); err != nil {
			t.Fatalf("ScheduleNoteOn %d failed: %v", i, err)
		}
	}

	// Render up to just before each note's own onset (should be silent -
	// nothing has started yet), then up to shortly after the *next* note's
	// onset (the fixed force-mute fade should have finished, regardless of
	// this voice's near-infinite ReleaseRate, so only the new note should
	// remain sounding).
	rendered := int64(0)
	renderTo := func(target int64) {
		if target <= rendered {
			return
		}
		buf := make([]byte, bytesPerFrame*(target-rendered))
		if _, err := e.render(buf); err != nil {
			t.Fatalf("render failed: %v", err)
		}
		rendered = target
	}

	const settleSamples = int64(0.010 * 44100) // 10ms past onset: past the 3ms force-mute fade with margin
	for i := range notes {
		onset := int64(i) * windowSamples
		renderTo(onset - 1)
		e.mu.Lock()
		before := len(e.parts[1].notes)
		e.mu.Unlock()
		if i > 0 && before != 1 {
			t.Fatalf("note %d: expected exactly the previous note still sounding right before this note's onset, got %d notes", i, before)
		}

		renderTo(onset + settleSamples)
		e.mu.Lock()
		after := len(e.parts[1].notes)
		e.mu.Unlock()
		if after != 1 {
			t.Fatalf("note %d (%s): expected exactly 1 note sounding %dms after this note's onset (previous note force-muted away despite its slow ReleaseRate), got %d",
				i, notes[i].name, settleSamples*1000/44100, after)
		}
	}
}

// moderateReleaseVoice has a clearly audible but not extreme release tail
// (a few hundred ms), for tests that need to observe AnySounding
// transitioning from true to false without waiting through a pathological
// ReleaseRate.
func moderateReleaseVoice() Voice {
	return Voice{
		Algorithm: 0, // serial chain, only the last operator is a carrier
		Operators: [4]OperatorParams{
			{Multiple: 1, TotalLevel: 20, Envelope: EnvelopeParams{AttackRate: 31, Decay1Rate: 10, SustainLevel: 0.6, Decay2Rate: 4, ReleaseRate: 20}},
			{Multiple: 2, TotalLevel: 40, Envelope: EnvelopeParams{AttackRate: 31, Decay1Rate: 12, SustainLevel: 0.3, Decay2Rate: 4, ReleaseRate: 20}},
			{Multiple: 1, TotalLevel: 30, Envelope: EnvelopeParams{AttackRate: 31, Decay1Rate: 10, SustainLevel: 0.5, Decay2Rate: 4, ReleaseRate: 20}},
			{Multiple: 1, TotalLevel: 0, Envelope: EnvelopeParams{AttackRate: 31, Decay1Rate: 8, SustainLevel: 0.7, Decay2Rate: 2, ReleaseRate: 20}},
		},
	}
}

func TestAnySoundingTracksNoteLifetimeIncludingReleaseTail(t *testing.T) {
	e := newTestEngine(44100)
	e.RegisterVoice(1, moderateReleaseVoice())
	_ = e.SetPart(0, 1, 1.0, 0.0)

	if e.AnySounding(0) {
		t.Fatalf("expected silence before any note is triggered")
	}

	if _, err := e.NoteOnRow(0, 69, 50, 100); err != nil { // 50ms note
		t.Fatalf("NoteOn failed: %v", err)
	}
	if !e.AnySounding(0) {
		t.Fatalf("expected AnySounding to report true right after triggering a note")
	}

	// 50ms note duration + release tail: render generously past the note's
	// own duration but confirm it's still sounding (releasing), not gone.
	buf := make([]byte, bytesPerFrame*44100*60/1000) // 60ms
	if _, err := e.render(buf); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	if !e.AnySounding(0) {
		t.Fatalf("expected AnySounding to still report true during the release tail")
	}

	// Render well past this voice's release tail (a few hundred ms).
	buf2 := make([]byte, bytesPerFrame*44100) // 1s
	if _, err := e.render(buf2); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	if e.AnySounding(0) {
		t.Fatalf("expected AnySounding to report false once the note has fully released")
	}
}

// TestScheduleNoteOnWithTieTicksOverridesDuration covers Phase 5.1's
// single-'&' tie: when NoteBend.TieTicks is set, the note's held duration
// must come from it (resolved via common.Tempo at fire time, like every
// other duration) instead of noteType's own single-note-length beats -
// proving fireScheduled's schedNoteOn branch actually uses it.
func TestScheduleNoteOnWithTieTicksOverridesDuration(t *testing.T) {
	prevTempo := common.Tempo
	common.Tempo = 120
	defer func() { common.Tempo = prevTempo }()

	e := newTestEngine(44100)
	e.RegisterVoice(1, testVoice())
	_ = e.SetPart(0, 1, 1.0, 0.0)

	const target = int64(1000)
	// TieTicks = 2 quarter notes; at 120bpm that's 1000ms, twice NOTE4's own
	// 500ms - noteType is deliberately left as NOTE4 to prove TieTicks
	// overrides it rather than being added to it.
	bend := NoteBend{TieTicks: 2 * common.TicksPerQuarterNote}
	if err := e.ScheduleNoteOnWithBend(target, 0, "A4", "NOTE4", 100, 100, bend); err != nil {
		t.Fatalf("ScheduleNoteOnWithBend failed: %v", err)
	}

	buf := make([]byte, bytesPerFrame*(target+10))
	if _, err := e.render(buf); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	e.mu.Lock()
	n := e.parts[0].notes[0]
	e.mu.Unlock()

	wantRelease := target + int64(1000)*int64(e.sampleRate)/1000
	if n.releaseAtSample != wantRelease {
		t.Fatalf("expected TieTicks to give a 1000ms hold from the note's onset at sample %d (releaseAtSample %d), got %d", target, wantRelease, n.releaseAtSample)
	}
}

func TestCancelScheduledDiscardsPendingActions(t *testing.T) {
	e := newTestEngine(44100)
	e.RegisterVoice(1, testVoice())
	_ = e.SetPart(0, 1, 1.0, 0.0)

	if err := e.ScheduleNoteOn(1000, 0, "A4", "NOTE4", 100, 100); err != nil {
		t.Fatalf("ScheduleNoteOn failed: %v", err)
	}
	e.CancelScheduled()

	buf := make([]byte, bytesPerFrame*2000)
	if _, err := e.render(buf); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	e.mu.Lock()
	n := len(e.parts[0].notes)
	e.mu.Unlock()
	if n != 0 {
		t.Fatalf("expected the cancelled note never to fire, got %d sounding", n)
	}
}
