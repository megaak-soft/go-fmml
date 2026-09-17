/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package fmcore

import "github.com/megaak-soft/go-fmml/pcmcore"

// This file exposes go-fmml/pcmcore's PCM playback (Phase 3) through
// Engine, so a single Engine instance drives both FM synthesis and PCM
// sample playback off the same sample clock and the same oto output (see
// engine.go's render/mixSample and pcmcore.Engine's doc comment for why
// that single-clock design matters for sync). Every method here just locks
// Engine's own mutex and delegates to the embedded pcmcore.Engine, which
// has no locking of its own.

// RegisterPCMVoice stores a PCM voice (音色) definition in the PCM voice
// bank under id, overwriting any existing voice with the same id.
func (e *Engine) RegisterPCMVoice(id pcmcore.VoiceID, voice pcmcore.Voice) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pcm.RegisterVoice(id, voice)
}

// ClearPCMVoices deletes every stored PCM voice, freeing the PCM voice
// bank.
func (e *Engine) ClearPCMVoices() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pcm.ClearVoices()
}

// SetPCMPart assigns a registered PCM voice to a PCM sounding part
// ('A'-'P'), with volume in [0,1] and pan in [-1,1] (-1 = full left, 0 =
// center, 1 = full right) - a continuous scale, not just those three
// points; the caller converts from CLAUDE.md's -16..16 PCM pan scale (e.g.
// 7 there becomes roughly 0.44 here, slightly right of center).
func (e *Engine) SetPCMPart(id pcmcore.PartID, voice pcmcore.VoiceID, volume, pan float64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.pcm.SetPart(id, voice, volume, pan)
}

// ScheduleTriggerPCMOneShot arranges for a OneShot PCM sample to be
// triggered on partID once the engine's sample clock reaches atSample (see
// ScheduleNoteOn's FM counterpart).
func (e *Engine) ScheduleTriggerPCMOneShot(atSample int64, partID pcmcore.PartID, noteNumber uint8, velocity uint8) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pcm.ScheduleTriggerOneShot(atSample, partID, noteNumber, velocity)
}

// ScheduleNoteOnPCMLong arranges for a Long PCM voice note to sound on
// partID once the engine's sample clock reaches atSample.
func (e *Engine) ScheduleNoteOnPCMLong(atSample int64, partID pcmcore.PartID, noteName string, noteType string, sustain int, velocity uint8) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.pcm.ScheduleNoteOnLong(atSample, partID, noteName, noteType, sustain, velocity)
}

// ScheduleNoteOnPCMLongWithBend is ScheduleNoteOnPCMLong plus an optional
// linear pitch-glide envelope (Phase 5's portamento/pitch-bend MML
// notation - see pcmcore.NoteBend). A zero-value bend behaves exactly like
// ScheduleNoteOnPCMLong.
func (e *Engine) ScheduleNoteOnPCMLongWithBend(atSample int64, partID pcmcore.PartID, noteName string, noteType string, sustain int, velocity uint8, bend pcmcore.NoteBend) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.pcm.ScheduleNoteOnLongWithBend(atSample, partID, noteName, noteType, sustain, velocity, bend)
}

// ScheduleForceStopPCMPart arranges for every note currently sounding on a
// PCM partID to stop once the engine's sample clock reaches atSample.
func (e *Engine) ScheduleForceStopPCMPart(atSample int64, partID pcmcore.PartID) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pcm.ScheduleForceStopPart(atSample, partID)
}

// ScheduleSetPCMPart arranges for a PCM partID's voice/volume/pan
// assignment to change once the engine's sample clock reaches atSample.
func (e *Engine) ScheduleSetPCMPart(atSample int64, partID pcmcore.PartID, voiceID pcmcore.VoiceID, volume, pan float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pcm.ScheduleSetPart(atSample, partID, voiceID, volume, pan)
}

// AnySoundingPCM reports whether any of the given PCM parts currently has a
// note still audible, including a Long note's release tail.
func (e *Engine) AnySoundingPCM(partIDs ...pcmcore.PartID) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.pcm.AnySounding(partIDs...)
}

// SetPCMPartReverbSend sets a Long PCM part's reverb send level (Phase 4),
// in [0,1]. Unused for a OneShot part - see SetPCMPartOneShotReverbSend.
func (e *Engine) SetPCMPartReverbSend(id pcmcore.PartID, send float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pcm.SetPartReverbSend(id, send)
}

// SetPCMPartOneShotReverbSend sets a OneShot PCM part's per-note reverb
// send levels (Phase 4): MIDI note number -> [0,1] send level. Unused for a
// Long part - see SetPCMPartReverbSend.
func (e *Engine) SetPCMPartOneShotReverbSend(id pcmcore.PartID, sends map[uint8]float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pcm.SetPartOneShotReverbSend(id, sends)
}
