/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package analyze

import (
	"strings"
	"testing"
)

const sampleFMVoiceYAML = `
fmVoice:
  - voiceID: 0
    algorithm: 2
    feedback: 20
    operators:
      - multiple: 1
        totallevel: 127
        detuneCents: 0
        envelope: "11, 10, 0.6, 4, 12"
      - multiple: 2
        totallevel: 40
        detuneCents: 0
        envelope: "31, 12, 0.3, 4, 12"
      - multiple: 1
        totallevel: 30
        detuneCents: 0
        envelope: "31, 10, 0.5, 4, 12"
      - multiple: 1
        totallevel: 0
        detuneCents: 0
        envelope: "31, 8, 0.7, 2, 10"
`

// TestParseFMVoiceFileLFODefaults confirms Phase 8's "各lfoパラメータが未
// 設定の場合はデフォルト値を使用する": lfo:true with every sub-parameter
// omitted resolves to the documented defaults (0.4s delay, 0.5s fade, 30
// cents depth, 6Hz rate).
func TestParseFMVoiceFileLFODefaults(t *testing.T) {
	const withLFO = `
fmVoice:
  - voiceID: 0
    algorithm: 2
    feedback: 20
    lfo: true
    operators:
      - multiple: 1
        totallevel: 127
        detuneCents: 0
        envelope: "11, 10, 0.6, 4, 12"
`
	defs, warnings, err := ParseFMVoiceFile([]byte(withLFO))
	if err != nil {
		t.Fatalf("ParseFMVoiceFile failed: %v (warnings: %v)", err, warnings)
	}
	lfo := defs[0].Voice.LFO
	if !lfo.Enabled {
		t.Fatalf("expected LFO to be enabled")
	}
	if lfo.DelaySeconds != 0.4 || lfo.FadeSeconds != 0.5 || lfo.DepthCents != 30 || lfo.RateHz != 6 {
		t.Fatalf("expected default LFO params (0.4, 0.5, 30, 6), got %+v", lfo)
	}
}

// TestParseFMVoiceFileLFOExplicitValues confirms explicit lfoDelay/lfoFade/
// lfoDepth/lfoHz values override the defaults.
func TestParseFMVoiceFileLFOExplicitValues(t *testing.T) {
	const withLFO = `
fmVoice:
  - voiceID: 0
    algorithm: 4
    feedback: 20
    lfo: true
    lfoDelay: 0.3
    lfoFade: 0.4
    lfoDepth: 50
    lfoHz: 7
    operators:
      - multiple: 1
        totallevel: 127
        detuneCents: 0
        envelope: "11, 10, 0.6, 4, 12"
`
	defs, warnings, err := ParseFMVoiceFile([]byte(withLFO))
	if err != nil {
		t.Fatalf("ParseFMVoiceFile failed: %v (warnings: %v)", err, warnings)
	}
	lfo := defs[0].Voice.LFO
	if !lfo.Enabled || lfo.DelaySeconds != 0.3 || lfo.FadeSeconds != 0.4 || lfo.DepthCents != 50 || lfo.RateHz != 7 {
		t.Fatalf("expected explicit LFO params (0.3, 0.4, 50, 7), got %+v", lfo)
	}
}

// TestParseFMVoiceFileLFOOmittedIsDisabled confirms a voice with no "lfo"
// field at all leaves LFO disabled (Enabled false), matching CLAUDE.md's
// "LFOパラメータは無記載でも可".
func TestParseFMVoiceFileLFOOmittedIsDisabled(t *testing.T) {
	defs, _, err := ParseFMVoiceFile([]byte(sampleFMVoiceYAML))
	if err != nil {
		t.Fatalf("ParseFMVoiceFile failed: %v", err)
	}
	if defs[0].Voice.LFO.Enabled {
		t.Fatalf("expected LFO to be disabled when omitted from the YAML")
	}
}

