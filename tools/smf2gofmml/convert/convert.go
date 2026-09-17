/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

// Package convert turns a parsed Standard MIDI File (midiparse.SMFResult)
// into a complete go-fmml .mml file's text, per CLAUDE.md's Phase 6 spec.
package convert

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/megaak-soft/go-fmml/common"
	"github.com/megaak-soft/go-fmml/tools/smf2gofmml/midiparse"
	"github.com/megaak-soft/go-fmml/tools/smf2gofmml/mmlgen"
)

// trackKind is which kind of engine part an SMF track's note data converts
// to, decided from its track-name prefix (see classifyTrackKind).
type trackKind int

const (
	kindFM trackKind = iota
	kindPCMLong
	kindPCMOneShot
)

// classifyTrackKind reads a track name's leading "[...]" tag (if any) and
// maps it to a part kind. Whitespace anywhere inside the brackets (e.g. a
// stray "[PCM oneShot ]" from hand-edited track names) is ignored: every
// space/tab inside is stripped before comparing, so "[PCM oneShot]",
// "[PCM oneShot ]", and "[ PCM  oneShot ]" all match the same way.
func classifyTrackKind(name string) trackKind {
	trimmed := strings.TrimSpace(name)
	if strings.HasPrefix(trimmed, "[") {
		if end := strings.Index(trimmed, "]"); end >= 0 {
			inner := strings.ToUpper(strings.Join(strings.Fields(trimmed[1:end]), ""))
			switch inner {
			case "PCMONESHOT":
				return kindPCMOneShot
			case "PCMLONG":
				return kindPCMLong
			case "FM":
				return kindFM
			}
		}
	}
	return kindFM
}

// stripTrackTag removes a track name's leading "[FM]"/"[PCM long]"/
// "[PCM oneShot]" classification tag (see classifyTrackKind), returning
// just the descriptive remainder for use as a comment. A name with no
// recognized tag is returned trimmed but otherwise unchanged.
func stripTrackTag(name string) string {
	trimmed := strings.TrimSpace(name)
	if strings.HasPrefix(trimmed, "[") {
		if end := strings.Index(trimmed, "]"); end >= 0 {
			inner := strings.ToUpper(strings.Join(strings.Fields(trimmed[1:end]), ""))
			switch inner {
			case "PCMONESHOT", "PCMLONG", "FM":
				return strings.TrimSpace(trimmed[end+1:])
			}
		}
	}
	return trimmed
}

// sanitizeComment collapses a track name onto a single line, so it can
// never be split across the "# ..." comment line CLAUDE.md's MML grammar
// expects it on.
func sanitizeComment(s string) string {
	r := strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ")
	return strings.TrimSpace(r.Replace(s))
}

// outPart is one converted MML part, already numbered/lettered and rendered
// into its MML command body, ready for renderMML to assemble into a file.
type outPart struct {
	numericNo int    // FM parts: 1-16
	letterNo  byte   // PCM parts: 'A'-'P'
	partType  string // PCM only: "long" or "oneShot"
	volume    int    // 0-127
	pan       int    // -16..16
	comment   string // source track name, tag stripped; "" if none worth noting
	body      string
}

func scaleFactor(division int) float64 {
	return float64(common.TicksPerQuarterNote) / float64(division)
}

