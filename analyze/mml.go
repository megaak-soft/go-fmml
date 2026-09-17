/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package analyze

import (
	"fmt"
	"math"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/megaak-soft/go-fmml/common"
	"github.com/megaak-soft/go-fmml/fmcore"
	"github.com/megaak-soft/go-fmml/memory"
	"github.com/megaak-soft/go-fmml/pcmcore"
	"github.com/megaak-soft/go-fmml/reverb"
)

// mmlHeader is the required first non-blank/non-comment line of an .mml
// file, identifying it as belonging to this engine.
const mmlHeader = "[GoFMML File]"

var sectionHeaderRe = regexp.MustCompile(`^\[([A-Za-z0-9]+)\]$`)

// globalSection is CLAUDE.md's [global] YAML shape. Volume is 0-127 (the
// engine's overall master volume, see fmcore.Engine.SetMasterVolume),
// normalized into the [0,1] scale before being stored into
// memory.SequenceData.MasterVolume (see normalizeVolume127). Volume is a
// pointer so an omitted field (nil, defaults to full volume) can be told
// apart from an explicit "volume: 0" (silence) - both would otherwise
// unmarshal to the same float64 zero value.
type globalSection struct {
	Tempo      float64  `yaml:"tempo"`
	SequenceID string   `yaml:"sequenceID"`
	Loop       bool     `yaml:"loop"`
	Volume     *float64 `yaml:"volume"`

	// Reverb*/ReverbType/ReverbTime/ReverbLevel are Phase 4's [global]
	// reverb settings (see memory.SequenceData's identically-purposed
	// Reverb* fields). All four may be omitted (reverb stays off).
	Reverb      bool     `yaml:"reverb"`
	ReverbType  string   `yaml:"reverbType"`
	ReverbTime  float64  `yaml:"reverbTime"`
	ReverbLevel *float64 `yaml:"reverbLevel"`

	// StartOffset is the playback start offset, in whole notes (2 = 2
	// whole notes' worth of time skipped from every part before playback
	// begins). Omitted (zero value) means no offset - see resolveStartOffset.
	StartOffset float64 `yaml:"startOffset"`
}

// partSettingFile is CLAUDE.md's [partSetting] YAML shape: Volume is 0-127
// and Pan is -16(left)..16(right), both normalized into the [0,1]/[-1,1]
// scale fmcore.Engine.SetPart expects (see normalizeVolume127/
// normalizePan16) before being stored into memory.PartSequence. Volume is
// a pointer for the same "omitted vs. explicit 0" reason as
// globalSection's.
type partSettingFile struct {
	Part []struct {
		PartNo     int      `yaml:"partNo"`
		VoiceID    int      `yaml:"voiceID"`
		Volume     *float64 `yaml:"volume"`
		Pan        float64  `yaml:"pan"`
		ReverbSend *float64 `yaml:"reverbSend"`
		Transpose  int      `yaml:"transpose"`
		Mute       bool     `yaml:"mute"`
		// Solo is the MML addendum's debug-aid "solo" setting; see
		// memory.PartSequence.Solo's doc comment for its full semantics.
		Solo bool `yaml:"solo"`
	} `yaml:"part"`
}

// pcmPartSettingFile is CLAUDE.md's [pcmPartSetting] YAML shape: PartNo is
// a letter 'A'-'P' (as a one-character string). Volume is 0-127 and Pan is
// -16(left)..16(right), the same scale as partSettingFile, both normalized
// before being stored into memory.PCMPartSequence. Volume is a pointer for
// the same "omitted vs. explicit 0" reason as globalSection's. ReverbSend
// is untyped (interface{}) because its YAML shape depends on PartType (see
// CLAUDE.md's Phase 4 spec): a bare number (0-127) for a Long part, or a
// quoted "note: level, ..." string for a OneShot part - resolved by
// resolvePCMReverbSend after PartType is known.
type pcmPartSettingFile struct {
	PCMPart []struct {
		PartNo     string      `yaml:"partNo"`
		VoiceID    int         `yaml:"voiceID"`
		PartType   string      `yaml:"partType"`
		Volume     *float64    `yaml:"volume"`
		Pan        float64     `yaml:"pan"`
		ReverbSend interface{} `yaml:"reverbSend"`
		Transpose  int         `yaml:"transpose"`
		Mute       bool        `yaml:"mute"`
		// Solo mirrors partSettingFile's identical field; see
		// memory.PCMPartSequence.Solo's doc comment.
		Solo bool `yaml:"solo"`
	} `yaml:"pcmPart"`
}

