/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package pcmcore

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// buildWAV encodes a minimal mono 16-bit PCM WAV file at 44100Hz from the
// given samples (each in [-1,1]). If loopStart != loopEnd, a "smpl" chunk
// with a single sample loop is included.
func buildWAV(t *testing.T, samples []float32, loopStart, loopEnd int) []byte {
	t.Helper()
	var data bytes.Buffer
	for _, s := range samples {
		v := int16(s * 32767)
		binary.Write(&data, binary.LittleEndian, v)
	}

	var buf bytes.Buffer
	buf.WriteString("RIFF")
	binary.Write(&buf, binary.LittleEndian, uint32(0)) // placeholder, fixed up below
	buf.WriteString("WAVE")

	buf.WriteString("fmt ")
	binary.Write(&buf, binary.LittleEndian, uint32(16))
	binary.Write(&buf, binary.LittleEndian, uint16(1))       // PCM
	binary.Write(&buf, binary.LittleEndian, uint16(1))       // mono
	binary.Write(&buf, binary.LittleEndian, uint32(44100))   // sample rate
	binary.Write(&buf, binary.LittleEndian, uint32(44100*2)) // byte rate
	binary.Write(&buf, binary.LittleEndian, uint16(2))       // block align
	binary.Write(&buf, binary.LittleEndian, uint16(16))      // bits per sample

	if loopStart != loopEnd {
		buf.WriteString("smpl")
		binary.Write(&buf, binary.LittleEndian, uint32(36+24))
		for i := 0; i < 7; i++ {
			binary.Write(&buf, binary.LittleEndian, uint32(0)) // manufacturer..smpteOffset
		}
		binary.Write(&buf, binary.LittleEndian, uint32(1)) // numSampleLoops
		binary.Write(&buf, binary.LittleEndian, uint32(0)) // samplerData
		binary.Write(&buf, binary.LittleEndian, uint32(0)) // cuePointID
		binary.Write(&buf, binary.LittleEndian, uint32(0)) // type
		binary.Write(&buf, binary.LittleEndian, uint32(loopStart))
		binary.Write(&buf, binary.LittleEndian, uint32(loopEnd-1)) // inclusive end
		binary.Write(&buf, binary.LittleEndian, uint32(0))         // fraction
		binary.Write(&buf, binary.LittleEndian, uint32(0))         // playCount
	}

	buf.WriteString("data")
	binary.Write(&buf, binary.LittleEndian, uint32(data.Len()))
	buf.Write(data.Bytes())

	out := buf.Bytes()
	binary.LittleEndian.PutUint32(out[4:8], uint32(len(out)-8))
	return out
}

func TestParseWAVDecodesMonoPCM16(t *testing.T) {
	samples := []float32{0, 0.5, -0.5, 1, -1}
	raw := buildWAV(t, samples, 0, 0)

	wav, err := parseWAV(raw)
	if err != nil {
		t.Fatalf("parseWAV failed: %v", err)
	}
	if wav.SampleRate != 44100 {
		t.Fatalf("expected 44100Hz, got %d", wav.SampleRate)
	}
	if len(wav.Samples) != len(samples) {
		t.Fatalf("expected %d samples, got %d", len(samples), len(wav.Samples))
	}
	for i, s := range samples {
		if diff := float64(wav.Samples[i] - s); diff > 0.001 || diff < -0.001 {
			t.Fatalf("sample %d: expected ~%f, got %f", i, s, wav.Samples[i])
		}
	}
	if wav.HasLoop {
		t.Fatalf("expected no loop when no smpl chunk was written")
	}
}

func TestParseWAVDecodesLoopPoints(t *testing.T) {
	samples := make([]float32, 100)
	raw := buildWAV(t, samples, 10, 90)

	wav, err := parseWAV(raw)
	if err != nil {
		t.Fatalf("parseWAV failed: %v", err)
	}
	if !wav.HasLoop {
		t.Fatalf("expected a loop to be parsed from the smpl chunk")
	}
	if wav.LoopStart != 10 || wav.LoopEnd != 90 {
		t.Fatalf("expected loop [10,90), got [%d,%d)", wav.LoopStart, wav.LoopEnd)
	}
}

