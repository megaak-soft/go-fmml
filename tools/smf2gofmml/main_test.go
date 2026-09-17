/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package main

import (
	"testing"

	"github.com/megaak-soft/go-fmml/analyze"
	"github.com/megaak-soft/go-fmml/pcmcore"
	"github.com/megaak-soft/go-fmml/tools/smf2gofmml/convert"
	"github.com/megaak-soft/go-fmml/tools/smf2gofmml/midiparse"
)

// writeVarLen appends v as a MIDI variable-length quantity.
func writeVarLen(buf []byte, v int) []byte {
	var stack []byte
	stack = append(stack, byte(v&0x7F))
	v >>= 7
	for v > 0 {
		stack = append(stack, byte(v&0x7F)|0x80)
		v >>= 7
	}
	for i := len(stack) - 1; i >= 0; i-- {
		buf = append(buf, stack[i])
	}
	return buf
}

func metaEvent(delta int, metaType byte, payload []byte) []byte {
	var b []byte
	b = writeVarLen(b, delta)
	b = append(b, 0xFF, metaType)
	b = writeVarLen(b, len(payload))
	b = append(b, payload...)
	return b
}

func noteOn(delta int, channel, note, vel byte) []byte {
	b := writeVarLen(nil, delta)
	return append(b, 0x90|channel, note, vel)
}

func noteOff(delta int, channel, note byte) []byte {
	b := writeVarLen(nil, delta)
	return append(b, 0x80|channel, note, 0)
}

func trackChunk(events ...[]byte) []byte {
	var data []byte
	for _, e := range events {
		data = append(data, e...)
	}
	data = append(data, metaEvent(0, 0x2F, nil)...)
	chunk := []byte("MTrk")
	chunk = append(chunk, byte(len(data)>>24), byte(len(data)>>16), byte(len(data)>>8), byte(len(data)))
	return append(chunk, data...)
}

// buildTestSMF assembles a minimal Format-1 SMF with a conductor track
// (name + tempo) plus one [FM], one [PCM long], and one [PCM oneShot] track,
// at division=96 ticks/quarter (deliberately not go-fmml's own 48-tick
// resolution, to exercise the tick-scaling path).
func buildTestSMF(t *testing.T) []byte {
	t.Helper()
	const division = 96

	header := []byte("MThd")
	header = append(header, 0, 0, 0, 6, 0, 1, 0, 4, byte(division>>8), byte(division))

	conductor := trackChunk(
		metaEvent(0, 0x03, []byte("Unit Test Song")),
		metaEvent(0, 0x51, []byte{0x07, 0xA1, 0x20}), // 500000us -> 120 BPM
	)

	fmTrack := trackChunk(
		metaEvent(0, 0x03, []byte("[FM] Lead")),
		noteOn(0, 0, 60, 100), // C4, quarter
		noteOff(division, 0, 60),
		noteOn(0, 0, 62, 100), // D4, eighth
		noteOff(division/2, 0, 62),
		noteOn(0, 0, 64, 90), // chord: E4+G4, quarter
		noteOn(0, 0, 67, 90),
		noteOff(division, 0, 64),
		noteOff(0, 0, 67),
	)

	pcmLongTrack := trackChunk(
		metaEvent(0, 0x03, []byte("[PCM long] Bass")),
		noteOn(0, 1, 48, 100), // C3, half note
		noteOff(division*2, 1, 48),
	)

	pcmOneShotTrack := trackChunk(
		metaEvent(0, 0x03, []byte("[PCM oneShot] Drums")),
		noteOn(0, 9, 36, 120), // kick on beat 1
		noteOff(1, 9, 36),
		noteOn(division-1, 9, 40, 100), // snare on beat 2
		noteOff(1, 9, 40),
		noteOn(division-1, 9, 36, 120), // kick on beat 3
		noteOff(1, 9, 36),
	)

	var smf []byte
	smf = append(smf, header...)
	smf = append(smf, conductor...)
	smf = append(smf, fmTrack...)
	smf = append(smf, pcmLongTrack...)
	smf = append(smf, pcmOneShotTrack...)
	return smf
}

func TestConvertToMMLRoundTrip(t *testing.T) {
	data := buildTestSMF(t)

	res, err := midiparse.ParseSMF(data)
	if err != nil {
		t.Fatalf("ParseSMF failed: %v", err)
	}
	if res.SongName != "Unit Test Song" {
		t.Errorf("SongName = %q, want %q", res.SongName, "Unit Test Song")
	}
	if res.TempoBPM < 119.9 || res.TempoBPM > 120.1 {
		t.Errorf("TempoBPM = %v, want ~120", res.TempoBPM)
	}

	mml, warnings, err := convert.ConvertToMML(res)
	if err != nil {
		t.Fatalf("ConvertToMML failed: %v (warnings: %v)", err, warnings)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}

	seq, parseWarnings, err := analyze.ParseMML([]byte(mml))
	if err != nil {
		t.Fatalf("generated MML failed to parse: %v\n--- MML ---\n%s", err, mml)
	}
	if len(parseWarnings) != 0 {
		t.Errorf("generated MML produced parser warnings: %v\n--- MML ---\n%s", parseWarnings, mml)
	}

	if seq.Tempo != 120 {
		t.Errorf("seq.Tempo = %v, want 120", seq.Tempo)
	}
	if len(seq.Parts) != 1 {
		t.Errorf("len(seq.Parts) = %d, want 1 (one [FM] track)", len(seq.Parts))
	}
	if len(seq.PCMParts) != 2 {
		t.Errorf("len(seq.PCMParts) = %d, want 2 (one long, one oneShot)", len(seq.PCMParts))
	}
	for pid, p := range seq.PCMParts {
		if p.PartType == pcmcore.OneShot && len(p.Events) != 3 {
			t.Errorf("oneShot part %c: got %d trigger events, want 3", pid, len(p.Events))
		}
	}
}

func TestSanitizeFileName(t *testing.T) {
	if got := sanitizeFileName(`Song: "Title"?`); got != "Song_ _Title__" {
		t.Errorf("sanitizeFileName = %q", got)
	}
	if got := sanitizeFileName(""); got != "untitled" {
		t.Errorf("sanitizeFileName(\"\") = %q, want \"untitled\"", got)
	}
}