// normalizeVolume127 converts a [partSetting]/[pcmPartSetting] volume
// (0-127) into the [0,1] scale fmcore.Engine.SetPart/pcmcore.Engine.SetPart
// expect.
func normalizeVolume127(v float64) float64 {
	return v / 127
}

// normalizePan16 converts a [partSetting]/[pcmPartSetting] pan (-16..16,
// also CLAUDE.md's MML 'P' command scale) into the [-1,1] scale
// fmcore.Engine.SetPart/pcmcore.Engine.SetPart expect.
func normalizePan16(v float64) float64 {
	return v / 16
}

// clampVolume127Float clamps a 0-127 scale value (reverbSend/reverbLevel,
// float64 because unlike Volume they're never split across an int/float64
// YAML type ambiguity) to that range before normalizeVolume127 converts it.
func clampVolume127Float(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 127 {
		return 127
	}
	return v
}

// resolveTranspose validates a [partSetting]/[pcmPartSetting] transpose
// value (semitones, CLAUDE.md's -36..36 range - 1 is a semitone, 12 an
// octave). Per CLAUDE.md, an invalid value is a minor error: ignored (no
// transpose) with a warning, rather than failing the whole parse.
func resolveTranspose(raw int) (transpose int, warning string) {
	if raw < -36 || raw > 36 {
		return 0, fmt.Sprintf("transpose %d is out of the -36..36 range, ignoring (no transpose)", raw)
	}
	return raw, ""
}

// resolveStartOffset converts a [global] startOffset value (whole notes,
// e.g. 2 = 2 whole notes) into ticks. Per CLAUDE.md, an invalid value
// (negative - zero/omitted already means "no offset") is a minor error:
// ignored (0, no offset) with a warning, rather than failing the parse.
func resolveStartOffset(raw float64) (offsetTicks int, warning string) {
	if raw < 0 {
		return 0, fmt.Sprintf("[global] startOffset %v is negative, ignoring (no offset)", raw)
	}
	if raw == 0 {
		return 0, ""
	}
	return int(math.Round(raw * 4 * common.TicksPerQuarterNote)), ""
}

// reverbSendNumber extracts a plain number from a pcmPartSettingFile
// ReverbSend value decoded into interface{} (see its doc comment): a bare
// YAML scalar unmarshals as int (no decimal point) or float64 (with one).
func reverbSendNumber(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case float64:
		return n, true
	default:
		return 0, false
	}
}

// resolvePCMReverbSend interprets a [pcmPartSetting] entry's reverbSend
// value per CLAUDE.md's Phase 4 spec, which depends on the part's own
// partType: a Long part's reverbSend is a single 0-127 number (normalized
// into reverbSend); a OneShot part's is a quoted "note: level, ..." string
// (parsed into oneShotReverbSend, keyed by MIDI note number). A value of
// the wrong shape for the part's type is a minor error: reported as a
// warning, the part gets no reverb send.
func resolvePCMReverbSend(partNo string, partType pcmcore.VoiceType, raw interface{}) (reverbSend float64, oneShotReverbSend map[uint8]float64, warnings []string) {
	if raw == nil {
		return 0, nil, nil
	}
	if partType == pcmcore.OneShot {
		s, ok := raw.(string)
		if !ok {
			return 0, nil, []string{fmt.Sprintf("[pcmPartSetting] partNo %s: reverbSend for a oneShot part must be a \"note: level, ...\" string, ignoring", partNo)}
		}
		m, warns := parseOneShotReverbSendMap(s)
		for i, w := range warns {
			warns[i] = fmt.Sprintf("[pcmPartSetting] partNo %s reverbSend: %s", partNo, w)
		}
		return 0, m, warns
	}

	n, ok := reverbSendNumber(raw)
	if !ok {
		return 0, nil, []string{fmt.Sprintf("[pcmPartSetting] partNo %s: reverbSend for a long part must be numeric, ignoring", partNo)}
	}
	return normalizeVolume127(clampVolume127Float(n)), nil, nil
}

