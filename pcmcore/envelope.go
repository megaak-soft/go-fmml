/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package pcmcore

import "math"

type envStage int

const (
	stageAttack envStage = iota
	stageDecay1
	stageDecay2
	stageRelease
)

// envelope is the runtime state of a Long PCM voice's amplitude envelope
// generator. Its shape and math are identical to fmcore's operator envelope
// (see that package's envelope.go for the reasoning); duplicated here
// rather than shared to avoid an fmcore<->pcmcore import cycle.
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

func (e *envelope) finished() bool {
	return e.stage == stageRelease && e.level <= silenceThreshold
}

const silenceThreshold = 0.0005

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

func rateCoefficient(rate uint8, sampleRate int) float64 {
	if rate == 0 {
		return 0
	}
	const minTau = 0.001
	const maxTau = 8.0
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
