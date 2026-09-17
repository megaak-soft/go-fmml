/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package fmcore

import (
	"sort"

	"github.com/megaak-soft/go-fmml/common"
)

// scheduledKind distinguishes the actions the sample-accurate scheduler can
// perform.
type scheduledKind int

const (
	schedNoteOn scheduledKind = iota
	schedForceStopPart
	schedSetPart
	schedSetTempo
)

// scheduledAction is one caller-requested action to run once the engine's
// sample clock reaches atSample. Actions run in (atSample, submission
// order) order, from inside the audio render loop itself (see
// Engine.runDueScheduled), so their timing is exact and immune to any
// jitter in whatever goroutine scheduled them.
type scheduledAction struct {
	atSample int64
	seq      uint64
	kind     scheduledKind

	// schedNoteOn
	partID     PartID
	noteNumber uint8
	noteType   string
	sustain    int
	velocity   uint8
	bend       NoteBend // Phase 5; zero value = no pitch glide

	// schedSetPart
	voiceID VoiceID
	volume  float64
	pan     float64

	// schedSetTempo
	tempoBPM float64
}

// ScheduleNoteOn arranges for a note to sound on partID once the engine's
// internal sample clock reaches atSample (see SampleClock/SampleRate),
// rather than sounding it immediately as NoteOn does. This is the
// building block a sequencer (see go-fmml/player) uses to get
// sample-accurate note timing driven by the same clock that generates
// audio, instead of a wall-clock timer that's subject to OS/goroutine
// scheduling jitter.
//
// noteName/noteType are validated immediately, so a bad value is reported
// right away rather than silently later; the note's duration is still
// derived from common.Tempo and sustain at the moment it actually fires
// (matching NoteOn's behavior), not at scheduling time.
func (e *Engine) ScheduleNoteOn(atSample int64, partID PartID, noteName string, noteType string, sustain int, velocity uint8) error {
	return e.ScheduleNoteOnWithBend(atSample, partID, noteName, noteType, sustain, velocity, NoteBend{})
}

// ScheduleNoteOnWithBend is ScheduleNoteOn plus an optional linear
// pitch-glide envelope (Phase 5's portamento/pitch-bend MML notation - see
// NoteBend). A zero-value bend behaves exactly like ScheduleNoteOn.
func (e *Engine) ScheduleNoteOnWithBend(atSample int64, partID PartID, noteName string, noteType string, sustain int, velocity uint8, bend NoteBend) error {
	noteNumber, err := parseNoteName(noteName)
	if err != nil {
		return err
	}
	if _, err := noteTypeBeats(noteType); err != nil {
		return err
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	e.nextSchedSeq++
	e.scheduled = append(e.scheduled, scheduledAction{
		atSample: atSample, seq: e.nextSchedSeq, kind: schedNoteOn,
		partID: partID, noteNumber: noteNumber, noteType: noteType, sustain: sustain, velocity: velocity, bend: bend,
	})
	e.sortScheduled()
	return nil
}

// ScheduleForceStopPart arranges for every note currently sounding on
// partID to be force-muted (see runningNote.forceMute) once the engine's
// sample clock reaches atSample. The fade this triggers has a fixed,
// short duration regardless of the sounding voice's own envelope/
// ReleaseRate, so a caller can guarantee no audible bleed into whatever
// is scheduled to start at the same sample without having to tune (or
// depend on) any voice's tone-color settings.
func (e *Engine) ScheduleForceStopPart(atSample int64, partID PartID) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.nextSchedSeq++
	e.scheduled = append(e.scheduled, scheduledAction{atSample: atSample, seq: e.nextSchedSeq, kind: schedForceStopPart, partID: partID})
	e.sortScheduled()
}

// ScheduleSetPart arranges for partID's voice/volume/pan assignment to
// change once the engine's sample clock reaches atSample (e.g. an MML
// part's mid-sequence '@'/'P' commands).
func (e *Engine) ScheduleSetPart(atSample int64, partID PartID, voiceID VoiceID, volume, pan float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.nextSchedSeq++
	e.scheduled = append(e.scheduled, scheduledAction{atSample: atSample, seq: e.nextSchedSeq, kind: schedSetPart, partID: partID, voiceID: voiceID, volume: volume, pan: pan})
	e.sortScheduled()
}