// ParseMML parses the contents of an .mml sequence file (see CLAUDE.md's
// "MMLファイル仕様") into a ready-to-play memory.SequenceData. A missing or
// mismatched file header, or malformed [global]/[partSetting] YAML, is a
// fatal error since playback can't proceed without them; unrecognized MML
// commands and out-of-range values within a part's note data are minor
// errors, reported as warnings and skipped/defaulted so parsing continues.
func ParseMML(data []byte) (*memory.SequenceData, []string, error) {
	var warnings []string

	global, partSettings, partMML, pcmPartSettings, pcmPartMML, conductorMML, err := sectionize(string(data))
	if err != nil {
		return nil, warnings, err
	}

	// Phase 7's [conductor] part: tempo automation and the loop
	// jump-restart point. A tempo command at tick 0 overrides [global]'s
	// own tempo entirely ("先頭にテンポが記載している場合は[global]のテン
	// ポを無視"); anything else is a mid-song tempo change layered on top.
	conductorTempoMap, jumpTick, hasJump, conductorWarnings := parseConductorCommands(conductorMML)
	warnings = append(warnings, conductorWarnings...)
	if len(conductorTempoMap) > 0 && conductorTempoMap[0].AtTick == 0 {
		global.Tempo = conductorTempoMap[0].BPM
	}

	if global.Tempo <= 0 {
		warnings = append(warnings, fmt.Sprintf("[global] Tempo %v is invalid, defaulting to 120", global.Tempo))
		global.Tempo = 120
	}
	if len(conductorTempoMap) > 0 && conductorTempoMap[0].AtTick != 0 {
		// The conductor's first tempo change isn't at tick 0, so it's a
		// mid-song change layered on top of [global]'s own (possibly just-
		// defaulted) starting tempo - prepend that as the map's own tick-0
		// breakpoint so every tick from 0 on has a defined tempo.
		conductorTempoMap = append([]common.TempoPoint{{AtTick: 0, BPM: global.Tempo}}, conductorTempoMap...)
	}
	if global.SequenceID == "" {
		warnings = append(warnings, "[global] SequenceID is empty, defaulting to \"untitled\"")
		global.SequenceID = "untitled"
	}
	masterVolume := 127.0
	if global.Volume != nil {
		masterVolume = *global.Volume
	}

	reverbKind, kindOK := reverb.ParseKind(global.ReverbType)
	if !kindOK {
		warnings = append(warnings, fmt.Sprintf("[global] reverbType %q is unrecognized, defaulting to \"normal\"", global.ReverbType))
	}
	reverbType := "simple"
	if reverbKind == reverb.Normal {
		reverbType = "normal"
	}
	reverbEnabled := global.Reverb
	reverbTimeSeconds := global.ReverbTime
	if reverbEnabled && reverbTimeSeconds <= reverb.MinDecaySeconds {
		// Per CLAUDE.md: "0.5以下はバイパス" - a decay time at or below the
		// minimum meaningful value bypasses reverb entirely, regardless of
		// reverb:true.
		reverbEnabled = false
	}
	if reverbTimeSeconds > reverb.MaxDecaySeconds {
		warnings = append(warnings, fmt.Sprintf("[global] reverbTime %v exceeds the %vs maximum, clamping", reverbTimeSeconds, reverb.MaxDecaySeconds))
		reverbTimeSeconds = reverb.MaxDecaySeconds
	}
	reverbLevel := 0.0
	if global.ReverbLevel != nil {
		reverbLevel = normalizeVolume127(clampVolume127Float(*global.ReverbLevel))
	}

	startOffsetTicks, offsetWarn := resolveStartOffset(global.StartOffset)
	if offsetWarn != "" {
		warnings = append(warnings, offsetWarn)
	}

	seq := &memory.SequenceData{
		SequenceID:        global.SequenceID,
		Tempo:             global.Tempo,
		Loop:              global.Loop,
		MasterVolume:      normalizeVolume127(masterVolume),
		Parts:             make(map[fmcore.PartID]*memory.PartSequence),
		PCMParts:          make(map[pcmcore.PartID]*memory.PCMPartSequence),
		ReverbEnabled:     reverbEnabled,
		ReverbType:        reverbType,
		ReverbTimeSeconds: reverbTimeSeconds,
		ReverbLevel:       reverbLevel,
		StartOffsetTicks:  startOffsetTicks,
		TempoMap:          conductorTempoMap,
		JumpPointTick:     jumpTick,
		HasJumpPoint:      hasJump,
	}

	partTranspose := make(map[fmcore.PartID]int, len(partSettings.Part))
	for _, ps := range partSettings.Part {
		partID := fmcore.PartID(ps.PartNo)
		volume := 127.0
		if ps.Volume != nil {
			volume = *ps.Volume
		}
		reverbSend := 0.0
		if ps.ReverbSend != nil {
			reverbSend = normalizeVolume127(clampVolume127Float(*ps.ReverbSend))
		}
		transpose, transWarn := resolveTranspose(ps.Transpose)
		if transWarn != "" {
			warnings = append(warnings, fmt.Sprintf("[partSetting] partNo %d: %s", ps.PartNo, transWarn))
		}
		partTranspose[partID] = transpose
		seq.Parts[partID] = &memory.PartSequence{
			PartID:     partID,
			VoiceID:    fmcore.VoiceID(ps.VoiceID),
			Volume:     normalizeVolume127(volume),
			Pan:        normalizePan16(ps.Pan),
			ReverbSend: reverbSend,
			Mute:       ps.Mute,
			Solo:       ps.Solo,
		}
	}

	for partNo, mml := range partMML {
		partID := fmcore.PartID(partNo)
		ps, ok := seq.Parts[partID]
		if !ok {
			warnings = append(warnings, fmt.Sprintf("[part%d] has MML data but no [partSetting] entry, defaulting voice/volume/pan", partNo))
			ps = &memory.PartSequence{PartID: partID, VoiceID: 0, Volume: 1, Pan: 0}
			seq.Parts[partID] = ps
		}
		events, lengthTicks, cmdWarnings := parsePartCommands(mml, partTranspose[partID])
		for _, w := range cmdWarnings {
			warnings = append(warnings, fmt.Sprintf("[part%d]: %s", partNo, w))
		}
		ps.Events = events
		ps.LengthTicks = lengthTicks
	}

	pcmPartTranspose := make(map[pcmcore.PartID]int, len(pcmPartSettings.PCMPart))
	for _, pp := range pcmPartSettings.PCMPart {
		partID, err := pcmPartLetter("pcmPart" + pp.PartNo)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("[pcmPartSetting]: %s, ignoring entry", err))
			continue
		}
		partType, err := parsePCMPartType(pp.PartType)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("[pcmPartSetting] partNo %s: %s, defaulting to oneShot", pp.PartNo, err))
			partType = pcmcore.OneShot
		}
		volume := 127.0
		if pp.Volume != nil {
			volume = *pp.Volume
		}
		reverbSend, oneShotReverbSend, sendWarnings := resolvePCMReverbSend(pp.PartNo, partType, pp.ReverbSend)
		warnings = append(warnings, sendWarnings...)
		transpose, transWarn := resolveTranspose(pp.Transpose)
		if transWarn != "" {
			warnings = append(warnings, fmt.Sprintf("[pcmPartSetting] partNo %s: %s", pp.PartNo, transWarn))
		}
		pcmPartTranspose[pcmcore.PartID(partID)] = transpose
		seq.PCMParts[pcmcore.PartID(partID)] = &memory.PCMPartSequence{
			PartID:            pcmcore.PartID(partID),
			VoiceID:           pcmcore.VoiceID(pp.VoiceID),
			PartType:          partType,
			Volume:            normalizeVolume127(volume),
			Pan:               normalizePan16(pp.Pan),
			ReverbSend:        reverbSend,
			OneShotReverbSend: oneShotReverbSend,
			Mute:              pp.Mute,
			Solo:              pp.Solo,
		}
	}

	for letter, mml := range pcmPartMML {
		partID := pcmcore.PartID(letter)
		pps, ok := seq.PCMParts[partID]
		if !ok {
			warnings = append(warnings, fmt.Sprintf("[pcmPart%c] has MML data but no [pcmPartSetting] entry, defaulting voice/type/volume/pan", letter))
			pps = &memory.PCMPartSequence{PartID: partID, VoiceID: 0, PartType: pcmcore.OneShot, Volume: 1, Pan: 0}
			seq.PCMParts[partID] = pps
		}

		var events []memory.PCMSeqEvent
		var lengthTicks int
		var cmdWarnings []string
		switch pps.PartType {
		case pcmcore.Long:
			// A OneShot part's notes are sample triggers, not pitches (see
			// CLAUDE.md's oneShot grammar), so transpose - a pitch shift -
			// only makes sense, and is only applied, for a Long part.
			events, lengthTicks, cmdWarnings = parsePCMLongPartCommands(mml, pcmPartTranspose[partID])
		default:
			events, lengthTicks, cmdWarnings = parsePCMOneShotPartCommands(mml)
		}
		for _, w := range cmdWarnings {
			warnings = append(warnings, fmt.Sprintf("[pcmPart%c]: %s", letter, w))
		}
		pps.Events = events
		pps.LengthTicks = lengthTicks
	}

	return seq, warnings, nil
}

