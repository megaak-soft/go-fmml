/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package convert

import (
	"fmt"
	"strings"
	"testing"

	"github.com/megaak-soft/go-fmml/tools/smf2gofmml/midiparse"
)

func TestClassifyTrackKind(t *testing.T) {
	cases := []struct {
		name string
		want trackKind
	}{
		{"[FM] Piano", kindFM},
		{"[PCM long] Strings", kindPCMLong},
		{"[PCM oneShot] Kit", kindPCMOneShot},
		{"[PCM oneShot ]Drum", kindPCMOneShot}, // real-world stray space before ']'
		{"[ PCM  long ] Strings", kindPCMLong},
		{"Untitled Track", kindFM},
		{"", kindFM},
	}
	for _, c := range cases {
		if got := classifyTrackKind(c.name); got != c.want {
			t.Errorf("classifyTrackKind(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestStripTrackTag(t *testing.T) {
	cases := []struct{ name, want string }{
		{"[FM] FM Bass", "FM Bass"},
		{"[PCM long] Strings", "Strings"},
		{"[PCM oneShot ]Drum", "Drum"},
		{"[FM]", ""},
		{"Untitled Track", "Untitled Track"},
		{"", ""},
	}
	for _, c := range cases {
		if got := stripTrackTag(c.name); got != c.want {
			t.Errorf("stripTrackTag(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestSanitizeComment(t *testing.T) {
	if got := sanitizeComment("Line1\r\nLine2\nLine3"); got != "Line1 Line2 Line3" {
		t.Errorf("sanitizeComment = %q", got)
	}
}

// TestConvertToMMLImportsUpToSixteenPCMParts locks in the PCM part import
// limit's expansion from 8 ('A'-'H') to 16 ('A'-'P'): 17 PCM-tagged tracks
// should yield exactly 16 imported parts (lettered A through P) plus a
// warning that the 17th was skipped for exceeding the limit.
func TestConvertToMMLImportsUpToSixteenPCMParts(t *testing.T) {
	res := &midiparse.SMFResult{Division: 480, TempoBPM: 120, SongName: "SixteenPCMTest"}
	for i := 0; i < 17; i++ {
		res.Tracks = append(res.Tracks, midiparse.ParsedTrack{
			Name:  fmt.Sprintf("[PCM long] Track%d", i),
			Notes: []midiparse.NoteEvent{{OnTick: 0, OffTick: 480, Note: 60}},
		})
	}

	mml, warnings, err := ConvertToMML(res)
	if err != nil {
		t.Fatalf("ConvertToMML failed: %v", err)
	}
	if strings.Count(mml, "partType: long") != 16 {
		t.Errorf("expected exactly 16 imported PCM parts, got %d in:\n%s", strings.Count(mml, "partType: long"), mml)
	}
	if !strings.Contains(mml, "partNo: P") {
		t.Errorf("expected the 16th PCM part to be lettered P, got:\n%s", mml)
	}
	if strings.Contains(mml, "partNo: Q") {
		t.Errorf("expected no 17th PCM part (Q), got:\n%s", mml)
	}
	foundWarning := false
	for _, w := range warnings {
		if strings.Contains(w, "PCM part limit (16) reached") {
			foundWarning = true
		}
	}
	if !foundWarning {
		t.Errorf("expected a warning that the 17th PCM track was skipped, got %v", warnings)
	}
}

// TestBuildOneShotBodyExtendsToSharedTrackEndNotAFixedPad locks in a fix
// for a real bug found from a user's actual converted MIDI: every pitch's
// LAST onset used to get a flat +MinUnitTicks() pad regardless of how far
// it actually was from the track's true end, inflating that pitch's own
// reported length with fabricated trailing silence (and, had the padded
// pitch been the part with the overall longest length, would have inflated
// memory.SequenceData's whole loop point too - see buildOneShotBody's own
// doc comment). Only the pitch whose own last onset truly is the track's
// last onset should need the minimal floor; every other pitch's final
// segment must reach the shared trackEnd exactly.
func TestBuildOneShotBodyExtendsToSharedTrackEndNotAFixedPad(t *testing.T) {
	// C4 (MIDI 60): one onset at tick 0 only.
	// D4 (MIDI 62): onsets at tick 0 and tick 96 - the track's true last onset.
	notes := []midiparse.NoteEvent{
		{OnTick: 0, OffTick: 10, Note: 60},
		{OnTick: 0, OffTick: 10, Note: 62},
		{OnTick: 96, OffTick: 106, Note: 62},
	}
	body := buildOneShotBody(notes, 1.0)

	if !strings.Contains(body, "C4{ V127 X2 }") {
		t.Errorf("C4's only onset should extend the full 96 ticks to trackEnd (X2, a half note), got: %q", body)
	}
	if !strings.Contains(body, "X2 X32 }") {
		t.Errorf("D4's last onset (which IS trackEnd) should fall back to the minimal X32 floor only, got: %q", body)
	}
	if strings.Count(body, "X32") != 1 {
		t.Errorf("expected exactly 1 minimal-floor trigger (only the pitch that defines trackEnd), got %d in: %q", strings.Count(body, "X32"), body)
	}
}

func TestCC10ToPan16(t *testing.T) {
	if p := cc10ToPan16(0); p != -16 {
		t.Errorf("cc10ToPan16(0) = %d, want -16", p)
	}
	if p := cc10ToPan16(127); p != 16 {
		t.Errorf("cc10ToPan16(127) = %d, want 16", p)
	}
	if p := cc10ToPan16(64); p < -1 || p > 1 {
		t.Errorf("cc10ToPan16(64) = %d, want ~0", p)
	}
}
