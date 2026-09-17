/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package analyze

import (
	"testing"

	"github.com/megaak-soft/go-fmml/common"
	"github.com/megaak-soft/go-fmml/fmcore"
	"github.com/megaak-soft/go-fmml/memory"
	"github.com/megaak-soft/go-fmml/pcmcore"
)

const sampleMML = `[GoFMML File]

# comment lines and blank lines are ignored

[global]
  tempo: 120
  sequenceID: SampleMML
  loop: false
  volume: 64

[partSetting]
part:
  - partNo: 1
    voiceID: 0
    volume: 127
    pan: 0
  - partNo: 2
    voiceID: 1
    volume: 76
    pan: -16

[part1]
O3 C4D4E4F#4
G8A8B8
O4 C16C#16D16e16 F2R4G8
[part1End]

[part2]
L4 V100 [C4E4G4]
[part2End]
`

func TestParseMML(t *testing.T) {
	seq, warnings, err := ParseMML([]byte(sampleMML))
	if err != nil {
		t.Fatalf("ParseMML failed: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	if seq.SequenceID != "SampleMML" || seq.Tempo != 120 || seq.Loop != false {
		t.Fatalf("unexpected sequence metadata: %+v", seq)
	}
	if want := 64.0 / 127; seq.MasterVolume != want {
		t.Fatalf("expected [global] volume:64 to normalize to %v, got %v", want, seq.MasterVolume)
	}
	if len(seq.Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(seq.Parts))
	}

	p1 := seq.Parts[fmcore.PartID(1)]
	if p1 == nil {
		t.Fatalf("expected part 1")
	}
	if p1.VoiceID != 0 || p1.Volume != 1 || p1.Pan != 0 {
		t.Fatalf("unexpected part1 setting: %+v", p1)
	}
	// O3 C4 D4 E4 F#4 G8 A8 B8 O4 C16 C#16 D16 E16(lowercase) F2 R4(rest) G8 = 13 notes total
	wantNotes := 13
	gotNotes := 0
	for _, ev := range p1.Events {
		if ev.Kind == memory.EventNote {
			gotNotes += len(ev.Notes)
		}
	}
	if gotNotes != wantNotes {
		t.Fatalf("expected %d notes in part1, got %d", wantNotes, gotNotes)
	}

	first := p1.Events[0]
	if first.AtTick != 0 || first.Notes[0].NoteName != "C3" || first.Notes[0].NoteType != "NOTE4" {
		t.Fatalf("unexpected first event: %+v", first)
	}
	fourth := p1.Events[3]
	if fourth.Notes[0].NoteName != "F#3" {
		t.Fatalf("expected F#3 (sharp via '#'), got %+v", fourth.Notes[0])
	}

	p2 := seq.Parts[fmcore.PartID(2)]
	if p2 == nil {
		t.Fatalf("expected part 2")
	}
	if len(p2.Events) != 1 || len(p2.Events[0].Notes) != 3 {
		t.Fatalf("expected 1 chord event with 3 notes in part2, got %+v", p2.Events)
	}
	for _, n := range p2.Events[0].Notes {
		if n.Velocity != 100 {
			t.Fatalf("expected chord notes to use V100, got %+v", n)
		}
	}
}

// --- Phase 5: mute, portamento ('&&'), and pitch-bend ('_/','_\','/_','\_') ---

func TestParseMMLPartMute(t *testing.T) {
	const mml = `[GoFMML File]
[global]
  tempo: 120
  sequenceID: MuteTest
[partSetting]
part:
  - partNo: 1
    voiceID: 0
    volume: 127
    pan: 0
    mute: true
  - partNo: 2
    voiceID: 0
    volume: 127
    pan: 0
[part1]
C4
[part1End]
[part2]
C4
[part2End]
[pcmPartSetting]
pcmPart:
  - partNo: A
    voiceID: 0
    partType: long
    volume: 100
    pan: 0
    mute: true
[pcmPartA]
C4
[pcmPartAEnd]
`
	seq, _, err := ParseMML([]byte(mml))
	if err != nil {
		t.Fatalf("ParseMML failed: %v", err)
	}
	if !seq.Parts[fmcore.PartID(1)].Mute {
		t.Fatalf("expected part 1's mute:true to be parsed")
	}
	if seq.Parts[fmcore.PartID(2)].Mute {
		t.Fatalf("expected part 2's omitted mute to default to false")
	}
	if !seq.PCMParts[pcmcore.PartID('A')].Mute {
		t.Fatalf("expected pcmPart A's mute:true to be parsed")
	}
}

// TestParseMMLPartSolo covers the MML addendum's "solo" debug-aid parsing: it should
// simply carry through to memory.PartSequence/PCMPartSequence.Solo exactly
// as written (go-fmml/player, not go-fmml/analyze, resolves it into an
// effective mute across every part).
func TestParseMMLPartSolo(t *testing.T) {
	const mml = `[GoFMML File]
[global]
  tempo: 120
  sequenceID: SoloTest
[partSetting]
part:
  - partNo: 1
    voiceID: 0
    volume: 127
    pan: 0
    mute: true
    solo: true
  - partNo: 2
    voiceID: 0
    volume: 127
    pan: 0
[part1]
C4
[part1End]
[part2]
C4
[part2End]
[pcmPartSetting]
pcmPart:
  - partNo: A
    voiceID: 0
    partType: long
    volume: 100
    pan: 0
    solo: false
[pcmPartA]
C4
[pcmPartAEnd]
`
	seq, _, err := ParseMML([]byte(mml))
	if err != nil {
		t.Fatalf("ParseMML failed: %v", err)
	}
	if !seq.Parts[fmcore.PartID(1)].Solo {
		t.Fatalf("expected part 1's solo:true to be parsed")
	}
	if seq.Parts[fmcore.PartID(2)].Solo {
		t.Fatalf("expected part 2's omitted solo to default to false")
	}
	if seq.PCMParts[pcmcore.PartID('A')].Solo {
		t.Fatalf("expected pcmPart A's solo:false to be parsed as false")
	}
}

