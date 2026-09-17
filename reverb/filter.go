/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package reverb

import "math"

// reverbHighpassHz is a fixed pre-filter cutoff applied to the reverb bus
// before it ever reaches either decay network (see Processor.Process), so
// low end doesn't resonantly build up into a boomy/muddy low end: a
// comb/FDN delay line is itself a resonant filter (see wetDriveGain's doc
// comment for the same phenomenon at other frequencies), and bass energy
// is exactly what a listener notices piling up as mud rather than a clean
// decay tail. Filtering it out before the tank, not after, is what
// actually prevents that buildup - filtering the wet output afterward
// would still let the resonance ring internally, just muted on the way
// out. A gentler one-pole (-6dB/octave) cut here left too much of that
// resonance fighting through even below the nominal cutoff, so this uses
// a steeper two-pole (-12dB/octave) filter instead.
const reverbHighpassHz = 200.0

// reverbHighpassQ is a Butterworth Q (1/sqrt(2)): maximally flat above the
// cutoff, no resonant bump right at it, which a boomy-bass complaint is
// exactly what you don't want to reintroduce via the filter itself.
const reverbHighpassQ = 0.70710678

// highpassFilter is a standard two-pole (12dB/octave) Butterworth
// high-pass biquad in Direct Form I, per the widely-published RBJ Audio
// EQ Cookbook coefficient formulas (a standard textbook reference, not
// sourced from any particular existing codebase).
type highpassFilter struct {
	b0, b1, b2 float32
	a1, a2     float32

	x1, x2 float32 // input history
	y1, y2 float32 // output history
}

func newHighpassFilter(sampleRate int, cutoffHz, q float64) highpassFilter {
	w0 := 2 * math.Pi * cutoffHz / float64(sampleRate)
	cosW0, sinW0 := math.Cos(w0), math.Sin(w0)
	alpha := sinW0 / (2 * q)

	b0 := (1 + cosW0) / 2
	b1 := -(1 + cosW0)
	b2 := (1 + cosW0) / 2
	a0 := 1 + alpha
	a1 := -2 * cosW0
	a2 := 1 - alpha

	return highpassFilter{
		b0: float32(b0 / a0),
		b1: float32(b1 / a0),
		b2: float32(b2 / a0),
		a1: float32(a1 / a0),
		a2: float32(a2 / a0),
	}
}

func (f *highpassFilter) step(x float32) float32 {
	y := f.b0*x + f.b1*f.x1 + f.b2*f.x2 - f.a1*f.y1 - f.a2*f.y2
	f.x2, f.x1 = f.x1, x
	f.y2, f.y1 = f.y1, y
	return y
}

func (f *highpassFilter) reset() {
	f.x1, f.x2, f.y1, f.y2 = 0, 0, 0, 0
}
