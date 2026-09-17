/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package player

import (
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/megaak-soft/go-fmml/fmcore"
	"github.com/megaak-soft/go-fmml/memory"
	"github.com/megaak-soft/go-fmml/pcmcore"
)

// fakeEngine simulates just enough of fmcore.Engine's sample-accurate
// scheduling for tests to drive playback without opening a real audio
// device: SampleClock advances from wall-clock time at a fixed sample
// rate, and a background pump goroutine fires scheduled actions once that
// simulated clock reaches them - mirroring how fmcore.Engine's render loop
// does it for real, so these tests still exercise the actual scheduling
// arithmetic in player.go.
type fakeEngine struct {
	mu                 sync.Mutex
	noteOns            []fakeNoteOn
	setParts           []fakeSetPart
	registeredVoic     []fmcore.VoiceID
	forceStops         []fakeForceStop
	masterVolume       float64
	masterVolumeSmooth []fakeMasterVolumeSmooth
	tempoChanges       []fakeTempoChange

	sampleRate int
	start      time.Time
	nextSeq    uint64
	scheduled  []fakeScheduled

	// simulatedReleaseTail, if set, is how much longer than a note's own
	// onset AnySounding should keep reporting that part as sounding - a
	// stand-in for a real voice's release tail, so tests can exercise
	// startCompletionWatcher's wait-for-silence behavior without needing
	// full FM envelope simulation.
	simulatedReleaseTail time.Duration
	soundingUntil        time.Time

	pcmNoteOns          []fakePCMNoteOn
	pcmOneShotTriggers  []fakePCMOneShotTrigger
	pcmSetParts         []fakePCMSetPart
	pcmForceStops       []fakePCMForceStop
	registeredPCMVoices []pcmcore.VoiceID

	partReverbSends     []fakePartReverbSend
	pcmPartReverbSends  []fakePCMPartReverbSend
	pcmPartOneShotSends []fakePCMPartOneShotReverbSend
	reverbConfigs       []fakeReverbConfig
	reverbDuckCount     int

	stop chan struct{}
}

type fakePartReverbSend struct {
	partID fmcore.PartID
	send   float64
}

type fakePCMPartReverbSend struct {
	partID pcmcore.PartID
	send   float64
}

type fakePCMPartOneShotReverbSend struct {
	partID pcmcore.PartID
	sends  map[uint8]float64
}

type fakeReverbConfig struct {
	enabled     bool
	kind        string
	timeSeconds float64
	level       float64
}

type fakePCMNoteOn struct {
	partID   pcmcore.PartID
	noteName string
	noteType string
	sustain  int
	velocity uint8
	at       time.Time
}

type fakePCMOneShotTrigger struct {
	partID     pcmcore.PartID
	noteNumber uint8
	velocity   uint8
	at         time.Time
}

type fakePCMSetPart struct {
	partID  pcmcore.PartID
	voiceID pcmcore.VoiceID
	volume  float64
	pan     float64
}

type fakePCMForceStop struct {
	partID pcmcore.PartID
	at     time.Time
}

type fakeNoteOn struct {
	partID   fmcore.PartID
	noteName string
	noteType string
	sustain  int
	velocity uint8
	bend     fmcore.NoteBend
	at       time.Time
}

type fakeSetPart struct {
	partID  fmcore.PartID
	voiceID fmcore.VoiceID
	volume  float64
	pan     float64
}

type fakeForceStop struct {
	partID fmcore.PartID
	at     time.Time
}

type fakeMasterVolumeSmooth struct {
	target      float64
	rampSeconds float64
}

type fakeTempoChange struct {
	atSample int64
	bpm      float64
}

type fakeScheduled struct {
	atSample int64
	seq      uint64
	fire     func() // called with f.mu already held
}

func newFakeEngine(t *testing.T) *fakeEngine {
	f := &fakeEngine{sampleRate: 44100, start: time.Now(), stop: make(chan struct{})}
	go f.pump()
	t.Cleanup(func() { close(f.stop) })
	return f
}

func (f *fakeEngine) pump() {
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-f.stop:
			return
		case <-ticker.C:
			f.mu.Lock()
			now := f.sampleClockLocked()
			sort.Slice(f.scheduled, func(i, j int) bool {
				if f.scheduled[i].atSample != f.scheduled[j].atSample {
					return f.scheduled[i].atSample < f.scheduled[j].atSample
				}
				return f.scheduled[i].seq < f.scheduled[j].seq
			})
			i := 0
			for i < len(f.scheduled) && f.scheduled[i].atSample <= now {
				f.scheduled[i].fire()
				i++
			}
			if i > 0 {
				f.scheduled = f.scheduled[i:]
			}
			f.mu.Unlock()
		}
	}
}

func (f *fakeEngine) sampleClockLocked() int64 {
	return int64(time.Since(f.start).Seconds() * float64(f.sampleRate))
}

func (f *fakeEngine) SampleClock() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sampleClockLocked()
}

func (f *fakeEngine) SampleRate() int { return f.sampleRate }

func (f *fakeEngine) RegisterVoice(id fmcore.VoiceID, voice fmcore.Voice) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.registeredVoic = append(f.registeredVoic, id)
}

func (f *fakeEngine) SetPart(id fmcore.PartID, voice fmcore.VoiceID, volume, pan float64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setParts = append(f.setParts, fakeSetPart{id, voice, volume, pan})
	return nil
}

func (f *fakeEngine) SetMasterVolume(volume float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.masterVolume = volume
}

func (f *fakeEngine) SetMasterVolumeSmooth(target float64, rampSeconds float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.masterVolume = target
	f.masterVolumeSmooth = append(f.masterVolumeSmooth, fakeMasterVolumeSmooth{target, rampSeconds})
}

func (f *fakeEngine) MasterVolumeTarget() float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.masterVolume
}

func (f *fakeEngine) ScheduleSetTempo(atSample int64, bpm float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextSeq++
	f.scheduled = append(f.scheduled, fakeScheduled{atSample: atSample, seq: f.nextSeq, fire: func() {
		f.tempoChanges = append(f.tempoChanges, fakeTempoChange{atSample, bpm})
	}})
}

func (f *fakeEngine) ScheduleNoteOn(atSample int64, partID fmcore.PartID, noteName, noteType string, sustain int, velocity uint8) error {
	return f.ScheduleNoteOnWithBend(atSample, partID, noteName, noteType, sustain, velocity, fmcore.NoteBend{})
}

func (f *fakeEngine) ScheduleNoteOnWithBend(atSample int64, partID fmcore.PartID, noteName, noteType string, sustain int, velocity uint8, bend fmcore.NoteBend) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextSeq++
	f.scheduled = append(f.scheduled, fakeScheduled{atSample: atSample, seq: f.nextSeq, fire: func() {
		f.noteOns = append(f.noteOns, fakeNoteOn{partID, noteName, noteType, sustain, velocity, bend, time.Now()})
		if until := time.Now().Add(f.simulatedReleaseTail); until.After(f.soundingUntil) {
			f.soundingUntil = until
		}
	}})
	return nil
}

func (f *fakeEngine) AnySounding(partIDs ...fmcore.PartID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return time.Now().Before(f.soundingUntil)
}

func (f *fakeEngine) ScheduleForceStopPart(atSample int64, partID fmcore.PartID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextSeq++
	f.scheduled = append(f.scheduled, fakeScheduled{atSample: atSample, seq: f.nextSeq, fire: func() {
		f.forceStops = append(f.forceStops, fakeForceStop{partID, time.Now()})
	}})
}