func TestTieSameNoteMergesIntoSingleSustainedAttack(t *testing.T) {
	// C1 & C1 & C1: 3 whole notes tied into a single attack, held for their
	// combined length.
	events, _, warnings := parsePartCommands("C1&C1&C1", 0)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	if len(events) != 1 {
		t.Fatalf("expected the whole tied chain to collapse into 1 event (1 attack), got %d", len(events))
	}
	whole, err := common.NoteTypeTicks("NOTE1")
	if err != nil {
		t.Fatalf("NoteTypeTicks(NOTE1) failed: %v", err)
	}
	if got, want := events[0].Notes[0].Bend.TieTicks, whole*3; got != want {
		t.Fatalf("expected the merged note's total held ticks to be 3 whole notes (%d), got %d", want, got)
	}
	if got := events[0].Notes[0].Sustain; got != 100 {
		t.Fatalf("expected the merged note's gate to stay at the default 100%%, got %d", got)
	}
}

func TestTieUsesLastSegmentsGateForTheCombinedNote(t *testing.T) {
	events, _, warnings := parsePartCommands("Q4C4&Q2C4", 0)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 merged event, got %d", len(events))
	}
	quarter, _ := common.NoteTypeTicks("NOTE4")
	if got, want := events[0].Notes[0].Bend.TieTicks, quarter*2; got != want {
		t.Fatalf("expected 2 quarter notes' worth of held ticks (%d), got %d", want, got)
	}
	if got := events[0].Notes[0].Sustain; got != 50 {
		t.Fatalf("expected the combined note's gate to be the last tied segment's own Q2 (50%%), got %d", got)
	}
}

func TestTieBetweenDifferentNotesIsIgnoredAndPlaysSeparately(t *testing.T) {
	events, _, warnings := parsePartCommands("C4&D4", 0)
	if len(warnings) == 0 {
		t.Fatalf("expected a warning about tying different notes")
	}
	if len(events) != 2 {
		t.Fatalf("expected the tie to be ignored and both notes to play separately, got %d events", len(events))
	}
	if events[0].Notes[0].Bend.TieTicks != 0 {
		t.Fatalf("expected the first note to be left untouched, got %+v", events[0].Notes[0].Bend)
	}
	if events[1].Notes[0].Bend != (memory.NoteBend{}) {
		t.Fatalf("expected the second note to have its own fresh attack with no bend, got %+v", events[1].Notes[0].Bend)
	}
}

func TestTieAcrossARestIsInvalid(t *testing.T) {
	events, _, warnings := parsePartCommands("C4&R4C4", 0)
	if len(warnings) == 0 {
		t.Fatalf("expected a warning about a tie across a rest")
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 separate note events (the rest doesn't produce one), got %d", len(events))
	}
	if events[0].Notes[0].Bend.TieTicks != 0 {
		t.Fatalf("expected no merge across the rest, got %+v", events[0].Notes[0].Bend)
	}
}

func TestTieBetweenSingleAndChordIsInvalid(t *testing.T) {
	events, _, warnings := parsePartCommands("C4&[C4E4G4]", 0)
	if len(warnings) == 0 {
		t.Fatalf("expected a warning about tying a single note to a chord")
	}
	if len(events) != 2 {
		t.Fatalf("expected the chord to play as its own separate event, got %d", len(events))
	}
}

func TestTieChordSamePitchesMergesEveryNote(t *testing.T) {
	events, _, warnings := parsePartCommands("[C4E4G4]&[C4E4G4]", 0)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	if len(events) != 1 {
		t.Fatalf("expected the tied chords to collapse into 1 event, got %d", len(events))
	}
	whole, _ := common.NoteTypeTicks("NOTE4")
	for _, n := range events[0].Notes {
		if n.Bend.TieTicks != whole*2 {
			t.Fatalf("expected every chord note to carry 2 quarter notes' worth of held ticks, got %+v", n.Bend)
		}
	}
}

func TestTieChordDifferentPitchesIsInvalid(t *testing.T) {
	events, _, warnings := parsePartCommands("[C4E4G4]&[C4E4A4]", 0)
	if len(warnings) == 0 {
		t.Fatalf("expected a warning about tying chords with different pitches")
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 separate chord events, got %d", len(events))
	}
}

func TestDanglingTieAtEndOfPartIsWarnedAndIgnored(t *testing.T) {
	_, _, warnings := parsePartCommands("C4&", 0)
	if len(warnings) == 0 {
		t.Fatalf("expected a warning about a dangling tie at end of part")
	}
}

func TestRepeatBlockExpandsToNCopiesOfItsContents(t *testing.T) {
	// CLAUDE.md's own worked example: "|: L4 C D E :|*2" == "L4 C D E L4 C
	// D E".
	repeated, _, warnings := parsePartCommands("|:L4CDE:|*2", 0)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	literal, _, warnings := parsePartCommands("L4CDEL4CDE", 0)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings from the literal form, got %v", warnings)
	}
	if len(repeated) != len(literal) {
		t.Fatalf("expected the repeat block to produce the same %d events as writing it out twice, got %d", len(literal), len(repeated))
	}
	for i := range repeated {
		if repeated[i].Notes[0].NoteName != literal[i].Notes[0].NoteName || repeated[i].AtTick != literal[i].AtTick {
			t.Fatalf("event %d: expected %+v to match the literal form's %+v", i, repeated[i], literal[i])
		}
	}
}

func TestRepeatBlockWithoutCountPlaysOnce(t *testing.T) {
	events, _ := mustParsePartCommandsNoWarnings(t, "|:L4CDE:|F4")
	if len(events) != 4 {
		t.Fatalf("expected 4 notes (C D E F, the block playing only once), got %d", len(events))
	}
}

func TestRepeatBlockAllowsOtherCommandsInside(t *testing.T) {
	// State changes (here, O) inside a repeat block must carry over exactly
	// as if the block had been typed out by hand each time.
	events, _ := mustParsePartCommandsNoWarnings(t, "|:O3CO4C:|*2")
	if len(events) != 4 {
		t.Fatalf("expected 4 note events, got %d", len(events))
	}
	if events[0].Notes[0].NoteName != "C3" || events[1].Notes[0].NoteName != "C4" {
		t.Fatalf("expected the first repetition's octave changes to apply, got %+v and %+v", events[0].Notes[0], events[1].Notes[0])
	}
	if events[2].Notes[0].NoteName != "C3" || events[3].Notes[0].NoteName != "C4" {
		t.Fatalf("expected the second repetition to reapply the same octave changes from scratch, got %+v and %+v", events[2].Notes[0], events[3].Notes[0])
	}
}

