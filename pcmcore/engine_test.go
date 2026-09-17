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

	"github.com/megaak-soft/go-fmml/common"
)

// TestScheduleNoteOnLongWithTieTicksOverridesDuration covers Phase 5.1's
// single-'&' tie: when NoteBend.TieTicks is set, the note's held duration
// must come from it (resolved via common.Tempo at fire time, like every
// other duration) instead of noteType's own single-note-length beats -
// proving fireScheduled's schedLongNoteOn branch actually uses it.
func TestScheduleNoteOnLongWithTieTicksOverridesDuration(t *testing.T) {
	prevTempo := common.Tempo
	common.Tempo = 120
	defer func() { common.Tempo = prevTempo }()

	e := NewEngine(44100)
	e.RegisterVoice(1, longVoice(false))
	_ = e.SetPart('A', 1, 1.0, 0.0)

	// TieTicks = 2 quarter notes; at 120bpm that's 1000ms, twice NOTE4's own
	// 500ms - noteType is deliberately left as NOTE4 to prove TieTicks
	// overrides it rather than being added to it.
	bend := NoteBend{TieTicks: 2 * common.TicksPerQuarterNote}
	if err := e.ScheduleNoteOnLongWithBend(1000, 'A', "A4", "NOTE4", 100, 100, bend); err != nil {
		t.Fatalf("ScheduleNoteOnLongWithBend failed: %v", err)
	}
	e.Advance(1000)

	n := e.parts['A'].notes[0]
	wantRelease := int64(1000) + int64(1000)*int64(e.sampleRate)/1000
	if n.releaseAtSample != wantRelease {
		t.Fatalf("expected TieTicks to give a 1000ms hold from the note's onset (releaseAtSample %d), got %d", wantRelease, n.releaseAtSample)
	}
}

// TestPanGainsIsContinuousNotJustThreeStops mirrors fmcore's identical
// test: CLAUDE.md's -16..16 PCM pan scale must render every intermediate
// position distinctly, not just hard left/center/right.
func TestPanGainsIsContinuousNotJustThreeStops(t *testing.T) {
	prevLeft, prevRight := math.Inf(1), math.Inf(-1)
	for v := -16; v <= 16; v++ {
		l, r := panGains(float64(v) / 16)
		if v > -16 {
			if l > prevLeft {
				t.Fatalf("pan %d/16: left gain %v should be non-increasing as pan moves right (prev %v)", v, l, prevLeft)
			}
			if r < prevRight {
				t.Fatalf("pan %d/16: right gain %v should be non-decreasing as pan moves right (prev %v)", v, r, prevRight)
			}
		}
		prevLeft, prevRight = l, r
	}

	lCenter, rCenter := panGains(0)
	lSlightRight, rSlightRight := panGains(7.0 / 16)
	lFullRight, rFullRight := panGains(1)
	if !(rCenter < rSlightRight && rSlightRight < rFullRight) {
		t.Fatalf("expected pan 7 right gain strictly between center (%v) and full-right (%v), got %v", rCenter, rFullRight, rSlightRight)
	}
	if !(lFullRight < lSlightRight && lSlightRight < lCenter) {
		t.Fatalf("expected pan 7 left gain strictly between full-right (%v) and center (%v), got %v", lFullRight, lCenter, lSlightRight)
	}
}

func flatWave(n int, level float32) *WAVData {
	samples := make([]float32, n)
	for i := range samples {
		samples[i] = level
	}
	return &WAVData{SampleRate: 44100, Samples: samples}
}

func oneShotVoice() Voice {
	return Voice{
		VoiceType: OneShot,
		Settings: []VoiceSetting{
			{Note: 36, HasNote: true, Wave: flatWave(100, 1), Volume: 127, Pan: 0},   // kick
			{Note: 40, HasNote: true, Wave: flatWave(100, 1), Volume: 127, Pan: -16}, // snare
		},
	}
}

func longVoice(loop bool) Voice {
	return Voice{
		VoiceType: Long,
		Loop:      loop,
		Settings: []VoiceSetting{
			{
				BaseNote: 69, // A4
				Wave:     flatWave(10000, 1),
				Volume:   127,
				Envelope: EnvelopeParams{AttackRate: 31, Decay1Rate: 0, SustainLevel: 1, Decay2Rate: 0, ReleaseRate: 20},
			},
		},
	}
}

