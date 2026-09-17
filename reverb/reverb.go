/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

// Package reverb implements go-fmml's Phase 4 master-bus stereo reverb:
// two selectable decay-tail algorithms (a cheap parallel-comb/series-
// allpass network, and a denser feedback delay network), run as an
// original, from-scratch implementation of each general textbook topology -
// not a port of any specific existing reverb codebase or tuning table.
package reverb

import (
	"math"
	"strings"
)

// Kind selects which decay-tail algorithm Configure builds.
type Kind int

const (
	// Simple is a lightweight parallel-comb / series-allpass network
	// (a classic Schroeder-style topology), cheap enough to run
	// continuously even on modest hardware.
	Simple Kind = iota
	// Normal is a denser Feedback Delay Network, costing more CPU for a
	// smoother, less metallic decay tail.
	Normal
)

// ParseKind resolves a CLAUDE.md [global] reverbType string ("simple" or
// "normal", case-insensitive) into a Kind, defaulting to Normal for an
// empty or unrecognized value; ok reports whether the input was
// recognized, so the caller can warn on a bad value while still picking a
// safe default.
func ParseKind(s string) (kind Kind, ok bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "simple":
		return Simple, true
	case "normal":
		return Normal, true
	case "":
		return Normal, true
	default:
		return Normal, false
	}
}

// MinDecaySeconds/MaxDecaySeconds mirror CLAUDE.md's reverbTime bounds: a
// value at or below MinDecaySeconds means "bypass" (the caller should never
// call Configure at all - see fmcore.Engine.SetReverb), values above
// MaxDecaySeconds clamp down to it.
const (
	MinDecaySeconds = 0.5
	MaxDecaySeconds = 3.0
)

// duckFadeMs is how quickly Duck silences the wet tail: short enough that a
// Stop or Pause/Resume never leaves an audible reverb release ringing past
// the moment playback actually stopped (see CLAUDE.md's Phase 4 spec), long
// enough to avoid an audible click.
const duckFadeMs = 8.0

// tankChannel is one mono decay network (comb+allpass or FDN, see
// schroeder.go/fdn.go). Processor runs two independent instances, one per
// stereo side, so a hard-panned input keeps roughly its position in the wet
// output instead of collapsing to mono ("true stereo" per CLAUDE.md).
type tankChannel interface {
	step(in float32) float32
	clear()
}

// Processor is the master reverb send/return effect: feed it the summed,
// already-panned reverb-bus signal once per audio sample via Process, and
// mix its wet output back into the dry mix scaled by the sequence's
// reverbLevel. A zero-value Processor (before the first Configure) is
// inert: Process always returns silence.
type Processor struct {
	sampleRate int
	left       tankChannel
	right      tankChannel

	// preHPL/preHPR strip everything below reverbHighpassHz from the bus
	// before it reaches left/right (see Process), independent of Kind -
	// both algorithms are resonant delay networks that would otherwise let
	// bass build up into mud.
	preHPL, preHPR highpassFilter

	duckTotal     int
	duckRemaining int
}

// New creates a Processor with no active decay network - Process returns
// silence (bypass) until Configure is called. sampleRate <= 0 defaults to
// 44100.
func New(sampleRate int) *Processor {
	if sampleRate <= 0 {
		sampleRate = 44100
	}
	return &Processor{
		sampleRate: sampleRate,
		preHPL:     newHighpassFilter(sampleRate, reverbHighpassHz, reverbHighpassQ),
		preHPR:     newHighpassFilter(sampleRate, reverbHighpassHz, reverbHighpassQ),
	}
}