func (f *fakeEngine) ScheduleSetPart(atSample int64, partID fmcore.PartID, voiceID fmcore.VoiceID, volume, pan float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextSeq++
	f.scheduled = append(f.scheduled, fakeScheduled{atSample: atSample, seq: f.nextSeq, fire: func() {
		f.setParts = append(f.setParts, fakeSetPart{partID, voiceID, volume, pan})
	}})
}

func (f *fakeEngine) CancelScheduled() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scheduled = nil
}

func (f *fakeEngine) RegisterPCMVoice(id pcmcore.VoiceID, voice pcmcore.Voice) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.registeredPCMVoices = append(f.registeredPCMVoices, id)
}

func (f *fakeEngine) SetPCMPart(id pcmcore.PartID, voice pcmcore.VoiceID, volume, pan float64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pcmSetParts = append(f.pcmSetParts, fakePCMSetPart{id, voice, volume, pan})
	return nil
}

func (f *fakeEngine) ScheduleTriggerPCMOneShot(atSample int64, partID pcmcore.PartID, noteNumber uint8, velocity uint8) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextSeq++
	f.scheduled = append(f.scheduled, fakeScheduled{atSample: atSample, seq: f.nextSeq, fire: func() {
		f.pcmOneShotTriggers = append(f.pcmOneShotTriggers, fakePCMOneShotTrigger{partID, noteNumber, velocity, time.Now()})
		if until := time.Now().Add(f.simulatedReleaseTail); until.After(f.soundingUntil) {
			f.soundingUntil = until
		}
	}})
}

func (f *fakeEngine) ScheduleNoteOnPCMLong(atSample int64, partID pcmcore.PartID, noteName string, noteType string, sustain int, velocity uint8) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextSeq++
	f.scheduled = append(f.scheduled, fakeScheduled{atSample: atSample, seq: f.nextSeq, fire: func() {
		f.pcmNoteOns = append(f.pcmNoteOns, fakePCMNoteOn{partID, noteName, noteType, sustain, velocity, time.Now()})
		if until := time.Now().Add(f.simulatedReleaseTail); until.After(f.soundingUntil) {
			f.soundingUntil = until
		}
	}})
	return nil
}

func (f *fakeEngine) ScheduleNoteOnPCMLongWithBend(atSample int64, partID pcmcore.PartID, noteName string, noteType string, sustain int, velocity uint8, bend pcmcore.NoteBend) error {
	return f.ScheduleNoteOnPCMLong(atSample, partID, noteName, noteType, sustain, velocity)
}

func (f *fakeEngine) ScheduleForceStopPCMPart(atSample int64, partID pcmcore.PartID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextSeq++
	f.scheduled = append(f.scheduled, fakeScheduled{atSample: atSample, seq: f.nextSeq, fire: func() {
		f.pcmForceStops = append(f.pcmForceStops, fakePCMForceStop{partID, time.Now()})
	}})
}

func (f *fakeEngine) ScheduleSetPCMPart(atSample int64, partID pcmcore.PartID, voiceID pcmcore.VoiceID, volume, pan float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextSeq++
	f.scheduled = append(f.scheduled, fakeScheduled{atSample: atSample, seq: f.nextSeq, fire: func() {
		f.pcmSetParts = append(f.pcmSetParts, fakePCMSetPart{partID, voiceID, volume, pan})
	}})
}

func (f *fakeEngine) AnySoundingPCM(partIDs ...pcmcore.PartID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return time.Now().Before(f.soundingUntil)
}

func (f *fakeEngine) SetPartReverbSend(id fmcore.PartID, send float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.partReverbSends = append(f.partReverbSends, fakePartReverbSend{id, send})
}

func (f *fakeEngine) SetPCMPartReverbSend(id pcmcore.PartID, send float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pcmPartReverbSends = append(f.pcmPartReverbSends, fakePCMPartReverbSend{id, send})
}

func (f *fakeEngine) SetPCMPartOneShotReverbSend(id pcmcore.PartID, sends map[uint8]float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pcmPartOneShotSends = append(f.pcmPartOneShotSends, fakePCMPartOneShotReverbSend{id, sends})
}

func (f *fakeEngine) SetReverb(enabled bool, kind string, timeSeconds, level float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reverbConfigs = append(f.reverbConfigs, fakeReverbConfig{enabled, kind, timeSeconds, level})
}

func (f *fakeEngine) DuckReverb() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reverbDuckCount++
}

func (f *fakeEngine) snapshot() (notes []fakeNoteOn, setParts []fakeSetPart) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeNoteOn(nil), f.noteOns...), append([]fakeSetPart(nil), f.setParts...)
}

func testSequence(loop bool) *memory.SequenceData {
	return &memory.SequenceData{
		SequenceID: "test-seq",
		Tempo:      600, // 100ms per quarter note, for fast tests
		Loop:       loop,
		Parts: map[fmcore.PartID]*memory.PartSequence{
			1: {
				PartID:  1,
				VoiceID: 1,
				Volume:  1,
				Pan:     0,
				Events: []memory.SeqEvent{
					{AtTick: 0, Kind: memory.EventNote, Notes: []memory.NoteSpec{{NoteName: "C4", NoteType: "NOTE4", Sustain: 100, Velocity: 100}}},
					{AtTick: 48, Kind: memory.EventNote, Notes: []memory.NoteSpec{{NoteName: "D4", NoteType: "NOTE4", Sustain: 100, Velocity: 100}}},
				},
				LengthTicks: 96, // NOTE4 = 48 ticks @ TicksPerQuarterNote=48; two quarter notes = 96
			},
		},
	}
}

func TestPlayFiresNotesInOrder(t *testing.T) {
	memory.StoreSequence(testSequence(false))
	defer memory.ClearSequences()

	fe := newFakeEngine(t)
	if err := Play(fe, "test-seq", nil); err != nil {
		t.Fatalf("Play failed: %v", err)
	}
	defer Stop()

	time.Sleep(250 * time.Millisecond)

	notes, setParts := fe.snapshot()
	if len(notes) != 2 {
		t.Fatalf("expected 2 notes fired, got %d: %+v", len(notes), notes)
	}
	if notes[0].noteName != "C4" || notes[1].noteName != "D4" {
		t.Fatalf("expected C4 then D4, got %+v", notes)
	}
	if len(setParts) == 0 || setParts[0].voiceID != 1 {
		t.Fatalf("expected part 1 to be set up with voice 1, got %+v", setParts)
	}
	gap := notes[1].at.Sub(notes[0].at)
	if gap < 60*time.Millisecond || gap > 160*time.Millisecond {
		t.Fatalf("expected ~100ms between notes, got %v", gap)
	}
}

func TestPlayLoops(t *testing.T) {
	memory.StoreSequence(testSequence(true))
	defer memory.ClearSequences()

	fe := newFakeEngine(t)
	if err := Play(fe, "test-seq", nil); err != nil {
		t.Fatalf("Play failed: %v", err)
	}
	defer Stop()

	time.Sleep(450 * time.Millisecond)

	notes, _ := fe.snapshot()
	if len(notes) < 4 {
		t.Fatalf("expected looping to fire at least 4 notes in ~450ms (200ms/loop), got %d: %+v", len(notes), notes)
	}
}