func TestTriggerOneShotProducesAudibleOutput(t *testing.T) {
	e := NewEngine(44100)
	e.RegisterVoice(1, oneShotVoice())
	if err := e.SetPart('A', 1, 1.0, 0.0); err != nil {
		t.Fatalf("SetPart failed: %v", err)
	}
	if err := e.TriggerOneShot('A', 36, 127); err != nil {
		t.Fatalf("TriggerOneShot failed: %v", err)
	}

	l, r, _, _ := e.Advance(0)
	if l == 0 && r == 0 {
		t.Fatalf("expected audible output right after triggering a oneShot sample")
	}
}

func TestTriggerOneShotUnknownNoteErrors(t *testing.T) {
	e := NewEngine(44100)
	e.RegisterVoice(1, oneShotVoice())
	_ = e.SetPart('A', 1, 1.0, 0.0)
	if err := e.TriggerOneShot('A', 99, 100); err == nil {
		t.Fatalf("expected an error triggering a note with no mapped sample")
	}
}

func TestOneShotDifferentNotesOverlap(t *testing.T) {
	e := NewEngine(44100)
	e.RegisterVoice(1, oneShotVoice())
	_ = e.SetPart('A', 1, 1.0, 0.0)

	if err := e.TriggerOneShot('A', 36, 127); err != nil {
		t.Fatalf("TriggerOneShot(kick) failed: %v", err)
	}
	if err := e.TriggerOneShot('A', 40, 127); err != nil {
		t.Fatalf("TriggerOneShot(snare) failed: %v", err)
	}
	if n := len(e.parts['A'].notes); n != 2 {
		t.Fatalf("expected kick and snare to sound concurrently, got %d notes", n)
	}
}

// TestOneShotRetriggerSameNoteFadesOldInstanceInsteadOfCuttingItInstantly
// covers the same click fix as fmcore's identically-named test: retriggering
// the same note number force-mutes (fades) the still-sounding old instance
// instead of dropping it from p.notes outright, so both briefly coexist
// right after a retrigger rather than the old one's amplitude vanishing in
// a single sample.
func TestOneShotRetriggerSameNoteFadesOldInstanceInsteadOfCuttingItInstantly(t *testing.T) {
	e := NewEngine(44100)
	// A sample long enough to still be sounding past the ~3ms/132-sample
	// force-mute fade window this test renders through (unlike
	// oneShotVoice()'s 100-sample kick, which would finish naturally before
	// that fade completes and make "exactly 1 note left" ambiguous).
	e.RegisterVoice(1, Voice{VoiceType: OneShot, Settings: []VoiceSetting{
		{Note: 36, HasNote: true, Wave: flatWave(1000, 1), Volume: 127},
	}})
	_ = e.SetPart('A', 1, 1.0, 0.0)

	if err := e.TriggerOneShot('A', 36, 127); err != nil {
		t.Fatalf("TriggerOneShot failed: %v", err)
	}
	if err := e.TriggerOneShot('A', 36, 127); err != nil {
		t.Fatalf("TriggerOneShot failed: %v", err)
	}
	if n := len(e.parts['A'].notes); n != 2 {
		t.Fatalf("expected the old instance to still be fading out alongside the new one right after a retrigger, got %d notes", n)
	}

	// Advance past the fixed ~3ms force-mute fade (forceMuteFadeMs): the
	// faded-out old instance should now be dropped, leaving only the new one.
	for s := int64(0); s < 200; s++ {
		e.Advance(s)
	}
	if n := len(e.parts['A'].notes); n != 1 {
		t.Fatalf("expected the old instance to be dropped once its force-mute fade completed, got %d notes", n)
	}
}

func TestOneShotFinishesAtSampleEnd(t *testing.T) {
	e := NewEngine(44100)
	e.RegisterVoice(1, oneShotVoice())
	_ = e.SetPart('A', 1, 1.0, 0.0)
	_ = e.TriggerOneShot('A', 36, 127)

	for i := int64(0); i < 100; i++ {
		e.Advance(i)
	}
	if n := len(e.parts['A'].notes); n != 0 {
		t.Fatalf("expected the oneShot note to be gone once its 100-sample wave finished, got %d remaining", n)
	}
}