// Configure (re)builds the decay network for kind at decaySeconds (clamped
// here to [MinDecaySeconds, MaxDecaySeconds]) and discards any previous
// tail, so switching algorithm/time mid-playback never carries over stale
// echo content into the new one.
func (p *Processor) Configure(kind Kind, decaySeconds float64) {
	if decaySeconds < MinDecaySeconds {
		decaySeconds = MinDecaySeconds
	}
	if decaySeconds > MaxDecaySeconds {
		decaySeconds = MaxDecaySeconds
	}

	if kind == Normal {
		p.left = newFDNChannel(p.sampleRate, decaySeconds, 0)
		p.right = newFDNChannel(p.sampleRate, decaySeconds, 1)
	} else {
		p.left = newSchroederChannel(p.sampleRate, decaySeconds, 0)
		p.right = newSchroederChannel(p.sampleRate, decaySeconds, 1)
	}

	p.preHPL.reset()
	p.preHPR.reset()

	p.duckRemaining = 0
	p.duckTotal = int(duckFadeMs / 1000 * float64(p.sampleRate))
	if p.duckTotal < 1 {
		p.duckTotal = 1
	}
}

// Disable clears any active decay network so Process returns silence
// (bypass) - see CLAUDE.md's reverb:false / reverbTime<=MinDecaySeconds
// bypass rules.
func (p *Processor) Disable() {
	p.left = nil
	p.right = nil
	p.duckRemaining = 0
}

// Active reports whether Process currently does anything (a decay network
// is configured and hasn't been Disabled).
func (p *Processor) Active() bool {
	return p.left != nil && p.right != nil
}

// wetDriveGain compensates for both algorithms' inherent tap-averaging
// loss (schroeder.go's 4-comb sum /4, fdn.go's 8-line sum /sqrt(8)) so a
// full-strength send (reverbSend=127) into a full-strength level
// (reverbLevel=127) actually sounds prominent against typically short,
// decaying notes - not just against a tone sustained long enough to build
// the tank up to its own steady state. Driving into wetSoftClip rather
// than returning this raw is what makes that safe: a comb/FDN network is
// a set of resonant filters, so a sustained tone landing on one of their
// resonant frequencies can already ring up past unit gain on its own
// (before this drive is even applied) - wetSoftClip tames that gracefully
// instead of the raw amplitude (and downstream fmcore.Engine's own final
// mix) clipping harshly.
const wetDriveGain = 2.5

// wetSoftClip bounds a driven reverb-channel sample to (-1,1), compressing
// a loud (typically resonant-peak) sample gently rather than letting it
// grow unbounded, while barely touching a quiet one - so wetDriveGain adds
// real presence to normal decaying-note content without a sustained tone
// at a resonant frequency blowing up disproportionately.
func wetSoftClip(v float32) float32 {
	return float32(math.Tanh(float64(v)))
}

// Process runs one stereo sample through the reverb bus and returns its wet
// output; the caller scales this by reverbLevel and adds it to the always-
// present dry mix. Returns silence when Disabled/never Configured.
func (p *Processor) Process(inL, inR float32) (float32, float32) {
	if !p.Active() {
		return 0, 0
	}
	inL = p.preHPL.step(inL)
	inR = p.preHPR.step(inR)
	outL := wetSoftClip(p.left.step(inL) * wetDriveGain)
	outR := wetSoftClip(p.right.step(inR) * wetDriveGain)

	if p.duckRemaining > 0 {
		gain := float32(p.duckRemaining) / float32(p.duckTotal)
		outL *= gain
		outR *= gain
		p.duckRemaining--
		if p.duckRemaining == 0 {
			p.left.clear()
			p.right.clear()
		}
	}
	return outL, outR
}

// Duck starts a short (duckFadeMs) fade of the wet output to silence, then
// clears the decay network's internal memory once the fade completes, so a
// Stop or Pause/Resume never leaves an audible reverb tail ringing on (see
// CLAUDE.md's Phase 4 spec: "極短フェードアウトしてリバーブリリースを残さ
// ない様にする"). A no-op if no decay network is active. Safe to call again
// mid-fade (simply restarts it).
func (p *Processor) Duck() {
	if !p.Active() || p.duckTotal == 0 {
		return
	}
	p.duckRemaining = p.duckTotal
}
