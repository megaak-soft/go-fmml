/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package fmcore

import "math"

// operator is the runtime state of one FM operator within a sounding note.
type operator struct {
	params  OperatorParams
	wave    int // W1-W8 selector, see OperatorParams.Wave
	eg      envelope
	phase   float64 // current phase in cycles, wrapped to [0,1)
	lastOut float64 // previous output sample, used for self-feedback
}

func newOperator(params OperatorParams) operator {
	wave := params.Wave
	if wave < 1 || wave > waveCount {
		wave = 1
	}
	return operator{params: params, wave: wave, eg: newEnvelope(params.Envelope)}
}

// totalLevelGain converts a 0-127 total-level attenuation value into a
// linear amplitude, at roughly 0.75dB per step.
func totalLevelGain(tl int) float64 {
	if tl <= 0 {
		return 1
	}
	if tl >= 127 {
		return 0
	}
	return math.Pow(10, -float64(tl)*0.75/20)
}

// step advances the operator by one sample and returns its output, given the
// modulation phase offset contributed by other operators and any
// self-feedback phase offset (operator 1 only).
func (o *operator) step(sampleRate int, noteFreq float64, modInput float64, feedback float64) float64 {
	detune := math.Pow(2, o.params.DetuneCents/1200)
	freq := noteFreq * o.params.Multiple * detune
	o.phase += freq / float64(sampleRate)
	if o.phase >= 1 {
		o.phase -= math.Floor(o.phase)
	}

	level := o.eg.advance(sampleRate)
	gain := totalLevelGain(o.params.TotalLevel)
	out := lookupWave(o.wave, o.phase+modInput+feedback) * level * gain
	o.lastOut = out
	return out
}