func scaleTicks(raw int, factor float64) int {
	return int(math.Round(float64(raw) * factor))
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// cc10ToPan16 maps a MIDI pan controller's 0-127 range onto go-fmml's
// -16(left)..16(right) scale (CLAUDE.md's "127段階から32段階へ変換").
func cc10ToPan16(v int) int {
	p := int(math.Round(float64(v)/127.0*32.0)) - 16
	return clamp(p, -16, 16)
}

// slot is one chronological onset in a melodic track: a single note, or a
// chord when multiple notes share the exact same (scaled) onset tick.
type slot struct {
	onTick int
	notes  []uint8
	sound  int // longest individual note's sounding ticks among notes
}

// buildSlots groups a track's (already tick-scaled) notes into chronological
// slots, merging same-onset notes into a chord per CLAUDE.md's rule that a
// chord's gate follows its longest member note.
func buildSlots(notes []midiparse.NoteEvent) []slot {
	if len(notes) == 0 {
		return nil
	}
	sorted := make([]midiparse.NoteEvent, len(notes))
	copy(sorted, notes)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].OnTick < sorted[j].OnTick })

	var slots []slot
	i := 0
	for i < len(sorted) {
		on := sorted[i].OnTick
		var pitches []uint8
		sound := 0
		j := i
		for j < len(sorted) && sorted[j].OnTick == on {
			pitches = append(pitches, sorted[j].Note)
			if d := sorted[j].OffTick - sorted[j].OnTick; d > sound {
				sound = d
			}
			j++
		}
		slots = append(slots, slot{onTick: on, notes: pitches, sound: sound})
		i = j
	}
	return slots
}

// buildMelodicBody converts one FM or PCM-Long track's notes into a single
// MML command stream: chronological slots (single notes or chords), each
// occupying exactly the gap to the next onset (or to the track's own last
// note-off, for the final slot) so timing is anchored to the source MIDI's
// actual onset ticks and can't drift across a long part.
func buildMelodicBody(notes []midiparse.NoteEvent, factor float64) string {
	scaled := make([]midiparse.NoteEvent, len(notes))
	trackEnd := 0
	for i, n := range notes {
		on := scaleTicks(n.OnTick, factor)
		off := scaleTicks(n.OffTick, factor)
		if off < on {
			off = on
		}
		scaled[i] = midiparse.NoteEvent{OnTick: on, OffTick: off, Note: n.Note}
		if off > trackEnd {
			trackEnd = off
		}
	}

	slots := buildSlots(scaled)
	var sb strings.Builder
	sb.WriteString("V127")
	prevEnd := 0
	for i, s := range slots {
		if s.onTick > prevEnd {
			mmlgen.EmitRest(&sb, s.onTick-prevEnd)
		}
		var slotLen int
		if i+1 < len(slots) {
			slotLen = slots[i+1].onTick - s.onTick
		} else {
			slotLen = trackEnd - s.onTick
		}
		if slotLen <= 0 {
			slotLen = mmlgen.MinUnitTicks()
		}
		mmlgen.EmitMelodicSlot(&sb, s.notes, slotLen, s.sound)
		prevEnd = s.onTick + slotLen
	}
	return sb.String()
}

// buildOneShotBody converts one PCM-OneShot track's notes into CLAUDE.md's
// per-pitch "<note>{ ... }" block syntax: one independent X/R timeline per
// distinct pitch used, each starting at tick 0 (matching how the engine
// itself plays every block concurrently from the part's start). Each
// block's own tokens are wrapped to width individually (rather than left to
// renderMML's generic wrap, which would re-flow across block boundaries and
// destroy the blank-line separator below), then blocks are joined with a
// blank line between them for readability.
//
// Every block's last trigger extends to trackEnd - the whole track's own
// last onset across every pitch, mirroring buildMelodicBody's trackEnd -
// rather than each pitch independently tacking on a fixed pad after its own
// last onset: that would inflate the reported part length (and so
// potentially the whole sequence's loop length, see memory.SequenceData's
// loop point being the longest part) by fabricated trailing silence with no
// basis in the source SMF. A pitch whose own last onset already equals
// trackEnd still needs some minimal length to represent that final trigger
// at all (the grammar has no zero-length note); every other pitch's last
// block segment is exactly as long as the source data actually implies.
func buildOneShotBody(notes []midiparse.NoteEvent, factor float64) string {
	byPitch := make(map[uint8][]int)
	trackEnd := 0
	for _, n := range notes {
		on := scaleTicks(n.OnTick, factor)
		byPitch[n.Note] = append(byPitch[n.Note], on)
		if on > trackEnd {
			trackEnd = on
		}
	}
	var pitches []uint8
	for p := range byPitch {
		pitches = append(pitches, p)
	}
	sort.Slice(pitches, func(i, j int) bool { return pitches[i] < pitches[j] })

	blocks := make([]string, 0, len(pitches))
	for _, p := range pitches {
		onsets := byPitch[p]
		sort.Ints(onsets)

		var sb strings.Builder
		letter, oct := mmlgen.NoteNameParts(p)
		fmt.Fprintf(&sb, "%s%d{ V127", letter, oct)
		prevEnd := 0
		for i, on := range onsets {
			if on > prevEnd {
				mmlgen.EmitRest(&sb, on-prevEnd)
			}
			var gap int
			if i+1 < len(onsets) {
				gap = onsets[i+1] - on
			} else {
				gap = trackEnd - on
			}
			if gap <= 0 {
				gap = mmlgen.MinUnitTicks()
			}
			mmlgen.EmitOnsetGap(&sb, gap)
			prevEnd = on + gap
		}
		sb.WriteString(" }")
		blocks = append(blocks, mmlgen.WrapMML(sb.String(), 100))
	}
	return strings.Join(blocks, "\n\n  ")
}

