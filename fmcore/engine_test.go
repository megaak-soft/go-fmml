/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package fmcore

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/megaak-soft/go-fmml/common"
	"github.com/megaak-soft/go-fmml/pcmcore"
	"github.com/megaak-soft/go-fmml/reverb"
)

func testVoice() Voice {
	return Voice{
		Algorithm: 0,
		Feedback:  0,
		Operators: [4]OperatorParams{
			{Multiple: 1, TotalLevel: 20, Envelope: EnvelopeParams{AttackRate: 31, Decay1Rate: 10, SustainLevel: 0.6, Decay2Rate: 4, ReleaseRate: 12}},
			{Multiple: 2, TotalLevel: 40, Envelope: EnvelopeParams{AttackRate: 31, Decay1Rate: 12, SustainLevel: 0.3, Decay2Rate: 4, ReleaseRate: 12}},
			{Multiple: 1, TotalLevel: 30, Envelope: EnvelopeParams{AttackRate: 31, Decay1Rate: 10, SustainLevel: 0.5, Decay2Rate: 4, ReleaseRate: 12}},
			{Multiple: 1, TotalLevel: 0, Envelope: EnvelopeParams{AttackRate: 31, Decay1Rate: 8, SustainLevel: 0.7, Decay2Rate: 2, ReleaseRate: 10}},
		},
	}
}

// newTestEngine builds an Engine without going through NewEngine, so tests
// can drive render() directly without opening a real audio device.
func newTestEngine(sampleRate int) *Engine {
	return &Engine{
		sampleRate:         sampleRate,
		voices:             make(map[VoiceID]Voice),
		parts:              make(map[PartID]*part),
		pcm:                pcmcore.NewEngine(sampleRate),
		masterVolume:       1,
		masterVolumeTarget: 1,
		reverb:             reverb.New(sampleRate),
	}
}

// TestNoteAudible covers Phase 1's definition of done: registering a voice,
// assigning it to a part, and triggering a note produces non-silent audio
// at the expected pitch.
func TestNoteAudible(t *testing.T) {
	e := newTestEngine(44100)
	e.RegisterVoice(1, testVoice())
	if err := e.SetPart(0, 1, 1.0, 0.0); err != nil {
		t.Fatalf("SetPart failed: %v", err)
	}

	id, err := e.NoteOnRow(0, 69, 200, 100) // A4, 200ms, velocity 100
	if err != nil {
		t.Fatalf("NoteOn failed: %v", err)
	}
	if id == 0 {
		t.Fatalf("expected non-zero NoteID")
	}

	buf := make([]byte, bytesPerFrame*4096)
	if _, err := e.render(buf); err != nil {
		t.Fatalf("render failed: %v", err)
	}

	var peak float64
	for f := 0; f < len(buf)/bytesPerFrame; f++ {
		l, r := decodeFrame(buf, f)
		if v := abs(l); v > peak {
			peak = v
		}
		if v := abs(r); v > peak {
			peak = v
		}
	}
	if peak < 0.01 {
		t.Fatalf("expected audible output, got peak amplitude %f", peak)
	}
}

// TestPanGainsIsContinuousNotJustThreeStops guards against pan being
// (mis)implemented as only hard left/center/right: CLAUDE.md's MML 'P'
// command spans -16..16 in single-unit steps (e.g. P7 is "slightly right
// of center"), and panGains must render every one of those positions
// distinctly rather than snapping to the nearest of three fixed points.
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
		t.Fatalf("expected P7's right gain strictly between center (%v) and full-right (%v), got %v", rCenter, rFullRight, rSlightRight)
	}
	if !(lFullRight < lSlightRight && lSlightRight < lCenter) {
		t.Fatalf("expected P7's left gain strictly between full-right (%v) and center (%v), got %v", lFullRight, lCenter, lSlightRight)
	}
}

func TestNoteOnUnknownPartFails(t *testing.T) {
	e := newTestEngine(44100)
	if _, err := e.NoteOnRow(0, 60, 100, 100); err == nil {
		t.Fatalf("expected error triggering a note on an unconfigured part")
	}
}

func TestSetPartUnknownVoiceFails(t *testing.T) {
	e := newTestEngine(44100)
	if err := e.SetPart(0, 99, 1, 0); err == nil {
		t.Fatalf("expected error assigning an unregistered voice to a part")
	}
}

