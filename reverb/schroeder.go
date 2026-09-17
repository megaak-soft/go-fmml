/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package reverb

import "math"

// schroederChannel is a single-channel lightweight reverb network: four
// parallel damped feedback comb filters summed together, feeding two
// series allpass stages for diffusion - a classic textbook comb+allpass
// arrangement, assembled here from scratch (own delay-length spacing, own
// gain derivation) rather than copied from any specific existing reverb
// implementation's tuning table.
type schroederChannel struct {
	combs     [4]combFilter
	allpasses [2]allpassFilter
}

// combBaseMs/allpassBaseMs are this package's own base delay-length spacing
// in milliseconds (at a nominal reference), deliberately irregular (no
// small-integer ratios between them) so the four combs don't reinforce a
// single periodic pitch. Scaled by sample rate and nudged per stereo side
// in newSchroederChannel so the left/right networks decorrelate slightly
// instead of mirroring each other exactly.
var combBaseMs = [4]float64{23.3, 29.7, 34.9, 40.1}
var allpassBaseMs = [2]float64{5.3, 1.9}

// combDampingCoeff is a fixed one-pole lowpass amount applied inside each
// comb's feedback path, rolling off the high end of the decay tail a bit
// more on each pass so it doesn't ring metallically forever.
const combDampingCoeff = 0.20

func newSchroederChannel(sampleRate int, decaySeconds float64, side int) *schroederChannel {
	c := &schroederChannel{}
	sideOffsetMs := float64(side) * 0.8
	for i := range c.combs {
		c.combs[i] = newCombFilter(sampleRate, combBaseMs[i]+sideOffsetMs, decaySeconds)
	}
	for i := range c.allpasses {
		c.allpasses[i] = newAllpassFilter(sampleRate, allpassBaseMs[i]+sideOffsetMs)
	}
	return c
}

func (c *schroederChannel) step(in float32) float32 {
	var sum float32
	for i := range c.combs {
		sum += c.combs[i].step(in)
	}
	sum *= 0.25
	for i := range c.allpasses {
		sum = c.allpasses[i].step(sum)
	}
	return sum
}

func (c *schroederChannel) clear() {
	for i := range c.combs {
		c.combs[i].clear()
	}
	for i := range c.allpasses {
		c.allpasses[i].clear()
	}
}

// combFilter is a damped feedback (lowpass) comb filter: it outputs its
// delay line's current tap, then writes the new input plus a damped,
// gain-scaled copy of that same tap back in - the standard way to give a
// comb filter's decay a naturally darkening tail instead of an infinitely
// bright one.
type combFilter struct {
	buf     []float32
	pos     int
	gain    float32
	lpState float32
}

func newCombFilter(sampleRate int, lengthMs float64, decaySeconds float64) combFilter {
	n := int(lengthMs / 1000 * float64(sampleRate))
	if n < 1 {
		n = 1
	}
	return combFilter{buf: make([]float32, n), gain: feedbackGainForRT60(n, sampleRate, decaySeconds)}
}

func (f *combFilter) step(in float32) float32 {
	out := f.buf[f.pos]
	f.lpState += combDampingCoeff * (out - f.lpState)
	f.buf[f.pos] = in + f.gain*f.lpState
	f.pos++
	if f.pos >= len(f.buf) {
		f.pos = 0
	}
	return out
}

func (f *combFilter) clear() {
	for i := range f.buf {
		f.buf[i] = 0
	}
	f.lpState = 0
}

// allpassFilter is a standard fixed-gain Schroeder allpass diffuser: it
// smears a signal in time without coloring its frequency content, used
// here in series after the combs to blur their otherwise-regular echoes
// into a smoother tail.
type allpassFilter struct {
	buf  []float32
	pos  int
	gain float32
}

func newAllpassFilter(sampleRate int, lengthMs float64) allpassFilter {
	n := int(lengthMs / 1000 * float64(sampleRate))
	if n < 1 {
		n = 1
	}
	return allpassFilter{buf: make([]float32, n), gain: 0.5}
}

func (f *allpassFilter) step(in float32) float32 {
	bufOut := f.buf[f.pos]
	out := -f.gain*in + bufOut
	f.buf[f.pos] = in + f.gain*out
	f.pos++
	if f.pos >= len(f.buf) {
		f.pos = 0
	}
	return out
}

func (f *allpassFilter) clear() {
	for i := range f.buf {
		f.buf[i] = 0
	}
}

// feedbackGainForRT60 derives a delay line's per-pass feedback gain so its
// energy decays by 60dB after roughly decaySeconds, from the standard
// RT60/delay-length relationship: gain = 10^(-3*delaySamples/(RT60*rate)).
// Shared by both algorithms in this package (see fdn.go).
func feedbackGainForRT60(delaySamples int, sampleRate int, decaySeconds float64) float32 {
	if decaySeconds <= 0 {
		return 0
	}
	g := math.Pow(10, -3*float64(delaySamples)/(decaySeconds*float64(sampleRate)))
	if g > 0.98 {
		g = 0.98
	}
	return float32(g)
}