// parsePCMPartType parses a [pcmPartSetting] partType value ("oneShot" or
// "long"), case-insensitively.
func parsePCMPartType(s string) (pcmcore.VoiceType, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "oneshot":
		return pcmcore.OneShot, nil
	case "long":
		return pcmcore.Long, nil
	default:
		return 0, fmt.Errorf("unknown partType %q", s)
	}
}

// sectionize splits an .mml file's text into its [global]/[partSetting]/
// [partN]..[partNEnd] sections, per CLAUDE.md's MML file spec: comment lines
// (# ...) and blank/space-only lines are dropped; within a [partN] block all
// remaining whitespace is stripped and lines are concatenated into one
// continuous MML command string.
func sectionize(text string) (globalSection, partSettingFile, map[int]string, pcmPartSettingFile, map[byte]string, string, error) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")

	fail := func(err error) (globalSection, partSettingFile, map[int]string, pcmPartSettingFile, map[byte]string, string, error) {
		return globalSection{}, partSettingFile{}, nil, pcmPartSettingFile{}, nil, "", err
	}

	headerSeen := false
	section := ""
	var globalBuf, partSettingBuf, pcmPartSettingBuf, conductorBuf strings.Builder
	partBuf := make(map[int]*strings.Builder)
	openPart := -1
	pcmPartBuf := make(map[byte]*strings.Builder)
	openPCMPart := byte(0)

	for _, raw := range lines {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			continue
		}

		if !headerSeen {
			if trimmed != mmlHeader {
				return fail(fmt.Errorf("analyze: missing or invalid MML header, expected %q, got %q", mmlHeader, trimmed))
			}
			headerSeen = true
			continue
		}

		if m := sectionHeaderRe.FindStringSubmatch(trimmed); m != nil {
			name := m[1]
			switch {
			case name == "global":
				section = "global"
			case name == "partSetting":
				section = "partSetting"
			case strings.HasSuffix(name, "End") && strings.HasPrefix(name, "part"):
				section = ""
				openPart = -1
			case strings.HasPrefix(name, "part"):
				n, err := partNumber(name)
				if err != nil {
					return fail(fmt.Errorf("analyze: invalid part section header %q: %w", trimmed, err))
				}
				section = "part"
				openPart = n
				if _, ok := partBuf[n]; !ok {
					partBuf[n] = &strings.Builder{}
				}
			case name == "conductor":
				// A [conductor] header is only a grouping marker above
				// [conductorPart]/[conductorPartEnd] (see CLAUDE.md's Phase
				// 7 spec example) - it carries no content of its own.
				section = ""
			case name == "conductorPart":
				section = "conductorPart"
			case name == "conductorPartEnd":
				section = ""
			case name == "pcmPartSetting":
				section = "pcmPartSetting"
			case strings.HasSuffix(name, "End") && strings.HasPrefix(name, "pcmPart"):
				section = ""
				openPCMPart = 0
			case strings.HasPrefix(name, "pcmPart"):
				letter, err := pcmPartLetter(name)
				if err != nil {
					return fail(fmt.Errorf("analyze: invalid PCM part section header %q: %w", trimmed, err))
				}
				section = "pcmPart"
				openPCMPart = letter
				if _, ok := pcmPartBuf[letter]; !ok {
					pcmPartBuf[letter] = &strings.Builder{}
				}
			default:
				section = ""
			}
			continue
		}

		switch section {
		case "global":
			globalBuf.WriteString(raw)
			globalBuf.WriteByte('\n')
		case "partSetting":
			partSettingBuf.WriteString(raw)
			partSettingBuf.WriteByte('\n')
		case "part":
			partBuf[openPart].WriteString(stripWhitespace(raw))
		case "pcmPartSetting":
			pcmPartSettingBuf.WriteString(raw)
			pcmPartSettingBuf.WriteByte('\n')
		case "pcmPart":
			pcmPartBuf[openPCMPart].WriteString(stripWhitespace(raw))
		case "conductorPart":
			conductorBuf.WriteString(stripWhitespace(raw))
		}
	}
	if !headerSeen {
		return fail(fmt.Errorf("analyze: missing MML header, expected %q", mmlHeader))
	}

	var global globalSection
	if globalBuf.Len() > 0 {
		if err := yaml.Unmarshal([]byte(globalBuf.String()), &global); err != nil {
			return fail(fmt.Errorf("analyze: invalid [global] section: %w", err))
		}
	}

	var partSettings partSettingFile
	if partSettingBuf.Len() > 0 {
		if err := yaml.Unmarshal([]byte(partSettingBuf.String()), &partSettings); err != nil {
			return fail(fmt.Errorf("analyze: invalid [partSetting] section: %w", err))
		}
	}

	var pcmPartSettings pcmPartSettingFile
	if pcmPartSettingBuf.Len() > 0 {
		if err := yaml.Unmarshal([]byte(pcmPartSettingBuf.String()), &pcmPartSettings); err != nil {
			return fail(fmt.Errorf("analyze: invalid [pcmPartSetting] section: %w", err))
		}
	}

	partMML := make(map[int]string, len(partBuf))
	for n, b := range partBuf {
		partMML[n] = b.String()
	}
	pcmPartMML := make(map[byte]string, len(pcmPartBuf))
	for letter, b := range pcmPartBuf {
		pcmPartMML[letter] = b.String()
	}
	return global, partSettings, partMML, pcmPartSettings, pcmPartMML, conductorBuf.String(), nil
}

// pcmPartLetter extracts and validates the single 'A'-'P' part letter from
// a PCM part section header name (e.g. "pcmPartA" -> 'A').
func pcmPartLetter(sectionName string) (byte, error) {
	letter := strings.TrimPrefix(sectionName, "pcmPart")
	if len(letter) != 1 {
		return 0, fmt.Errorf("no single-letter PCM part id in %q", sectionName)
	}
	c := letter[0]
	if c < 'A' || c > 'P' {
		return 0, fmt.Errorf("PCM part id %q out of range A-P", letter)
	}
	return c, nil
}

func partNumber(sectionName string) (int, error) {
	digits := strings.TrimPrefix(sectionName, "part")
	var n int
	if _, err := fmt.Sscanf(digits, "%d", &n); err != nil {
		return 0, fmt.Errorf("no part number in %q", sectionName)
	}
	if n < 1 || n > 16 {
		return 0, fmt.Errorf("part number %d out of range 1-16", n)
	}
	return n, nil
}

func stripWhitespace(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == ' ' || r == '\t' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
