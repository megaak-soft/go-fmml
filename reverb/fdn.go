/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package reverb

import "math"

// fdnLines is the delay-line count for the Normal (denser) reverb network.
const fdnLines = 8

// fdnChannel is a single-channel Feedback Delay Network reverb: fdnLines
// delay lines cross-coupled through a Householder reflection matrix (a
// well-known, energy-preserving feedback topology from general DSP theory,
// not a specific existing codebase), each with its own one-pole damping
// filter and RT60-derived feedback gain. This is a fresh implementation of
// that general technique: delay-length spacing and mix/output taps below
// are this package's own choices, not copied from any particular reverb
// library's tuning table.
type fdnChannel struct {
	delays [fdnLines]delayLine
	damp   [fdnLines]float32 // one-pole lowpass state per line
	gain   [fdnLines]float32
}

// fdnBaseMs is this package's own delay-length spacing for the FDN's 8
// lines (milliseconds at a nominal reference sample rate), deliberately
// mutually irregular (no small-integer ratios between entries) so the
// lines don't reinforce a single periodic echo. Scaled by sample rate and
// nudged per stereo side in newFDNChannel.
var fdnBaseMs = [fdnLines]float64{19.7, 24.3, 28.9, 33.1, 37.7, 42.3, 46.9, 51.7}

// fdnDampingCoeff is a fixed one-pole lowpass amount applied inside each
// line's feedback path, matching combDampingCoeff's role in the Simple
// algorithm (see schroeder.go).
const fdnDampingCoeff = 0.15

func newFDNChannel(sampleRate int, decaySeconds float64, side int) *fdnChannel {
	c := &fdnChannel{}
	sideOffsetMs := float64(side) * 1.1
	for i := 0; i < fdnLines; i++ {
		n := int((fdnBaseMs[i] + sideOffsetMs) / 1000 * float64(sampleRate))
		if n < 1 {
			n = 1
		}
		c.delays[i] = newDelayLine(n)
		c.gain[i] = feedbackGainForRT60(n, sampleRate, decaySeconds)
	}
	return c
}

// sqrtFdnLines is the L2 norm of the injection vector (1 per line, applied
// to inject below) and of the output-tap vector (also 1 per line, applied
// to out below). Scaling each by 1/sqrtFdnLines - not 1/fdnLines - keeps
// both taps unit-norm, so input->output gain along any single line stays
// of order 1 instead of collapsing by an extra 1/sqrtFdnLines the way
// dividing the output by fdnLines outright would (an early Phase 4 bug:
// input and output together attenuated by 1/(fdnLines*sqrtFdnLines),
// leaving Normal's wet output audibly quieter than Simple's).
var sqrtFdnLines = float32(math.Sqrt(fdnLines))

// step advances the network by one sample: it reads every line's current
// tap, mixes the input into all of them equally, feeds each line back a
// Householder-reflected (energy-preserving) combination of every tap so a
// single input diffuses across the whole network over successive passes,
// and taps the output as the (unit-norm-scaled) sum of this sample's
// reads. An earlier version summed the taps with an alternating +/- sign
// per line to spread their spectral peaks/notches, but for a sustained
// (non-impulse) input that pattern lines up with the taps' relative phase
// often enough to cancel most of the output - a much larger loss than the
// scaling bug above, and the real reason Normal sounded barely audible
// under real (sustained-note) playback despite testing fine against a
// single impulse. A plain same-sign sum avoids that cancellation, at the
// cost of leaning on the delay lines' own irregular spacing (fdnBaseMs)
// alone for decorrelation - the same trade-off schroeder.go's parallel
// comb sum already makes.
func (c *fdnChannel) step(in float32) float32 {
	var read [fdnLines]float32
	var sum float32
	for i := 0; i < fdnLines; i++ {
		read[i] = c.delays[i].read()
		sum += read[i]
	}

	inject := in / sqrtFdnLines
	const two = float32(2)
	for i := 0; i < fdnLines; i++ {
		reflected := read[i] - two/fdnLines*sum
		c.damp[i] += fdnDampingCoeff * (reflected - c.damp[i])
		c.delays[i].write(inject + c.gain[i]*c.damp[i])
	}

	return sum / sqrtFdnLines
}

func (c *fdnChannel) clear() {
	for i := range c.delays {
		c.delays[i].clear()
		c.damp[i] = 0
	}
}

// delayLine is a simple circular-buffer sample delay used by fdnChannel
// (schroeder.go's comb/allpass filters implement their own, tighter-scoped
// read/write-in-one-step versions inline).
type delayLine struct {
	buf []float32
	pos int
}

func newDelayLine(n int) delayLine {
	return delayLine{buf: make([]float32, n)}
}

func (d *delayLine) read() float32 {
	return d.buf[d.pos]
}

func (d *delayLine) write(v float32) {
	d.buf[d.pos] = v
	d.pos++
	if d.pos >= len(d.buf) {
		d.pos = 0
	}
}

func (d *delayLine) clear() {
	for i := range d.buf {
		d.buf[i] = 0
	}
}
