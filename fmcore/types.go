/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

// Package fmcore implements an asynchronous, multi-timbral FM sound
// synthesis core in the general style of classic four-operator FM chips
// (four operators, a selectable operator-routing algorithm, and per-
// operator envelope generators), for embedding as a BGM engine inside Go
// game projects. This is an original, from-scratch implementation of that
// general architecture, not a port or derivative of any specific chip's
// register map or existing emulator/synthesizer codebase.
package fmcore

// VoiceID identifies a registered voice (音色) in the voice bank.
type VoiceID uint32

// PartID identifies a sounding part (パート) that a voice is assigned to.
type PartID int

// NoteID identifies a single note-on request, returned immediately by NoteOn
// while the note continues sounding asynchronously.
type NoteID uint64

// EnvelopeParams models a multi-stage amplitude envelope generator: an
// exponential attack up to full level, decay to a sustain level, a slow
// secondary decay of the sustain, and a release after key-off.
type EnvelopeParams struct {
	AttackRate   uint8   // 0-31; 0 = never rises, 31 = fastest attack
	Decay1Rate   uint8   // 0-31; 0 = holds at full level
	SustainLevel float64 // 0.0-1.0 level that decay1 settles to
	Decay2Rate   uint8   // 0-31; 0 = sustain held indefinitely
	ReleaseRate  uint8   // 0-31; 0 = never falls (avoid on carriers)
}

// OperatorParams configures one of a voice's four FM operators.
type OperatorParams struct {
	Multiple    float64 // frequency ratio applied to the note's base frequency
	DetuneCents float64 // fine detune in cents, added on top of Multiple
	TotalLevel  int     // output attenuation on a 0 (loudest) - 127 (silent) scale
	Envelope    EnvelopeParams

	// Wave selects this operator's basic oscillator waveform (Phase 5's
	// W1-W8; see CLAUDE.md and go-fmml/analyze's FM voice YAML
	// per-operator "wave" field). 1 = W1 (the classic sine, this engine's
	// original waveform); any other value outside 1-8 also falls back to
	// W1, so an operator from before this field existed sounds unchanged.
	Wave int
}

// LFOParams configures a voice's optional vibrato LFO (Phase 8): a sine-
// wave pitch modulation layered on top of a note's own frequency (see
// runningNote.lfoOffsetSemitones), starting DelaySeconds after note-on and
// fading its depth in linearly over FadeSeconds before holding at full
// DepthCents. Ignored entirely while Enabled is false.
type LFOParams struct {
	Enabled      bool
	DelaySeconds float64 // seconds after note-on before the LFO starts
	FadeSeconds  float64 // seconds from LFO start to full depth
	DepthCents   float64 // vibrato depth in cents (100 = one semitone)
	RateHz       float64 // vibrato oscillation frequency, in Hz
}

// Voice (音色) is a complete FM timbre: four operators wired together by an
// algorithm, with optional self-feedback on operator 1.
type Voice struct {
	Operators [4]OperatorParams
	Algorithm int // 0-7, selects the operator connection graph, see algorithm.go
	Feedback  int // 0-7, self-feedback amount applied to operator 1
	LFO       LFOParams
}