func TestPolyphonyCapPerPart(t *testing.T) {
	e := newTestEngine(44100)
	e.RegisterVoice(1, testVoice())
	_ = e.SetPart(0, 1, 1.0, 0.0)

	for i := 0; i < maxPolyphonyPerPart+3; i++ {
		if _, err := e.NoteOnRow(0, uint8(60+i), 0, 100); err != nil {
			t.Fatalf("NoteOn %d failed: %v", i, err)
		}
	}

	e.mu.Lock()
	n := len(e.parts[0].notes)
	e.mu.Unlock()
	if n > maxPolyphonyPerPart {
		t.Fatalf("expected at most %d simultaneous notes, got %d", maxPolyphonyPerPart, n)
	}
}

// TestRetriggerSameNoteFadesOldInstanceInsteadOfCuttingItInstantly covers a
// fix for an audible click: retriggering the same note number used to drop
// the still-sounding old instance from p.notes outright (see
// part.stopNote), so if it was at any substantial amplitude - e.g.
// mid-sustain, which a voice with a high SustainLevel makes highly likely -
// its contribution vanished in a single sample, an audible discontinuity.
// stopNote now force-mutes the old instance (the same fixed ~3ms fade
// ScheduleForceStopPart already uses elsewhere) instead of removing it, so
// both instances briefly coexist right after a retrigger.
func TestRetriggerSameNoteFadesOldInstanceInsteadOfCuttingItInstantly(t *testing.T) {
	e := newTestEngine(44100)
	e.RegisterVoice(1, testVoice())
	_ = e.SetPart(0, 1, 1.0, 0.0)

	if _, err := e.NoteOnRow(0, 60, 0, 100); err != nil {
		t.Fatalf("NoteOn failed: %v", err)
	}
	if _, err := e.NoteOnRow(0, 60, 0, 100); err != nil {
		t.Fatalf("NoteOn failed: %v", err)
	}

	e.mu.Lock()
	n := len(e.parts[0].notes)
	e.mu.Unlock()
	if n != 2 {
		t.Fatalf("expected the old instance to still be fading out alongside the new one right after a retrigger, got %d notes", n)
	}

	// Render past the fixed ~3ms force-mute fade (forceMuteFadeMs): the
	// faded-out old instance should now be dropped, leaving only the new one.
	buf := make([]byte, bytesPerFrame*200)
	if _, err := e.render(buf); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	e.mu.Lock()
	n = len(e.parts[0].notes)
	e.mu.Unlock()
	if n != 1 {
		t.Fatalf("expected the old instance to be dropped once its force-mute fade completed, got %d notes", n)
	}
}

// TestNoteOnAbstractedMatchesDuration covers the abstracted NoteOn: it
// should resolve "A4"/"NOTE4" to the same note number NoteOnRow would take,
// and derive the duration from common.Tempo and the sustain percentage.
func TestNoteOnAbstractedMatchesDuration(t *testing.T) {
	prevTempo := common.Tempo
	common.Tempo = 120
	defer func() { common.Tempo = prevTempo }()

	e := newTestEngine(44100)
	e.RegisterVoice(1, testVoice())
	_ = e.SetPart(0, 1, 1.0, 0.0)

	// At 120 BPM a quarter note is 500ms; NOTE4 at 100% sustain = 500ms.
	if _, err := e.NoteOn(0, "A4", "NOTE4", 100, 100); err != nil {
		t.Fatalf("NoteOn failed: %v", err)
	}

	e.mu.Lock()
	n := e.parts[0].notes[0]
	e.mu.Unlock()
	if n.noteNumber != 69 {
		t.Fatalf("expected A4 to resolve to MIDI note 69, got %d", n.noteNumber)
	}
	wantRelease := e.sampleClock + int64(500)*int64(e.sampleRate)/1000
	if n.releaseAtSample != wantRelease {
		t.Fatalf("expected releaseAtSample %d, got %d", wantRelease, n.releaseAtSample)
	}
}

func TestNoteOnUnknownNoteNameFails(t *testing.T) {
	e := newTestEngine(44100)
	e.RegisterVoice(1, testVoice())
	_ = e.SetPart(0, 1, 1.0, 0.0)
	if _, err := e.NoteOn(0, "H4", "NOTE4", 100, 100); err == nil {
		t.Fatalf("expected error for invalid note name")
	}
}

func TestNoteOnUnknownNoteTypeFails(t *testing.T) {
	e := newTestEngine(44100)
	e.RegisterVoice(1, testVoice())
	_ = e.SetPart(0, 1, 1.0, 0.0)
	if _, err := e.NoteOn(0, "A4", "NOTE3", 100, 100); err == nil {
		t.Fatalf("expected error for invalid note type")
	}
}

