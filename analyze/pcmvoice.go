/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package analyze

import (
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/megaak-soft/go-fmml/pcmcore"
)

// PCMVoiceDefinition pairs a parsed PCM voice with the ID it should be
// registered under, plus the WAV file names its voiceSettings reference
// (not yet loaded - see CLAUDE.md's spec, go-fmml/fileio resolves and
// loads those WAV files after parsing, then fills in each VoiceSetting's
// Wave field before storing the voice).
type PCMVoiceDefinition struct {
	VoiceID pcmcore.VoiceID
	Voice   pcmcore.Voice
}

type pcmVoiceFile struct {
	PCMVoice []struct {
		VoiceID   int    `yaml:"voiceID"`
		VoiceType string `yaml:"voiceType"`
		Loop      bool   `yaml:"loop"`

		// LFO/LFODelay/LFOFade/LFODepth/LFOHz are Phase 8's optional
		// vibrato settings (see CLAUDE.md), meaningful only for a "long"
		// voiceType - a "oneShot" voice ignores them entirely. The
		// pointer fields distinguish "omitted" (use resolveLFOValues's
		// default) from an explicit value.
		LFO      bool     `yaml:"lfo"`
		LFODelay *float64 `yaml:"lfoDelay"`
		LFOFade  *float64 `yaml:"lfoFade"`
		LFODepth *float64 `yaml:"lfoDepth"`
		LFOHz    *float64 `yaml:"lfoHz"`

		VoiceSetting []struct {
			FileName string `yaml:"fileName"`
			Note     string `yaml:"note"`
			BaseNote string `yaml:"baseNote"`
			Volume   int    `yaml:"volume"`
			Pan      int    `yaml:"pan"`
			Envelope string `yaml:"envelope"`
		} `yaml:"voiceSetting"`
	} `yaml:"pcmVoice"`
}

const maxOneShotSettings = 16

