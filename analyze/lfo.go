/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package analyze

// Phase 8's vibrato LFO defaults, used whenever "lfo: true" is set on a
// voice but one of its lfoDelay/lfoFade/lfoDepth/lfoHz fields is omitted
// (see CLAUDE.md: "各lfoパラメータが未設定の場合はデフォルト値を使用する").
const (
	defaultLFODelaySeconds = 0.4
	defaultLFOFadeSeconds  = 0.5
	defaultLFODepthCents   = 30.0
	defaultLFORateHz       = 6.0
)

// resolveLFOValues fills in any of delay/fade/depth/hz left nil (the field
// was omitted from the voice's YAML) with Phase 8's documented defaults,
// leaving an explicitly-given value (including an explicit 0) untouched.
// Shared by fmvoice.go and pcmvoice.go, whose FM/PCM voice YAML shapes both
// define an identical set of "lfo"/"lfoDelay"/"lfoFade"/"lfoDepth"/"lfoHz"
// fields.
func resolveLFOValues(delay, fade, depth, hz *float64) (delaySeconds, fadeSeconds, depthCents, rateHz float64) {
	delaySeconds, fadeSeconds, depthCents, rateHz = defaultLFODelaySeconds, defaultLFOFadeSeconds, defaultLFODepthCents, defaultLFORateHz
	if delay != nil {
		delaySeconds = *delay
	}
	if fade != nil {
		fadeSeconds = *fade
	}
	if depth != nil {
		depthCents = *depth
	}
	if hz != nil {
		rateHz = *hz
	}
	return delaySeconds, fadeSeconds, depthCents, rateHz
}