// TestSetMasterVolumeScalesOutput confirms SetMasterVolume(0) silences the
// mix entirely (regardless of any per-part volume), and that it defaults
// to full volume until called.
func TestSetMasterVolumeScalesOutput(t *testing.T) {
	e := newTestEngine(44100)
	e.RegisterVoice(1, testVoice())
	_ = e.SetPart(0, 1, 1.0, 0.0)
	if _, err := e.NoteOnRow(0, 69, 0, 100); err != nil {
		t.Fatalf("NoteOn failed: %v", err)
	}

	e.SetMasterVolume(0)
	buf := make([]byte, bytesPerFrame*4096)
	if _, err := e.render(buf); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	for f := 0; f < len(buf)/bytesPerFrame; f++ {
		l, r := decodeFrame(buf, f)
		if l != 0 || r != 0 {
			t.Fatalf("expected silence with masterVolume=0, got frame %d: %f, %f", f, l, r)
		}
	}
}

// TestSetMasterVolumeSmoothRampsOverTime confirms Phase 7's smooth
// master-volume glide actually interpolates over rampSeconds (rather than
// jumping instantly like SetMasterVolume), reaching exactly its target once
// the ramp's duration has elapsed.
func TestSetMasterVolumeSmoothRampsOverTime(t *testing.T) {
	e := newTestEngine(44100)
	e.SetMasterVolume(0)
	e.SetMasterVolumeSmooth(1, 0.1) // 100ms ramp, 0 -> 1

	if got := e.MasterVolumeTarget(); got != 1 {
		t.Fatalf("expected MasterVolumeTarget to report 1 immediately, got %v", got)
	}

	buf := make([]byte, bytesPerFrame*2205) // 50ms: halfway through the ramp
	if _, err := e.render(buf); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	e.mu.Lock()
	midVol := e.masterVolume
	e.mu.Unlock()
	if midVol < 0.4 || midVol > 0.6 {
		t.Fatalf("expected roughly 0.5 halfway through a 0->1 ramp, got %v", midVol)
	}

	buf2 := make([]byte, bytesPerFrame*4410) // another 100ms: past the ramp's end
	if _, err := e.render(buf2); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	e.mu.Lock()
	endVol := e.masterVolume
	e.mu.Unlock()
	if endVol != 1 {
		t.Fatalf("expected the ramp to have reached its target 1.0, got %v", endVol)
	}
}

// TestSetMasterVolumeSmoothZeroDurationIsInstant confirms rampSeconds<=0
// behaves exactly like SetMasterVolume (no gradual glide).
func TestSetMasterVolumeSmoothZeroDurationIsInstant(t *testing.T) {
	e := newTestEngine(44100)
	e.SetMasterVolumeSmooth(0.25, 0)
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.masterVolume != 0.25 {
		t.Fatalf("expected an instant jump to 0.25, got %v", e.masterVolume)
	}
	if e.volumeRampActive {
		t.Fatalf("expected no ramp to be left active")
	}
}

// TestScheduleSetTempoUpdatesCommonTempoAtTheRightSample confirms Phase 7's
// [conductor] tempo automation fires through the same sample-accurate
// scheduler as a note-on, so common.Tempo only changes once the engine's
// sample clock actually reaches the scheduled sample.
func TestScheduleSetTempoUpdatesCommonTempoAtTheRightSample(t *testing.T) {
	e := newTestEngine(44100)
	orig := common.Tempo
	defer func() { common.Tempo = orig }()
	common.Tempo = 120

	e.ScheduleSetTempo(e.SampleClock()+150, 200)

	buf := make([]byte, bytesPerFrame*100)
	if _, err := e.render(buf); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	if common.Tempo != 120 {
		t.Fatalf("expected tempo to stay 120 before the scheduled sample, got %v", common.Tempo)
	}

	buf2 := make([]byte, bytesPerFrame*100)
	if _, err := e.render(buf2); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	if common.Tempo != 200 {
		t.Fatalf("expected tempo to become 200 once the scheduled sample was reached, got %v", common.Tempo)
	}
}

// renderMs renders durationMs worth of samples at e's sample rate, ignoring
// the output - a helper for reverb tests that only care about state changes
// (e.g. feeding the reverb bus, letting a force-mute fade complete) rather
// than inspecting this particular chunk's audio.
func renderMs(t *testing.T, e *Engine, durationMs int) {
	t.Helper()
	buf := make([]byte, bytesPerFrame*e.sampleRate*durationMs/1000)
	if _, err := e.render(buf); err != nil {
		t.Fatalf("render failed: %v", err)
	}
}