// ParsePCMVoiceFile parses the YAML contents of a PCM voice parameter file
// (see CLAUDE.md's "PCM音色設定ファイル仕様") into voice definitions. A
// malformed envelope field or an out-of-range value is a minor error: it is
// reported as a warning and the affected value defaults to 0, parsing
// continues. A file that isn't valid YAML, or has an unrecognized
// voiceType, is a fatal error.
func ParsePCMVoiceFile(data []byte) ([]PCMVoiceDefinition, []string, error) {
	var file pcmVoiceFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, nil, fmt.Errorf("analyze: invalid PCM voice YAML: %w", err)
	}

	var warnings []string
	if len(file.PCMVoice) == 0 {
		warnings = append(warnings, "pcmVoice: no voices defined")
	}

	defs := make([]PCMVoiceDefinition, 0, len(file.PCMVoice))
	for _, pv := range file.PCMVoice {
		var voiceType pcmcore.VoiceType
		switch strings.ToLower(strings.TrimSpace(pv.VoiceType)) {
		case "oneshot":
			voiceType = pcmcore.OneShot
		case "long":
			voiceType = pcmcore.Long
		default:
			return nil, warnings, fmt.Errorf("analyze: voiceID %d: unknown voiceType %q, expected \"oneShot\" or \"long\"", pv.VoiceID, pv.VoiceType)
		}

		settings := pv.VoiceSetting
		if voiceType == pcmcore.OneShot && len(settings) > maxOneShotSettings {
			warnings = append(warnings, fmt.Sprintf("voiceID %d: %d voiceSettings given, only the first %d are used", pv.VoiceID, len(settings), maxOneShotSettings))
			settings = settings[:maxOneShotSettings]
		}
		if voiceType == pcmcore.Long && len(settings) > 1 {
			warnings = append(warnings, fmt.Sprintf("voiceID %d: long voices allow only 1 voiceSetting, using the first of %d given", pv.VoiceID, len(settings)))
			settings = settings[:1]
		}

		voice := pcmcore.Voice{VoiceType: voiceType, Loop: pv.Loop}
		if voiceType == pcmcore.Long {
			// Phase 8's vibrato LFO only applies to a "long" voice
			// (CLAUDE.md: "oneShotの場合は無視") - a oneShot voice's LFO
			// stays at its zero value (Enabled false) regardless of what
			// its YAML says.
			delaySeconds, fadeSeconds, depthCents, rateHz := resolveLFOValues(pv.LFODelay, pv.LFOFade, pv.LFODepth, pv.LFOHz)
			voice.LFO = pcmcore.LFOParams{
				Enabled:      pv.LFO,
				DelaySeconds: delaySeconds,
				FadeSeconds:  fadeSeconds,
				DepthCents:   depthCents,
				RateHz:       rateHz,
			}
		}
		for i, vs := range settings {
			setting := pcmcore.VoiceSetting{
				FileName: strings.TrimSpace(vs.FileName),
			}

			if voiceType == pcmcore.OneShot {
				setting.Volume = clampVolume127(vs.Volume)
				setting.Pan = clampPan16(vs.Pan)

				note, err := parseNoteName(vs.Note)
				if err != nil {
					warnings = append(warnings, fmt.Sprintf("voiceID %d voiceSetting %d: %s, ignoring this sample", pv.VoiceID, i, err))
					continue
				}
				setting.Note = note
				setting.HasNote = true
			} else {
				// Long: note/volume/pan are ignored even if present in the
				// YAML - playback volume/pan instead comes from the
				// pcmPart the voice is assigned to (see
				// pcmcore.Engine.noteOnLongRow), matching CLAUDE.md's spec.
				base, err := parseNoteName(vs.BaseNote)
				if err != nil {
					warnings = append(warnings, fmt.Sprintf("voiceID %d voiceSetting %d: baseNote %s, defaulting to A4", pv.VoiceID, i, err))
					base = 69
				}
				setting.BaseNote = base
				env, envWarnings := parseEnvelope(vs.Envelope)
				for _, w := range envWarnings {
					warnings = append(warnings, fmt.Sprintf("voiceID %d voiceSetting %d: %s", pv.VoiceID, i, w))
				}
				setting.Envelope = pcmcore.EnvelopeParams{
					AttackRate:   env.AttackRate,
					Decay1Rate:   env.Decay1Rate,
					SustainLevel: env.SustainLevel,
					Decay2Rate:   env.Decay2Rate,
					ReleaseRate:  env.ReleaseRate,
				}
			}

			if setting.FileName == "" {
				warnings = append(warnings, fmt.Sprintf("voiceID %d voiceSetting %d: missing fileName, ignoring this sample", pv.VoiceID, i))
				continue
			}

			voice.Settings = append(voice.Settings, setting)
		}

		defs = append(defs, PCMVoiceDefinition{VoiceID: pcmcore.VoiceID(pv.VoiceID), Voice: voice})
	}
	return defs, warnings, nil
}

// parseNoteName converts a scientific-pitch-notation note name such as "C1"
// or "F#1" into a MIDI note number (C4 = 60), reusing the same letter/
// octave grammar go-fmml/fmcore.Engine.NoteOn accepts.
func parseNoteName(name string) (uint8, error) {
	name = strings.TrimSpace(name)
	if len(name) < 2 {
		return 0, fmt.Errorf("invalid note name %q", name)
	}
	semitone, ok := noteLetterSemitone[strings.ToUpper(name)[0]]
	if !ok {
		return 0, fmt.Errorf("invalid note name %q", name)
	}

	rest := name[1:]
	if strings.HasPrefix(rest, "#") || strings.HasPrefix(rest, "+") {
		semitone++
		rest = rest[1:]
	} else if strings.HasPrefix(rest, "-") {
		semitone--
		rest = rest[1:]
	}

	octave, err := strconv.Atoi(rest)
	if err != nil {
		return 0, fmt.Errorf("invalid note name %q", name)
	}

	midi := (octave+1)*12 + semitone
	if midi < 0 || midi > 127 {
		return 0, fmt.Errorf("note name %q is out of MIDI range", name)
	}
	return uint8(midi), nil
}

func clampVolume127(v int) int {
	if v < 0 {
		return 0
	}
	if v > 127 {
		return 127
	}
	return v
}

func clampPan16(v int) int {
	if v < -16 {
		return -16
	}
	if v > 16 {
		return 16
	}
	return v
}
