/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package fmcore

import "math"

const sineTableSize = 1024

// waveCount is how many selectable basic oscillator waveforms an operator
// can choose from (Phase 5's W1-W8; see CLAUDE.md). W1 is the classic sine
// and is index 0, matching OperatorParams.Wave's "0/invalid defaults to W1"
// rule.
const waveCount = 8

var waveTables [waveCount][sineTableSize]float64

func init() {
	for i := 0; i < sineTableSize; i++ {
		p := 2 * math.Pi * float64(i) / sineTableSize
		sin := math.Sin(p)
		waveTables[0][i] = sin                           // W1: sine
		waveTables[1][i] = halfRectify(sin)              // W2: half-wave rectified sine
		waveTables[2][i] = math.Abs(sin)*2 - 1           // W3: full-wave rectified, bipolar
		waveTables[3][i] = compressedHalfSine(p)         // W4: half-wave rectified, doubled rate
		waveTables[4][i] = compressedHalfSine(p)         // W5: same shape as W4 per spec
		waveTables[5][i] = math.Abs(math.Sin(p*2))*2 - 1 // W6: full-wave rectified, doubled rate
		waveTables[6][i] = math.Abs(sin)                 // W7: full-wave rectified, unipolar
		waveTables[7][i] = math.Sin(p * 2)               // W8: doubled-rate sine
	}
}

func halfRectify(sin float64) float64 {
	if sin < 0 {
		return 0
	}
	return sin
}

func compressedHalfSine(phase float64) float64 {
	if phase < math.Pi {
		return math.Sin(phase * 2)
	}
	return 0
}

// lookupWave returns the selected waveform's value for phase expressed in
// cycles (not necessarily wrapped to [0,1)). wave is a 1-8 W1-W8 selector
// (see OperatorParams.Wave); any other value falls back to W1.
func lookupWave(wave int, phase float64) float64 {
	idx := wave - 1
	if idx < 0 || idx >= waveCount {
		idx = 0
	}
	phase -= math.Floor(phase)
	i := int(phase*sineTableSize) & (sineTableSize - 1)
	return waveTables[idx][i]
}
