/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

// Package memory holds the FM and PCM voice banks and parsed sequence data
// that go-fmml/fileio populates and go-fmml/player reads from, keyed by
// voice ID / sequence ID so multiple voices and sequences can be resident
// at once.
package memory

import (
	"github.com/megaak-soft/go-fmml/common"
	"github.com/megaak-soft/go-fmml/fmcore"
	"github.com/megaak-soft/go-fmml/pcmcore"
)

// SeqEventKind distinguishes the kinds of timed events a part's timeline can
// contain.
type SeqEventKind int

const (
	// EventNote sounds one or more notes (Notes has >1 entries for a chord).
	EventNote SeqEventKind = iota
	// EventSetPan changes the part's pan from this point in the timeline on.
	EventSetPan
	// EventSetVoice changes the part's voice from this point in the timeline on.
	EventSetVoice
)

// NoteSpec is one note to sound, already resolved into the same argument
// shape fmcore.Engine.NoteOn accepts.
type NoteSpec struct {
	NoteName string
	NoteType string
	Sustain  int
	Velocity uint8

	// Bend is Phase 5's optional linear pitch-glide envelope (MML's '&&'
	// portamento and '_/'/'_\'/'/_' /'\_' pitch-bend notation), in ticks -
	// see fmcore.NoteBend/pcmcore.NoteBend, which go-fmml/player resolves
	// this into at schedule time. The zero value means no glide.
	Bend NoteBend
}

// NoteBend is a note's optional linear pitch-glide envelope, expressed in
// ticks (tempo-independent, like SeqEvent.AtTick) rather than samples or
// milliseconds - see fmcore.NoteBend's doc comment for the full semantics.
type NoteBend struct {
	AttackOffsetSemitones float64
	AttackTicks           int
	// Portamento is true when this is a '&&' glide-in from the previous
	// still-sounding note: go-fmml/player skips force-stopping that
	// note before this one starts, and fmcore/pcmcore retarget it in
	// place (true legato) instead of retriggering - see fmcore.NoteBend's
	// doc comment. AttackOffsetSemitones/AttackTicks are still carried as
	// a fresh-attack fallback for when there's nothing left to glide from.
	Portamento bool

	ReleaseOffsetSemitones float64
	ReleaseTicks           int

	// TieTicks is Phase 5.1's single-'&' tie: when > 0, this note's total
	// held duration (in ticks, tempo-independent) is this value instead of
	// its own NoteType's length - go-fmml/analyze accumulates every
	// same-pitch note chained onto this one with a single '&' into this
	// field (and updates Sustain to the last chained note's own gate), so
	// the whole chain fires as a single attack held for their combined
	// length. 0 means no tie: duration comes from NoteType+Sustain as usual.
	TieTicks int
}

// SeqEvent is one timed event on a part's timeline, offset AtTick integer
// ticks (see common.TicksPerQuarterNote) from the start of the sequence (or
// of the current loop iteration). Ticks, not milliseconds, are the source
// of truth: they're tempo-independent and exact, so a long sequence can't
// accumulate rounding drift the way repeatedly adding rounded millisecond
// durations would.
type SeqEvent struct {
	AtTick  int
	Kind    SeqEventKind
	Notes   []NoteSpec     // EventNote
	Pan     float64        // EventSetPan
	VoiceID fmcore.VoiceID // EventSetVoice
}

// PartSequence is one part's (channel's) parsed MML timeline.
type PartSequence struct {
	PartID     fmcore.PartID
	VoiceID    fmcore.VoiceID
	Volume     float64
	Pan        float64
	ReverbSend float64 // [0,1]; see [partSetting]'s reverbSend (0-127)
	Mute       bool    // Phase 5; see [partSetting]'s mute
	// Solo is the MML addendum's [partSetting]/[pcmPartSetting] "solo" debug aid:
	// when true on any FM or PCM part in the sequence, every part without
	// Solo set is effectively muted regardless of its own Mute setting,
	// and this part plays regardless of its own Mute - see
	// go-fmml/player's effectiveMuteWithSolo.
	Solo        bool
	Events      []SeqEvent
	LengthTicks int // total duration of this part's timeline, for looping
}

