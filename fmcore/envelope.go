/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package fmcore

import "math"

type envStage int

const (
	stageAttack envStage = iota
	stageDecay1
	stageDecay2
	stageRelease
)

// envelope is the runtime state of one operator's envelope generator.
type envelope struct {
	params EnvelopeParams
	stage  envStage
	level  float64
}

func newEnvelope(params EnvelopeParams) envelope {
	return envelope{params: params, stage: stageAttack, level: 0}
}

func (e *envelope) noteOff() {
	e.stage = stageRelease
}

// finished reports whether this envelope has decayed to silence in its
// release stage, i.e. the note it belongs to is done sounding.
func (e *envelope) finished() bool {
	return e.stage == stageRelease && e.level <= silenceThreshold
}

const silenceThreshold = 0.0005

// advance steps the envelope by one sample and returns the new amplitude
// level in [0,1].
func (e *envelope) advance(sampleRate int) float64 {
	switch e.stage {
	case stageAttack:
		coef := rateCoefficient(e.params.AttackRate, sampleRate)
		e.level += (1 - e.level) * coef
		if e.params.AttackRate == 0 || e.level >= 1-silenceThreshold {
			e.level = 1
			e.stage = stageDecay1
		}
	case stageDecay1:
		target := clamp01(e.params.SustainLevel)
		coef := rateCoefficient(e.params.Decay1Rate, sampleRate)
		e.level -= (e.level - target) * coef
		if e.params.Decay1Rate == 0 || math.Abs(e.level-target) <= silenceThreshold {
			e.level = target
			e.stage = stageDecay2
		}
	case stageDecay2:
		coef := rateCoefficient(e.params.Decay2Rate, sampleRate)
		e.level -= e.level * coef
	case stageRelease:
		coef := rateCoefficient(e.params.ReleaseRate, sampleRate)
		e.level -= e.level * coef
		if e.level <= silenceThreshold {
			e.level = 0
		}
	}
	if e.level < 0 {
		e.level = 0
	} else if e.level > 1 {
		e.level = 1
	}
	return e.level
}

// rateCoefficient converts a 0-31 envelope rate into a per-sample
// exponential coefficient, independent of sample rate. Rate 0 always yields
// 0 (no movement, i.e. hold), rate 31 approaches an instant transition.
func rateCoefficient(rate uint8, sampleRate int) float64 {
	if rate == 0 {
		return 0
	}
	const minTau = 0.001 // seconds, fastest (rate 31)
	const maxTau = 8.0   // seconds, slowest (rate 1)
	t := float64(rate-1) / 30.0
	tau := maxTau * math.Pow(minTau/maxTau, t)
	return 1 - math.Exp(-1/(tau*float64(sampleRate)))
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