func TestLongNotePitchShiftsFromBaseNote(t *testing.T) {
	e := NewEngine(44100)
	e.RegisterVoice(2, longVoice(false))
	_ = e.SetPart('B', 2, 1.0, 0.0)

	if err := e.NoteOnLongRow('B', 81, 0, 127); err != nil { // A5, one octave above A4 baseNote
		t.Fatalf("NoteOnLongRow failed: %v", err)
	}
	n := e.parts['B'].notes[0]
	if diff := n.pitchRatio - 2.0; diff > 0.0001 || diff < -0.0001 {
		t.Fatalf("expected pitchRatio ~2.0 for one octave up, got %f", n.pitchRatio)
	}
}

func TestLongNoteLoopsWithinLoopRange(t *testing.T) {
	e := NewEngine(44100)
	voice := longVoice(true)
	voice.Settings[0].Wave = flatWave(1000, 1)
	e.RegisterVoice(2, voice)
	_ = e.SetPart('B', 2, 1.0, 0.0)

	if err := e.NoteOnLongRow('B', 69, 0, 127); err != nil {
		t.Fatalf("NoteOnLongRow failed: %v", err)
	}

	for i := int64(0); i < 5000; i++ { // far more than the 1000-sample wave, without looping this would finish
		e.Advance(i)
	}
	if n := len(e.parts['B'].notes); n != 1 {
		t.Fatalf("expected the looping long note to still be sounding after outlasting its raw sample length, got %d", n)
	}
}

func TestLongNoteReleasesAndFinishes(t *testing.T) {
	e := NewEngine(44100)
	e.RegisterVoice(2, longVoice(true))
	_ = e.SetPart('B', 2, 1.0, 0.0)

	if err := e.NoteOnLongRow('B', 69, 10, 127); err != nil { // 10ms duration
		t.Fatalf("NoteOnLongRow failed: %v", err)
	}
	for i := int64(0); i < int64(44100*2); i++ { // 2s: past the 10ms duration and its release tail
		e.Advance(i)
	}
	if n := len(e.parts['B'].notes); n != 0 {
		t.Fatalf("expected the long note to have released and finished, got %d remaining", n)
	}
}

func TestScheduleTriggerOneShotFiresAtExactSample(t *testing.T) {
	e := NewEngine(44100)
	e.RegisterVoice(1, oneShotVoice())
	_ = e.SetPart('A', 1, 1.0, 0.0)

	e.ScheduleTriggerOneShot(1000, 'A', 36, 100)

	e.Advance(999)
	if n := len(e.parts['A'].notes); n != 0 {
		t.Fatalf("expected no note before its scheduled sample, got %d", n)
	}
	e.Advance(1000)
	if n := len(e.parts['A'].notes); n != 1 {
		t.Fatalf("expected the note to fire exactly at its scheduled sample, got %d", n)
	}
}

// TestScheduleForceStopPartFadesOutAllNotes confirms ScheduleForceStopPart
// doesn't cut a sounding note off mid-waveform (which would produce an
// audible click): the note is still present, fading, right as it fires,
// and only actually gone once forceMuteFadeMs has elapsed. Uses a Long
// voice sustaining indefinitely (rather than OneShot) so the note can't
// finish naturally on its own within the test's sample window, which would
// make the assertion meaningless.
func TestScheduleForceStopPartFadesOutAllNotes(t *testing.T) {
	e := NewEngine(44100)
	e.RegisterVoice(1, longVoice(false))
	_ = e.SetPart('A', 1, 1.0, 0.0)
	_ = e.NoteOnLongRow('A', 69, 0, 100) // sustain indefinitely

	e.ScheduleForceStopPart(500, 'A')
	for s := int64(0); s <= 500; s++ {
		e.Advance(s)
	}
	if n := len(e.parts['A'].notes); n != 1 {
		t.Fatalf("expected the note to still be fading out right as ScheduleForceStopPart fires, got %d remaining", n)
	}

	for s := int64(501); s <= 500+200; s++ { // forceMuteFadeMs=3ms is ~132 samples at 44100Hz
		e.Advance(s)
	}
	if n := len(e.parts['A'].notes); n != 0 {
		t.Fatalf("expected the force-mute fade to have finished and cleared the note, got %d remaining", n)
	}
}

func TestAnySoundingPCM(t *testing.T) {
	e := NewEngine(44100)
	e.RegisterVoice(1, oneShotVoice())
	_ = e.SetPart('A', 1, 1.0, 0.0)

	if e.AnySounding('A') {
		t.Fatalf("expected silence before any trigger")
	}
	_ = e.TriggerOneShot('A', 36, 100)
	if !e.AnySounding('A') {
		t.Fatalf("expected AnySounding to report true right after triggering")
	}
}