func TestNestedRepeatBlocksExpandInsideOut(t *testing.T) {
	// |: |: C :|*2 D :|*3 == ((C*2) D)*3 == CCDCCDCCD
	events, _ := mustParsePartCommandsNoWarnings(t, "|:|:C:|*2D:|*3")
	want := []string{"C", "C", "D", "C", "C", "D", "C", "C", "D"}
	if len(events) != len(want) {
		t.Fatalf("expected %d notes from the nested repeat, got %d", len(want), len(events))
	}
	for i, name := range want {
		if got := events[i].Notes[0].NoteName; got[:1] != name {
			t.Fatalf("event %d: expected note %q, got %q", i, name, got)
		}
	}
}

func TestRepeatCountZeroOrNegativeDefaultsToOneWithWarning(t *testing.T) {
	events, _, warnings := parsePartCommands("|:C:|*0", 0)
	if len(warnings) == 0 {
		t.Fatalf("expected a warning about an invalid repeat count")
	}
	if len(events) != 1 {
		t.Fatalf("expected the block to still play once, got %d events", len(events))
	}
}

func TestRepeatCountAboveSafetyCapIsClamped(t *testing.T) {
	_, _, warnings := parsePartCommands("|:C:|*999999", 0)
	if len(warnings) == 0 {
		t.Fatalf("expected a warning about the repeat count exceeding the safety cap")
	}
}

func TestDanglingRepeatOpenIsWarnedAndIgnored(t *testing.T) {
	events, _, warnings := parsePartCommands("|:C4D4", 0)
	if len(warnings) == 0 {
		t.Fatalf("expected a warning about an unmatched '|:'")
	}
	if len(events) != 2 {
		t.Fatalf("expected both notes to still parse once the marker is stripped, got %d", len(events))
	}
}

func TestDanglingRepeatCloseIsWarnedAndIgnored(t *testing.T) {
	events, _, warnings := parsePartCommands("C4:|D4", 0)
	if len(warnings) == 0 {
		t.Fatalf("expected a warning about an unmatched ':|'")
	}
	if len(events) != 2 {
		t.Fatalf("expected both notes to still parse once the marker is stripped, got %d", len(events))
	}
}

func TestPortamentoBetweenSingleNotesGlidesAndClearsStaccato(t *testing.T) {
	// Q2 = 50% gate (staccato); portamento should override that back to
	// 100% on the note it glides away from.
	events, _, warnings := parsePartCommands("Q2C4&&4D4", 0)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 note events, got %d", len(events))
	}
	if got := events[0].Notes[0].Sustain; got != 100 {
		t.Fatalf("expected portamento to clear the first note's staccato (Sustain=100), got %d", got)
	}
	bend := events[1].Notes[0].Bend
	if got, want := bend.AttackOffsetSemitones, -2.0; got != want {
		t.Fatalf("expected D4's portamento-in offset %v (C4->D4 is 2 semitones), got %v", want, got)
	}
	if got, want := bend.AttackTicks, 48; got != want {
		t.Fatalf("expected explicit '&&4' to resolve to 48 ticks (a quarter note), got %d", got)
	}
	if !bend.Portamento {
		t.Fatalf("expected a single-note-to-single-note '&&' to be marked Portamento (true legato), got %+v", bend)
	}
}

func TestPortamentoDefaultLengthIsHalfTheTargetNote(t *testing.T) {
	events, _ := mustParsePartCommandsNoWarnings(t, "C4&&D4")
	bend := events[1].Notes[0].Bend
	if got, want := bend.AttackTicks, 24; got != want {
		t.Fatalf("expected an unspecified '&&' length to default to 50%% of D4's 48 ticks (24), got %d", got)
	}
}

func TestPortamentoExplicitLengthExceedingNoteFallsBackToDefault(t *testing.T) {
	// D8 is 24 ticks; an explicit "&&4" (48 ticks) exceeds it, so it must
	// fall back to 50% of D8's own length (12 ticks) per CLAUDE.md.
	events, _ := mustParsePartCommandsNoWarnings(t, "C8&&4D8")
	bend := events[1].Notes[0].Bend
	if got, want := bend.AttackTicks, 12; got != want {
		t.Fatalf("expected an over-long explicit portamento length to fall back to 12 ticks, got %d", got)
	}
}

func TestPortamentoAcrossARestIsInvalid(t *testing.T) {
	events, _, warnings := parsePartCommands("C4&&4R4D4", 0)
	if len(warnings) == 0 {
		t.Fatalf("expected a warning about portamento across a rest")
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 note events (the rest doesn't produce one), got %d", len(events))
	}
	if bend := events[1].Notes[0].Bend; bend != (memory.NoteBend{}) {
		t.Fatalf("expected no pitch glide on the note after the rest, got %+v", bend)
	}
}

func TestChordAllowsPerNoteOctaveOverride(t *testing.T) {
	events, _ := mustParsePartCommandsNoWarnings(t, "O4[O2G O3C]")
	if len(events) != 1 || len(events[0].Notes) != 2 {
		t.Fatalf("expected 1 chord event with 2 notes, got %+v", events)
	}
	if events[0].Notes[0].NoteName != "G2" {
		t.Fatalf("expected the first chord note to use the in-chord O2 override, got %+v", events[0].Notes[0])
	}
	if events[0].Notes[1].NoteName != "C3" {
		t.Fatalf("expected the second chord note to use the in-chord O3 override, got %+v", events[0].Notes[1])
	}
}

func TestChordOctaveOverrideStartsFromOuterOctaveAndDoesNotLeakOut(t *testing.T) {
	events, _ := mustParsePartCommandsNoWarnings(t, "O4[C O5E]D")
	if len(events) != 2 {
		t.Fatalf("expected 2 events (1 chord, 1 following note), got %+v", events)
	}
	if events[0].Notes[0].NoteName != "C4" {
		t.Fatalf("expected the chord's first note to keep the outer octave O4, got %+v", events[0].Notes[0])
	}
	if events[0].Notes[1].NoteName != "E5" {
		t.Fatalf("expected the chord's second note to use the in-chord O5 override, got %+v", events[0].Notes[1])
	}
	if events[1].Notes[0].NoteName != "D4" {
		t.Fatalf("expected the note after the chord to still be at the outer octave O4 (in-chord O5 must not leak out), got %+v", events[1].Notes[0])
	}
}

func TestChordOctaveOverrideMissingNumberWarnsAndIsIgnored(t *testing.T) {
	events, _, warnings := parsePartCommands("[OG]", 0)
	if len(warnings) == 0 {
		t.Fatalf("expected a warning about 'O' inside a chord missing an octave number")
	}
	if len(events) != 1 || len(events[0].Notes) != 1 {
		t.Fatalf("expected the chord's note to still be parsed, got %+v", events)
	}
	if events[0].Notes[0].NoteName != "G4" {
		t.Fatalf("expected the note to fall back to the part's current octave, got %+v", events[0].Notes[0])
	}
}

func TestTripletNoteLengthsResolveToTheDocumentedTickCounts(t *testing.T) {
	// CLAUDE.md's triplet lengths: 6/12/24 for a quarter/eighth/sixteenth
	// note triplet respectively - 3 of them exactly fill the duration 2 of
	// the corresponding power-of-2 length would (e.g. 3 C6's = 2 C4's).
	events, _, warnings := parsePartCommands("C6D12E24", 0)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for valid triplet lengths, got %v", warnings)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 note events, got %d", len(events))
	}
	wantTicks := []int{32, 16, 8} // NOTE6, NOTE12, NOTE24 at TicksPerQuarterNote=48
	wantTypes := []string{"NOTE6", "NOTE12", "NOTE24"}
	for i, ev := range events {
		if ev.Notes[0].NoteType != wantTypes[i] {
			t.Fatalf("event %d: expected NoteType %q, got %q", i, wantTypes[i], ev.Notes[0].NoteType)
		}
		gotTicks, err := common.NoteTypeTicks(ev.Notes[0].NoteType)
		if err != nil {
			t.Fatalf("NoteTypeTicks(%q) failed: %v", ev.Notes[0].NoteType, err)
		}
		if gotTicks != wantTicks[i] {
			t.Fatalf("event %d: expected %d ticks, got %d", i, wantTicks[i], gotTicks)
		}
	}
	// 3 triplet-quarters (32 ticks each) should span exactly 2 quarter
	// notes (96 ticks) - the defining property of a triplet.
	if got, want := events[1].AtTick-events[0].AtTick, 32; got != want {
		t.Fatalf("expected 32 ticks between the first two triplet notes, got %d", got)
	}
}