func TestParseWAVRejectsStereo(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString("RIFF")
	binary.Write(&buf, binary.LittleEndian, uint32(0))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	binary.Write(&buf, binary.LittleEndian, uint32(16))
	binary.Write(&buf, binary.LittleEndian, uint16(1))
	binary.Write(&buf, binary.LittleEndian, uint16(2)) // stereo
	binary.Write(&buf, binary.LittleEndian, uint32(44100))
	binary.Write(&buf, binary.LittleEndian, uint32(44100*4))
	binary.Write(&buf, binary.LittleEndian, uint16(4))
	binary.Write(&buf, binary.LittleEndian, uint16(16))
	buf.WriteString("data")
	binary.Write(&buf, binary.LittleEndian, uint32(4))
	buf.Write([]byte{0, 0, 0, 0})
	out := buf.Bytes()
	binary.LittleEndian.PutUint32(out[4:8], uint32(len(out)-8))

	if _, err := parseWAV(out); err == nil {
		t.Fatalf("expected an error for a stereo WAV file")
	}
}

func TestLoadWAVMissingFileErrors(t *testing.T) {
	if _, err := LoadWAV(filepath.Join(t.TempDir(), "missing.wav")); err == nil {
		t.Fatalf("expected an error for a missing file")
	}
}

// buildWAVAtRate is buildWAV with a caller-chosen sample rate, for testing
// resampling of real-world assets that don't land exactly on 44100Hz (see
// this repo's actual kick.wav/snare.wav, which are 48kHz).
func buildWAVAtRate(t *testing.T, samples []float32, rate uint32) []byte {
	t.Helper()
	var data bytes.Buffer
	for _, s := range samples {
		v := int16(s * 32767)
		binary.Write(&data, binary.LittleEndian, v)
	}

	var buf bytes.Buffer
	buf.WriteString("RIFF")
	binary.Write(&buf, binary.LittleEndian, uint32(0))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	binary.Write(&buf, binary.LittleEndian, uint32(16))
	binary.Write(&buf, binary.LittleEndian, uint16(1))
	binary.Write(&buf, binary.LittleEndian, uint16(1))
	binary.Write(&buf, binary.LittleEndian, rate)
	binary.Write(&buf, binary.LittleEndian, rate*2)
	binary.Write(&buf, binary.LittleEndian, uint16(2))
	binary.Write(&buf, binary.LittleEndian, uint16(16))
	buf.WriteString("data")
	binary.Write(&buf, binary.LittleEndian, uint32(data.Len()))
	buf.Write(data.Bytes())

	out := buf.Bytes()
	binary.LittleEndian.PutUint32(out[4:8], uint32(len(out)-8))
	return out
}

func TestParseWAVResamplesNonNativeSampleRate(t *testing.T) {
	samples := make([]float32, 480) // 10ms at 48000Hz
	for i := range samples {
		samples[i] = 1
	}
	raw := buildWAVAtRate(t, samples, 48000)

	wav, err := parseWAV(raw)
	if err != nil {
		t.Fatalf("parseWAV failed: %v", err)
	}
	if wav.SampleRate != 44100 {
		t.Fatalf("expected resampled output at 44100Hz, got %d", wav.SampleRate)
	}
	wantLen := 480 * 44100 / 48000
	if diff := len(wav.Samples) - wantLen; diff < -1 || diff > 1 {
		t.Fatalf("expected ~%d resampled frames (10ms @ 44100Hz), got %d", wantLen, len(wav.Samples))
	}
}

func TestLoadWAVFromDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wav")
	raw := buildWAV(t, []float32{0, 1, -1}, 0, 0)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}
	wav, err := LoadWAV(path)
	if err != nil {
		t.Fatalf("LoadWAV failed: %v", err)
	}
	if len(wav.Samples) != 3 {
		t.Fatalf("expected 3 samples, got %d", len(wav.Samples))
	}
}
