/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package fmcore

import (
	"encoding/binary"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/ebitengine/oto/v3"

	"github.com/megaak-soft/go-fmml/pcmcore"
	"github.com/megaak-soft/go-fmml/reverb"
)

const bytesPerFrame = 8 // stereo, 32-bit float samples

// Engine is the FM synthesis core: a bank of registered voices, a set of
// multi-timbral parts, and the currently sounding notes across them. All
// exported methods are safe to call concurrently with audio rendering;
// NoteOn in particular only sets up note state and returns immediately, the
// actual synthesis happens continuously on oto's playback goroutine.
type Engine struct {
	mu         sync.Mutex
	sampleRate int
	voices     map[VoiceID]Voice
	parts      map[PartID]*part

	sampleClock int64
	nextNoteID  uint64

	// masterVolume scales the final FM+PCM mix (after per-part volume/pan,
	// before the output soft-clip), in [0,1]. Defaults to 1 (full volume).
	// This is the instantaneous, possibly-mid-glide value actually applied
	// each sample (see advanceMasterVolume); masterVolumeTarget is the
	// authoritative value it's headed toward.
	masterVolume       float64
	masterVolumeTarget float64

	// volumeRamp* implement Phase 7's smooth master-volume changes/fades
	// (SetMasterVolumeSmooth): when active, masterVolume glides linearly
	// from volumeRampFrom to masterVolumeTarget as the sample clock moves
	// from volumeRampFromSample to volumeRampToSample - see
	// advanceMasterVolume, called once per sample from render.
	volumeRampActive     bool
	volumeRampFrom       float64
	volumeRampFromSample int64
	volumeRampToSample   int64

	// reverb is the master reverb send/return effect (Phase 4): every FM
	// part's/PCM part's/PCM oneShot note's own reverb send (see part.go's
	// reverbSend, pcmcore's identical fields) feeds a shared send bus,
	// processed once per sample here and mixed back into the dry output
	// scaled by reverbLevel. Inert (Process returns silence) until
	// SetReverb configures it. See go-fmml/reverb's doc comment for why
	// this single shared instance - rather than one per part - is what
	// CLAUDE.md's Phase 4 spec means by "1個だけ" master reverb.
	reverb      *reverb.Processor
	reverbLevel float64 // [0,1]; see SetReverb

	scheduled    []scheduledAction
	nextSchedSeq uint64

	// pcm mixes PCM sample playback (go-fmml/pcmcore, Phase 3) into the
	// same render loop and sample clock as the FM synthesis above, so the
	// two never drift apart. Every access goes through this Engine's mu,
	// same as parts/scheduled above; see pcmcore.Engine's doc comment.
	pcm *pcmcore.Engine

	ctx    *oto.Context
	player *oto.Player
}

// NewEngine initializes the FM core and starts asynchronous audio output by
// creating its own oto.Context. sampleRate of 0 defaults to 44100Hz.
//
// oto (and, since it's built on top of oto, Ebitengine's own
// github.com/hajimehoshi/ebiten/v2/audio package) can only have one
// oto.Context created per OS process - a second call anywhere in the
// process fails with "oto: context is already created". If your program
// already owns an audio context of its own (an ebiten/v2/audio.Context, or
// your own oto.Context), don't call NewEngine: call
// NewEngineWithoutOutput instead and feed the returned Engine (which
// implements io.Reader, streaming the same interleaved stereo float32LE
// samples oto expects) into that context's own player - see
// NewEngineWithoutOutput's doc comment for the exact call.
func NewEngine(sampleRate int) (*Engine, error) {
	return newEngineWithOutput(sampleRate, 0)
}

// NewEngineWithBufferSize is NewEngine, but lets the caller override the
// output device's buffer size instead of leaving it at the driver's
// default. On some setups (Windows/WASAPI in particular) the default
// buffer adds a consistent latency of a few hundred milliseconds between
// scheduling a note/sample and actually hearing it; passing a smaller
// duration (e.g. 20*time.Millisecond) trades some buffer-underrun margin
// for lower latency. bufferSize <= 0 behaves exactly like NewEngine (driver
// default) - see oto.NewContextOptions.BufferSize for the exact semantics
// and its own latency/glitch trade-off warning.
//
// If instead you're feeding an Engine from NewEngineWithoutOutput into a
// host-owned oto.Context or Ebitengine audio.Context, this function doesn't
// apply - call SetBufferSize on the player you create there instead (both
// oto.Player and Ebitengine's audio.Player implement it).
func NewEngineWithBufferSize(sampleRate int, bufferSize time.Duration) (*Engine, error) {
	return newEngineWithOutput(sampleRate, bufferSize)
}