func TestTripletNoteLengthAppliesToRestAndLCommand(t *testing.T) {
	events, _, warnings := parsePartCommands("R6L12CD", 0)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 note events (the rest doesn't produce one), got %d", len(events))
	}
	// R6 (32 ticks) followed by L12 defaulting both C and D to NOTE12 (16
	// ticks each).
	if got, want := events[0].AtTick, 32; got != want {
		t.Fatalf("expected the first note to start after R6's 32 ticks, got %d", got)
	}
	if events[0].Notes[0].NoteType != "NOTE12" || events[1].Notes[0].NoteType != "NOTE12" {
		t.Fatalf("expected both notes to default to L12's NOTE12, got %q and %q", events[0].Notes[0].NoteType, events[1].Notes[0].NoteType)
	}
}

func TestDottedTripletNoteLength(t *testing.T) {
	// A dotted NOTE12 (16 ticks) is 1.5x = 24 ticks.
	events, _ := mustParsePartCommandsNoWarnings(t, "C12.")
	if got, want := events[0].Notes[0].NoteType, "NOTE12_DOT"; got != want {
		t.Fatalf("expected NoteType %q, got %q", want, got)
	}
	ticks, err := common.NoteTypeTicks(events[0].Notes[0].NoteType)
	if err != nil {
		t.Fatalf("NoteTypeTicks failed: %v", err)
	}
	if ticks != 24 {
		t.Fatalf("expected a dotted NOTE12 to be 24 ticks, got %d", ticks)
	}
}

func TestPortamentoSingleToChordIsInvalid(t *testing.T) {
	events, _, warnings := parsePartCommands("C4&&4[D4F4]", 0)
	if len(warnings) == 0 {
		t.Fatalf("expected a warning about portamento from a single note to a chord")
	}
	for _, n := range events[1].Notes {
		if n.Bend != (memory.NoteBend{}) {
			t.Fatalf("expected no pitch glide on the invalid chord, got %+v", n.Bend)
		}
	}
}

func TestPortamentoChordToChordUsesLeadNoteDeltaUniformly(t *testing.T) {
	// Mismatched chord sizes (3 notes -> 2 notes) - CLAUDE.md says to apply
	// the lead notes' pitch difference uniformly to every note in the new
	// chord, regardless of how the counts compare.
	events, _ := mustParsePartCommandsNoWarnings(t, "[C4E4G4]&&4[D4F4]")
	if len(events) != 2 {
		t.Fatalf("expected 2 chord events, got %d", len(events))
	}
	wantOffset := -2.0 // C4->D4 is 2 semitones
	for _, n := range events[1].Notes {
		if n.Bend.AttackOffsetSemitones != wantOffset {
			t.Fatalf("expected every new-chord note to glide in by %v semitones, got %+v", wantOffset, n.Bend)
		}
		if n.Bend.Portamento {
			t.Fatalf("expected chord-to-chord portamento to stay a fresh attack (Portamento=false), got %+v", n.Bend)
		}
	}
	for _, n := range events[0].Notes {
		if n.Sustain != 100 {
			t.Fatalf("expected portamento to clear the old chord's staccato, got %+v", n)
		}
	}
}

func TestPitchBendPrefixUpStartsBelowTarget(t *testing.T) {
	// CLAUDE.md's own example shape: "_2/D" starts 2 semitones below D and
	// bends up into it.
	events, _ := mustParsePartCommandsNoWarnings(t, "_2/D4")
	bend := events[0].Notes[0].Bend
	if got, want := bend.AttackOffsetSemitones, -2.0; got != want {
		t.Fatalf("expected '_2/D4' to start %v semitones below D4, got %v", want, got)
	}
	if got, want := bend.AttackTicks, 24; got != want { // 50% of D4's 48 ticks
		t.Fatalf("expected an unspecified bend length to default to 24 ticks, got %d", got)
	}
}

func TestPitchBendPrefixDownStartsAboveTarget(t *testing.T) {
	events, _ := mustParsePartCommandsNoWarnings(t, "_2\\D4")
	bend := events[0].Notes[0].Bend
	if got, want := bend.AttackOffsetSemitones, 2.0; got != want {
		t.Fatalf("expected '_2\\D4' to start %v semitones above D4, got %v", want, got)
	}
}