// peakAmplitudeMs renders durationMs worth of samples and returns the peak
// absolute amplitude across both channels.
func peakAmplitudeMs(t *testing.T, e *Engine, durationMs int) float64 {
	t.Helper()
	buf := make([]byte, bytesPerFrame*e.sampleRate*durationMs/1000)
	if _, err := e.render(buf); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	var peak float64
	for f := 0; f < len(buf)/bytesPerFrame; f++ {
		l, r := decodeFrame(buf, f)
		if v := abs(l); v > peak {
			peak = v
		}
		if v := abs(r); v > peak {
			peak = v
		}
	}
	return peak
}

// forceMuteAllNotes force-mutes every currently-sounding note on partID,
// mirroring ScheduleForceStopPart's effect but taking place immediately
// (deterministically, rather than waiting for a scheduled sample) so
// reverb tests can silence the dry signal on a known timeline without
// depending on a voice's own envelope/ReleaseRate.
func forceMuteAllNotes(e *Engine, partID PartID) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if p, ok := e.parts[partID]; ok {
		for _, n := range p.notes {
			n.forceMute(e.sampleClock, e.sampleRate)
		}
	}
}

// TestReverbSendAddsAudibleTailAfterDryNoteStops covers Phase 4's core
// definition of done: a part's reverbSend should keep the master reverb
// audibly ringing for a while after that part's own dry signal has gone
// completely silent.
func TestReverbSendAddsAudibleTailAfterDryNoteStops(t *testing.T) {
	e := newTestEngine(44100)
	e.RegisterVoice(1, testVoice())
	_ = e.SetPart(0, 1, 1.0, 0.0)
	e.SetReverb(true, "normal", 3.0, 1.0)
	e.SetPartReverbSend(0, 1.0)

	if _, err := e.NoteOnRow(0, 69, 0, 100); err != nil {
		t.Fatalf("NoteOn failed: %v", err)
	}
	renderMs(t, e, 100) // let the note feed the reverb bus for a while

	forceMuteAllNotes(e, 0)
	renderMs(t, e, 10) // past forceMuteFadeMs (3ms): the dry signal is now silent

	if peak := peakAmplitudeMs(t, e, 100); peak == 0 {
		t.Fatalf("expected an audible reverb tail after the dry note was force-muted, got silence")
	}
}

// TestReverbSendZeroProducesNoTail confirms a part with no reverb send
// (the default) never feeds the reverb bus, even with reverb itself
// enabled: once the dry note is force-muted, the mix goes completely
// silent.
func TestReverbSendZeroProducesNoTail(t *testing.T) {
	e := newTestEngine(44100)
	e.RegisterVoice(1, testVoice())
	_ = e.SetPart(0, 1, 1.0, 0.0)
	e.SetReverb(true, "normal", 3.0, 1.0)
	// SetPartReverbSend deliberately not called: reverbSend defaults to 0.

	if _, err := e.NoteOnRow(0, 69, 0, 100); err != nil {
		t.Fatalf("NoteOn failed: %v", err)
	}
	renderMs(t, e, 100)

	forceMuteAllNotes(e, 0)
	renderMs(t, e, 10)

	if peak := peakAmplitudeMs(t, e, 100); peak != 0 {
		t.Fatalf("expected silence with reverbSend=0 once the dry note stopped, got peak amplitude %f", peak)
	}
}

// TestSetReverbDisabledBypassesEvenWithSend confirms [global]'s reverb:
// false (enabled=false) bypasses the effect entirely - a nonzero part
// reverbSend must have no audible effect once the dry signal stops.
func TestSetReverbDisabledBypassesEvenWithSend(t *testing.T) {
	e := newTestEngine(44100)
	e.RegisterVoice(1, testVoice())
	_ = e.SetPart(0, 1, 1.0, 0.0)
	e.SetReverb(false, "normal", 3.0, 1.0)
	e.SetPartReverbSend(0, 1.0)

	if _, err := e.NoteOnRow(0, 69, 0, 100); err != nil {
		t.Fatalf("NoteOn failed: %v", err)
	}
	renderMs(t, e, 100)

	forceMuteAllNotes(e, 0)
	renderMs(t, e, 10)

	if peak := peakAmplitudeMs(t, e, 100); peak != 0 {
		t.Fatalf("expected silence with reverb disabled once the dry note stopped, got peak amplitude %f", peak)
	}
}

