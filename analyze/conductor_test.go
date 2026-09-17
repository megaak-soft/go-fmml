/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package analyze

import "testing"

func TestParseConductorCommandsTempoAndJumpPoint(t *testing.T) {
	// R1 R1 J T120 R4 T115 R4 T110 R4 T80 - matches CLAUDE.md's own worked
	// example: two whole-note rests (192 ticks each), then the jump point,
	// then a tempo ramp advancing a quarter note (48 ticks) between each
	// change.
	tempoMap, jumpTick, hasJump, warnings := parseConductorCommands("R1R1JT120R4T115R4T110R4T80")
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	if !hasJump {
		t.Fatalf("expected a jump point to be recorded")
	}
	if jumpTick != 384 { // 2 whole notes * 192 ticks/whole note
		t.Fatalf("expected jump point at tick 384, got %d", jumpTick)
	}
	wantTicks := []int{384, 432, 480, 528}
	wantBPM := []float64{120, 115, 110, 80}
	if len(tempoMap) != len(wantTicks) {
		t.Fatalf("expected %d tempo breakpoints, got %d: %+v", len(wantTicks), len(tempoMap), tempoMap)
	}
	for i, tp := range tempoMap {
		if tp.AtTick != wantTicks[i] || tp.BPM != wantBPM[i] {
			t.Fatalf("breakpoint %d: expected {%d,%v}, got %+v", i, wantTicks[i], wantBPM[i], tp)
		}
	}
}

func TestParseConductorCommandsMultipleJumpPointsWarnsAndKeepsFirst(t *testing.T) {
	_, jumpTick, hasJump, warnings := parseConductorCommands("R4JR4J")
	if !hasJump || jumpTick != 48 {
		t.Fatalf("expected the first jump point (tick 48) to be kept, got tick=%d hasJump=%v", jumpTick, hasJump)
	}
	if len(warnings) == 0 {
		t.Fatalf("expected a warning about the second 'J' being ignored")
	}
}

func TestParseConductorCommandsInvalidTempoWarnsAndIgnores(t *testing.T) {
	tempoMap, _, _, warnings := parseConductorCommands("T0R4T-1T140")
	if len(warnings) == 0 {
		t.Fatalf("expected warnings for the invalid tempo values")
	}
	if len(tempoMap) != 1 || tempoMap[0].BPM != 140 {
		t.Fatalf("expected only the valid T140 to produce a breakpoint, got %+v", tempoMap)
	}
}

const conductorStartTempoMML = `[GoFMML File]
[global]
  tempo: 999
  sequenceID: ConductorStart
  loop: false
[partSetting]
part:
  - partNo: 1
    voiceID: 0
[part1]
C4
[part1End]
[conductor]
[conductorPart]
  T140 R4 T100
[conductorPartEnd]
`

// TestParseMMLConductorTempoAtTickZeroOverridesGlobal covers CLAUDE.md's
// "コンダクターパートの先頭にテンポが記載している場合は[global]のテンポを
// 無視して、コンダクターパートのテンポを優先する": a conductor tempo
// command at tick 0 replaces [global]'s own tempo as the sequence's
// starting tempo, and later ones become mid-song tempo-map entries.
func TestParseMMLConductorTempoAtTickZeroOverridesGlobal(t *testing.T) {
	seq, warnings, err := ParseMML([]byte(conductorStartTempoMML))
	if err != nil {
		t.Fatalf("ParseMML failed: %v (warnings: %v)", err, warnings)
	}
	if seq.Tempo != 140 {
		t.Fatalf("expected the conductor's tick-0 tempo (140) to override [global]'s 999, got %v", seq.Tempo)
	}
	if len(seq.TempoMap) != 2 {
		t.Fatalf("expected a 2-entry tempo map, got %+v", seq.TempoMap)
	}
	if seq.TempoMap[0].AtTick != 0 || seq.TempoMap[0].BPM != 140 {
		t.Fatalf("expected the first breakpoint to be {0,140}, got %+v", seq.TempoMap[0])
	}
	if seq.TempoMap[1].AtTick != 48 || seq.TempoMap[1].BPM != 100 {
		t.Fatalf("expected the second breakpoint to be {48,100}, got %+v", seq.TempoMap[1])
	}
}

