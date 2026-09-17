/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package analyze

import (
	"testing"

	"github.com/megaak-soft/go-fmml/pcmcore"
)

const testPCMVoiceYAML = `
pcmVoice:
  - voiceID: 0
    voiceType: oneShot
    loop: false
    voiceSetting:
      - fileName: kick.wav
        note: C1
        baseNote:
        volume: 100
        pan: 0
        envelope:
      - fileName: snare.wav
        note: E1
        baseNote:
        volume: 80
        pan: -16
  - voiceID: 1
    voiceType: long
    loop: true
    voiceSetting:
      - fileName: uhh.wav
        note:
        baseNote: A3
        volume: 65
        pan: 0
        envelope: "28, 14, 0.5, 3, 14"
`

func TestParsePCMVoiceFileOneShot(t *testing.T) {
	defs, warnings, err := ParsePCMVoiceFile([]byte(testPCMVoiceYAML))
	if err != nil {
		t.Fatalf("ParsePCMVoiceFile failed: %v (warnings: %v)", err, warnings)
	}
	if len(defs) != 2 {
		t.Fatalf("expected 2 voice definitions, got %d", len(defs))
	}

	kick := defs[0]
	if kick.VoiceID != 0 {
		t.Fatalf("expected voiceID 0, got %d", kick.VoiceID)
	}
	if kick.Voice.VoiceType != pcmcore.OneShot {
		t.Fatalf("expected OneShot voiceType")
	}
	if len(kick.Voice.Settings) != 2 {
		t.Fatalf("expected 2 voiceSettings, got %d", len(kick.Voice.Settings))
	}
	if kick.Voice.Settings[0].FileName != "kick.wav" || !kick.Voice.Settings[0].HasNote || kick.Voice.Settings[0].Note != 24 {
		t.Fatalf("unexpected kick setting: %+v", kick.Voice.Settings[0])
	}
	if kick.Voice.Settings[1].FileName != "snare.wav" || kick.Voice.Settings[1].Pan != -16 {
		t.Fatalf("unexpected snare setting: %+v", kick.Voice.Settings[1])
	}
}

func TestParsePCMVoiceFileLong(t *testing.T) {
	defs, _, err := ParsePCMVoiceFile([]byte(testPCMVoiceYAML))
	if err != nil {
		t.Fatalf("ParsePCMVoiceFile failed: %v", err)
	}
	long := defs[1]
	if long.Voice.VoiceType != pcmcore.Long {
		t.Fatalf("expected Long voiceType")
	}
	if !long.Voice.Loop {
		t.Fatalf("expected loop: true to be parsed")
	}
	if len(long.Voice.Settings) != 1 {
		t.Fatalf("expected 1 voiceSetting for a long voice, got %d", len(long.Voice.Settings))
	}
	setting := long.Voice.Settings[0]
	if setting.BaseNote != 57 { // A3
		t.Fatalf("expected baseNote A3 (57), got %d", setting.BaseNote)
	}
	if setting.Envelope.AttackRate != 28 || setting.Envelope.SustainLevel != 0.5 {
		t.Fatalf("unexpected envelope: %+v", setting.Envelope)
	}
}

func TestParsePCMVoiceFileLongIgnoresNoteVolumePanEvenWhenPresent(t *testing.T) {
	const yaml = `
pcmVoice:
  - voiceID: 1
    voiceType: long
    loop: true
    voiceSetting:
      - fileName: uhh.wav
        note: C4
        baseNote: A3
        volume: 90
        pan: 12
        envelope: "28, 14, 0.5, 3, 14"
`
	defs, warnings, err := ParsePCMVoiceFile([]byte(yaml))
	if err != nil {
		t.Fatalf("ParsePCMVoiceFile failed: %v (warnings: %v)", err, warnings)
	}
	setting := defs[0].Voice.Settings[0]
	if setting.HasNote {
		t.Fatalf("expected note to be ignored for a long voice, got HasNote=true Note=%d", setting.Note)
	}
	if setting.Volume != 0 {
		t.Fatalf("expected volume to be ignored for a long voice, got %d", setting.Volume)
	}
	if setting.Pan != 0 {
		t.Fatalf("expected pan to be ignored for a long voice, got %d", setting.Pan)
	}
	if setting.BaseNote != 57 { // A3
		t.Fatalf("expected baseNote A3 (57) to still be parsed, got %d", setting.BaseNote)
	}
}