func TestPitchBendPrefixExplicitLength(t *testing.T) {
	events, _ := mustParsePartCommandsNoWarnings(t, "_2/4D2")
	bend := events[0].Notes[0].Bend
	if got, want := bend.AttackTicks, 48; got != want { // NOTE4, fits within D2's 96 ticks
		t.Fatalf("expected explicit bend length '4' to resolve to 48 ticks, got %d", got)
	}
}

func TestPitchBendPrefixDefaultsToOneSemitone(t *testing.T) {
	events, _ := mustParsePartCommandsNoWarnings(t, "_/D4")
	if got, want := events[0].Notes[0].Bend.AttackOffsetSemitones, -1.0; got != want {
		t.Fatalf("expected an unspecified bend amount to default to 1 semitone, got offset %v", got)
	}
}

func TestPitchBendPostfixUpEndsAboveTarget(t *testing.T) {
	// CLAUDE.md's example: "C2/2_" ends 2 semitones above the written note.
	events, _ := mustParsePartCommandsNoWarnings(t, "C2/2_4")
	bend := events[0].Notes[0].Bend
	if got, want := bend.ReleaseOffsetSemitones, 2.0; got != want {
		t.Fatalf("expected 'C2/2_4' to end %v semitones above C2, got %v", want, got)
	}
	if got, want := bend.ReleaseTicks, 48; got != want {
		t.Fatalf("expected explicit release-bend length '4' to resolve to 48 ticks, got %d", got)
	}
}

func TestPitchBendPostfixDownEndsBelowTarget(t *testing.T) {
	events, _ := mustParsePartCommandsNoWarnings(t, "D2\\2_")
	bend := events[0].Notes[0].Bend
	if got, want := bend.ReleaseOffsetSemitones, -2.0; got != want {
		t.Fatalf("expected 'D2\\2_' to end %v semitones below D2, got %v", want, got)
	}
	if got, want := bend.ReleaseTicks, 48; got != want { // 50% of D2's 96 ticks
		t.Fatalf("expected an unspecified release-bend length to default to 48 ticks, got %d", got)
	}
}

func TestPitchBendOnChordAppliesToEveryNote(t *testing.T) {
	events, _ := mustParsePartCommandsNoWarnings(t, "_/[C4E4G4]")
	for _, n := range events[0].Notes {
		if n.Bend.AttackOffsetSemitones != -1 {
			t.Fatalf("expected every chord note to carry the same -1 semitone attack bend, got %+v", n.Bend)
		}
	}
}

func TestDanglingBendMarkersAreWarnedAndIgnored(t *testing.T) {
	if _, _, warnings := parsePartCommands("_/", 0); len(warnings) == 0 {
		t.Fatalf("expected a warning for a pitch-bend marker with no following note")
	}
	if _, _, warnings := parsePartCommands("C4&&", 0); len(warnings) == 0 {
		t.Fatalf("expected a warning for a portamento marker with no following note")
	}
}

// mustParsePartCommandsNoWarnings is a small test helper for the common
// case where a Phase 5 bend/portamento MML snippet is expected to parse
// cleanly with no warnings at all.
func mustParsePartCommandsNoWarnings(t *testing.T, mml string) ([]memory.SeqEvent, []string) {
	t.Helper()
	events, _, warnings := parsePartCommands(mml, 0)
	if len(warnings) != 0 {
		t.Fatalf("parsePartCommands(%q, 0): expected no warnings, got %v", mml, warnings)
	}
	return events, warnings
}

func TestParseMMLMasterVolumeDefaultsToFullWhenOmitted(t *testing.T) {
	const mml = `[GoFMML File]
[global]
  tempo: 120
  sequenceID: NoVolume
  loop: false
[partSetting]
part:
  - partNo: 1
    voiceID: 0
    volume: 127
    pan: 0
[part1]
C4
[part1End]
`
	seq, _, err := ParseMML([]byte(mml))
	if err != nil {
		t.Fatalf("ParseMML failed: %v", err)
	}
	if seq.MasterVolume != 1 {
		t.Fatalf("expected an omitted [global] volume to default to full volume (1.0), got %v", seq.MasterVolume)
	}
}

// TestParseMMLExplicitVolumeZeroMeansSilence guards against a regression
// where "volume: 0" (a legitimate, explicit request for silence) was
// indistinguishable from an omitted volume field (which defaults to full
// volume) - both unmarshal to the float64 zero value unless the YAML
// field is a pointer, so an equality check against 0 can't tell them
// apart. Covers [global], [partSetting], and [pcmPartSetting] all three.
func TestParseMMLExplicitVolumeZeroMeansSilence(t *testing.T) {
	const mml = `[GoFMML File]
[global]
  tempo: 120
  sequenceID: SilentVolumeTest
  loop: false
  volume: 0
[partSetting]
part:
  - partNo: 1
    voiceID: 0
    volume: 0
    pan: 0
[part1]
C4
[part1End]
[pcmPartSetting]
pcmPart:
  - partNo: A
    voiceID: 0
    partType: oneShot
    volume: 0
    pan: 0
[pcmPartA]
C1 { X }
[pcmPartAEnd]
`
	seq, _, err := ParseMML([]byte(mml))
	if err != nil {
		t.Fatalf("ParseMML failed: %v", err)
	}
	if seq.MasterVolume != 0 {
		t.Fatalf("expected explicit [global] volume:0 to mean silence (0.0), got %v", seq.MasterVolume)
	}
	if v := seq.Parts[1].Volume; v != 0 {
		t.Fatalf("expected explicit [partSetting] volume:0 to mean silence (0.0), got %v", v)
	}
	if v := seq.PCMParts['A'].Volume; v != 0 {
		t.Fatalf("expected explicit [pcmPartSetting] volume:0 to mean silence (0.0), got %v", v)
	}
}

func TestParseMMLMissingHeaderIsFatal(t *testing.T) {
	if _, _, err := ParseMML([]byte("[global]\ntempo: 120\n")); err == nil {
		t.Fatalf("expected an error for a missing MML header")
	}
}