// ScheduleSetTempo arranges for the sequencer's shared tempo (common.Tempo)
// to change to bpm once the engine's sample clock reaches atSample (Phase
// 7's [conductor] tempo automation - see go-fmml/analyze's conductor
// parsing and go-fmml/player's use of this method). Firing this through
// the same sample-accurate queue as ScheduleNoteOn guarantees a note
// scheduled at or after this sample already sees the new tempo when its own
// duration is resolved (see fireScheduled's schedNoteOn case) - and, since
// this runs from fmcore's own render loop before it calls into
// go-fmml/pcmcore's Advance for that same sample, a PCM Long note's tied-
// note duration calc sees it too.
func (e *Engine) ScheduleSetTempo(atSample int64, bpm float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.nextSchedSeq++
	e.scheduled = append(e.scheduled, scheduledAction{atSample: atSample, seq: e.nextSchedSeq, kind: schedSetTempo, tempoBPM: bpm})
	e.sortScheduled()
}

// CancelScheduled discards every not-yet-fired scheduled action, FM and PCM
// alike (see go-fmml/pcmcore's Engine.CancelScheduled). Notes already
// sounding are unaffected (see ScheduleForceStopPart to silence them too).
// A sequencer calls this when pausing or re-planning playback.
func (e *Engine) CancelScheduled() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.scheduled = nil
	e.pcm.CancelScheduled()
}

// SampleClock reports the engine's current audio sample position: the
// clock that ScheduleNoteOn/ScheduleForceStopPart/ScheduleSetPart's
// atSample arguments are measured against.
func (e *Engine) SampleClock() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.sampleClock
}

// SampleRate reports the engine's configured audio sample rate, fixed for
// the lifetime of the Engine.
func (e *Engine) SampleRate() int {
	return e.sampleRate
}

// AnySounding reports whether any of the given parts currently has a note
// still audible - including one in its release tail, however long that
// voice's ReleaseRate makes it. Lets a caller (see go-fmml/player's
// Play onComplete) wait for the actual sound to finish, not just for the
// written timeline to end.
func (e *Engine) AnySounding(partIDs ...PartID) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, id := range partIDs {
		p, ok := e.parts[id]
		if ok && len(p.notes) > 0 {
			return true
		}
	}
	return false
}

func (e *Engine) sortScheduled() {
	sort.Slice(e.scheduled, func(i, j int) bool {
		if e.scheduled[i].atSample != e.scheduled[j].atSample {
			return e.scheduled[i].atSample < e.scheduled[j].atSample
		}
		return e.scheduled[i].seq < e.scheduled[j].seq
	})
}

// runDueScheduled fires every scheduled action whose atSample has been
// reached, in order. Must be called with e.mu held; called once per
// sample from render, before that sample is mixed, so a note scheduled
// for sample N is already sounding when sample N itself is rendered.
func (e *Engine) runDueScheduled() {
	i := 0
	for i < len(e.scheduled) && e.scheduled[i].atSample <= e.sampleClock {
		e.fireScheduled(e.scheduled[i])
		i++
	}
	if i > 0 {
		e.scheduled = e.scheduled[i:]
	}
}

// fireScheduled must be called with e.mu held.
func (e *Engine) fireScheduled(a scheduledAction) {
	switch a.kind {
	case schedNoteOn:
		var durationMs int
		if a.bend.TieTicks > 0 {
			// Phase 5.1's single-'&' tie: the combined chain's total held
			// length is already resolved into ticks (see
			// analyze/mmlcommands.go), so duration comes from that instead
			// of a.noteType's own (single note-length) beats.
			durationMs = int(common.TicksToMs(a.bend.TieTicks, common.Tempo) * float64(a.sustain) / 100)
		} else {
			beats, err := noteTypeBeats(a.noteType)
			if err != nil {
				return // already validated in ScheduleNoteOn; defensive only
			}
			quarterNoteMs := 60000 / common.Tempo
			durationMs = int(beats * quarterNoteMs * float64(a.sustain) / 100)
		}
		rb := e.resolveBend(a.bend)
		if rb.portamento {
			_, _ = e.glideNoteOn(a.partID, a.noteNumber, durationMs, a.velocity, rb)
		} else {
			_, _ = e.triggerNote(a.partID, a.noteNumber, durationMs, a.velocity, rb)
		}

	case schedForceStopPart:
		p, ok := e.parts[a.partID]
		if !ok {
			return
		}
		for _, n := range p.notes {
			n.forceMute(e.sampleClock, e.sampleRate)
		}

	case schedSetPart:
		p, ok := e.parts[a.partID]
		if !ok {
			p = &part{}
			e.parts[a.partID] = p
		}
		p.voiceID = a.voiceID
		p.volume = clamp01(a.volume)
		p.pan = clampPan(a.pan)

	case schedSetTempo:
		if a.tempoBPM > 0 {
			common.Tempo = a.tempoBPM
		}
	}
}
