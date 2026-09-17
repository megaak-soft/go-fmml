/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package analyze

import (
	"testing"

	"github.com/megaak-soft/go-fmml/memory"
	"github.com/megaak-soft/go-fmml/pcmcore"
)

const testPCMMML = `[GoFMML File]

[global]
  tempo: 120
  sequenceID: PCMTest
  loop: false

[partSetting]
part:
  - partNo: 1
    voiceID: 0
    volume: 127
    pan: 0

[part1]
C4D4
[part1End]

[pcmPartSetting]
pcmPart:
  - partNo: A
    voiceID: 0
    partType: oneShot
    volume: 127
    pan: 0
  - partNo: B
    voiceID: 1
    partType: long
    volume: 76
    pan: 16

[pcmPartA]
    C1 {
      V100 L4 X R X8 X8 X
    }
    E1 {
      V100 L4 R X R X
    }
[pcmPartAEnd]

[pcmPartB]
  V60 L2 O3
  G B C1
[pcmPartBEnd]
`

func TestParseMMLPCMPartSetting(t *testing.T) {
	seq, warnings, err := ParseMML([]byte(testPCMMML))
	if err != nil {
		t.Fatalf("ParseMML failed: %v (warnings: %v)", err, warnings)
	}
	if len(seq.PCMParts) != 2 {
		t.Fatalf("expected 2 PCM parts, got %d: %+v", len(seq.PCMParts), seq.PCMParts)
	}

	a, ok := seq.PCMParts['A']
	if !ok {
		t.Fatalf("expected pcmPart A to be present")
	}
	if a.PartType != pcmcore.OneShot || a.VoiceID != 0 {
		t.Fatalf("unexpected pcmPart A setting: %+v", a)
	}

	b, ok := seq.PCMParts['B']
	if !ok {
		t.Fatalf("expected pcmPart B to be present")
	}
	if b.PartType != pcmcore.Long || b.VoiceID != 1 || b.Pan != 1 { // pan:16 normalized to [-1,1]
		t.Fatalf("unexpected pcmPart B setting: %+v", b)
	}
}

func TestParseMMLPCMOneShotBlocksTriggerAtTickZero(t *testing.T) {
	seq, _, err := ParseMML([]byte(testPCMMML))
	if err != nil {
		t.Fatalf("ParseMML failed: %v", err)
	}
	a := seq.PCMParts['A']

	var kicks, snares int
	for _, ev := range a.Events {
		if ev.Kind != memory.PCMEventOneShotTrigger {
			t.Fatalf("expected only oneShot trigger events on a oneShot part, got kind %v", ev.Kind)
		}
		switch ev.OneShot.NoteNumber {
		case 24: // C1
			kicks++
		case 28: // E1
			snares++
		default:
			t.Fatalf("unexpected trigger note number %d", ev.OneShot.NoteNumber)
		}
	}
	// C1 block: X R X8 X8 X = 4 X triggers (R doesn't trigger)
	if kicks != 4 {
		t.Fatalf("expected 4 kick triggers, got %d", kicks)
	}
	// E1 block: R X R X = 2 triggers
	if snares != 2 {
		t.Fatalf("expected 2 snare triggers, got %d", snares)
	}

	// First event overall must start at tick 0 (each block starts its own
	// timeline at the part's start).
	if a.Events[0].AtTick != 0 {
		t.Fatalf("expected the first event to be at tick 0, got %d", a.Events[0].AtTick)
	}
}

func TestParseMMLPCMLongPartReusesFMGrammar(t *testing.T) {
	seq, _, err := ParseMML([]byte(testPCMMML))
	if err != nil {
		t.Fatalf("ParseMML failed: %v", err)
	}
	b := seq.PCMParts['B']
	if len(b.Events) != 3 {
		t.Fatalf("expected 3 note events (G, B, C1), got %d: %+v", len(b.Events), b.Events)
	}
	for _, ev := range b.Events {
		if ev.Kind != memory.PCMEventNote {
			t.Fatalf("expected PCMEventNote events on a long part, got kind %v", ev.Kind)
		}
		if len(ev.Notes) != 1 {
			t.Fatalf("expected exactly 1 note per event, got %d", len(ev.Notes))
		}
	}
	// "G B C1" at O3: G3, B3, then C1 - the trailing digit is a note
	// LENGTH (whole note), not an octave change, so this is still C3.
	if b.Events[0].Notes[0].NoteName != "G3" || b.Events[2].Notes[0].NoteName != "C3" {
		t.Fatalf("unexpected note names: %s, %s", b.Events[0].Notes[0].NoteName, b.Events[2].Notes[0].NoteName)
	}
	if b.Events[2].Notes[0].NoteType != "NOTE1" {
		t.Fatalf("expected C1 to parse as a whole note (NOTE1), got %s", b.Events[2].Notes[0].NoteType)
	}
}

func TestParseMMLPCMPartMissingSettingWarns(t *testing.T) {
	const mml = `[GoFMML File]
[global]
  tempo: 120
  sequenceID: NoSettingTest
  loop: false
[pcmPartA]
C1 {
  X
}
[pcmPartAEnd]
`
	seq, warnings, err := ParseMML([]byte(mml))
	if err != nil {
		t.Fatalf("ParseMML failed: %v", err)
	}
	if _, ok := seq.PCMParts['A']; !ok {
		t.Fatalf("expected pcmPart A to still be created with defaults")
	}
	found := false
	for _, w := range warnings {
		if w != "" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a warning about the missing [pcmPartSetting] entry")
	}
}
