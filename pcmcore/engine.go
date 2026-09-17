/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package pcmcore

import (
	"fmt"
	"math"
	"sort"

	"github.com/megaak-soft/go-fmml/common"
)

// Engine holds the PCM voice bank, PCM parts (up to 16, 'A'-'P') and their
// currently-sounding notes, plus a sample-accurate scheduling queue that
// mirrors fmcore.Engine's (see schedule.go's fmcore counterpart) so PCM
// playback timing is driven by the exact same sample clock as FM playback.
//
// Engine has no mutex of its own: fmcore.Engine embeds one and serializes
// every call into it (from its own render loop and from its PCM-prefixed
// pass-through methods) under its own mutex, so adding a second lock here
// would only add nesting for no benefit. Engine must not be used directly
// from multiple goroutines without equivalent external synchronization.
type Engine struct {
	sampleRate int
	voices     map[VoiceID]Voice
	parts      map[PartID]*part

	sampleClock int64

	scheduled    []scheduledAction
	nextSchedSeq uint64
}

// NewEngine creates a PCM engine sharing sampleRate with its owning
// fmcore.Engine (0 defaults to 44100Hz).
func NewEngine(sampleRate int) *Engine {
	if sampleRate <= 0 {
		sampleRate = 44100
	}
	return &Engine{
		sampleRate: sampleRate,
		voices:     make(map[VoiceID]Voice),
		parts:      make(map[PartID]*part),
	}
}

// RegisterVoice stores a parsed PCM voice in the voice bank under id,
// overwriting any existing voice with the same id.
func (e *Engine) RegisterVoice(id VoiceID, voice Voice) {
	e.voices[id] = voice
}

// ClearVoices deletes every stored PCM voice.
func (e *Engine) ClearVoices() {
	e.voices = make(map[VoiceID]Voice)
}

// SetPart assigns a registered voice to a PCM sounding part. volume is
// [0,1]; pan is [-1,1] (-1 = full left, 0 = center, 1 = full right) - a
// continuous scale, not just those three points, matching CLAUDE.md's
// -16..16 PCM pan scale after normalization (e.g. 7 there becomes roughly
// 0.44 here, slightly right of center) - the caller is responsible for
// that conversion.
func (e *Engine) SetPart(id PartID, voice VoiceID, volume, pan float64) error {
	v, ok := e.voices[voice]
	if !ok {
		return fmt.Errorf("pcmcore: voice %d is not registered", voice)
	}
	p, ok := e.parts[id]
	if !ok {
		p = &part{}
		e.parts[id] = p
	}
	p.voiceID = voice
	p.voiceType = v.VoiceType
	p.volume = clamp01(volume)
	p.pan = clampPan(pan)
	return nil
}

// SetPartReverbSend sets a Long PCM part's reverb send level (Phase 4), in
// [0,1], applied uniformly to every note sounding on the part (see
// mixSample). Unused for a OneShot part - see SetPartOneShotReverbSend.
// Lazily creates the part entry if SetPart hasn't been called yet, mirroring
// SetPart/fireScheduled's own lazy-create behavior.
func (e *Engine) SetPartReverbSend(id PartID, send float64) {
	p, ok := e.parts[id]
	if !ok {
		p = &part{}
		e.parts[id] = p
	}
	p.reverbSend = clamp01(send)
}

// SetPartOneShotReverbSend sets a OneShot PCM part's per-note reverb send
// levels (Phase 4): MIDI note number -> [0,1] send level, read by
// TriggerOneShot each time that note fires. A note absent from sends gets
// no reverb. Unused for a Long part - see SetPartReverbSend.
func (e *Engine) SetPartOneShotReverbSend(id PartID, sends map[uint8]float64) {
	p, ok := e.parts[id]
	if !ok {
		p = &part{}
		e.parts[id] = p
	}
	clamped := make(map[uint8]float64, len(sends))
	for note, send := range sends {
		clamped[note] = clamp01(send)
	}
	p.oneShotReverbSend = clamped
}