func newEngineWithOutput(sampleRate int, bufferSize time.Duration) (*Engine, error) {
	e := NewEngineWithoutOutput(sampleRate)

	ctx, ready, err := oto.NewContext(&oto.NewContextOptions{
		SampleRate:   e.sampleRate,
		ChannelCount: 2,
		Format:       oto.FormatFloat32LE,
		BufferSize:   bufferSize,
	})
	if err != nil {
		return nil, fmt.Errorf("fmcore: failed to initialize audio output: %w", err)
	}
	<-ready

	e.ctx = ctx
	e.player = ctx.NewPlayer(e)
	e.player.Play()
	return e, nil
}

// NewEngineWithoutOutput builds the FM+PCM synthesis core without opening
// any audio output device itself, for embedding into a program that
// already owns an audio context (see NewEngine's doc comment for why that
// matters). sampleRate of 0 defaults to 44100Hz. Since no device is
// opened, this cannot fail.
//
// The returned Engine implements io.Reader, producing the same
// interleaved stereo float32LE PCM stream NewEngine's own internal oto
// player would - feed it directly into whatever owns your process's one
// audio context:
//
//	// Your own oto.Context:
//	player := ctx.NewPlayer(engine)
//
//	// Ebitengine's github.com/hajimehoshi/ebiten/v2/audio.Context (its
//	// sampleRate must match the one passed here):
//	player, err := audioContext.NewPlayerF32(engine)
//
// Either way, call the resulting player's Play method yourself; Engine has
// nothing more to do with starting or owning playback once handed off like
// this. Close is a no-op on an Engine built this way - closing the
// player/context you created it for is your own responsibility.
func NewEngineWithoutOutput(sampleRate int) *Engine {
	if sampleRate <= 0 {
		sampleRate = 44100
	}
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

// Read implements io.Reader, filling buf with interleaved stereo
// float32LE samples - see NewEngineWithoutOutput for feeding this into an
// audio context/player your own program already owns. It's the exact same
// rendering NewEngine's own internal oto player pulls from.
func (e *Engine) Read(buf []byte) (int, error) {
	return e.render(buf)
}

// Close stops audio playback and releases the output player NewEngine
// opened. A no-op (returns nil) on an Engine built with
// NewEngineWithoutOutput, which never opened one.
func (e *Engine) Close() error {
	if e.player == nil {
		return nil
	}
	return e.player.Close()
}

// RegisterVoice stores a voice (音色) definition in the voice bank under id,
// overwriting any existing voice with the same id.
func (e *Engine) RegisterVoice(id VoiceID, voice Voice) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.voices[id] = voice
}

// SetPart assigns a registered voice to a sounding part, with volume in
// [0,1] and pan in [-1,1] (-1 = full left, 0 = center, 1 = full right) -
// a continuous scale, not just those three points: it's the normalized
// form of CLAUDE.md's -16..16 MML pan scale (see analyze's normalizePan16
// and its 'P' command), so e.g. an MML pan of 7 arrives here as roughly
// 0.44, a position slightly right of center, and panGains renders it that
// way rather than snapping to hard left/center/right.
func (e *Engine) SetPart(id PartID, voice VoiceID, volume, pan float64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.voices[voice]; !ok {
		return fmt.Errorf("fmcore: voice %d is not registered", voice)
	}
	p, ok := e.parts[id]
	if !ok {
		p = &part{}
		e.parts[id] = p
	}
	p.voiceID = voice
	p.volume = clamp01(volume)
	p.pan = clampPan(pan)
	return nil
}

// SetMasterVolume sets the engine's overall output volume in [0,1],
// applied to the combined FM+PCM mix on top of each part's own volume.
// Defaults to 1 (full volume) until called.
func (e *Engine) SetMasterVolume(volume float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	v := clamp01(volume)
	e.masterVolume = v
	e.masterVolumeTarget = v
	e.volumeRampActive = false
}

// SetMasterVolumeSmooth is SetMasterVolume, but glides there linearly over
// rampSeconds (Phase 7's master-volume-change/fade methods, see
// go-fmml/player) instead of jumping instantly, starting from whatever
// the output volume actually is right now - including mid-glide - so
// back-to-back calls never produce a discontinuity. rampSeconds <= 0
// behaves exactly like SetMasterVolume.
func (e *Engine) SetMasterVolumeSmooth(target float64, rampSeconds float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	target = clamp01(target)
	e.masterVolumeTarget = target
	if rampSeconds <= 0 {
		e.masterVolume = target
		e.volumeRampActive = false
		return
	}
	e.volumeRampFrom = e.masterVolume
	e.volumeRampFromSample = e.sampleClock
	e.volumeRampToSample = e.sampleClock + int64(rampSeconds*float64(e.sampleRate))
	e.volumeRampActive = e.volumeRampToSample > e.volumeRampFromSample
	if !e.volumeRampActive {
		e.masterVolume = target
	}
}