const conductorMidSongTempoMML = `[GoFMML File]
[global]
  tempo: 90
  sequenceID: ConductorMidSong
  loop: false
[partSetting]
part:
  - partNo: 1
    voiceID: 0
[part1]
C4
[part1End]
[conductor]
[conductorPart]
  R4 T150
[conductorPartEnd]
`

// TestParseMMLConductorMidSongTempoChangeKeepsGlobalAsStart covers the case
// where the conductor's first tempo change ISN'T at tick 0: [global]'s own
// tempo remains the sequence's starting tempo, and the tempo map gets a
// synthetic tick-0 breakpoint for it so every tick from 0 on is covered.
func TestParseMMLConductorMidSongTempoChangeKeepsGlobalAsStart(t *testing.T) {
	seq, warnings, err := ParseMML([]byte(conductorMidSongTempoMML))
	if err != nil {
		t.Fatalf("ParseMML failed: %v (warnings: %v)", err, warnings)
	}
	if seq.Tempo != 90 {
		t.Fatalf("expected [global]'s own tempo (90) to remain the start tempo, got %v", seq.Tempo)
	}
	if len(seq.TempoMap) != 2 {
		t.Fatalf("expected a 2-entry tempo map (synthetic tick-0 + the mid-song change), got %+v", seq.TempoMap)
	}
	if seq.TempoMap[0].AtTick != 0 || seq.TempoMap[0].BPM != 90 {
		t.Fatalf("expected a synthetic {0,90} breakpoint, got %+v", seq.TempoMap[0])
	}
	if seq.TempoMap[1].AtTick != 48 || seq.TempoMap[1].BPM != 150 {
		t.Fatalf("expected the mid-song change to be {48,150}, got %+v", seq.TempoMap[1])
	}
}

const conductorJumpPointMML = `[GoFMML File]
[global]
  tempo: 120
  sequenceID: ConductorJump
  loop: true
[partSetting]
part:
  - partNo: 1
    voiceID: 0
[part1]
C4D4
[part1End]
[conductor]
[conductorPart]
  R4 J
[conductorPartEnd]
`

// TestParseMMLConductorJumpPointSetsSequenceFields confirms a [conductor]
// 'J' command populates memory.SequenceData's JumpPointTick/HasJumpPoint.
func TestParseMMLConductorJumpPointSetsSequenceFields(t *testing.T) {
	seq, warnings, err := ParseMML([]byte(conductorJumpPointMML))
	if err != nil {
		t.Fatalf("ParseMML failed: %v (warnings: %v)", err, warnings)
	}
	if !seq.HasJumpPoint {
		t.Fatalf("expected HasJumpPoint to be true")
	}
	if seq.JumpPointTick != 48 {
		t.Fatalf("expected JumpPointTick 48, got %d", seq.JumpPointTick)
	}
}

// TestParseMMLWithoutConductorLeavesTempoMapEmpty confirms a plain MML file
// with no [conductor] section leaves TempoMap/JumpPointTick/HasJumpPoint at
// their zero values, so go-fmml/player's own single-tempo fallback
// applies unchanged.
func TestParseMMLWithoutConductorLeavesTempoMapEmpty(t *testing.T) {
	seq, _, err := ParseMML([]byte(sampleMML))
	if err != nil {
		t.Fatalf("ParseMML failed: %v", err)
	}
	if len(seq.TempoMap) != 0 {
		t.Fatalf("expected an empty TempoMap with no [conductor] section, got %+v", seq.TempoMap)
	}
	if seq.HasJumpPoint {
		t.Fatalf("expected HasJumpPoint to be false with no [conductor] section")
	}
}
