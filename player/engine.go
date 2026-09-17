/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package player

import (
	"github.com/megaak-soft/go-fmml/fmcore"
	"github.com/megaak-soft/go-fmml/pcmcore"
)

// Engine is the subset of fmcore.Engine that the player needs to drive
// playback (both FM, Phase 1/2, and PCM, Phase 3). *fmcore.Engine satisfies
// it automatically; it exists mainly so tests can play sequences against a
// fake engine without opening a real audio device.
//
// Note timing goes entirely through the Schedule*/SampleClock/SampleRate
// methods: the player computes every event's target as an absolute sample
// position once (from the sequence's tick-based timeline) and hands it to
// the engine, which fires it from inside its own audio render loop. This
// is what makes playback timing immune to this package's own goroutine
// scheduling jitter - and, since ScheduleForceStopPart's cutoff is a fixed
// fade independent of any voice's envelope, correct regardless of how a
// voice's ReleaseRate is tuned. The same holds for the PCM Schedule*
// methods below: fmcore.Engine mixes PCM playback from the same render
// loop and sample clock as FM, so FM and PCM parts never drift apart.
type Engine interface {
	RegisterVoice(id fmcore.VoiceID, voice fmcore.Voice)
	SetPart(id fmcore.PartID, voice fmcore.VoiceID, volume, pan float64) error
	SetMasterVolume(volume float64)
	// SetMasterVolumeSmooth/MasterVolumeTarget back Phase 7's master-volume
	// fade methods (see go-fmml/player's SetMasterVolume/
	// SetMasterVolumePercent/FadeInPlay/FadeOutPlay/FadeOut and the short
	// fades Play/Pause/Resume/Stop apply on their own transitions).
	SetMasterVolumeSmooth(target float64, rampSeconds float64)
	MasterVolumeTarget() float64
	SetPartReverbSend(id fmcore.PartID, send float64)

	SampleClock() int64
	SampleRate() int
	ScheduleNoteOn(atSample int64, partID fmcore.PartID, noteName string, noteType string, sustain int, velocity uint8) error
	ScheduleNoteOnWithBend(atSample int64, partID fmcore.PartID, noteName string, noteType string, sustain int, velocity uint8, bend fmcore.NoteBend) error
	ScheduleForceStopPart(atSample int64, partID fmcore.PartID)
	ScheduleSetPart(atSample int64, partID fmcore.PartID, voiceID fmcore.VoiceID, volume, pan float64)
	// ScheduleSetTempo backs Phase 7's [conductor] tempo automation (see
	// go-fmml/analyze's conductor parsing and go-fmml/player's
	// newPlayback, which schedules one of these per tempo-map breakpoint).
	ScheduleSetTempo(atSample int64, bpm float64)
	CancelScheduled()
	AnySounding(partIDs ...fmcore.PartID) bool

	RegisterPCMVoice(id pcmcore.VoiceID, voice pcmcore.Voice)
	SetPCMPart(id pcmcore.PartID, voice pcmcore.VoiceID, volume, pan float64) error
	ScheduleTriggerPCMOneShot(atSample int64, partID pcmcore.PartID, noteNumber uint8, velocity uint8)
	ScheduleNoteOnPCMLong(atSample int64, partID pcmcore.PartID, noteName string, noteType string, sustain int, velocity uint8) error
	ScheduleNoteOnPCMLongWithBend(atSample int64, partID pcmcore.PartID, noteName string, noteType string, sustain int, velocity uint8, bend pcmcore.NoteBend) error
	ScheduleForceStopPCMPart(atSample int64, partID pcmcore.PartID)
	ScheduleSetPCMPart(atSample int64, partID pcmcore.PartID, voiceID pcmcore.VoiceID, volume, pan float64)
	AnySoundingPCM(partIDs ...pcmcore.PartID) bool
	SetPCMPartReverbSend(id pcmcore.PartID, send float64)
	SetPCMPartOneShotReverbSend(id pcmcore.PartID, sends map[uint8]float64)

	// SetReverb/DuckReverb drive the master reverb send/return effect
	// (Phase 4, see go-fmml/reverb). SetReverb is called once per Play
	// from the sequence's [global] reverb*/reverbType/reverbTime/
	// reverbLevel settings (already resolved by go-fmml/analyze's
	// ParseMML - see memory.SequenceData's Reverb* fields); DuckReverb is
	// called on Pause/Stop/Rewind alongside ScheduleForceStopPart/
	// ScheduleForceStopPCMPart so a still-ringing reverb tail never
	// survives past the point playback actually stopped.
	SetReverb(enabled bool, kind string, timeSeconds, level float64)
	DuckReverb()
}