// TestOneShotReverbSendIsZeroByDefault confirms a note whose part has no
// SetPartOneShotReverbSend entry feeds nothing into the reverb bus, even
// though it's fully audible in the dry mix - Phase 4's "未記載ノート場合は
// リバーブをかけない" rule.
func TestOneShotReverbSendIsZeroByDefault(t *testing.T) {
	e := NewEngine(44100)
	e.RegisterVoice(1, oneShotVoice())
	_ = e.SetPart('A', 1, 1.0, 0.0)
	_ = e.TriggerOneShot('A', 36, 127)

	dryL, dryR, sendL, sendR := e.Advance(0)
	if dryL == 0 && dryR == 0 {
		t.Fatalf("expected an audible dry note")
	}
	if sendL != 0 || sendR != 0 {
		t.Fatalf("expected no reverb send with no configured send map, got %v, %v", sendL, sendR)
	}
}

// TestOneShotReverbSendAppliesConfiguredLevel confirms a note's own reverb
// send level (per Phase 4's per-note pcmPart.reverbSend map) is used: with
// a send of 1.0 on a part sounding a single note, the reverb-bus
// contribution should equal that note's own dry contribution exactly.
func TestOneShotReverbSendAppliesConfiguredLevel(t *testing.T) {
	e := NewEngine(44100)
	e.RegisterVoice(1, oneShotVoice())
	_ = e.SetPart('A', 1, 1.0, 0.0)
	e.SetPartOneShotReverbSend('A', map[uint8]float64{36: 1.0}) // full send on the kick note only

	_ = e.TriggerOneShot('A', 36, 127) // sends 1.0
	dryL, dryR, sendL, sendR := e.Advance(0)
	if dryL != sendL || dryR != sendR {
		t.Fatalf("expected reverbSend=1.0 to send exactly the note's dry output, got dry=(%v,%v) send=(%v,%v)", dryL, dryR, sendL, sendR)
	}
}

// TestOneShotReverbSendIsPerNoteNotPerPart confirms two different notes on
// the same OneShot part can carry different reverb send levels: a note
// absent from the send map contributes nothing to the bus even while a
// concurrently-sounding note on the same part does.
func TestOneShotReverbSendIsPerNoteNotPerPart(t *testing.T) {
	e := NewEngine(44100)
	e.RegisterVoice(1, oneShotVoice())
	_ = e.SetPart('A', 1, 1.0, 0.0)
	e.SetPartOneShotReverbSend('A', map[uint8]float64{36: 1.0}) // kick (36) sent, snare (40) not

	_ = e.TriggerOneShot('A', 36, 127)
	_ = e.TriggerOneShot('A', 40, 127)
	_, _, sendL, sendR := e.Advance(0)
	if sendL == 0 && sendR == 0 {
		t.Fatalf("expected the sent note (36) to contribute to the reverb bus")
	}

	e2 := NewEngine(44100)
	e2.RegisterVoice(1, oneShotVoice())
	_ = e2.SetPart('A', 1, 1.0, 0.0)
	e2.SetPartOneShotReverbSend('A', map[uint8]float64{36: 1.0})
	_ = e2.TriggerOneShot('A', 40, 127) // only the unsent note
	_, _, sendL2, sendR2 := e2.Advance(0)
	if sendL2 != 0 || sendR2 != 0 {
		t.Fatalf("expected the unsent note (40) alone to contribute nothing to the reverb bus, got %v, %v", sendL2, sendR2)
	}
}

// TestLongPartReverbSendAppliesUniformly confirms SetPartReverbSend applies
// to a Long part's whole dry output: with a send of 1.0, the reverb-bus
// contribution should equal the part's dry contribution exactly.
func TestLongPartReverbSendAppliesUniformly(t *testing.T) {
	e := NewEngine(44100)
	e.RegisterVoice(1, longVoice(false))
	_ = e.SetPart('A', 1, 1.0, 0.0)
	e.SetPartReverbSend('A', 1.0)
	_ = e.NoteOnLongRow('A', 69, 0, 100)

	dryL, dryR, sendL, sendR := e.Advance(0)
	if dryL != sendL || dryR != sendR {
		t.Fatalf("expected reverbSend=1.0 to send exactly the part's dry output, got dry=(%v,%v) send=(%v,%v)", dryL, dryR, sendL, sendR)
	}
}