// TestPlayLoopsCleanlyWithUnevenPartLengths mirrors a real SMF-converted
// song's shape: the loop point is the longest part's own length (per
// CLAUDE.md), so a shorter part goes silent for the back half of every
// iteration before retriggering at the next loop boundary. This checks
// that retrigger lands exactly one loop length after the previous one -
// neither early nor late - i.e. the scheduler itself introduces no gap or
// overlap at the loop seam, regardless of how uneven the parts' own
// lengths are.
func TestPlayLoopsCleanlyWithUnevenPartLengths(t *testing.T) {
	seq := &memory.SequenceData{
		SequenceID: "uneven-loop-seq",
		Tempo:      600, // 100ms/quarter note; 48 ticks/quarter
		Loop:       true,
		Parts: map[fmcore.PartID]*memory.PartSequence{
			1: {
				PartID:  1,
				VoiceID: 1,
				Volume:  1,
				Events: []memory.SeqEvent{
					{AtTick: 0, Kind: memory.EventNote, Notes: []memory.NoteSpec{{NoteName: "C4", NoteType: "NOTE4", Sustain: 100, Velocity: 100}}},
					{AtTick: 48, Kind: memory.EventNote, Notes: []memory.NoteSpec{{NoteName: "D4", NoteType: "NOTE4", Sustain: 100, Velocity: 100}}},
				},
				LengthTicks: 96,
			},
			2: {
				PartID:  2,
				VoiceID: 1,
				Volume:  1,
				Events: []memory.SeqEvent{
					{AtTick: 0, Kind: memory.EventNote, Notes: []memory.NoteSpec{{NoteName: "E4", NoteType: "NOTE4", Sustain: 100, Velocity: 100}}},
				},
				LengthTicks: 48, // half of part 1's length: silent for the back half of every loop
			},
		},
	}
	memory.StoreSequence(seq)
	defer memory.ClearSequences()

	fe := newFakeEngine(t)
	if err := Play(fe, "uneven-loop-seq", nil); err != nil {
		t.Fatalf("Play failed: %v", err)
	}
	defer Stop()

	time.Sleep(650 * time.Millisecond) // ~3.25 loop iterations at 200ms/loop

	notes, _ := fe.snapshot()
	var part2Fires []time.Time
	for _, n := range notes {
		if n.partID == 2 {
			part2Fires = append(part2Fires, n.at)
		}
	}
	if len(part2Fires) < 3 {
		t.Fatalf("expected the shorter part to retrigger at least 3 times across loops, got %d: %+v", len(part2Fires), part2Fires)
	}
	for i := 1; i < len(part2Fires); i++ {
		gap := part2Fires[i].Sub(part2Fires[i-1])
		if gap < 150*time.Millisecond || gap > 260*time.Millisecond {
			t.Errorf("gap between successive loop restarts of the shorter part = %v, want ~200ms (the loop length) - a gap outside this range would indicate a scheduling glitch at the loop boundary", gap)
		}
	}
}

