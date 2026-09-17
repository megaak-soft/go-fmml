/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package player

import (
	"math"
	"testing"
	"time"

	"github.com/megaak-soft/go-fmml/common"
	"github.com/megaak-soft/go-fmml/fmcore"
	"github.com/megaak-soft/go-fmml/memory"
)

func TestSetMasterVolumeAppliesInstantlyWhenNothingIsPlaying(t *testing.T) {
	_ = Stop() // make sure nothing is left over from a previous test

	fe := newFakeEngine(t)
	SetMasterVolume(fe, 63.5)

	fe.mu.Lock()
	defer fe.mu.Unlock()
	if want := 63.5 / 127; fe.masterVolume != want {
		t.Fatalf("expected master volume %v, got %v", want, fe.masterVolume)
	}
	if len(fe.masterVolumeSmooth) != 0 {
		t.Fatalf("expected an instant set (no smooth ramp) when nothing is playing, got %d ramp calls", len(fe.masterVolumeSmooth))
	}
}

func TestSetMasterVolumeGlidesSmoothlyWhilePlaying(t *testing.T) {
	memory.StoreSequence(testSequence(true))
	defer memory.ClearSequences()

	fe := newFakeEngine(t)
	if err := Play(fe, "test-seq", nil); err != nil {
		t.Fatalf("Play failed: %v", err)
	}
	defer Stop()
	time.Sleep(20 * time.Millisecond)

	SetMasterVolume(fe, 127)

	fe.mu.Lock()
	defer fe.mu.Unlock()
	if len(fe.masterVolumeSmooth) == 0 {
		t.Fatalf("expected a smooth master-volume ramp to be used while playing")
	}
	last := fe.masterVolumeSmooth[len(fe.masterVolumeSmooth)-1]
	if last.target != 1.0 {
		t.Fatalf("expected the ramp's target to be 1.0 (127/127), got %v", last.target)
	}
	if last.rampSeconds != masterVolumeChangeSeconds {
		t.Fatalf("expected rampSeconds %v, got %v", masterVolumeChangeSeconds, last.rampSeconds)
	}
}

func TestSetMasterVolumePercentUsesOriginalLoadedVolume(t *testing.T) {
	_ = Stop()

	seq := testSequence(false)
	seq.MasterVolume = 0.8
	memory.StoreSequence(seq)
	defer memory.ClearSequences()

	fe := newFakeEngine(t)
	if err := Play(fe, "test-seq", nil); err != nil {
		t.Fatalf("Play failed: %v", err)
	}
	defer Stop()

	SetMasterVolumePercent(fe, 50)

	fe.mu.Lock()
	defer fe.mu.Unlock()
	if want := 0.4; math.Abs(fe.masterVolume-want) > 1e-9 {
		t.Fatalf("expected 50%% of the original 0.8 volume = 0.4, got %v", fe.masterVolume)
	}
}

func TestFadeInPlayStartsSilentThenRampsToTarget(t *testing.T) {
	seq := testSequence(false)
	seq.MasterVolume = 1.0
	memory.StoreSequence(seq)
	defer memory.ClearSequences()

	fe := newFakeEngine(t)
	if err := FadeInPlay(fe, "test-seq", 0.5, nil); err != nil {
		t.Fatalf("FadeInPlay failed: %v", err)
	}
	defer Stop()

	fe.mu.Lock()
	defer fe.mu.Unlock()
	if len(fe.masterVolumeSmooth) == 0 {
		t.Fatalf("expected a smooth ramp up to be scheduled")
	}
	first := fe.masterVolumeSmooth[0]
	if first.target != 1.0 || first.rampSeconds != 0.5 {
		t.Fatalf("expected a ramp to target 1.0 over 0.5s, got %+v", first)
	}
}

// TestFadeOutPlaySkipsAutoFadeWhenLooping confirms CLAUDE.md's "loopがtrue
// の曲の場合はフェードアウトはしない": FadeOutPlay on a looping sequence
// behaves exactly like Play, never scheduling an automatic fade-out.
func TestFadeOutPlaySkipsAutoFadeWhenLooping(t *testing.T) {
	memory.StoreSequence(testSequence(true))
	defer memory.ClearSequences()

	fe := newFakeEngine(t)
	if err := FadeOutPlay(fe, "test-seq", 0.2, nil); err != nil {
		t.Fatalf("FadeOutPlay failed: %v", err)
	}
	defer Stop()
	time.Sleep(20 * time.Millisecond)

	fe.mu.Lock()
	defer fe.mu.Unlock()
	if len(fe.masterVolumeSmooth) != 0 {
		t.Fatalf("expected no auto-fade-out to be scheduled for a looping sequence, got %+v", fe.masterVolumeSmooth)
	}
}