// MasterVolumeTarget reports the master volume SetMasterVolume/
// SetMasterVolumeSmooth last requested, in [0,1] - the value a smooth ramp
// is headed toward (or has already reached), regardless of the
// instantaneous mid-glide output. Used by go-fmml/player's Pause/Resume
// to know what level to glide back up to.
func (e *Engine) MasterVolumeTarget() float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.masterVolumeTarget
}

// advanceMasterVolume steps masterVolume one sample along its active ramp
// (if any), called once per sample from render before that sample is
// mixed. Must be called with e.mu held.
func (e *Engine) advanceMasterVolume() {
	if !e.volumeRampActive {
		return
	}
	if e.sampleClock >= e.volumeRampToSample {
		e.masterVolume = e.masterVolumeTarget
		e.volumeRampActive = false
		return
	}
	t := float64(e.sampleClock-e.volumeRampFromSample) / float64(e.volumeRampToSample-e.volumeRampFromSample)
	e.masterVolume = e.volumeRampFrom + (e.masterVolumeTarget-e.volumeRampFrom)*t
}

// SetReverb configures the master reverb send/return effect (Phase 4) from
// a sequence's [global] reverb*/reverbType/reverbTime/reverbLevel settings
// (see go-fmml/memory.SequenceData's Reverb* fields, already resolved by
// go-fmml/analyze's ParseMML). enabled=false, or timeSeconds at or below
// reverb.MinDecaySeconds, bypasses the effect entirely - render's Process
// call then costs only a cheap Active() check per sample. kind is "simple"
// or "normal" (case-insensitive; anything else defaults to "normal"), level
// is the wet mix applied on top of the always-present dry mix, in [0,1].
func (e *Engine) SetReverb(enabled bool, kind string, timeSeconds, level float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.reverbLevel = clamp01(level)
	if !enabled || timeSeconds <= reverb.MinDecaySeconds {
		e.reverb.Disable()
		return
	}
	k, _ := reverb.ParseKind(kind)
	e.reverb.Configure(k, timeSeconds)
}

// DuckReverb starts a short fade of the master reverb's wet output to
// silence and clears its decay tail once that fade completes, so a
// playback Pause/Stop/Rewind (see go-fmml/player) never leaves an
// audible reverb tail ringing on past the moment it stopped (see
// reverb.Processor.Duck). A no-op if reverb isn't currently active.
func (e *Engine) DuckReverb() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.reverb.Duck()
}

// SetPartReverbSend sets partID's reverb send level (Phase 4), in [0,1],
// applied to the part's combined dry output each sample (see mixSample).
// Deliberately separate from SetPart/ScheduleSetPart so a mid-sequence
// '@'/'P' voice/pan change never resets it. Lazily creates the part entry
// if SetPart hasn't been called yet, mirroring SetPart's own behavior.
func (e *Engine) SetPartReverbSend(id PartID, send float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	p, ok := e.parts[id]
	if !ok {
		p = &part{}
		e.parts[id] = p
	}
	p.reverbSend = clamp01(send)
}

// NoteOnRow requests that the given part sound noteNumber (MIDI note number)
// for durationMs milliseconds at velocity (0-127), then release naturally.
// durationMs <= 0 sustains the note until it is stolen or explicitly
// retriggered. It returns a NoteID immediately; the note itself keeps
// sounding asynchronously on the audio thread.
//
// This is the "raw", numeric-parameter form; see NoteOn for the
// note-name/note-type form.
func (e *Engine) NoteOnRow(partID PartID, noteNumber uint8, durationMs int, velocity uint8) (NoteID, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.triggerNote(partID, noteNumber, durationMs, velocity, resolvedBend{})
}

// triggerNote performs the actual voice-management work for NoteOnRow: note
// retriggering, polyphony/voice-stealing, and starting the new note's
// envelopes. Must be called with mu held.
func (e *Engine) triggerNote(partID PartID, noteNumber uint8, durationMs int, velocity uint8, bend resolvedBend) (NoteID, error) {
	p, ok := e.parts[partID]
	if !ok {
		return 0, fmt.Errorf("fmcore: part %d is not configured", partID)
	}
	voice, ok := e.voices[p.voiceID]
	if !ok {
		return 0, fmt.Errorf("fmcore: voice %d is not registered", p.voiceID)
	}

	p.stopNote(noteNumber, e.sampleClock, e.sampleRate)
	p.stealVoiceIfFull()

	e.nextNoteID++
	id := NoteID(e.nextNoteID)

	releaseAt := int64(-1)
	if durationMs > 0 {
		releaseAt = e.sampleClock + int64(durationMs)*int64(e.sampleRate)/1000
	}

	n := newRunningNote(id, noteNumber, velocity, voice, e.sampleRate, e.sampleClock, releaseAt, bend)
	p.notes = append(p.notes, n)
	return id, nil
}