// TriggerOneShot immediately sounds the OneShot sample mapped to noteNumber
// on partID, per CLAUDE.md's 'X' MML command. If a sample triggered by the
// same note is still playing on this part it is stopped first (retrigger),
// but other concurrently-sounding samples on the part are unaffected.
func (e *Engine) TriggerOneShot(partID PartID, noteNumber uint8, velocity uint8) error {
	p, ok := e.parts[partID]
	if !ok {
		return fmt.Errorf("pcmcore: part %c is not configured", partID)
	}
	voice, ok := e.voices[p.voiceID]
	if !ok {
		return fmt.Errorf("pcmcore: voice %d is not registered", p.voiceID)
	}

	var setting *VoiceSetting
	for i := range voice.Settings {
		if voice.Settings[i].HasNote && voice.Settings[i].Note == noteNumber {
			setting = &voice.Settings[i]
			break
		}
	}
	if setting == nil || setting.Wave == nil {
		return fmt.Errorf("pcmcore: part %c voice %d has no oneShot sample for note %d", partID, p.voiceID, noteNumber)
	}

	p.stopNote(noteNumber, e.sampleClock, e.sampleRate)
	p.stealVoiceIfFull()

	gain := p.volume * (float64(setting.Volume) / 127) * (float64(velocity) / 127)
	pan := float64(setting.Pan) / 16
	reverbSend := p.oneShotReverbSend[noteNumber] // zero value if the note has no entry
	p.notes = append(p.notes, newOneShotNote(setting.Wave, gain, pan, noteNumber, reverbSend))
	return nil
}

// NoteOnLongRow sounds partID's Long voice at noteNumber (MIDI note
// number), pitch-shifted from the voice's BaseNote, for durationMs
// milliseconds (<=0 sustains until explicitly force-stopped).
func (e *Engine) NoteOnLongRow(partID PartID, noteNumber uint8, durationMs int, velocity uint8) error {
	return e.noteOnLongRow(partID, noteNumber, durationMs, velocity, resolvedBend{})
}

func (e *Engine) noteOnLongRow(partID PartID, noteNumber uint8, durationMs int, velocity uint8, bend resolvedBend) error {
	p, ok := e.parts[partID]
	if !ok {
		return fmt.Errorf("pcmcore: part %c is not configured", partID)
	}
	voice, ok := e.voices[p.voiceID]
	if !ok {
		return fmt.Errorf("pcmcore: voice %d is not registered", p.voiceID)
	}
	if len(voice.Settings) == 0 || voice.Settings[0].Wave == nil {
		return fmt.Errorf("pcmcore: part %c voice %d has no long sample", partID, p.voiceID)
	}
	setting := voice.Settings[0]

	p.stopNote(noteNumber, e.sampleClock, e.sampleRate)
	p.stealVoiceIfFull()

	pitchRatio := math.Pow(2, (float64(noteNumber)-float64(setting.BaseNote))/12)
	gain := p.volume * (float64(velocity) / 127)

	releaseAt := int64(-1)
	if durationMs > 0 {
		releaseAt = e.sampleClock + int64(durationMs)*int64(e.sampleRate)/1000
	}

	n := newLongNote(setting.Wave, setting.Envelope, voice.Loop, gain, p.pan, pitchRatio, voice.LFO, e.sampleRate, e.sampleClock, releaseAt, bend)
	n.triggerNote = noteNumber
	p.notes = append(p.notes, n)
	return nil
}

// glideNoteOnLong performs a portamento ('&&') note-on on a Long PCM part:
// rather than starting a fresh note, it retargets partID's currently-
// sounding note (see part.activeNoteForPortamento) to noteNumber's pitch
// and glides there linearly from that note's actual live pitch ratio over
// bend.attackSamples - true legato, since the note's own envelope simply
// keeps running rather than being retriggered. Falls back to a fresh
// attack (via noteOnLongRow, using bend's attack offset/duration as an
// explicit pitch-bend) if there's no note left to glide from.
func (e *Engine) glideNoteOnLong(partID PartID, noteNumber uint8, durationMs int, velocity uint8, bend resolvedBend) error {
	p, ok := e.parts[partID]
	if !ok {
		return fmt.Errorf("pcmcore: part %c is not configured", partID)
	}
	voice, ok := e.voices[p.voiceID]
	if !ok {
		return fmt.Errorf("pcmcore: voice %d is not registered", p.voiceID)
	}
	if len(voice.Settings) == 0 || voice.Settings[0].Wave == nil {
		return fmt.Errorf("pcmcore: part %c voice %d has no long sample", partID, p.voiceID)
	}

	n := p.activeNoteForPortamento()
	if n == nil {
		return e.noteOnLongRow(partID, noteNumber, durationMs, velocity, resolvedBend{
			attackOffsetSemitones: bend.attackOffsetSemitones,
			attackSamples:         bend.attackSamples,
		})
	}

	setting := voice.Settings[0]
	targetRatio := math.Pow(2, (float64(noteNumber)-float64(setting.BaseNote))/12)
	startRatio := n.pitchRatioAt(e.sampleClock)
	startOffset := 12 * math.Log2(startRatio/targetRatio)

	n.triggerNote = noteNumber
	n.pitchRatio = targetRatio
	n.gain = p.volume * (float64(velocity) / 127)
	n.leftGain, n.rightGain = panGains(p.pan)
	n.releaseGlide = pitchGlide{}
	if bend.attackSamples > 0 {
		n.attackGlide = pitchGlide{
			active: true, startSample: e.sampleClock, durSamples: bend.attackSamples,
			startSemitones: startOffset, endSemitones: 0,
		}
	} else {
		n.attackGlide = pitchGlide{}
	}

	releaseAt := int64(-1)
	if durationMs > 0 {
		releaseAt = e.sampleClock + int64(durationMs)*int64(e.sampleRate)/1000
	}
	n.releaseAtSample = releaseAt
	return nil
}

