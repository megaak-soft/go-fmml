/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

// Package pcmcore implements the PCM sample-playback core described in
// CLAUDE.md's Phase 3: decoding WAV files, holding PCM voice (音色)
// definitions (oneShot drum-style multi-sample kits, and long pitched
// single-sample voices), and rendering their runtime playback state sample
// by sample.
//
// pcmcore deliberately does not import go-fmml/fmcore, even though the
// two engines' shapes (Voice/Part/scheduled-action/render) closely mirror
// each other: fmcore.Engine embeds a pcmcore.Engine so both FM and PCM
// audio are mixed from the same render loop, off the same sample clock -
// the only way to guarantee the two never drift apart. Importing fmcore
// from here would create an import cycle, so a couple of small pieces
// (EnvelopeParams, note-name parsing, pan-gain math) are intentionally
// duplicated rather than shared.
package pcmcore

// VoiceID identifies a registered PCM voice (音色) in the PCM voice bank.
type VoiceID uint32

// PartID identifies a PCM sounding part. Per CLAUDE.md's spec, PCM parts are
// named 'A'-'P' (up to 16), distinct from the FM engine's numeric 1-16 part
// IDs.
type PartID byte

// VoiceType distinguishes the two PCM voice authoring styles CLAUDE.md
// defines.
type VoiceType int

const (
	// OneShot is a drum-kit-style voice: up to 16 samples, each triggered by
	// its own note, always played back at its authored pitch from start to
	// natural end (loop/baseNote/envelope settings are ignored).
	OneShot VoiceType = iota
	// Long is a single pitched sample, played back from BaseNote and
	// pitch-shifted per note, with an FM-style amplitude envelope and
	// optional looping.
	Long
)

// EnvelopeParams mirrors fmcore.EnvelopeParams's fields and semantics for a
// Long PCM voice's amplitude envelope (see the package doc comment for why
// this isn't literally the same type).
type EnvelopeParams struct {
	AttackRate   uint8   // 0-31; 0 = never rises, 31 = fastest attack
	Decay1Rate   uint8   // 0-31; 0 = holds at full level
	SustainLevel float64 // 0.0-1.0 level that decay1 settles to
	Decay2Rate   uint8   // 0-31; 0 = sustain held indefinitely
	ReleaseRate  uint8   // 0-31; 0 = never falls
}

// WAVData is one decoded mono WAV sample, normalized to [-1,1] float32
// frames at its native sample rate, with optional loop points parsed from a
// "smpl" chunk.
type WAVData struct {
	SampleRate int
	Samples    []float32
	HasLoop    bool
	LoopStart  int // inclusive, in sample frames
	LoopEnd    int // exclusive, in sample frames
}

// VoiceSetting is one WAV sample mapped into a PCM voice.
type VoiceSetting struct {
	FileName string
	Wave     *WAVData

	// OneShot only: the MIDI note number that triggers this sample.
	Note    uint8
	HasNote bool

	// Long only: the MIDI note the sample plays back at its authored pitch.
	BaseNote uint8

	// OneShot only: this sample's own volume/pan (each OneShot sample can
	// be mixed independently). A Long voice's single sample instead always
	// plays at its part's volume/pan (see Engine.noteOnLongRow) - these two
	// fields are left at their zero value for a Long voiceSetting, even if
	// the source YAML specified them, matching CLAUDE.md's spec.
	Volume int // 0-127
	Pan    int // -16..16, continuous (e.g. 7 = slightly right of center), not just the three named points -16 (full left)/0 (center)/16 (full right)

	Envelope EnvelopeParams // Long only
}

// LFOParams mirrors fmcore.LFOParams for a Long PCM voice's optional
// vibrato LFO (Phase 8; see the package doc comment for why this isn't
// literally the same type). Meaningless - and always left at its zero
// value (Enabled false) - for a OneShot voice, per CLAUDE.md.
type LFOParams struct {
	Enabled      bool
	DelaySeconds float64 // seconds after note-on before the LFO starts
	FadeSeconds  float64 // seconds from LFO start to full depth
	DepthCents   float64 // vibrato depth in cents (100 = one semitone)
	RateHz       float64 // vibrato oscillation frequency, in Hz
}

// Voice (PCM音色) is a complete PCM timbre: either a bank of one-shot
// samples keyed by note (up to 16), or a single pitch-shiftable long sample.
type Voice struct {
	VoiceType VoiceType
	Loop      bool // Long only: whether to loop the sample during sustain
	Settings  []VoiceSetting
	LFO       LFOParams // Long only; ignored for OneShot
}
