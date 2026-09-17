/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package reverb

import (
	"math"
	"testing"
)

func TestProcessorInertUntilConfigured(t *testing.T) {
	p := New(44100)
	l, r := p.Process(1, 1)
	if l != 0 || r != 0 {
		t.Fatalf("expected silence before Configure, got %v, %v", l, r)
	}
}

func TestProcessorProducesTailAfterImpulse(t *testing.T) {
	for _, kind := range []Kind{Simple, Normal} {
		p := New(44100)
		p.Configure(kind, 1.5)

		l, r := p.Process(1, 1)
		if l != 0 || r != 0 {
			t.Fatalf("kind %v: expected the very first sample (before any delay line fills) to be silence, got %v, %v", kind, l, r)
		}

		var peak float32
		for i := 0; i < 4096; i++ {
			l, r := p.Process(0, 0)
			if v := abs32(l); v > peak {
				peak = v
			}
			if v := abs32(r); v > peak {
				peak = v
			}
		}
		if peak == 0 {
			t.Fatalf("kind %v: expected a non-silent decay tail following an impulse, got total silence", kind)
		}
	}
}

func TestProcessorDisableBypasses(t *testing.T) {
	p := New(44100)
	p.Configure(Normal, 1.5)
	p.Process(1, 1)
	p.Disable()
	if p.Active() {
		t.Fatalf("expected Disable to clear Active")
	}
	l, r := p.Process(1, 1)
	if l != 0 || r != 0 {
		t.Fatalf("expected silence after Disable, got %v, %v", l, r)
	}
}

func TestProcessorDuckSilencesTailQuickly(t *testing.T) {
	p := New(44100)
	p.Configure(Normal, 3.0)
	for i := 0; i < 200; i++ {
		p.Process(1, 1)
	}

	p.Duck()
	// duckFadeMs (8ms) worth of samples at 44100Hz is ~353 samples; give it
	// generous headroom and confirm the wet output has reached silence.
	for i := 0; i < 1000; i++ {
		p.Process(0, 0)
	}
	l, r := p.Process(0, 0)
	if l != 0 || r != 0 {
		t.Fatalf("expected Duck to silence the reverb tail well within 1000 samples, got %v, %v", l, r)
	}
}

func TestProcessorTrueStereoKeepsHardPannedInputOnItsSide(t *testing.T) {
	for _, kind := range []Kind{Simple, Normal} {
		p := New(44100)
		p.Configure(kind, 1.5)

		var leftEnergy, rightEnergy float64
		// Feed a hard-left-only impulse and accumulate energy on each wet
		// output side over the resulting tail.
		p.Process(1, 0)
		for i := 0; i < 4096; i++ {
			l, r := p.Process(0, 0)
			leftEnergy += float64(l) * float64(l)
			rightEnergy += float64(r) * float64(r)
		}
		if leftEnergy <= rightEnergy {
			t.Fatalf("kind %v: expected a hard-left input to concentrate wet energy on the left side, got left=%v right=%v", kind, leftEnergy, rightEnergy)
		}
	}
}

// TestNormalReverbLevelIsComparableToSimple guards against a regression
// where Normal's (FDN) input/output tap scaling double-attenuated its wet
// output relative to Simple's (a real bug: an early build's output tap
// divided by fdnLines instead of sqrt(fdnLines), on top of the input
// injection's own 1/sqrt(fdnLines) scaling, left Normal audibly much
// quieter than Simple at an identical reverbTime - see fdn.go's step doc
// comment). Both algorithms' RMS level shortly after an identical impulse
// should be within the same order of magnitude, not off by many times.
func TestNormalReverbLevelIsComparableToSimple(t *testing.T) {
	rmsAfterImpulse := func(kind Kind) float64 {
		p := New(44100)
		p.Configure(kind, 1.5)
		p.Process(1, 1)

		var sumSq float64
		const n = 2205 // 50ms at 44100Hz
		for i := 0; i < n; i++ {
			l, r := p.Process(0, 0)
			sumSq += float64(l)*float64(l) + float64(r)*float64(r)
		}
		return sumSq / float64(n)
	}

	simpleRMS := rmsAfterImpulse(Simple)
	normalRMS := rmsAfterImpulse(Normal)
	if simpleRMS == 0 {
		t.Fatalf("expected Simple to produce a non-silent tail to compare against")
	}
	if ratio := normalRMS / simpleRMS; ratio < 0.2 {
		t.Fatalf("expected Normal's tail level to be within the same order of magnitude as Simple's, got Normal/Simple RMS ratio %v (Normal=%v, Simple=%v)", ratio, normalRMS, simpleRMS)
	}
}