func TestParseFMVoiceFile(t *testing.T) {
	defs, warnings, err := ParseFMVoiceFile([]byte(sampleFMVoiceYAML))
	if err != nil {
		t.Fatalf("ParseFMVoiceFile failed: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	if len(defs) != 1 {
		t.Fatalf("expected 1 voice, got %d", len(defs))
	}
	v := defs[0]
	if v.VoiceID != 0 {
		t.Fatalf("expected voiceID 0, got %d", v.VoiceID)
	}
	if v.Voice.Algorithm != 2 || v.Voice.Feedback != 20 {
		t.Fatalf("unexpected algorithm/feedback: %+v", v.Voice)
	}
	op0 := v.Voice.Operators[0]
	if op0.Multiple != 1 || op0.TotalLevel != 127 {
		t.Fatalf("unexpected operator 0: %+v", op0)
	}
	if op0.Envelope.AttackRate != 11 || op0.Envelope.Decay1Rate != 10 || op0.Envelope.SustainLevel != 0.6 ||
		op0.Envelope.Decay2Rate != 4 || op0.Envelope.ReleaseRate != 12 {
		t.Fatalf("unexpected envelope: %+v", op0.Envelope)
	}
}

func TestParseFMVoiceFileMinorErrorsDefaultToZero(t *testing.T) {
	yaml := `
fmVoice:
  - voiceID: 1
    algorithm: 0
    feedback: 0
    operators:
      - multiple: 1
        totallevel: 10
        envelope: "not, a, valid, envelope"
`
	defs, warnings, err := ParseFMVoiceFile([]byte(yaml))
	if err != nil {
		t.Fatalf("expected minor envelope errors to be non-fatal, got: %v", err)
	}
	if len(warnings) == 0 {
		t.Fatalf("expected warnings for the invalid envelope")
	}
	if len(defs) != 1 {
		t.Fatalf("expected 1 voice despite the bad envelope, got %d", len(defs))
	}
	env := defs[0].Voice.Operators[0].Envelope
	if env.AttackRate != 0 || env.Decay1Rate != 0 || env.SustainLevel != 0 {
		t.Fatalf("expected invalid envelope fields to default to 0, got %+v", env)
	}
}

func TestParseFMVoiceFileInvalidYAMLIsFatal(t *testing.T) {
	if _, _, err := ParseFMVoiceFile([]byte("fmVoice: [this is not: valid: yaml")); err == nil {
		t.Fatalf("expected an error for malformed YAML")
	}
}

func TestParseFMVoiceFileWaveField(t *testing.T) {
	yaml := `
fmVoice:
  - voiceID: 0
    algorithm: 0
    feedback: 0
    operators:
      - multiple: 1
        totallevel: 0
        wave: W2
      - multiple: 1
        totallevel: 0
      - multiple: 1
        totallevel: 0
        wave: bogus
      - multiple: 1
        totallevel: 0
`
	defs, warnings, err := ParseFMVoiceFile([]byte(yaml))
	if err != nil {
		t.Fatalf("ParseFMVoiceFile failed: %v", err)
	}
	if len(defs) != 1 {
		t.Fatalf("expected 1 voice, got %d", len(defs))
	}
	ops := defs[0].Voice.Operators
	if ops[0].Wave != 2 {
		t.Fatalf("expected operator 0's wave W2 to parse as 2, got %d", ops[0].Wave)
	}
	if ops[1].Wave != 1 {
		t.Fatalf("expected an omitted wave field to default to 1 (W1), got %d", ops[1].Wave)
	}
	if ops[2].Wave != 1 {
		t.Fatalf("expected an unrecognized wave value to default to 1 (W1), got %d", ops[2].Wave)
	}
	foundWarning := false
	for _, w := range warnings {
		if strings.Contains(w, "operator 2") {
			foundWarning = true
		}
	}
	if !foundWarning {
		t.Fatalf("expected a warning about operator 2's unrecognized wave, got %v", warnings)
	}
}