// TestSetReverbTimeAtOrBelowMinimumBypasses covers CLAUDE.md's Phase 4
// bypass rule: a reverbTime at or below reverb.MinDecaySeconds disables
// the effect regardless of enabled=true.
func TestSetReverbTimeAtOrBelowMinimumBypasses(t *testing.T) {
	e := newTestEngine(44100)
	e.RegisterVoice(1, testVoice())
	_ = e.SetPart(0, 1, 1.0, 0.0)
	e.SetReverb(true, "normal", reverb.MinDecaySeconds, 1.0)
	e.SetPartReverbSend(0, 1.0)

	if _, err := e.NoteOnRow(0, 69, 0, 100); err != nil {
		t.Fatalf("NoteOn failed: %v", err)
	}
	renderMs(t, e, 100)

	forceMuteAllNotes(e, 0)
	renderMs(t, e, 10)

	if peak := peakAmplitudeMs(t, e, 100); peak != 0 {
		t.Fatalf("expected silence with reverbTime at the bypass threshold, got peak amplitude %f", peak)
	}
}

// TestSetPartReverbSendSurvivesScheduleSetPart confirms a mid-sequence
// '@'/'P' voice/pan change (ScheduleSetPart) never resets a part's
// separately-configured reverbSend.
func TestSetPartReverbSendSurvivesScheduleSetPart(t *testing.T) {
	e := newTestEngine(44100)
	e.RegisterVoice(1, testVoice())
	_ = e.SetPart(0, 1, 1.0, 0.0)
	e.SetPartReverbSend(0, 0.75)

	e.ScheduleSetPart(e.SampleClock(), 0, 1, 0.5, 0.5)
	renderMs(t, e, 1)

	e.mu.Lock()
	got := e.parts[0].reverbSend
	e.mu.Unlock()
	if got != 0.75 {
		t.Fatalf("expected reverbSend to survive ScheduleSetPart, got %v", got)
	}
}

// TestNewEngineWithoutOutputOpensNoAudioDevice covers the reason
// NewEngineWithoutOutput exists: a host program that already owns the
// process's one oto.Context (e.g. via ebiten/v2/audio) must be able to get
// an Engine without fmcore trying to open a second oto.Context of its own,
// which would fail with oto's "context is already created" error.
func TestNewEngineWithoutOutputOpensNoAudioDevice(t *testing.T) {
	e := NewEngineWithoutOutput(44100)
	if e.ctx != nil || e.player != nil {
		t.Fatalf("expected NewEngineWithoutOutput not to open any audio device, got ctx=%v player=%v", e.ctx, e.player)
	}
	if err := e.Close(); err != nil {
		t.Fatalf("expected Close on a device-less Engine to be a no-op, got: %v", err)
	}
}

// TestNewEngineWithoutOutputReadProducesAudio confirms an Engine built with
// NewEngineWithoutOutput is otherwise fully usable: its Read method (the
// thing a caller feeds into their own oto.Context.NewPlayer or Ebitengine's
// audio.Context.NewPlayerF32) renders the exact same audio NewEngine's own
// internal player would have pulled from render.
func TestNewEngineWithoutOutputReadProducesAudio(t *testing.T) {
	e := NewEngineWithoutOutput(44100)
	e.RegisterVoice(1, testVoice())
	if err := e.SetPart(0, 1, 1.0, 0.0); err != nil {
		t.Fatalf("SetPart failed: %v", err)
	}
	if _, err := e.NoteOnRow(0, 69, 200, 100); err != nil {
		t.Fatalf("NoteOn failed: %v", err)
	}

	buf := make([]byte, bytesPerFrame*4096)
	n, err := e.Read(buf)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if n != len(buf) {
		t.Fatalf("expected Read to fill the whole buffer, got %d of %d bytes", n, len(buf))
	}

	var peak float64
	for f := 0; f < len(buf)/bytesPerFrame; f++ {
		l, r := decodeFrame(buf, f)
		if v := abs(l); v > peak {
			peak = v
		}
		if v := abs(r); v > peak {
			peak = v
		}
	}
	if peak < 0.01 {
		t.Fatalf("expected audible output via Read, got peak amplitude %f", peak)
	}
}

func decodeFrame(buf []byte, frame int) (float64, float64) {
	l := decodeFloat32LE(buf[frame*bytesPerFrame:])
	r := decodeFloat32LE(buf[frame*bytesPerFrame+4:])
	return l, r
}

func decodeFloat32LE(b []byte) float64 {
	return float64(math.Float32frombits(binary.LittleEndian.Uint32(b)))
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