// TestNormalReverbHandlesSustainedInputAtFullLevel guards against a second,
// more severe regression than TestNormalReverbLevelIsComparableToSimple's
// scaling bug: an earlier version of the FDN output tap summed its 8
// delay-line reads with an alternating +/- sign per line (meant to spread
// spectral peaks/notches). Against a single impulse that looked fine (the
// reads never overlapped in time), but against a sustained tone - i.e. real
// playback, not a single click - the sign pattern lined up with the taps'
// relative phase closely enough to cancel most of the output, leaving
// Normal barely audible even at reverbLevel/reverbSend maxed out. Feed a
// continuous tone (not an impulse) and require the wet output's RMS to
// stay within the same order of magnitude as the dry input's, across
// several frequencies.
func TestNormalReverbHandlesSustainedInputAtFullLevel(t *testing.T) {
	for _, freq := range []float64{220, 440, 880, 1500} {
		p := New(44100)
		p.Configure(Normal, 1.5)

		const n = 88200 // 2 seconds at 44100Hz
		var wetSumSq float64
		for i := 0; i < n; i++ {
			in := float32(math.Sin(2 * math.Pi * freq * float64(i) / 44100))
			l, r := p.Process(in, in)
			wetSumSq += float64(l)*float64(l) + float64(r)*float64(r)
		}
		wetRMS := math.Sqrt(wetSumSq / (2 * n))
		const dryRMS = 0.7071067811865476 // RMS of a unit sine
		if ratio := wetRMS / dryRMS; ratio < 0.3 {
			t.Fatalf("freq %vHz: expected a sustained tone's wet/dry RMS ratio to stay within the same order of magnitude, got %v (wetRMS=%v)", freq, ratio, wetRMS)
		}
	}
}

// TestProcessorCutsBassBelowHighpassCutoff confirms the reverb bus's fixed
// pre-filter (reverbHighpassHz, see filter.go) actually attenuates a
// sustained low-frequency tone well below reverbHighpassHz relative to one
// comfortably above it - the fix for a "低音部分がモコモコする" (boomy/
// muddy bass) complaint against the reverb tail.
func TestProcessorCutsBassBelowHighpassCutoff(t *testing.T) {
	rms := func(kind Kind, freq float64) float64 {
		p := New(44100)
		p.Configure(kind, 1.5)

		const n = 88200 // 2 seconds at 44100Hz
		var sumSq float64
		for i := 0; i < n; i++ {
			in := float32(math.Sin(2 * math.Pi * freq * float64(i) / 44100))
			l, r := p.Process(in, in)
			sumSq += float64(l)*float64(l) + float64(r)*float64(r)
		}
		return math.Sqrt(sumSq / (2 * n))
	}

	for _, kind := range []Kind{Simple, Normal} {
		bassRMS := rms(kind, 60) // well below reverbHighpassHz
		midRMS := rms(kind, 440) // well above it
		if bassRMS >= midRMS {
			t.Fatalf("kind %v: expected 60Hz to be attenuated well below 440Hz by the highpass pre-filter, got bassRMS=%v midRMS=%v", kind, bassRMS, midRMS)
		}
	}
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