func TestParseMMLUnknownCommandIsWarningNotFatal(t *testing.T) {
	mml := `[GoFMML File]
[global]
  tempo: 120
  sequenceID: Warn
  loop: false
[partSetting]
part:
  - partNo: 1
    voiceID: 0
    volume: 127
    pan: 0
[part1]
C4 Z4 D4
[part1End]
`
	seq, warnings, err := ParseMML([]byte(mml))
	if err != nil {
		t.Fatalf("expected unknown command to be non-fatal, got: %v", err)
	}
	if len(warnings) == 0 {
		t.Fatalf("expected a warning for the unrecognized 'Z' command")
	}
	p1 := seq.Parts[fmcore.PartID(1)]
	gotNotes := 0
	for _, ev := range p1.Events {
		gotNotes += len(ev.Notes)
	}
	if gotNotes != 2 {
		t.Fatalf("expected C4 and D4 to still be parsed, got %d notes", gotNotes)
	}
}

// TestParseMMLReverbSettings covers Phase 4's [global] reverb settings plus
// [partSetting]/[pcmPartSetting]'s reverbSend, across an FM part, a Long
// PCM part (numeric reverbSend) and a OneShot PCM part (per-note
// "note: level" string reverbSend, including CLAUDE.md's own "F1#" -
// accidental-after-octave - spelling for the sharp note).
func TestParseMMLReverbSettings(t *testing.T) {
	const mml = `[GoFMML File]
[global]
  tempo: 120
  sequenceID: ReverbTest
  loop: false
  volume: 100
  reverb: true
  reverbType: simple
  reverbTime: 1.5
  reverbLevel: 64
[partSetting]
part:
  - partNo: 1
    voiceID: 0
    volume: 127
    pan: 0
    reverbSend: 30
[part1]
C4
[part1End]
[pcmPartSetting]
pcmPart:
  - partNo: A
    voiceID: 0
    partType: oneShot
    volume: 100
    pan: 0
    reverbSend: "C1: 5, E1: 30, F1#: 15"
  - partNo: B
    voiceID: 1
    partType: long
    volume: 100
    pan: 0
    reverbSend: 40
[pcmPartA]
C1 { X }
[pcmPartAEnd]
[pcmPartB]
A4
[pcmPartBEnd]
`
	seq, warnings, err := ParseMML([]byte(mml))
	if err != nil {
		t.Fatalf("ParseMML failed: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}

	if !seq.ReverbEnabled {
		t.Fatalf("expected reverb:true to enable reverb")
	}
	if seq.ReverbType != "simple" {
		t.Fatalf("expected reverbType simple, got %q", seq.ReverbType)
	}
	if seq.ReverbTimeSeconds != 1.5 {
		t.Fatalf("expected reverbTime 1.5, got %v", seq.ReverbTimeSeconds)
	}
	if want := 64.0 / 127; seq.ReverbLevel != want {
		t.Fatalf("expected reverbLevel 64 to normalize to %v, got %v", want, seq.ReverbLevel)
	}

	if want := 30.0 / 127; seq.Parts[1].ReverbSend != want {
		t.Fatalf("expected part 1 reverbSend 30 to normalize to %v, got %v", want, seq.Parts[1].ReverbSend)
	}

	longPart := seq.PCMParts['B']
	if longPart == nil {
		t.Fatalf("expected PCM part B")
	}
	if want := 40.0 / 127; longPart.ReverbSend != want {
		t.Fatalf("expected long part B reverbSend 40 to normalize to %v, got %v", want, longPart.ReverbSend)
	}

	oneShotPart := seq.PCMParts['A']
	if oneShotPart == nil {
		t.Fatalf("expected PCM part A")
	}
	wantSends := map[uint8]float64{
		24: 5.0 / 127,  // C1
		28: 30.0 / 127, // E1
		30: 15.0 / 127, // F#1, spelled "F1#" per CLAUDE.md's own example
	}
	if len(oneShotPart.OneShotReverbSend) != len(wantSends) {
		t.Fatalf("expected %d oneShot reverbSend entries, got %+v", len(wantSends), oneShotPart.OneShotReverbSend)
	}
	for note, want := range wantSends {
		if got := oneShotPart.OneShotReverbSend[note]; got != want {
			t.Fatalf("expected note %d reverbSend %v, got %v", note, want, got)
		}
	}
}

// TestParseMMLReverbTimeAtOrBelowMinimumBypasses covers CLAUDE.md's
// "0.5以下はバイパス" rule: a reverbTime at or below the minimum
// meaningful value disables reverb even though reverb:true was given.
func TestParseMMLReverbTimeAtOrBelowMinimumBypasses(t *testing.T) {
	const mml = `[GoFMML File]
[global]
  tempo: 120
  sequenceID: BypassTest
  loop: false
  reverb: true
  reverbTime: 0.3
[partSetting]
part:
  - partNo: 1
    voiceID: 0
    volume: 127
    pan: 0
[part1]
C4
[part1End]
`
	seq, _, err := ParseMML([]byte(mml))
	if err != nil {
		t.Fatalf("ParseMML failed: %v", err)
	}
	if seq.ReverbEnabled {
		t.Fatalf("expected reverbTime 0.3 (<= the bypass threshold) to disable reverb despite reverb:true")
	}
}

// TestParseMMLReverbTimeAboveMaximumClamps confirms a reverbTime beyond
// CLAUDE.md's 3.0s ceiling clamps down to it, with a warning.
func TestParseMMLReverbTimeAboveMaximumClamps(t *testing.T) {
	const mml = `[GoFMML File]
[global]
  tempo: 120
  sequenceID: ClampTest
  loop: false
  reverb: true
  reverbTime: 5.0
[partSetting]
part:
  - partNo: 1
    voiceID: 0
    volume: 127
    pan: 0
[part1]
C4
[part1End]
`
	seq, warnings, err := ParseMML([]byte(mml))
	if err != nil {
		t.Fatalf("ParseMML failed: %v", err)
	}
	if seq.ReverbTimeSeconds != 3.0 {
		t.Fatalf("expected reverbTime 5.0 to clamp to 3.0, got %v", seq.ReverbTimeSeconds)
	}
	if len(warnings) == 0 {
		t.Fatalf("expected a warning about the clamped reverbTime")
	}
}

// TestParseMMLReverbTypeDefaultsToNormalWhenOmitted confirms an omitted
// [global] reverbType defaults to "normal" (FDN), matching CLAUDE.md's
// sample MML.
func TestParseMMLReverbTypeDefaultsToNormalWhenOmitted(t *testing.T) {
	const mml = `[GoFMML File]
[global]
  tempo: 120
  sequenceID: DefaultTypeTest
  loop: false
  reverb: true
  reverbTime: 1.5
[partSetting]
part:
  - partNo: 1
    voiceID: 0
    volume: 127
    pan: 0
[part1]
C4
[part1End]
`
	seq, _, err := ParseMML([]byte(mml))
	if err != nil {
		t.Fatalf("ParseMML failed: %v", err)
	}
	if seq.ReverbType != "normal" {
		t.Fatalf("expected an omitted reverbType to default to \"normal\", got %q", seq.ReverbType)
	}
}

// TestParseMMLOneShotReverbSendRejectsNumericValue confirms a OneShot
// part's reverbSend must be the "note: level, ..." string form: a bare
// number (valid only for a Long part) is a minor error, warned and
// ignored rather than fatal.
func TestParseMMLOneShotReverbSendRejectsNumericValue(t *testing.T) {
	const mml = `[GoFMML File]
[global]
  tempo: 120
  sequenceID: BadOneShotSend
  loop: false
[pcmPartSetting]
pcmPart:
  - partNo: A
    voiceID: 0
    partType: oneShot
    volume: 100
    pan: 0
    reverbSend: 40
[pcmPartA]
C1 { X }
[pcmPartAEnd]
`
	seq, warnings, err := ParseMML([]byte(mml))
	if err != nil {
		t.Fatalf("ParseMML failed: %v", err)
	}
	if len(warnings) == 0 {
		t.Fatalf("expected a warning about the numeric reverbSend on a oneShot part")
	}
	if len(seq.PCMParts['A'].OneShotReverbSend) != 0 {
		t.Fatalf("expected no reverb send entries from a rejected numeric value, got %+v", seq.PCMParts['A'].OneShotReverbSend)
	}
}

// TestParseMMLCommentLinesInsidePartBlocks locks in CLAUDE.md's addendum
// that a "#"-led line is a comment anywhere in the MML data - including
// inside a [partN]/[pcmPartX] body, with arbitrary leading whitespace
// before the "#", and with "#" appearing again inside the comment's own
// text - without that comment ever being confused with the sharp-note "#"
// accidental used on an actual note command.
func TestParseMMLCommentLinesInsidePartBlocks(t *testing.T) {
	const mml = `[GoFMML File]

[global]
  tempo: 120
  sequenceID: CommentTest
  loop: false

[partSetting]
part:
  - partNo: 1
    voiceID: 0
    volume: 127
    pan: 0

[part1]
O4 C#4
  # a comment line indented with spaces, mentioning C#4 and D#5 as text
	# a comment line indented with a tab
# a flush-left comment
D#4
[part1End]

[pcmPartSetting]
pcmPart:
  - partNo: A
    voiceID: 0
    partType: oneShot
    volume: 100
    pan: 0

[pcmPartA]
C1 {
  # comment inside a oneShot block, not a note
  X
}
[pcmPartAEnd]
`
	seq, warnings, err := ParseMML([]byte(mml))
	if err != nil {
		t.Fatalf("ParseMML failed: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}

	part1 := seq.Parts[1]
	if len(part1.Events) != 2 {
		t.Fatalf("expected 2 note events (comments must not add/alter notes), got %d: %+v", len(part1.Events), part1.Events)
	}
	if got := part1.Events[0].Notes[0].NoteName; got != "C#4" {
		t.Errorf("first note = %q, want %q (comment text must not corrupt the preceding sharp note)", got, "C#4")
	}
	if got := part1.Events[1].Notes[0].NoteName; got != "D#4" {
		t.Errorf("second note = %q, want %q", got, "D#4")
	}

	oneShot := seq.PCMParts['A']
	if len(oneShot.Events) != 1 {
		t.Fatalf("expected 1 trigger event (comment inside the {} block must be ignored), got %d", len(oneShot.Events))
	}
}

// --- transpose (part/pcmPart "transpose", semitones, -36..36) ---

func TestParseMMLTransposeShiftsFMPartPitch(t *testing.T) {
	const mml = `[GoFMML File]
[global]
  tempo: 120
  sequenceID: TransposeTest
[partSetting]
part:
  - partNo: 1
    voiceID: 0
    volume: 127
    pan: 0
    transpose: -12
  - partNo: 2
    voiceID: 0
    volume: 127
    pan: 0
    transpose: 2
[part1]
C4
[part1End]
[part2]
B4
[part2End]
`
	seq, warnings, err := ParseMML([]byte(mml))
	if err != nil {
		t.Fatalf("ParseMML failed: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	if got := seq.Parts[1].Events[0].Notes[0].NoteName; got != "C3" {
		t.Errorf("transpose:-12 (one octave down) on C4 = %q, want C3", got)
	}
	if got := seq.Parts[2].Events[0].Notes[0].NoteName; got != "C#5" {
		t.Errorf("transpose:2 (whole tone up) on B4 = %q, want C#5 (crossing the octave boundary)", got)
	}
}

func TestParseMMLTransposeOutOfRangeIsIgnoredWithWarning(t *testing.T) {
	const mml = `[GoFMML File]
[global]
  tempo: 120
  sequenceID: TransposeRangeTest
[partSetting]
part:
  - partNo: 1
    voiceID: 0
    volume: 127
    pan: 0
    transpose: 37
[part1]
C4
[part1End]
`
	seq, warnings, err := ParseMML([]byte(mml))
	if err != nil {
		t.Fatalf("ParseMML failed: %v", err)
	}
	if len(warnings) == 0 {
		t.Fatalf("expected a warning about the out-of-range transpose")
	}
	if got := seq.Parts[1].Events[0].Notes[0].NoteName; got != "C4" {
		t.Errorf("out-of-range transpose should be ignored (no shift), got %q, want C4", got)
	}
}

func TestParseMMLTransposeAppliesToPCMLongButNotOneShot(t *testing.T) {
	const mml = `[GoFMML File]
[global]
  tempo: 120
  sequenceID: PCMTransposeTest
[pcmPartSetting]
pcmPart:
  - partNo: A
    voiceID: 0
    partType: long
    volume: 127
    pan: 0
    transpose: 12
  - partNo: B
    voiceID: 0
    partType: oneShot
    volume: 127
    pan: 0
    transpose: 12
[pcmPartA]
C4
[pcmPartAEnd]
[pcmPartB]
C4 { X }
[pcmPartBEnd]
`
	seq, warnings, err := ParseMML([]byte(mml))
	if err != nil {
		t.Fatalf("ParseMML failed: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	if got := seq.PCMParts['A'].Events[0].Notes[0].NoteName; got != "C5" {
		t.Errorf("PCM long transpose:12 (one octave up) on C4 = %q, want C5", got)
	}
	// A oneShot part's "note" selects which sample triggers, not a pitch, so
	// transpose has no defined meaning there - CLAUDE.md's example only
	// shows it on a Long part - and is simply not applied.
	if got := seq.PCMParts['B'].Events[0].OneShot.NoteNumber; got != 60 {
		t.Errorf("oneShot transpose must have no effect on the trigger note, got MIDI %d, want 60 (C4)", got)
	}
}

func TestParseMMLTransposePreservesPortamentoInterval(t *testing.T) {
	const mml = `[GoFMML File]
[global]
  tempo: 120
  sequenceID: TransposePortamentoTest
[partSetting]
part:
  - partNo: 1
    voiceID: 0
    volume: 127
    pan: 0
    transpose: 5
[part1]
L2 C &&4 E
[part1End]
`
	seq, warnings, err := ParseMML([]byte(mml))
	if err != nil {
		t.Fatalf("ParseMML failed: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	events := seq.Parts[1].Events
	if len(events) != 2 {
		t.Fatalf("expected 2 note events, got %d", len(events))
	}
	if got := events[0].Notes[0].NoteName; got != "F4" {
		t.Errorf("first note (C, transpose:5) = %q, want F4", got)
	}
	if got := events[1].Notes[0].NoteName; got != "A4" {
		t.Errorf("second note (E, transpose:5) = %q, want A4", got)
	}
	// C->E is a 4-semitone rise; transposing both ends by the same amount
	// must not change the portamento's glide distance.
	if got, want := events[1].Notes[0].Bend.AttackOffsetSemitones, -4.0; got != want {
		t.Errorf("portamento glide offset = %v, want %v (transpose must not distort the interval)", got, want)
	}
}

// --- [global] startOffset (whole notes, -> memory.SequenceData.StartOffsetTicks) ---

func TestParseMMLStartOffsetConvertsWholeNotesToTicks(t *testing.T) {
	const mml = `[GoFMML File]
[global]
  tempo: 120
  sequenceID: StartOffsetTest
  startOffset: 2
[partSetting]
part:
  - partNo: 1
    voiceID: 0
    volume: 127
    pan: 0
[part1]
C4
[part1End]
`
	seq, warnings, err := ParseMML([]byte(mml))
	if err != nil {
		t.Fatalf("ParseMML failed: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	whole, _ := common.NoteTypeTicks("NOTE1")
	if got, want := seq.StartOffsetTicks, whole*2; got != want {
		t.Errorf("StartOffsetTicks = %d, want %d (2 whole notes)", got, want)
	}
}

func TestParseMMLStartOffsetOmittedDefaultsToNoOffset(t *testing.T) {
	const mml = `[GoFMML File]
[global]
  tempo: 120
  sequenceID: NoOffsetTest
[partSetting]
part:
  - partNo: 1
    voiceID: 0
    volume: 127
    pan: 0
[part1]
C4
[part1End]
`
	seq, warnings, err := ParseMML([]byte(mml))
	if err != nil {
		t.Fatalf("ParseMML failed: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	if seq.StartOffsetTicks != 0 {
		t.Errorf("StartOffsetTicks = %d, want 0 when startOffset is omitted", seq.StartOffsetTicks)
	}
}

func TestParseMMLStartOffsetNegativeIsIgnoredWithWarning(t *testing.T) {
	const mml = `[GoFMML File]
[global]
  tempo: 120
  sequenceID: NegativeOffsetTest
  startOffset: -1
[partSetting]
part:
  - partNo: 1
    voiceID: 0
    volume: 127
    pan: 0
[part1]
C4
[part1End]
`
	seq, warnings, err := ParseMML([]byte(mml))
	if err != nil {
		t.Fatalf("ParseMML failed: %v", err)
	}
	if len(warnings) == 0 {
		t.Fatalf("expected a warning about the negative startOffset")
	}
	if seq.StartOffsetTicks != 0 {
		t.Errorf("StartOffsetTicks = %d, want 0 (invalid value ignored)", seq.StartOffsetTicks)
	}
}

// --- PCM parts expanded from 8 ('A'-'H') to 16 ('A'-'P') ---

func TestParseMMLAcceptsPCMPartPBeyondTheOldEightPartLimit(t *testing.T) {
	const mml = `[GoFMML File]
[global]
  tempo: 120
  sequenceID: SixteenPCMPartsTest
[pcmPartSetting]
pcmPart:
  - partNo: P
    voiceID: 0
    partType: oneShot
    volume: 100
    pan: 0
[pcmPartP]
C4 { X }
[pcmPartPEnd]
`
	seq, warnings, err := ParseMML([]byte(mml))
	if err != nil {
		t.Fatalf("ParseMML failed: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	if _, ok := seq.PCMParts['P']; !ok {
		t.Fatalf("expected pcmPart P (the 16th PCM part) to be accepted")
	}
}

func TestParseMMLRejectsPCMPartBeyondQInSettingsWithWarning(t *testing.T) {
	const mml = `[GoFMML File]
[global]
  tempo: 120
  sequenceID: SeventeenthPCMPartSettingTest
[pcmPartSetting]
pcmPart:
  - partNo: Q
    voiceID: 0
    partType: oneShot
    volume: 100
    pan: 0
`
	seq, warnings, err := ParseMML([]byte(mml))
	if err != nil {
		t.Fatalf("ParseMML failed: %v", err)
	}
	if len(warnings) == 0 {
		t.Fatalf("expected a warning about pcmPart Q being beyond the 16-part ('A'-'P') limit")
	}
	if _, ok := seq.PCMParts['Q']; ok {
		t.Fatalf("expected pcmPart Q to be ignored, not stored")
	}
}

func TestParseMMLRejectsPCMPartQSectionHeader(t *testing.T) {
	const mml = `[GoFMML File]
[global]
  tempo: 120
  sequenceID: SeventeenthPCMPartHeaderTest
[pcmPartQ]
C4 { X }
[pcmPartQEnd]
`
	_, _, err := ParseMML([]byte(mml))
	if err == nil {
		t.Fatalf("expected a fatal error for an opening [pcmPartQ] section header, beyond the 16-part ('A'-'P') limit")
	}
}
