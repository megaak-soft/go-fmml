/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package fileio

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/megaak-soft/go-fmml/memory"
	"github.com/megaak-soft/go-fmml/pcmcore"
)

// writeTestWAV writes a minimal mono 16-bit PCM WAV file at 44100Hz.
func writeTestWAV(t *testing.T, path string, samples []int16) {
	t.Helper()
	var data bytes.Buffer
	for _, s := range samples {
		binary.Write(&data, binary.LittleEndian, s)
	}

	var buf bytes.Buffer
	buf.WriteString("RIFF")
	binary.Write(&buf, binary.LittleEndian, uint32(0))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	binary.Write(&buf, binary.LittleEndian, uint32(16))
	binary.Write(&buf, binary.LittleEndian, uint16(1))
	binary.Write(&buf, binary.LittleEndian, uint16(1))
	binary.Write(&buf, binary.LittleEndian, uint32(44100))
	binary.Write(&buf, binary.LittleEndian, uint32(44100*2))
	binary.Write(&buf, binary.LittleEndian, uint16(2))
	binary.Write(&buf, binary.LittleEndian, uint16(16))
	buf.WriteString("data")
	binary.Write(&buf, binary.LittleEndian, uint32(data.Len()))
	buf.Write(data.Bytes())

	out := buf.Bytes()
	binary.LittleEndian.PutUint32(out[4:8], uint32(len(out)-8))
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatalf("failed to write WAV fixture: %v", err)
	}
}

const testPCMVoiceYAML = `
pcmVoice:
  - voiceID: 5
    voiceType: oneShot
    loop: false
    voiceSetting:
      - fileName: kick.wav
        note: C1
        volume: 100
        pan: 0
`

func TestLoadPCMVoiceFile(t *testing.T) {
	defer memory.ClearPCMVoices()
	dir := t.TempDir()
	writeTestWAV(t, filepath.Join(dir, "kick.wav"), []int16{100, -100, 200})
	yamlPath := filepath.Join(dir, "voice.yaml")
	if err := os.WriteFile(yamlPath, []byte(testPCMVoiceYAML), 0o644); err != nil {
		t.Fatalf("failed to write yaml fixture: %v", err)
	}

	if err := LoadPCMVoiceFile(yamlPath); err != nil {
		t.Fatalf("LoadPCMVoiceFile failed: %v", err)
	}

	voice, ok := memory.GetPCMVoice(5)
	if !ok {
		t.Fatalf("expected PCM voice 5 to be stored in memory")
	}
	if voice.VoiceType != pcmcore.OneShot {
		t.Fatalf("expected OneShot voiceType")
	}
	if len(voice.Settings) != 1 || voice.Settings[0].Wave == nil {
		t.Fatalf("expected the kick.wav sample to be loaded, got %+v", voice.Settings)
	}
	if len(voice.Settings[0].Wave.Samples) != 3 {
		t.Fatalf("expected 3 decoded samples, got %d", len(voice.Settings[0].Wave.Samples))
	}
}

// TestLoadPCMVoiceFileAbsolutePathFileName covers the case where a
// voiceSetting's fileName is an absolute path pointing somewhere other
// than the YAML file's own directory - it should be used as-is, not
// resolved relative to the YAML file.
func TestLoadPCMVoiceFileAbsolutePathFileName(t *testing.T) {
	defer memory.ClearPCMVoices()
	yamlDir := t.TempDir()
	wavDir := t.TempDir() // deliberately a different directory than yamlDir

	wavPath := filepath.Join(wavDir, "kick.wav")
	writeTestWAV(t, wavPath, []int16{100, -100, 200})

	yaml := "\npcmVoice:\n  - voiceID: 6\n    voiceType: oneShot\n    loop: false\n    voiceSetting:\n      - fileName: " + wavPath + "\n        note: C1\n        volume: 100\n        pan: 0\n"
	yamlPath := filepath.Join(yamlDir, "voice.yaml")
	if err := os.WriteFile(yamlPath, []byte(yaml), 0o644); err != nil {
		t.Fatalf("failed to write yaml fixture: %v", err)
	}

	if err := LoadPCMVoiceFile(yamlPath); err != nil {
		t.Fatalf("LoadPCMVoiceFile failed: %v", err)
	}

	voice, ok := memory.GetPCMVoice(6)
	if !ok {
		t.Fatalf("expected PCM voice 6 to be stored in memory")
	}
	if len(voice.Settings) != 1 || voice.Settings[0].Wave == nil {
		t.Fatalf("expected the absolute-path kick.wav to be loaded, got %+v", voice.Settings)
	}
	if len(voice.Settings[0].Wave.Samples) != 3 {
		t.Fatalf("expected 3 decoded samples, got %d", len(voice.Settings[0].Wave.Samples))
	}
}

func TestLoadPCMVoiceFileMissingWAVErrors(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "voice.yaml")
	if err := os.WriteFile(yamlPath, []byte(testPCMVoiceYAML), 0o644); err != nil {
		t.Fatalf("failed to write yaml fixture: %v", err)
	}
	// kick.wav intentionally not written.
	if err := LoadPCMVoiceFile(yamlPath); err == nil {
		t.Fatalf("expected an error when a referenced WAV file is missing")
	}
}

func TestLoadPCMVoiceFileMissingYAMLErrors(t *testing.T) {
	if err := LoadPCMVoiceFile(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Fatalf("expected an error for a missing YAML file")
	}
}