// SequenceData is one fully parsed MML sequence, ready for go-fmml/player
// to play back.
type SequenceData struct {
	SequenceID   string
	Tempo        float64
	Loop         bool
	MasterVolume float64 // [0,1], for fmcore.Engine.SetMasterVolume; see [global]'s volume (0-127)
	Parts        map[fmcore.PartID]*PartSequence
	PCMParts     map[pcmcore.PartID]*PCMPartSequence

	// Reverb mirrors [global]'s reverb/reverbType/reverbTime/reverbLevel
	// (Phase 4). ReverbEnabled is already resolved to account for the
	// reverbTime<=reverb.MinDecaySeconds bypass rule (see
	// go-fmml/analyze's ParseMML), so a caller need only check it
	// directly. ReverbType is "simple" or "normal" (see go-fmml/reverb's
	// Kind), ReverbTimeSeconds is clamped to
	// [reverb.MinDecaySeconds, reverb.MaxDecaySeconds], and ReverbLevel is
	// normalized to [0,1] (see [global]'s reverbLevel, 0-127).
	ReverbEnabled     bool
	ReverbType        string
	ReverbTimeSeconds float64
	ReverbLevel       float64

	// StartOffsetTicks is [global]'s startOffset (whole notes, 2 = 2 whole
	// notes) already converted to ticks; 0 means no offset (the default
	// when startOffset is absent or invalid). go-fmml/player begins
	// playback at this tick instead of tick 0, applying any part's
	// voice/pan change from before this point immediately (so the running
	// state is correct) while simply never sounding a note that started
	// before it, even one still ringing across it - see player.Play's use
	// of this field.
	StartOffsetTicks int

	// TempoMap is Phase 7's [conductor] tempo automation: breakpoints
	// (sorted by AtTick, the first always at tick 0 once resolved)
	// describing how tempo changes over the sequence instead of staying
	// fixed at Tempo. Empty means no conductor tempo automation - the whole
	// sequence plays at Tempo throughout (go-fmml/player defaults to an
	// equivalent single-entry map in that case).
	TempoMap []common.TempoPoint

	// JumpPointTick/HasJumpPoint are Phase 7's [conductor] 'J' loop-restart
	// point. When HasJumpPoint is true and Loop is true, the first
	// play-through still starts at tick 0 (or StartOffsetTicks) as usual,
	// but once it reaches the sequence's end, playback loops back to
	// JumpPointTick instead of back to tick 0/StartOffsetTicks - see
	// go-fmml/player.Play's use of this field, which takes priority over
	// StartOffsetTicks's own loop-restart behavior when both are set.
	JumpPointTick int
	HasJumpPoint  bool
}

// PCMEventKind distinguishes the kinds of timed events a PCM part's
// timeline can contain. Which kinds actually appear depends on the part's
// PartType: a OneShot part only ever produces EventOneShotTrigger events
// (see CLAUDE.md's oneShot MML grammar); a Long part reuses the exact same
// grammar (and so the same event kinds) as an FM part.
type PCMEventKind int

const (
	// PCMEventOneShotTrigger triggers a OneShot part's sample mapped to
	// OneShot.NoteNumber (the 'X' MML command).
	PCMEventOneShotTrigger PCMEventKind = iota
	// PCMEventNote sounds one or more notes on a Long part (Notes has >1
	// entries for a chord), identical in shape to fmcore's EventNote.
	PCMEventNote
	// PCMEventSetPan changes a Long part's pan from this point on.
	PCMEventSetPan
	// PCMEventSetVoice changes a Long part's voice from this point on.
	PCMEventSetVoice
)

// PCMOneShotTrigger is one 'X' trigger in a OneShot part's timeline.
type PCMOneShotTrigger struct {
	NoteNumber uint8
	Velocity   uint8
}

// PCMSeqEvent is one timed event on a PCM part's timeline, offset AtTick
// integer ticks from the start of the sequence (see SeqEvent's comment for
// why ticks, not milliseconds, are the source of truth).
type PCMSeqEvent struct {
	AtTick  int
	Kind    PCMEventKind
	OneShot PCMOneShotTrigger // PCMEventOneShotTrigger
	Notes   []NoteSpec        // PCMEventNote
	Pan     float64           // PCMEventSetPan
	VoiceID pcmcore.VoiceID   // PCMEventSetVoice
}

// PCMPartSequence is one PCM part's ('A'-'P') parsed MML timeline.
type PCMPartSequence struct {
	PartID      pcmcore.PartID
	VoiceID     pcmcore.VoiceID
	PartType    pcmcore.VoiceType
	Volume      float64
	Pan         float64
	Mute        bool // Phase 5; see [pcmPartSetting]'s mute
	Solo        bool // see PartSequence.Solo's identical doc comment
	Events      []PCMSeqEvent
	LengthTicks int

	// ReverbSend is a Long part's reverb send level, [0,1] (see
	// [pcmPartSetting]'s reverbSend, 0-127). Unused for a OneShot part -
	// see OneShotReverbSend.
	ReverbSend float64
	// OneShotReverbSend is a OneShot part's per-note reverb send levels
	// ([0,1], keyed by MIDI note number), parsed from [pcmPartSetting]'s
	// reverbSend "note: level, ..." string. A note absent from this map
	// gets no reverb. Unused for a Long part.
	OneShotReverbSend map[uint8]float64
}