// ConvertToMML turns one parsed SMF file into a complete .mml file's text,
// per CLAUDE.md's Phase 6 spec: track order decides partNo (numeric for FM,
// 'A'-'P' for PCM), a track with no notes is skipped, and a track beyond
// either type's part-count limit is skipped with a warning rather than
// erroring out the whole conversion.
func ConvertToMML(res *midiparse.SMFResult) (string, []string, error) {
	var warnings []string
	factor := scaleFactor(res.Division)

	var fmParts []outPart
	var pcmParts []outPart
	fmCounter := 1
	pcmCounter := byte('A')

	for idx, tr := range res.Tracks {
		if len(tr.Notes) == 0 {
			continue
		}
		kind := classifyTrackKind(tr.Name)
		comment := sanitizeComment(stripTrackTag(tr.Name))

		volume := 100
		if tr.VolumeCC7 != nil {
			volume = clamp(*tr.VolumeCC7, 0, 127)
		}
		pan := 0
		if tr.PanCC10 != nil {
			pan = cc10ToPan16(*tr.PanCC10)
		}

		switch kind {
		case kindFM:
			if fmCounter > 16 {
				warnings = append(warnings, fmt.Sprintf("track %d (%q): FM part limit (16) reached, skipping", idx, tr.Name))
				continue
			}
			fmParts = append(fmParts, outPart{
				numericNo: fmCounter,
				volume:    volume,
				pan:       pan,
				comment:   comment,
				body:      buildMelodicBody(tr.Notes, factor),
			})
			fmCounter++

		case kindPCMLong:
			if pcmCounter > 'P' {
				warnings = append(warnings, fmt.Sprintf("track %d (%q): PCM part limit (16) reached, skipping", idx, tr.Name))
				continue
			}
			pcmParts = append(pcmParts, outPart{
				letterNo: pcmCounter,
				partType: "long",
				volume:   volume,
				pan:      pan,
				comment:  comment,
				body:     buildMelodicBody(tr.Notes, factor),
			})
			pcmCounter++

		case kindPCMOneShot:
			if pcmCounter > 'P' {
				warnings = append(warnings, fmt.Sprintf("track %d (%q): PCM part limit (16) reached, skipping", idx, tr.Name))
				continue
			}
			pcmParts = append(pcmParts, outPart{
				letterNo: pcmCounter,
				partType: "oneShot",
				volume:   volume,
				pan:      pan,
				comment:  comment,
				body:     buildOneShotBody(tr.Notes, factor),
			})
			pcmCounter++
		}
	}

	if len(fmParts) == 0 && len(pcmParts) == 0 {
		return "", warnings, fmt.Errorf("no track in the SMF file contained any note data")
	}

	return renderMML(res, fmParts, pcmParts), warnings, nil
}