// Advance fires any scheduled actions due at nowSample and steps every
// sounding note by one sample, returning the mixed dry stereo output plus
// the reverb-bus (Phase 4) send stereo pair derived from each sounding
// part/note's own reverb send level. Called once per audio sample by
// fmcore.Engine's render loop, with the same sample clock value FM
// playback is driven by.
func (e *Engine) Advance(nowSample int64) (dryL, dryR, sendL, sendR float64) {
	e.sampleClock = nowSample
	e.runDueScheduled()
	return e.mixSample()
}

func (e *Engine) mixSample() (dryL, dryR, sendL, sendR float64) {
	for _, p := range e.parts {
		if len(p.notes) == 0 {
			continue
		}
		kept := p.notes[:0]
		var longSumL, longSumR float64 // Long only; see below
		for _, n := range p.notes {
			if !n.released && n.releaseAtSample >= 0 && e.sampleClock >= n.releaseAtSample {
				n.release()
			}
			l, r := n.render(e.sampleRate, e.sampleClock)
			gain := n.muteGain(e.sampleClock)
			l *= gain
			r *= gain
			dryL += l
			dryR += r

			// A OneShot note carries its own reverb send (different
			// samples on the same part can be sent differently); a Long
			// part's send is instead uniform across the part, so it's
			// applied once below to the part's combined dry sum rather
			// than per note.
			if p.voiceType == OneShot {
				if n.reverbSend > 0 {
					sendL += l * n.reverbSend
					sendR += r * n.reverbSend
				}
			} else {
				longSumL += l
				longSumR += r
			}

			if !n.finished() && !n.muteDone(e.sampleClock) {
				kept = append(kept, n)
			}
		}
		p.notes = kept

		if p.voiceType != OneShot && p.reverbSend > 0 {
			sendL += longSumL * p.reverbSend
			sendR += longSumR * p.reverbSend
		}
	}
	return dryL, dryR, sendL, sendR
}

// AnySounding reports whether any of the given parts currently has a note
// still audible, including a Long note's release tail.
func (e *Engine) AnySounding(partIDs ...PartID) bool {
	for _, id := range partIDs {
		if p, ok := e.parts[id]; ok && len(p.notes) > 0 {
			return true
		}
	}
	return false
}

func clampPan(v float64) float64 {
	if v < -1 {
		return -1
	}
	if v > 1 {
		return 1
	}
	return v
}

// --- Sample-accurate scheduling, mirroring fmcore.Engine's (see fmcore's
// schedule.go) so PCM events can be scheduled against the exact same
// absolute sample positions FM events use. ---

type scheduledKind int

const (
	schedOneShotTrigger scheduledKind = iota
	schedLongNoteOn
	schedForceStopPart
	schedSetPart
)

type scheduledAction struct {
	atSample int64
	seq      uint64
	kind     scheduledKind

	partID PartID

	// schedOneShotTrigger
	noteNumber uint8
	velocity   uint8

	// schedLongNoteOn (noteNumber/velocity above are reused)
	noteType string
	sustain  int
	bend     NoteBend // Phase 5; zero value = no pitch glide

	// schedSetPart
	voiceID VoiceID
	volume  float64
	pan     float64
}

// ScheduleTriggerOneShot arranges for a OneShot sample to be triggered on
// partID once the shared sample clock reaches atSample.
func (e *Engine) ScheduleTriggerOneShot(atSample int64, partID PartID, noteNumber uint8, velocity uint8) {
	e.nextSchedSeq++
	e.scheduled = append(e.scheduled, scheduledAction{
		atSample: atSample, seq: e.nextSchedSeq, kind: schedOneShotTrigger,
		partID: partID, noteNumber: noteNumber, velocity: velocity,
	})
	e.sortScheduled()
}

// ScheduleNoteOnLong arranges for a Long voice note to sound on partID once
// the shared sample clock reaches atSample. noteName (e.g. "A4") is
// resolved to a MIDI note number immediately, matching
// fmcore.Engine.ScheduleNoteOn's behavior; duration is resolved from
// common.Tempo and sustain later, at the moment it actually fires.
func (e *Engine) ScheduleNoteOnLong(atSample int64, partID PartID, noteName string, noteType string, sustain int, velocity uint8) error {
	return e.ScheduleNoteOnLongWithBend(atSample, partID, noteName, noteType, sustain, velocity, NoteBend{})
}