func TestPauseStopsFiringUntilResume(t *testing.T) {
	memory.StoreSequence(testSequence(false))
	defer memory.ClearSequences()

	fe := newFakeEngine(t)
	if err := Play(fe, "test-seq", nil); err != nil {
		t.Fatalf("Play failed: %v", err)
	}
	defer Stop()

	time.Sleep(20 * time.Millisecond) // after the first note (AtTick=0), before the second (AtTick=48/100ms)
	if err := Pause(); err != nil {
		t.Fatalf("Pause failed: %v", err)
	}
	notesAtPause, _ := fe.snapshot()

	time.Sleep(150 * time.Millisecond) // second note's original due time passes while paused
	notesStillPaused, _ := fe.snapshot()
	if len(notesStillPaused) != len(notesAtPause) {
		t.Fatalf("expected no new notes while paused, got %d -> %d", len(notesAtPause), len(notesStillPaused))
	}

	if err := Resume(); err != nil {
		t.Fatalf("Resume failed: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	notesAfterResume, _ := fe.snapshot()
	// Pausing landed inside C4 (its AtTick=0 note, before D4's AtTick=48), so
	// resume rewinds to replay C4 from its start before continuing on to D4.
	if len(notesAfterResume) != 3 {
		t.Fatalf("expected C4 to replay then D4 to follow after resume, got %d: %+v", len(notesAfterResume), notesAfterResume)
	}
	if notesAfterResume[1].noteName != "C4" || notesAfterResume[2].noteName != "D4" {
		t.Fatalf("expected replay order C4, D4, got %+v", notesAfterResume[1:])
	}
}

// TestPlayNonLoopingSchedulesEntireTimelineImmediately guards against a
// regression where non-looping sequences longer than the rolling lookahead
// window (used to bound how far ahead a *looping* sequence is scheduled)
// silently lost their tail: a non-looping timeline is finite, so it must
// be scheduled to completion up front rather than capped to that window.
func TestPlayNonLoopingSchedulesEntireTimelineImmediately(t *testing.T) {
	seq := &memory.SequenceData{
		SequenceID: "long-seq",
		Tempo:      120,
		Loop:       false,
		Parts: map[fmcore.PartID]*memory.PartSequence{
			1: {
				PartID:  1,
				VoiceID: 1,
				Volume:  1,
				Events: []memory.SeqEvent{
					{AtTick: 0, Kind: memory.EventNote, Notes: []memory.NoteSpec{{NoteName: "C4", NoteType: "NOTE4", Sustain: 100, Velocity: 100}}},
					{AtTick: 10000, Kind: memory.EventNote, Notes: []memory.NoteSpec{{NoteName: "D4", NoteType: "NOTE4", Sustain: 100, Velocity: 100}}},
					// 10000 ticks at 120bpm is ~104 real seconds out - far
					// past any reasonable lookahead window.
					{AtTick: 20000, Kind: memory.EventNote, Notes: []memory.NoteSpec{{NoteName: "E4", NoteType: "NOTE4", Sustain: 100, Velocity: 100}}},
				},
				LengthTicks: 20048,
			},
		},
	}
	memory.StoreSequence(seq)
	defer memory.ClearSequences()

	fe := newFakeEngine(t)
	if err := Play(fe, "long-seq", nil); err != nil {
		t.Fatalf("Play failed: %v", err)
	}
	defer Stop()

	fe.mu.Lock()
	scheduledCount := len(fe.scheduled)
	fe.mu.Unlock()
	if scheduledCount < 6 { // 3 notes + 3 force-stops
		t.Fatalf("expected the entire non-looping timeline (including its last, far-future note) to be scheduled immediately, got only %d pending actions", scheduledCount)
	}
}

func TestOnCompleteFiresWhenNonLoopingSequenceFinishes(t *testing.T) {
	memory.StoreSequence(testSequence(false)) // 2 quarter notes @600bpm = 200ms total
	defer memory.ClearSequences()

	fe := newFakeEngine(t)
	completed := make(chan struct{})
	if err := Play(fe, "test-seq", func() { close(completed) }); err != nil {
		t.Fatalf("Play failed: %v", err)
	}
	defer Stop()

	select {
	case <-completed:
		t.Fatalf("onComplete fired too early, before the sequence's ~200ms had elapsed")
	case <-time.After(80 * time.Millisecond):
	}

	select {
	case <-completed:
	case <-time.After(1 * time.Second):
		t.Fatalf("onComplete never fired after the non-looping sequence finished")
	}
}

// TestOnCompleteWaitsForReleaseTail confirms onComplete doesn't fire the
// instant the written timeline ends: it must wait for the last note's
// still-sounding release tail (simulated here via fakeEngine's
// simulatedReleaseTail / AnySounding) to actually finish, so playback
// doesn't cut off mid-release.
func TestOnCompleteWaitsForReleaseTail(t *testing.T) {
	memory.StoreSequence(testSequence(false)) // ~200ms written timeline
	defer memory.ClearSequences()

	fe := newFakeEngine(t)
	fe.simulatedReleaseTail = 400 * time.Millisecond
	completed := make(chan struct{})
	if err := Play(fe, "test-seq", func() { close(completed) }); err != nil {
		t.Fatalf("Play failed: %v", err)
	}
	defer Stop()

	// Written timeline (~200ms) plus refillPollInterval slack has passed,
	// but the simulated release tail (400ms after the last note's onset)
	// has not - onComplete must not have fired yet.
	select {
	case <-completed:
		t.Fatalf("onComplete fired before the simulated release tail finished")
	case <-time.After(280 * time.Millisecond):
	}

	select {
	case <-completed:
	case <-time.After(1 * time.Second):
		t.Fatalf("onComplete never fired once the release tail finished")
	}
}

func TestOnCompleteDoesNotFireForLoopingSequence(t *testing.T) {
	memory.StoreSequence(testSequence(true))
	defer memory.ClearSequences()

	fe := newFakeEngine(t)
	completed := make(chan struct{})
	if err := Play(fe, "test-seq", func() { close(completed) }); err != nil {
		t.Fatalf("Play failed: %v", err)
	}
	defer Stop()

	select {
	case <-completed:
		t.Fatalf("onComplete must never fire for a looping sequence")
	case <-time.After(500 * time.Millisecond): // several loop iterations
	}
}

func TestOnCompleteDoesNotFireAfterExplicitStop(t *testing.T) {
	memory.StoreSequence(testSequence(false))
	defer memory.ClearSequences()

	fe := newFakeEngine(t)
	completed := make(chan struct{})
	if err := Play(fe, "test-seq", func() { close(completed) }); err != nil {
		t.Fatalf("Play failed: %v", err)
	}
	time.Sleep(10 * time.Millisecond) // well before the ~200ms sequence would finish on its own
	if err := Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	select {
	case <-completed:
		t.Fatalf("onComplete must not fire when playback was stopped explicitly, not finished naturally")
	case <-time.After(400 * time.Millisecond):
	}
}

func TestOnCompleteNilIsSafe(t *testing.T) {
	memory.StoreSequence(testSequence(false))
	defer memory.ClearSequences()

	fe := newFakeEngine(t)
	if err := Play(fe, "test-seq", nil); err != nil {
		t.Fatalf("Play failed: %v", err)
	}
	defer Stop()
	time.Sleep(300 * time.Millisecond) // past natural completion; must not panic
}

// TestOnCompleteMayCallStopWithoutDeadlocking covers the natural real-world
// use of onComplete: calling player.Stop() (or starting a new player.Play)
// from inside the callback itself, e.g. to shut the app down. That call
// re-enters this same playback's stopPlayback -> stopCompletionWatcher
// from the very goroutine driving the watcher, which must not deadlock
// waiting on its own completion.
func TestOnCompleteMayCallStopWithoutDeadlocking(t *testing.T) {
	memory.StoreSequence(testSequence(false))
	defer memory.ClearSequences()

	fe := newFakeEngine(t)
	returned := make(chan struct{})
	if err := Play(fe, "test-seq", func() {
		if err := Stop(); err != nil {
			t.Errorf("Stop from within onComplete failed: %v", err)
		}
		close(returned)
	}); err != nil {
		t.Fatalf("Play failed: %v", err)
	}
	defer Stop()

	select {
	case <-returned:
	case <-time.After(1 * time.Second):
		t.Fatalf("onComplete calling Stop() deadlocked")
	}
}

func TestPlayMissingSequenceErrors(t *testing.T) {
	fe := newFakeEngine(t)
	if err := Play(fe, "does-not-exist", nil); err == nil {
		t.Fatalf("expected an error for an unloaded sequence")
	}
}

func TestPlayUsesDefaultVoiceForMissingVoiceID(t *testing.T) {
	seq := testSequence(false)
	seq.Parts[1].VoiceID = 999 // not registered in go-fmml/memory
	memory.StoreSequence(seq)
	defer memory.ClearSequences()

	fe := newFakeEngine(t)
	if err := Play(fe, "test-seq", nil); err != nil {
		t.Fatalf("Play failed: %v", err)
	}
	defer Stop()
	time.Sleep(30 * time.Millisecond)

	fe.mu.Lock()
	defer fe.mu.Unlock()
	if len(fe.registeredVoic) == 0 || fe.registeredVoic[0] != 999 {
		t.Fatalf("expected default voice to be registered under id 999, got %+v", fe.registeredVoic)
	}
}

// TestForceStopPartFiresBeforeEachNote confirms every note event's
// ScheduleForceStopPart lands at (or before, in submission order) the same
// sample as its own ScheduleNoteOn calls, matching the ordering
// fmcore.Engine's real scheduler guarantees.
func TestForceStopPartFiresBeforeEachNote(t *testing.T) {
	memory.StoreSequence(testSequence(false))
	defer memory.ClearSequences()

	fe := newFakeEngine(t)
	if err := Play(fe, "test-seq", nil); err != nil {
		t.Fatalf("Play failed: %v", err)
	}
	defer Stop()

	time.Sleep(250 * time.Millisecond)

	fe.mu.Lock()
	defer fe.mu.Unlock()
	if len(fe.forceStops) != 2 {
		t.Fatalf("expected one ScheduleForceStopPart per note event, got %d: %+v", len(fe.forceStops), fe.forceStops)
	}
	if len(fe.noteOns) != 2 {
		t.Fatalf("expected 2 notes, got %d", len(fe.noteOns))
	}
	if !fe.forceStops[0].at.Before(fe.noteOns[0].at) && fe.forceStops[0].at != fe.noteOns[0].at {
		t.Fatalf("expected first ScheduleForceStopPart not to land after the first note")
	}
}

// TestPortamentoNoteSkipsForceStopAndCarriesBend covers Phase 5's true-
// legato portamento: the note that glides in ('&&', memory.NoteBend's
// Portamento flag) must not be preceded by a ScheduleForceStopPart call
// (that would force-mute the very note it's supposed to continue), unlike
// every ordinary note event.
func TestPortamentoNoteSkipsForceStopAndCarriesBend(t *testing.T) {
	seq := testSequence(false)
	seq.Parts[1].Events[1].Notes[0].Bend = memory.NoteBend{
		AttackOffsetSemitones: -2,
		AttackTicks:           24,
		Portamento:            true,
	}
	memory.StoreSequence(seq)
	defer memory.ClearSequences()

	fe := newFakeEngine(t)
	if err := Play(fe, "test-seq", nil); err != nil {
		t.Fatalf("Play failed: %v", err)
	}
	defer Stop()

	time.Sleep(250 * time.Millisecond)

	fe.mu.Lock()
	defer fe.mu.Unlock()
	if len(fe.forceStops) != 1 {
		t.Fatalf("expected only the first (non-portamento) note to get a ScheduleForceStopPart, got %d: %+v", len(fe.forceStops), fe.forceStops)
	}
	if len(fe.noteOns) != 2 {
		t.Fatalf("expected 2 notes, got %d", len(fe.noteOns))
	}
	got := fe.noteOns[1].bend
	want := fmcore.NoteBend{AttackOffsetSemitones: -2, AttackTicks: 24, Portamento: true}
	if got != want {
		t.Fatalf("expected the portamento note's bend to reach the engine as %+v, got %+v", want, got)
	}
}

// --- SkipPlay ---

func TestSeekTickComputation(t *testing.T) {
	memory.StoreSequence(testSequence(false))
	defer memory.ClearSequences()
	seq, ok := memory.GetSequence("test-seq")
	if !ok {
		t.Fatalf("test-seq not found")
	}
	p := newPlayback(newFakeEngine(t), seq)

	if got := p.seekTick(0); got != 0 {
		t.Fatalf("seekTick(0) = %d, want 0", got)
	}
	if got := p.seekTick(-1); got != 0 {
		t.Fatalf("seekTick(-1) = %d, want 0 (negative -> start over)", got)
	}
	if got := p.seekTick(0.1); got != 48 { // 100ms = 1 quarter note = 48 ticks @ tempo 600
		t.Fatalf("seekTick(0.1) = %d, want 48", got)
	}
	if got := p.seekTick(1000); got != 0 { // the sequence is only 200ms long
		t.Fatalf("seekTick(1000) = %d, want 0 (beyond the sequence's length -> start over)", got)
	}
}

func TestSkipPlayZeroBehavesLikePlay(t *testing.T) {
	memory.StoreSequence(testSequence(false))
	defer memory.ClearSequences()

	fe := newFakeEngine(t)
	if err := SkipPlay(fe, "test-seq", 0, nil); err != nil {
		t.Fatalf("SkipPlay failed: %v", err)
	}
	defer Stop()

	time.Sleep(250 * time.Millisecond)
	notes, _ := fe.snapshot()
	if len(notes) != 2 || notes[0].noteName != "C4" || notes[1].noteName != "D4" {
		t.Fatalf("expected SkipPlay(0) to play C4 then D4 from the start, got %+v", notes)
	}
}

func TestSkipPlaySkipsPastEarlierNotes(t *testing.T) {
	memory.StoreSequence(testSequence(false))
	defer memory.ClearSequences()

	fe := newFakeEngine(t)
	if err := SkipPlay(fe, "test-seq", 0.1, nil); err != nil { // exactly D4's own start
		t.Fatalf("SkipPlay failed: %v", err)
	}
	defer Stop()

	time.Sleep(250 * time.Millisecond)
	notes, _ := fe.snapshot()
	if len(notes) != 1 || notes[0].noteName != "D4" {
		t.Fatalf("expected SkipPlay(0.1s) to skip straight to D4 without ever playing C4, got %+v", notes)
	}
}

func TestSkipPlayMidNoteRewindsToItsStart(t *testing.T) {
	memory.StoreSequence(testSequence(false))
	defer memory.ClearSequences()

	fe := newFakeEngine(t)
	if err := SkipPlay(fe, "test-seq", 0.05, nil); err != nil { // strictly inside C4
		t.Fatalf("SkipPlay failed: %v", err)
	}
	defer Stop()

	time.Sleep(250 * time.Millisecond)
	notes, _ := fe.snapshot()
	if len(notes) != 2 || notes[0].noteName != "C4" || notes[1].noteName != "D4" {
		t.Fatalf("expected a mid-note seek to rewind slightly to C4's own start, got %+v", notes)
	}
}

func TestSkipPlayBeyondSequenceLengthStartsFromBeginning(t *testing.T) {
	memory.StoreSequence(testSequence(false))
	defer memory.ClearSequences()

	fe := newFakeEngine(t)
	if err := SkipPlay(fe, "test-seq", 10.0, nil); err != nil { // way past the 200ms song
		t.Fatalf("SkipPlay failed: %v", err)
	}
	defer Stop()

	time.Sleep(250 * time.Millisecond)
	notes, _ := fe.snapshot()
	if len(notes) != 2 || notes[0].noteName != "C4" || notes[1].noteName != "D4" {
		t.Fatalf("expected an out-of-range seek to be invalid and start from the beginning, got %+v", notes)
	}
}

func TestSkipPlayNegativeStartsFromBeginning(t *testing.T) {
	memory.StoreSequence(testSequence(false))
	defer memory.ClearSequences()

	fe := newFakeEngine(t)
	if err := SkipPlay(fe, "test-seq", -5, nil); err != nil {
		t.Fatalf("SkipPlay failed: %v", err)
	}
	defer Stop()

	time.Sleep(250 * time.Millisecond)
	notes, _ := fe.snapshot()
	if len(notes) != 2 || notes[0].noteName != "C4" || notes[1].noteName != "D4" {
		t.Fatalf("expected a negative seek to behave like Play (start from the beginning), got %+v", notes)
	}
}

func TestSkipPlayMissingSequenceErrors(t *testing.T) {
	if err := SkipPlay(newFakeEngine(t), "no-such-seq", 1.0, nil); err == nil {
		t.Fatalf("expected an error for a missing sequence")
	}
}

func TestSkipPlayStopsWhateverWasAlreadyPlaying(t *testing.T) {
	memory.StoreSequence(testSequence(true))
	defer memory.ClearSequences()

	fe := newFakeEngine(t)
	if err := Play(fe, "test-seq", nil); err != nil {
		t.Fatalf("Play failed: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	if err := SkipPlay(fe, "test-seq", 0.1, nil); err != nil {
		t.Fatalf("SkipPlay failed: %v", err)
	}
	defer Stop()
	if !IsPlaying() {
		t.Fatalf("expected SkipPlay to leave playback in the playing state")
	}
}

// --- [global] startOffset (CLAUDE.md's Phase 5 addendum) ---

func TestPlayStartOffsetSkipsEarlierNotesAndAppliesPriorStateChanges(t *testing.T) {
	seq := &memory.SequenceData{
		SequenceID: "offset-seq",
		Tempo:      600, // 100ms/quarter note
		Loop:       false,
		Parts: map[fmcore.PartID]*memory.PartSequence{
			1: {
				PartID:  1,
				VoiceID: 1,
				Volume:  1,
				Pan:     0,
				Events: []memory.SeqEvent{
					{AtTick: 0, Kind: memory.EventNote, Notes: []memory.NoteSpec{{NoteName: "C4", NoteType: "NOTE4", Sustain: 100, Velocity: 100}}},
					{AtTick: 48, Kind: memory.EventSetVoice, VoiceID: 2},
					{AtTick: 48, Kind: memory.EventSetPan, Pan: 0.5},
					{AtTick: 48, Kind: memory.EventNote, Notes: []memory.NoteSpec{{NoteName: "D4", NoteType: "NOTE4", Sustain: 100, Velocity: 100}}},
					{AtTick: 96, Kind: memory.EventNote, Notes: []memory.NoteSpec{{NoteName: "E4", NoteType: "NOTE4", Sustain: 100, Velocity: 100}}},
				},
				LengthTicks: 144,
			},
		},
		StartOffsetTicks: 90, // lands inside D4's span (48-96): C4 and D4 must both be skipped
	}
	memory.StoreSequence(seq)
	defer memory.ClearSequences()

	fe := newFakeEngine(t)
	if err := Play(fe, "offset-seq", nil); err != nil {
		t.Fatalf("Play failed: %v", err)
	}
	defer Stop()

	time.Sleep(150 * time.Millisecond)

	notes, setParts := fe.snapshot()
	if len(notes) != 1 || notes[0].noteName != "E4" {
		t.Fatalf("expected only E4 to fire (C4/D4 skipped by startOffset), got %+v", notes)
	}
	found := false
	for _, sp := range setParts {
		if sp.voiceID == 2 && sp.pan == 0.5 {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the pre-offset voice/pan change (tick 48, before offset 90) to still be applied, got %+v", setParts)
	}
}

func TestPlayStartOffsetDoesNotSoundANoteInProgressAtTheOffsetPoint(t *testing.T) {
	seq := &memory.SequenceData{
		SequenceID: "offset-inprogress-seq",
		Tempo:      600, // 100ms/quarter note
		Loop:       false,
		Parts: map[fmcore.PartID]*memory.PartSequence{
			1: {
				PartID:  1,
				VoiceID: 1,
				Volume:  1,
				Pan:     0,
				Events: []memory.SeqEvent{
					{AtTick: 0, Kind: memory.EventNote, Notes: []memory.NoteSpec{{NoteName: "C4", NoteType: "NOTE1", Sustain: 100, Velocity: 100}}}, // spans ticks 0-192
					{AtTick: 192, Kind: memory.EventNote, Notes: []memory.NoteSpec{{NoteName: "D4", NoteType: "NOTE4", Sustain: 100, Velocity: 100}}},
				},
				LengthTicks: 240,
			},
		},
		StartOffsetTicks: 96, // squarely inside C4's still-sounding span
	}
	memory.StoreSequence(seq)
	defer memory.ClearSequences()

	fe := newFakeEngine(t)
	if err := Play(fe, "offset-inprogress-seq", nil); err != nil {
		t.Fatalf("Play failed: %v", err)
	}
	defer Stop()

	time.Sleep(250 * time.Millisecond)

	notes, _ := fe.snapshot()
	if len(notes) != 1 || notes[0].noteName != "D4" {
		t.Fatalf("expected only D4 to fire - C4 was in progress at the offset point and must not sound (no back-snap), got %+v", notes)
	}
}

// TestPlayLoopRestartsFromStartOffsetNotTickZero is CLAUDE.md's addendum to
// startOffset: once startOffset has skipped the intro on the very first
// pass, looping back from the sequence's end must resume at the offset
// again - not replay the intro from tick 0 - so the skipped section never
// sounds on any iteration, not just the first.
func TestPlayLoopRestartsFromStartOffsetNotTickZero(t *testing.T) {
	seq := &memory.SequenceData{
		SequenceID: "offset-loop-seq",
		Tempo:      600, // 100ms/quarter note
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
		StartOffsetTicks: 48, // skips C4; loop body becomes D4,E4 only (96 ticks = 200ms/loop)
	}
	memory.StoreSequence(seq)
	defer memory.ClearSequences()

	fe := newFakeEngine(t)
	if err := Play(fe, "offset-loop-seq", nil); err != nil {
		t.Fatalf("Play failed: %v", err)
	}
	defer Stop()

	time.Sleep(700 * time.Millisecond) // ~3.5 loop bodies at 200ms each

	notes, _ := fe.snapshot()
	if len(notes) < 4 {
		t.Fatalf("expected at least 4 notes (D4/E4 repeating) in ~700ms, got %d: %+v", len(notes), notes)
	}
	for _, n := range notes {
		if n.noteName == "C4" {
			t.Fatalf("C4 (before startOffset) must never sound, on any loop iteration, got %+v", notes)
		}
	}
	// D4 should recur every loop body (~200ms), not every full original
	// length (~300ms) - that distinction is exactly what proves the loop
	// restarts from the offset rather than from tick 0.
	var d4Fires []time.Time
	for _, n := range notes {
		if n.noteName == "D4" {
			d4Fires = append(d4Fires, n.at)
		}
	}
	if len(d4Fires) < 2 {
		t.Fatalf("expected D4 to recur at least twice, got %d", len(d4Fires))
	}
	for i := 1; i < len(d4Fires); i++ {
		gap := d4Fires[i].Sub(d4Fires[i-1])
		if gap < 150*time.Millisecond || gap > 260*time.Millisecond {
			t.Errorf("gap between D4 recurrences = %v, want ~200ms (the offset-shrunk loop body); ~300ms would mean it wrongly restarted from tick 0", gap)
		}
	}
}

func testPCMSequence() *memory.SequenceData {
	return &memory.SequenceData{
		SequenceID: "pcm-test-seq",
		Tempo:      600,
		Loop:       false,
		PCMParts: map[pcmcore.PartID]*memory.PCMPartSequence{
			'A': {
				PartID:   'A',
				VoiceID:  1,
				PartType: pcmcore.OneShot,
				Volume:   1,
				Pan:      0,
				Events: []memory.PCMSeqEvent{
					{AtTick: 0, Kind: memory.PCMEventOneShotTrigger, OneShot: memory.PCMOneShotTrigger{NoteNumber: 36, Velocity: 100}},
					{AtTick: 24, Kind: memory.PCMEventOneShotTrigger, OneShot: memory.PCMOneShotTrigger{NoteNumber: 40, Velocity: 90}},
				},
				LengthTicks: 48,
			},
			'B': {
				PartID:   'B',
				VoiceID:  2,
				PartType: pcmcore.Long,
				Volume:   0.6,
				Pan:      16,
				Events: []memory.PCMSeqEvent{
					{AtTick: 0, Kind: memory.PCMEventNote, Notes: []memory.NoteSpec{{NoteName: "A4", NoteType: "NOTE4", Sustain: 100, Velocity: 100}}},
				},
				LengthTicks: 48,
			},
		},
	}
}

func TestPlayPCMOneShotTriggersFire(t *testing.T) {
	memory.StoreSequence(testPCMSequence())
	defer memory.ClearSequences()
	memory.StorePCMVoice(1, pcmcore.Voice{VoiceType: pcmcore.OneShot})
	memory.StorePCMVoice(2, pcmcore.Voice{VoiceType: pcmcore.Long})
	defer memory.ClearPCMVoices()

	fe := newFakeEngine(t)
	if err := Play(fe, "pcm-test-seq", nil); err != nil {
		t.Fatalf("Play failed: %v", err)
	}
	defer Stop()

	time.Sleep(250 * time.Millisecond)

	fe.mu.Lock()
	defer fe.mu.Unlock()
	if len(fe.pcmOneShotTriggers) != 2 {
		t.Fatalf("expected 2 oneShot triggers, got %d: %+v", len(fe.pcmOneShotTriggers), fe.pcmOneShotTriggers)
	}
	if fe.pcmOneShotTriggers[0].noteNumber != 36 || fe.pcmOneShotTriggers[1].noteNumber != 40 {
		t.Fatalf("unexpected trigger order: %+v", fe.pcmOneShotTriggers)
	}
	for _, fs := range fe.pcmForceStops {
		if fs.partID == 'A' {
			t.Fatalf("expected oneShot triggers never to force-stop the oneShot part, got a force-stop on part A")
		}
	}
}

func TestPlayPCMLongNoteFiresWithForceStop(t *testing.T) {
	memory.StoreSequence(testPCMSequence())
	defer memory.ClearSequences()
	memory.StorePCMVoice(1, pcmcore.Voice{VoiceType: pcmcore.OneShot})
	memory.StorePCMVoice(2, pcmcore.Voice{VoiceType: pcmcore.Long})
	defer memory.ClearPCMVoices()

	fe := newFakeEngine(t)
	if err := Play(fe, "pcm-test-seq", nil); err != nil {
		t.Fatalf("Play failed: %v", err)
	}
	defer Stop()

	time.Sleep(150 * time.Millisecond)

	fe.mu.Lock()
	defer fe.mu.Unlock()
	if len(fe.pcmNoteOns) != 1 || fe.pcmNoteOns[0].noteName != "A4" {
		t.Fatalf("expected 1 long PCM note (A4), got %+v", fe.pcmNoteOns)
	}
	if len(fe.pcmForceStops) != 1 {
		t.Fatalf("expected the long note event to force-stop the part first, got %d", len(fe.pcmForceStops))
	}
	if len(fe.pcmSetParts) == 0 {
		t.Fatalf("expected PCM parts to be set up via SetPCMPart")
	}
}

func TestPlayPCMUnknownVoiceSkipsPartWithoutError(t *testing.T) {
	memory.StoreSequence(testPCMSequence())
	defer memory.ClearSequences()
	// Neither PCM voice 1 nor 2 registered in memory.

	fe := newFakeEngine(t)
	if err := Play(fe, "pcm-test-seq", nil); err != nil {
		t.Fatalf("Play failed: %v", err)
	}
	defer Stop()

	time.Sleep(100 * time.Millisecond)

	fe.mu.Lock()
	defer fe.mu.Unlock()
	if len(fe.pcmOneShotTriggers) != 0 || len(fe.pcmNoteOns) != 0 {
		t.Fatalf("expected no PCM events to fire when the referenced voices aren't registered")
	}
}

func TestPlaySetsEngineMasterVolume(t *testing.T) {
	seq := testSequence(false)
	seq.MasterVolume = 0.5
	memory.StoreSequence(seq)
	defer memory.ClearSequences()

	fe := newFakeEngine(t)
	if err := Play(fe, "test-seq", nil); err != nil {
		t.Fatalf("Play failed: %v", err)
	}
	defer Stop()

	fe.mu.Lock()
	defer fe.mu.Unlock()
	if fe.masterVolume != 0.5 {
		t.Fatalf("expected engine.SetMasterVolume(0.5) to be called, got %f", fe.masterVolume)
	}
}

func TestIsPlayingTracksPlaybackState(t *testing.T) {
	memory.StoreSequence(testSequence(false))
	defer memory.ClearSequences()

	if IsPlaying() {
		t.Fatalf("expected IsPlaying to be false before any Play")
	}

	fe := newFakeEngine(t)
	if err := Play(fe, "test-seq", nil); err != nil {
		t.Fatalf("Play failed: %v", err)
	}
	defer Stop()

	if !IsPlaying() {
		t.Fatalf("expected IsPlaying to be true right after Play")
	}

	if err := Pause(); err != nil {
		t.Fatalf("Pause failed: %v", err)
	}
	if IsPlaying() {
		t.Fatalf("expected IsPlaying to be false while paused")
	}

	if err := Resume(); err != nil {
		t.Fatalf("Resume failed: %v", err)
	}
	if !IsPlaying() {
		t.Fatalf("expected IsPlaying to be true after Resume")
	}

	if err := Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	if IsPlaying() {
		t.Fatalf("expected IsPlaying to be false after Stop")
	}
}

func TestIsPlayingFalseWhenNothingEverPlayed(t *testing.T) {
	if IsPlaying() {
		t.Fatalf("expected IsPlaying to be false when nothing has ever played")
	}
}

func TestRewindWhilePlayingRestartsFromBeginning(t *testing.T) {
	memory.StoreSequence(testSequence(false))
	defer memory.ClearSequences()

	fe := newFakeEngine(t)
	if err := Play(fe, "test-seq", nil); err != nil {
		t.Fatalf("Play failed: %v", err)
	}
	defer Stop()

	time.Sleep(150 * time.Millisecond) // past D4's onset (AtTick=48 / 100ms)

	if err := Rewind(); err != nil {
		t.Fatalf("Rewind failed: %v", err)
	}
	time.Sleep(250 * time.Millisecond)

	notes, _ := fe.snapshot()
	if len(notes) < 4 {
		t.Fatalf("expected at least 4 notes (C4,D4,C4,D4) after rewind, got %d: %+v", len(notes), notes)
	}
	last2 := notes[len(notes)-2:]
	if last2[0].noteName != "C4" || last2[1].noteName != "D4" {
		t.Fatalf("expected the rewound sequence to replay C4 then D4, got %+v", last2)
	}
}

func TestRewindWhilePlayingForceStopsSoundingParts(t *testing.T) {
	memory.StoreSequence(testSequence(false))
	defer memory.ClearSequences()

	fe := newFakeEngine(t)
	if err := Play(fe, "test-seq", nil); err != nil {
		t.Fatalf("Play failed: %v", err)
	}
	defer Stop()

	time.Sleep(30 * time.Millisecond)
	if err := Rewind(); err != nil {
		t.Fatalf("Rewind failed: %v", err)
	}
	time.Sleep(30 * time.Millisecond)

	fe.mu.Lock()
	defer fe.mu.Unlock()
	if len(fe.forceStops) == 0 {
		t.Fatalf("expected Rewind to force-stop whatever was still sounding")
	}
}

// TestPauseForceStopsSoundingFMParts confirms Pause fades out whatever FM
// part was still sounding instead of leaving it to ring through the pause,
// mirroring Stop's identical behavior.
func TestPauseForceStopsSoundingFMParts(t *testing.T) {
	memory.StoreSequence(testSequence(false))
	defer memory.ClearSequences()

	fe := newFakeEngine(t)
	if err := Play(fe, "test-seq", nil); err != nil {
		t.Fatalf("Play failed: %v", err)
	}
	defer Stop()

	time.Sleep(30 * time.Millisecond)
	if err := Pause(); err != nil {
		t.Fatalf("Pause failed: %v", err)
	}

	fe.mu.Lock()
	defer fe.mu.Unlock()
	if len(fe.forceStops) == 0 {
		t.Fatalf("expected Pause to force-stop whatever FM part was still sounding")
	}
}

// TestPauseForceStopsSoundingPCMParts is
// TestPauseForceStopsSoundingFMParts's PCM counterpart.
func TestPauseForceStopsSoundingPCMParts(t *testing.T) {
	memory.StoreSequence(testPCMSequence())
	defer memory.ClearSequences()
	memory.StorePCMVoice(1, pcmcore.Voice{VoiceType: pcmcore.OneShot})
	memory.StorePCMVoice(2, pcmcore.Voice{VoiceType: pcmcore.Long})
	defer memory.ClearPCMVoices()

	fe := newFakeEngine(t)
	if err := Play(fe, "pcm-test-seq", nil); err != nil {
		t.Fatalf("Play failed: %v", err)
	}
	defer Stop()

	time.Sleep(30 * time.Millisecond)
	if err := Pause(); err != nil {
		t.Fatalf("Pause failed: %v", err)
	}

	fe.mu.Lock()
	defer fe.mu.Unlock()
	if len(fe.pcmForceStops) == 0 {
		t.Fatalf("expected Pause to force-stop whatever PCM part was still sounding")
	}
}

// TestStopForceStopsSoundingFMParts confirms Stop force-stops (fades out)
// whatever FM part was still sounding instead of leaving it to ring out on
// its own, so a stopped sequence doesn't keep audibly playing its release
// tail.
func TestStopForceStopsSoundingFMParts(t *testing.T) {
	memory.StoreSequence(testSequence(false))
	defer memory.ClearSequences()

	fe := newFakeEngine(t)
	if err := Play(fe, "test-seq", nil); err != nil {
		t.Fatalf("Play failed: %v", err)
	}

	time.Sleep(30 * time.Millisecond)
	if err := Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	fe.mu.Lock()
	defer fe.mu.Unlock()
	if len(fe.forceStops) == 0 {
		t.Fatalf("expected Stop to force-stop whatever FM part was still sounding")
	}
}

// TestStopForceStopsSoundingPCMParts is TestStopForceStopsSoundingFMParts's
// PCM counterpart.
func TestStopForceStopsSoundingPCMParts(t *testing.T) {
	memory.StoreSequence(testPCMSequence())
	defer memory.ClearSequences()
	memory.StorePCMVoice(1, pcmcore.Voice{VoiceType: pcmcore.OneShot})
	memory.StorePCMVoice(2, pcmcore.Voice{VoiceType: pcmcore.Long})
	defer memory.ClearPCMVoices()

	fe := newFakeEngine(t)
	if err := Play(fe, "pcm-test-seq", nil); err != nil {
		t.Fatalf("Play failed: %v", err)
	}

	time.Sleep(30 * time.Millisecond)
	if err := Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	fe.mu.Lock()
	defer fe.mu.Unlock()
	if len(fe.pcmForceStops) == 0 {
		t.Fatalf("expected Stop to force-stop whatever PCM part was still sounding")
	}
}

func TestRewindWhilePausedTakesEffectOnResume(t *testing.T) {
	memory.StoreSequence(testSequence(false))
	defer memory.ClearSequences()

	fe := newFakeEngine(t)
	if err := Play(fe, "test-seq", nil); err != nil {
		t.Fatalf("Play failed: %v", err)
	}
	defer Stop()

	time.Sleep(150 * time.Millisecond) // past D4's onset
	if err := Pause(); err != nil {
		t.Fatalf("Pause failed: %v", err)
	}
	notesAtPause, _ := fe.snapshot()

	if err := Rewind(); err != nil {
		t.Fatalf("Rewind failed: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	notesWhileStillPaused, _ := fe.snapshot()
	if len(notesWhileStillPaused) != len(notesAtPause) {
		t.Fatalf("expected Rewind while paused not to fire any notes immediately, got %d -> %d", len(notesAtPause), len(notesWhileStillPaused))
	}

	if err := Resume(); err != nil {
		t.Fatalf("Resume failed: %v", err)
	}
	time.Sleep(250 * time.Millisecond)
	notesAfterResume, _ := fe.snapshot()
	if len(notesAfterResume) != len(notesAtPause)+2 {
		t.Fatalf("expected resume-after-rewind to replay C4,D4 from the start, got %d -> %d: %+v", len(notesAtPause), len(notesAfterResume), notesAfterResume)
	}
	last2 := notesAfterResume[len(notesAfterResume)-2:]
	if last2[0].noteName != "C4" || last2[1].noteName != "D4" {
		t.Fatalf("expected replay order C4, D4 after rewind+resume, got %+v", last2)
	}
}

func TestRewindMissingSequenceErrors(t *testing.T) {
	if err := Rewind(); err == nil {
		t.Fatalf("expected an error calling Rewind when nothing has ever played")
	}
}

// reverbTestSequence is testSequence(false) with Phase 4 reverb settings
// attached: a global reverb configuration plus a nonzero FM part send.
func reverbTestSequence() *memory.SequenceData {
	seq := testSequence(false)
	seq.ReverbEnabled = true
	seq.ReverbType = "normal"
	seq.ReverbTimeSeconds = 1.5
	seq.ReverbLevel = 0.5
	seq.Parts[1].ReverbSend = 0.3
	return seq
}

// TestPlayConfiguresReverbAndPartSends confirms Play resolves a sequence's
// [global] reverb*/reverbType/reverbTime/reverbLevel settings into a single
// engine.SetReverb call, and each FM part's own reverbSend into
// engine.SetPartReverbSend - both up front, before any notes fire.
func TestPlayConfiguresReverbAndPartSends(t *testing.T) {
	memory.StoreSequence(reverbTestSequence())
	defer memory.ClearSequences()

	fe := newFakeEngine(t)
	if err := Play(fe, "test-seq", nil); err != nil {
		t.Fatalf("Play failed: %v", err)
	}
	defer Stop()

	fe.mu.Lock()
	defer fe.mu.Unlock()
	if len(fe.reverbConfigs) != 1 {
		t.Fatalf("expected exactly 1 SetReverb call, got %+v", fe.reverbConfigs)
	}
	got := fe.reverbConfigs[0]
	if !got.enabled || got.kind != "normal" || got.timeSeconds != 1.5 || got.level != 0.5 {
		t.Fatalf("unexpected SetReverb call: %+v", got)
	}

	if len(fe.partReverbSends) == 0 {
		t.Fatalf("expected SetPartReverbSend to be called")
	}
	if fe.partReverbSends[0].partID != 1 || fe.partReverbSends[0].send != 0.3 {
		t.Fatalf("expected part 1's reverbSend 0.3 to be set, got %+v", fe.partReverbSends)
	}
}

// TestPlayConfiguresPCMPartReverbSends is
// TestPlayConfiguresReverbAndPartSends's PCM counterpart: a Long part's
// send goes through SetPCMPartReverbSend, a OneShot part's per-note map
// through SetPCMPartOneShotReverbSend.
func TestPlayConfiguresPCMPartReverbSends(t *testing.T) {
	seq := testPCMSequence()
	seq.PCMParts['A'].OneShotReverbSend = map[uint8]float64{36: 0.4}
	seq.PCMParts['B'].ReverbSend = 0.6
	memory.StoreSequence(seq)
	defer memory.ClearSequences()
	memory.StorePCMVoice(1, pcmcore.Voice{VoiceType: pcmcore.OneShot})
	memory.StorePCMVoice(2, pcmcore.Voice{VoiceType: pcmcore.Long})
	defer memory.ClearPCMVoices()

	fe := newFakeEngine(t)
	if err := Play(fe, "pcm-test-seq", nil); err != nil {
		t.Fatalf("Play failed: %v", err)
	}
	defer Stop()

	fe.mu.Lock()
	defer fe.mu.Unlock()
	if len(fe.pcmPartOneShotSends) != 1 || fe.pcmPartOneShotSends[0].partID != 'A' || fe.pcmPartOneShotSends[0].sends[36] != 0.4 {
		t.Fatalf("expected OneShot part A's per-note reverb send map to be set, got %+v", fe.pcmPartOneShotSends)
	}
	if len(fe.pcmPartReverbSends) != 1 || fe.pcmPartReverbSends[0].partID != 'B' || fe.pcmPartReverbSends[0].send != 0.6 {
		t.Fatalf("expected Long part B's reverb send 0.6 to be set, got %+v", fe.pcmPartReverbSends)
	}
}

// TestPauseDucksReverb confirms Pause silences the master reverb tail
// (Phase 4: "極短フェードアウトしてリバーブリリースを残さない様にする"),
// mirroring its ScheduleForceStopPart call for the dry signal.
func TestPauseDucksReverb(t *testing.T) {
	memory.StoreSequence(testSequence(false))
	defer memory.ClearSequences()

	fe := newFakeEngine(t)
	if err := Play(fe, "test-seq", nil); err != nil {
		t.Fatalf("Play failed: %v", err)
	}
	defer Stop()

	time.Sleep(30 * time.Millisecond)
	if err := Pause(); err != nil {
		t.Fatalf("Pause failed: %v", err)
	}

	fe.mu.Lock()
	defer fe.mu.Unlock()
	if fe.reverbDuckCount == 0 {
		t.Fatalf("expected Pause to duck the reverb tail")
	}
}

// TestStopDucksReverb is TestPauseDucksReverb's Stop counterpart.
func TestStopDucksReverb(t *testing.T) {
	memory.StoreSequence(testSequence(false))
	defer memory.ClearSequences()

	fe := newFakeEngine(t)
	if err := Play(fe, "test-seq", nil); err != nil {
		t.Fatalf("Play failed: %v", err)
	}

	time.Sleep(30 * time.Millisecond)
	if err := Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	fe.mu.Lock()
	defer fe.mu.Unlock()
	if fe.reverbDuckCount == 0 {
		t.Fatalf("expected Stop to duck the reverb tail")
	}
}

// TestRewindDucksReverb is TestPauseDucksReverb's Rewind counterpart.
func TestRewindDucksReverb(t *testing.T) {
	memory.StoreSequence(testSequence(false))
	defer memory.ClearSequences()

	fe := newFakeEngine(t)
	if err := Play(fe, "test-seq", nil); err != nil {
		t.Fatalf("Play failed: %v", err)
	}
	defer Stop()

	time.Sleep(30 * time.Millisecond)
	if err := Rewind(); err != nil {
		t.Fatalf("Rewind failed: %v", err)
	}

	fe.mu.Lock()
	defer fe.mu.Unlock()
	if fe.reverbDuckCount == 0 {
		t.Fatalf("expected Rewind to duck the reverb tail")
	}
}