// sanitizeSequenceID strips any '"' characters from a source SMF song name
// before it's written as [global]'s sequenceID: a literal quote in the name
// itself is simply dropped rather than YAML-escaped, so sequenceID never
// ends up wrapped in quotes it didn't need.
func sanitizeSequenceID(s string) string {
	return strings.ReplaceAll(s, "\"", "")
}

// renderMML assembles a complete .mml file's text (header, [global],
// [partSetting]/[pcmPartSetting], and every part's command block) per
// CLAUDE.md's MML file spec.
func renderMML(res *midiparse.SMFResult, fmParts, pcmParts []outPart) string {
	var sb strings.Builder
	sb.WriteString("[GoFMML File]\n\n")

	sb.WriteString("[global]\n")
	fmt.Fprintf(&sb, "  tempo: %d\n", int(res.TempoBPM))
	fmt.Fprintf(&sb, "  sequenceID: %s\n", sanitizeSequenceID(res.SongName))
	sb.WriteString("  loop: false\n")
	sb.WriteString("  volume: 100\n")
	sb.WriteString("  reverb: false\n")
	sb.WriteString("  reverbType: normal\n")
	sb.WriteString("  reverbTime: 2.0\n")
	sb.WriteString("  reverbLevel: 100\n\n")

	if len(fmParts) > 0 {
		sb.WriteString("[partSetting]\n")
		sb.WriteString("part:\n")
		for _, p := range fmParts {
			if p.comment != "" {
				fmt.Fprintf(&sb, "  # %s\n", p.comment)
			}
			fmt.Fprintf(&sb, "  - partNo: %d\n", p.numericNo)
			sb.WriteString("    voiceID: 0\n")
			fmt.Fprintf(&sb, "    volume: %d\n", p.volume)
			fmt.Fprintf(&sb, "    pan: %d\n", p.pan)
			sb.WriteString("    mute: false\n\n")
		}
		for _, p := range fmParts {
			if p.comment != "" {
				fmt.Fprintf(&sb, "# %s\n", p.comment)
			}
			fmt.Fprintf(&sb, "[part%d]\n", p.numericNo)
			fmt.Fprintf(&sb, "  %s\n", mmlgen.WrapMML(p.body, 100))
			fmt.Fprintf(&sb, "[part%dEnd]\n\n", p.numericNo)
		}
	}

	if len(pcmParts) > 0 {
		sb.WriteString("[pcmPartSetting]\n")
		sb.WriteString("pcmPart:\n")
		for _, p := range pcmParts {
			if p.comment != "" {
				fmt.Fprintf(&sb, "  # %s\n", p.comment)
			}
			fmt.Fprintf(&sb, "  - partNo: %c\n", p.letterNo)
			sb.WriteString("    voiceID: 0\n")
			fmt.Fprintf(&sb, "    partType: %s\n", p.partType)
			fmt.Fprintf(&sb, "    volume: %d\n", p.volume)
			fmt.Fprintf(&sb, "    pan: %d\n", p.pan)
			sb.WriteString("    mute: false\n\n")
		}
		for _, p := range pcmParts {
			if p.comment != "" {
				fmt.Fprintf(&sb, "# %s\n", p.comment)
			}
			fmt.Fprintf(&sb, "[pcmPart%c]\n", p.letterNo)
			body := p.body
			if p.partType != "oneShot" {
				// A OneShot body is already wrapped per-block by
				// buildOneShotBody; re-wrapping here would re-flow across
				// its blank-line block separators via strings.Fields and
				// destroy them.
				body = mmlgen.WrapMML(body, 100)
			}
			fmt.Fprintf(&sb, "  %s\n", body)
			fmt.Fprintf(&sb, "[pcmPart%cEnd]\n\n", p.letterNo)
		}
	}

	return sb.String()
}