// ScheduleNoteOnLongWithBend is ScheduleNoteOnLong plus an optional linear
// pitch-glide envelope (Phase 5's portamento/pitch-bend MML notation - see
// NoteBend). A zero-value bend behaves exactly like ScheduleNoteOnLong.
func (e *Engine) ScheduleNoteOnLongWithBend(atSample int64, partID PartID, noteName string, noteType string, sustain int, velocity uint8, bend NoteBend) error {
	noteNumber, err := parseNoteName(noteName)
	if err != nil {
		return err
	}
	if _, err := common.NoteTypeBeats(noteType); err != nil {
		return err
	}
	e.nextSchedSeq++
	e.scheduled = append(e.scheduled, scheduledAction{
		atSample: atSample, seq: e.nextSchedSeq, kind: schedLongNoteOn,
		partID: partID, noteNumber: noteNumber, noteType: noteType, sustain: sustain, velocity: velocity, bend: bend,
	})
	e.sortScheduled()
	return nil
}

// ScheduleForceStopPart arranges for every note currently sounding on
// partID to be force-muted (see runningNote.forceMute) once the shared
// sample clock reaches atSample. The fade this triggers has a fixed, short
// duration regardless of the note's own envelope/natural length - mirrors
// fmcore.Engine.ScheduleForceStopPart's behavior/reasoning - so neither a
// Long part's non-overlap nor a full playback stop (see go-fmml/player)
// ever cuts a note off mid-waveform.
func (e *Engine) ScheduleForceStopPart(atSample int64, partID PartID) {
	e.nextSchedSeq++
	e.scheduled = append(e.scheduled, scheduledAction{atSample: atSample, seq: e.nextSchedSeq, kind: schedForceStopPart, partID: partID})
	e.sortScheduled()
}

// ScheduleSetPart arranges for partID's voice/volume/pan assignment to
// change once the shared sample clock reaches atSample.
func (e *Engine) ScheduleSetPart(atSample int64, partID PartID, voiceID VoiceID, volume, pan float64) {
	e.nextSchedSeq++
	e.scheduled = append(e.scheduled, scheduledAction{atSample: atSample, seq: e.nextSchedSeq, kind: schedSetPart, partID: partID, voiceID: voiceID, volume: volume, pan: pan})
	e.sortScheduled()
}

// CancelScheduled discards every not-yet-fired scheduled action.
func (e *Engine) CancelScheduled() {
	e.scheduled = nil
}

func (e *Engine) sortScheduled() {
	sort.Slice(e.scheduled, func(i, j int) bool {
		if e.scheduled[i].atSample != e.scheduled[j].atSample {
			return e.scheduled[i].atSample < e.scheduled[j].atSample
		}
		return e.scheduled[i].seq < e.scheduled[j].seq
	})
}

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

func (e *Engine) fireScheduled(a scheduledAction) {
	switch a.kind {
	case schedOneShotTrigger:
		_ = e.TriggerOneShot(a.partID, a.noteNumber, a.velocity)

	case schedLongNoteOn:
		var durationMs int
		if a.bend.TieTicks > 0 {
			// Phase 5.1's single-'&' tie: see fmcore's identical branch in
			// its own fireScheduled for the full reasoning.
			durationMs = int(common.TicksToMs(a.bend.TieTicks, common.Tempo) * float64(a.sustain) / 100)
		} else {
			beats, err := common.NoteTypeBeats(a.noteType)
			if err != nil {
				return // already validated in ScheduleNoteOnLong; defensive only
			}
			quarterNoteMs := 60000 / common.Tempo
			durationMs = int(beats * quarterNoteMs * float64(a.sustain) / 100)
		}
		rb := e.resolveBend(a.bend)
		if rb.portamento {
			_ = e.glideNoteOnLong(a.partID, a.noteNumber, durationMs, a.velocity, rb)
		} else {
			_ = e.noteOnLongRow(a.partID, a.noteNumber, durationMs, a.velocity, rb)
		}

	case schedForceStopPart:
		if p, ok := e.parts[a.partID]; ok {
			for _, n := range p.notes {
				n.forceMute(e.sampleClock, e.sampleRate)
			}
		}

	case schedSetPart:
		p, ok := e.parts[a.partID]
		if !ok {
			p = &part{}
			e.parts[a.partID] = p
		}
		voice, ok := e.voices[a.voiceID]
		if ok {
			p.voiceType = voice.VoiceType
		}
		p.voiceID = a.voiceID
		p.volume = clamp01(a.volume)
		p.pan = clampPan(a.pan)
	}
}