func TestFadeOutStopsPlaybackOnceFadeCompletes(t *testing.T) {
	memory.StoreSequence(testSequence(true)) // loops forever on its own, so only FadeOut should stop it
	defer memory.ClearSequences()

	fe := newFakeEngine(t)
	if err := Play(fe, "test-seq", nil); err != nil {
		t.Fatalf("Play failed: %v", err)
	}
	defer Stop()
	time.Sleep(20 * time.Millisecond)

	if err := FadeOut(0.05); err != nil {
		t.Fatalf("FadeOut failed: %v", err)
	}
	if !IsPlaying() {
		t.Fatalf("expected playback to still be active immediately after FadeOut (its fade hasn't completed yet)")
	}

	time.Sleep(200 * time.Millisecond)
	if IsPlaying() {
		t.Fatalf("expected FadeOut to stop playback once its fade completed")
	}
}

// TestConductorTempoMapSchedulesSetTempoChange confirms a sequence with a
// mid-song tempo-map breakpoint (Phase 7's [conductor] tempo automation)
// schedules exactly one ScheduleSetTempo call, with the new BPM, at the
// breakpoint's tick.
func TestConductorTempoMapSchedulesSetTempoChange(t *testing.T) {
	seq := &memory.SequenceData{
		SequenceID: "tempo-map-seq",
		Tempo:      600, // 100ms/quarter note
		Loop:       false,
		Parts: map[fmcore.PartID]*memory.PartSequence{
			1: {
				PartID:  1,
				VoiceID: 1,
				Volume:  1,
				Events: []memory.SeqEvent{
					{AtTick: 0, Kind: memory.EventNote, Notes: []memory.NoteSpec{{NoteName: "C4", NoteType: "NOTE1", Sustain: 100, Velocity: 100}}},
				},
				LengthTicks: 192,
			},
		},
		TempoMap: []common.TempoPoint{{AtTick: 0, BPM: 600}, {AtTick: 96, BPM: 1200}},
	}
	memory.StoreSequence(seq)
	defer memory.ClearSequences()

	fe := newFakeEngine(t)
	if err := Play(fe, "tempo-map-seq", nil); err != nil {
		t.Fatalf("Play failed: %v", err)
	}
	defer Stop()

	time.Sleep(300 * time.Millisecond)

	fe.mu.Lock()
	defer fe.mu.Unlock()
	if len(fe.tempoChanges) != 1 {
		t.Fatalf("expected exactly 1 tempo change to be scheduled, got %d: %+v", len(fe.tempoChanges), fe.tempoChanges)
	}
	if fe.tempoChanges[0].bpm != 1200 {
		t.Fatalf("expected the scheduled tempo change to be 1200 BPM, got %v", fe.tempoChanges[0].bpm)
	}
}

// TestJumpPointOverridesLoopRestartPoint is
// TestPlayLoopRestartsFromStartOffsetNotTickZero's [conductor] jump-point
// counterpart: once the sequence loops back, it must resume at
// JumpPointTick, so the notes before it never sound again after the first
// pass.
func TestJumpPointOverridesLoopRestartPoint(t *testing.T) {
	seq := &memory.SequenceData{
		SequenceID: "jump-point-seq",
		Tempo:      600,
		Loop:       true,
		Parts: map[fmcore.PartID]*memory.PartSequence{
			1: {
				PartID:  1,
				VoiceID: 1,
				Volume:  1,
				Events: []memory.SeqEvent{
					{AtTick: 0, Kind: memory.EventNote, Notes: []memory.NoteSpec{{NoteName: "C4", NoteType: "NOTE4", Sustain: 100, Velocity: 100}}},
					{AtTick: 48, Kind: memory.EventNote, Notes: []memory.NoteSpec{{NoteName: "D4", NoteType: "NOTE4", Sustain: 100, Velocity: 100}}},
					{AtTick: 96, Kind: memory.EventNote, Notes: []memory.NoteSpec{{NoteName: "E4", NoteType: "NOTE4", Sustain: 100, Velocity: 100}}},
				},
				LengthTicks: 144,
			},
		},
		HasJumpPoint:  true,
		JumpPointTick: 48, // loop restarts at D4; C4 must never sound again
	}
	memory.StoreSequence(seq)
	defer memory.ClearSequences()

	fe := newFakeEngine(t)
	if err := Play(fe, "jump-point-seq", nil); err != nil {
		t.Fatalf("Play failed: %v", err)
	}
	defer Stop()

	time.Sleep(700 * time.Millisecond)

	notes, _ := fe.snapshot()
	c4Count := 0
	for _, n := range notes {
		if n.noteName == "C4" {
			c4Count++
		}
	}
	if c4Count != 1 {
		t.Fatalf("expected C4 to sound exactly once (the jump point skips it on every loop restart), got %d: %+v", c4Count, notes)
	}
	if len(notes) < 5 {
		t.Fatalf("expected several D4/E4 repeats across ~700ms, got %d: %+v", len(notes), notes)
	}
}
