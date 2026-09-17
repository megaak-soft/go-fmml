/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

// Package analyze parses FM voice parameter files (YAML) and MML sequence
// files into go-fmml's internal data shapes, without touching disk itself
// (go-fmml/fileio owns reading bytes and storing the parsed result into
// go-fmml/memory).
package analyze

import (
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/megaak-soft/go-fmml/fmcore"
)

// VoiceDefinition pairs a parsed voice with the ID it should be registered
// under.
type VoiceDefinition struct {
	VoiceID fmcore.VoiceID
	Voice   fmcore.Voice
}

type fmVoiceFile struct {
	FmVoice []struct {
		VoiceID   int `yaml:"voiceID"`
		Algorithm int `yaml:"algorithm"`
		Feedback  int `yaml:"feedback"`

		// LFO/LFODelay/LFOFade/LFODepth/LFOHz are Phase 8's optional
		// vibrato settings (see CLAUDE.md); the pointer fields distinguish
		// "omitted" (use resolveLFOValues's default) from an explicit
		// value, including an explicit 0.
		LFO      bool     `yaml:"lfo"`
		LFODelay *float64 `yaml:"lfoDelay"`
		LFOFade  *float64 `yaml:"lfoFade"`
		LFODepth *float64 `yaml:"lfoDepth"`
		LFOHz    *float64 `yaml:"lfoHz"`

		Operators []struct {
			Multiple    float64 `yaml:"multiple"`
			TotalLevel  int     `yaml:"totallevel"`
			DetuneCents float64 `yaml:"detuneCents"`
			Wave        string  `yaml:"wave"`
			Envelope    string  `yaml:"envelope"`
		} `yaml:"operators"`
	} `yaml:"fmVoice"`
}

// ParseFMVoiceFile parses the YAML contents of an FM voice parameter file
// (see CLAUDE.md's "FM音源パラメータファイル仕様") into voice definitions.
// A malformed envelope field or a missing/extra operator is a minor error:
// it is reported as a warning and the affected value defaults to 0, parsing
// continues. A file that isn't valid YAML at all is a fatal error.
func ParseFMVoiceFile(data []byte) ([]VoiceDefinition, []string, error) {
	var file fmVoiceFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, nil, fmt.Errorf("analyze: invalid FM voice YAML: %w", err)
	}

	var warnings []string
	if len(file.FmVoice) == 0 {
		warnings = append(warnings, "fmVoice: no voices defined")
	}

	defs := make([]VoiceDefinition, 0, len(file.FmVoice))
	for _, fv := range file.FmVoice {
		delaySeconds, fadeSeconds, depthCents, rateHz := resolveLFOValues(fv.LFODelay, fv.LFOFade, fv.LFODepth, fv.LFOHz)
		voice := fmcore.Voice{
			Algorithm: fv.Algorithm,
			Feedback:  fv.Feedback,
			LFO: fmcore.LFOParams{
				Enabled:      fv.LFO,
				DelaySeconds: delaySeconds,
				FadeSeconds:  fadeSeconds,
				DepthCents:   depthCents,
				RateHz:       rateHz,
			},
		}

		ops := fv.Operators
		if len(ops) > 4 {
			warnings = append(warnings, fmt.Sprintf("voiceID %d: %d operators given, only the first 4 are used", fv.VoiceID, len(ops)))
			ops = ops[:4]
		} else if len(ops) < 4 {
			warnings = append(warnings, fmt.Sprintf("voiceID %d: only %d operators given, remaining default to silent", fv.VoiceID, len(ops)))
		}

		for i, op := range ops {
			env, envWarnings := parseEnvelope(op.Envelope)
			for _, w := range envWarnings {
				warnings = append(warnings, fmt.Sprintf("voiceID %d operator %d: %s", fv.VoiceID, i, w))
			}
			wave, waveOK := parseWave(op.Wave)
			if op.Wave != "" && !waveOK {
				warnings = append(warnings, fmt.Sprintf("voiceID %d operator %d: wave %q is unrecognized, defaulting to W1", fv.VoiceID, i, op.Wave))
			}
			voice.Operators[i] = fmcore.OperatorParams{
				Multiple:    op.Multiple,
				DetuneCents: op.DetuneCents,
				TotalLevel:  op.TotalLevel,
				Wave:        wave,
				Envelope:    env,
			}
		}

		defs = append(defs, VoiceDefinition{VoiceID: fmcore.VoiceID(fv.VoiceID), Voice: voice})
	}
	return defs, warnings, nil
}

// parseWave parses an operator's "wave" field value ("W1".."W8",
// case-insensitive) into fmcore.OperatorParams.Wave's 1-8 scale. An empty
// or unrecognized string returns (1, false) - the caller decides whether an
// empty string (field simply omitted) deserves a warning; an unrecognized
// non-empty one always does.
func parseWave(s string) (int, bool) {
	s = strings.ToUpper(strings.TrimSpace(s))
	if !strings.HasPrefix(s, "W") {
		return 1, false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(s, "W"))
	if err != nil || n < 1 || n > 8 {
		return 1, false
	}
	return n, true
}

// parseEnvelope parses a comma-separated envelope string of the form
// "attackRate, decay1Rate, sustainLevel, decay2Rate, releaseRate", e.g.
// "11, 10, 0.6, 4, 12". Any field that is missing or fails to parse is a
// minor error: it is reported as a warning and defaults to 0.
func parseEnvelope(s string) (fmcore.EnvelopeParams, []string) {
	var warnings []string
	fields := strings.Split(s, ",")
	get := func(i int) string {
		if i >= len(fields) {
			return ""
		}
		return strings.TrimSpace(fields[i])
	}

	parseUint8 := func(name string, i int) uint8 {
		v, err := strconv.ParseFloat(get(i), 64)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("envelope field %s (%q) invalid, defaulting to 0", name, get(i)))
			return 0
		}
		return uint8(v)
	}
	parseFloat := func(name string, i int) float64 {
		v, err := strconv.ParseFloat(get(i), 64)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("envelope field %s (%q) invalid, defaulting to 0", name, get(i)))
			return 0
		}
		return v
	}

	if len(fields) != 5 {
		warnings = append(warnings, fmt.Sprintf("envelope %q: expected 5 comma-separated fields, got %d", s, len(fields)))
	}

	return fmcore.EnvelopeParams{
		AttackRate:   parseUint8("attackRate", 0),
		Decay1Rate:   parseUint8("decay1Rate", 1),
		SustainLevel: parseFloat("sustainLevel", 2),
		Decay2Rate:   parseUint8("decay2Rate", 3),
		ReleaseRate:  parseUint8("releaseRate", 4),
	}, warnings
}