// glideNoteOn performs a portamento ('&&') note-on: rather than starting a
// fresh note, it retargets partID's currently-sounding note (see
// part.activeNoteForPortamento) to noteNumber's pitch and glides there
// linearly from that note's actual live pitch over bend.attackSamples -
// true legato, since the note's own envelope/operators simply keep
// running rather than being retriggered. Falls back to a fresh attack
// (via triggerNote, using bend's attack offset/duration as an explicit
// pitch-bend) if there's no note left to glide from. Must be called with
// mu held.
func (e *Engine) glideNoteOn(partID PartID, noteNumber uint8, durationMs int, velocity uint8, bend resolvedBend) (NoteID, error) {
	p, ok := e.parts[partID]
	if !ok {
		return 0, fmt.Errorf("fmcore: part %d is not configured", partID)
	}

	n := p.activeNoteForPortamento()
	if n == nil {
		return e.triggerNote(partID, noteNumber, durationMs, velocity, resolvedBend{
			attackOffsetSemitones: bend.attackOffsetSemitones,
			attackSamples:         bend.attackSamples,
		})
	}

	targetFreq := noteToFreq(noteNumber)
	startFreq := n.freqAt(e.sampleClock)
	startOffset := 12 * math.Log2(startFreq/targetFreq)

	n.noteNumber = noteNumber
	n.freq = targetFreq
	n.velocity = velocity
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
	return n.id, nil
}

// render fills buf with interleaved stereo float32LE samples, mixing every
// active note across every part. It is invoked repeatedly by oto's playback
// goroutine and is the only place actual sample synthesis happens. Before
// each frame it also fires any scheduled actions (see schedule.go) whose
// target sample has been reached, so note timing is driven by the same
// clock that generates audio rather than by a separate real-time timer.
func (e *Engine) render(buf []byte) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	frames := len(buf) / bytesPerFrame
	for f := 0; f < frames; f++ {
		e.runDueScheduled()
		e.advanceMasterVolume()
		fmLeft, fmRight, fmSendL, fmSendR := e.mixSample()
		pcmLeft, pcmRight, pcmSendL, pcmSendR := e.pcm.Advance(e.sampleClock)
		wetL, wetR := e.reverb.Process(float32(fmSendL+pcmSendL), float32(fmSendR+pcmSendR))
		left := math.Tanh((fmLeft + pcmLeft + float64(wetL)*e.reverbLevel) * e.masterVolume)
		right := math.Tanh((fmRight + pcmRight + float64(wetR)*e.reverbLevel) * e.masterVolume)
		binary.LittleEndian.PutUint32(buf[f*bytesPerFrame:], math.Float32bits(float32(left)))
		binary.LittleEndian.PutUint32(buf[f*bytesPerFrame+4:], math.Float32bits(float32(right)))
		e.sampleClock++
	}
	for i := frames * bytesPerFrame; i < len(buf); i++ {
		buf[i] = 0
	}
	return len(buf), nil
}

// mixSample mixes every active FM note across every part into the dry
// stereo output, and separately accumulates the reverb-bus (Phase 4) send
// stereo pair: each part's post-volume/pan dry signal, scaled by that
// part's own reverbSend. Feeding the send bus with the already-panned
// signal (rather than a pre-pan sum) is what keeps a hard-panned part's
// reverb roughly on the same side in the wet output (see reverb.Processor's
// "true stereo" doc comment).
func (e *Engine) mixSample() (dryL, dryR, sendL, sendR float64) {
	for _, p := range e.parts {
		if len(p.notes) == 0 {
			continue
		}
		var sum float64
		kept := p.notes[:0]
		for _, n := range p.notes {
			if !n.released && n.releaseAtSample >= 0 && e.sampleClock >= n.releaseAtSample {
				n.release()
			}
			sum += n.render(e.sampleRate, e.sampleClock) * n.muteGain(e.sampleClock)
			if !n.finished() && !n.muteDone(e.sampleClock) {
				kept = append(kept, n)
			}
		}
		p.notes = kept

		sum *= p.volume
		lg, rg := panGains(p.pan)
		l, r := sum*lg, sum*rg
		dryL += l
		dryR += r
		if p.reverbSend > 0 {
			sendL += l * p.reverbSend
			sendR += r * p.reverbSend
		}
	}
	return dryL, dryR, sendL, sendR
}

// panGains converts a pan in [-1,1] (-1 = full left, 0 = center, 1 = full
// right) into equal-power left/right gain multipliers. The conversion is
// continuous (a plain sin/cos curve over the whole range), so it renders
// every intermediate position CLAUDE.md's -16..16 MML pan scale can
// express - not just hard left/center/right.
func panGains(pan float64) (float64, float64) {
	angle := (pan + 1) / 2 * (math.Pi / 2)
	return math.Cos(angle), math.Sin(angle)
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