func TestParsePCMVoiceFileLongMissingNoteVolumePanIsNotAnError(t *testing.T) {
	const yaml = `
pcmVoice:
  - voiceID: 1
    voiceType: long
    loop: false
    voiceSetting:
      - fileName: uhh.wav
        baseNote: A3
        envelope: "28, 14, 0.5, 3, 14"
`
	defs, warnings, err := ParsePCMVoiceFile([]byte(yaml))
	if err != nil {
		t.Fatalf("expected omitted note/volume/pan to be valid for a long voice, got: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	if len(defs[0].Voice.Settings) != 1 {
		t.Fatalf("expected 1 voiceSetting, got %d", len(defs[0].Voice.Settings))
	}
}

func TestParsePCMVoiceFileUnknownVoiceTypeFails(t *testing.T) {
	const yaml = `
pcmVoice:
  - voiceID: 0
    voiceType: bogus
    loop: false
    voiceSetting: []
`
	if _, _, err := ParsePCMVoiceFile([]byte(yaml)); err == nil {
		t.Fatalf("expected an error for an unknown voiceType")
	}
}

func TestParsePCMVoiceFileTooManyOneShotSettingsWarns(t *testing.T) {
	const yaml = `
pcmVoice:
  - voiceID: 0
    voiceType: oneShot
    loop: false
    voiceSetting:
      - {fileName: a.wav, note: C1, volume: 100, pan: 0}
      - {fileName: b.wav, note: C#1, volume: 100, pan: 0}
      - {fileName: c.wav, note: D1, volume: 100, pan: 0}
      - {fileName: d.wav, note: D#1, volume: 100, pan: 0}
      - {fileName: e.wav, note: E1, volume: 100, pan: 0}
      - {fileName: f.wav, note: F1, volume: 100, pan: 0}
      - {fileName: g.wav, note: F#1, volume: 100, pan: 0}
      - {fileName: h.wav, note: G1, volume: 100, pan: 0}
      - {fileName: i.wav, note: G#1, volume: 100, pan: 0}
      - {fileName: j.wav, note: A1, volume: 100, pan: 0}
      - {fileName: k.wav, note: A#1, volume: 100, pan: 0}
      - {fileName: l.wav, note: B1, volume: 100, pan: 0}
      - {fileName: m.wav, note: C2, volume: 100, pan: 0}
      - {fileName: n.wav, note: C#2, volume: 100, pan: 0}
      - {fileName: o.wav, note: D2, volume: 100, pan: 0}
      - {fileName: p.wav, note: D#2, volume: 100, pan: 0}
      - {fileName: q.wav, note: E2, volume: 100, pan: 0}
`
	defs, warnings, err := ParsePCMVoiceFile([]byte(yaml))
	if err != nil {
		t.Fatalf("ParsePCMVoiceFile failed: %v", err)
	}
	if len(defs[0].Voice.Settings) != 16 {
		t.Fatalf("expected at most 16 oneShot settings, got %d", len(defs[0].Voice.Settings))
	}
	if len(warnings) == 0 {
		t.Fatalf("expected a warning about the 17th setting being dropped")
	}
}

// TestParsePCMVoiceFileLFOAppliesOnlyToLongVoiceType confirms CLAUDE.md's
// "PCM音色でLFOを掛けられるのはvoiceTypeがlongタイプのみとする(oneShotの
// 場合は無視)": a long voice's LFO settings (including defaults for
// omitted sub-parameters) are applied, while a oneShot voice's are ignored
// even if lfo:true is set on it.
func TestParsePCMVoiceFileLFOAppliesOnlyToLongVoiceType(t *testing.T) {
	const yaml = `
pcmVoice:
  - voiceID: 1
    voiceType: long
    loop: true
    lfo: true
    lfoDelay: 0.3
    lfoFade: 0.4
    lfoHz: 7
    voiceSetting:
      - fileName: uhh.wav
        baseNote: A3
        volume: 65
        pan: 16
        envelope: "31, 1, 2, 6, 14"
  - voiceID: 2
    voiceType: oneShot
    loop: false
    lfo: true
    voiceSetting:
      - fileName: kick.wav
        note: C1
`
	defs, warnings, err := ParsePCMVoiceFile([]byte(yaml))
	if err != nil {
		t.Fatalf("ParsePCMVoiceFile failed: %v (warnings: %v)", err, warnings)
	}

	longLFO := defs[0].Voice.LFO
	if !longLFO.Enabled {
		t.Fatalf("expected the long voice's LFO to be enabled")
	}
	if longLFO.DelaySeconds != 0.3 || longLFO.FadeSeconds != 0.4 || longLFO.RateHz != 7 {
		t.Fatalf("expected the long voice's explicit LFO params (0.3, 0.4, _, 7), got %+v", longLFO)
	}
	if longLFO.DepthCents != 30 {
		t.Fatalf("expected lfoDepth to default to 30 when omitted, got %v", longLFO.DepthCents)
	}

	oneShotLFO := defs[1].Voice.LFO
	if oneShotLFO.Enabled {
		t.Fatalf("expected a oneShot voice's LFO to be ignored (Enabled false) even with lfo:true set, got %+v", oneShotLFO)
	}
}

func TestParseNoteNameSharpAndFlat(t *testing.T) {
	sharp, err := parseNoteName("F#1")
	if err != nil || sharp != 30 {
		t.Fatalf("expected F#1 -> 30, got %d, err=%v", sharp, err)
	}
	flat, err := parseNoteName("D-1")
	if err != nil || flat != 25 {
		t.Fatalf("expected D-1 -> 25, got %d, err=%v", flat, err)
	}
}
