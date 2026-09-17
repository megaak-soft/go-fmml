/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package fileio

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/megaak-soft/go-fmml/memory"
)

// TestSetMMLTextData confirms MML data passed directly as a Go string
// (Phase 7's addendum, an alternative to LoadMMLFile) parses and stores
// exactly like a file with identical contents.
func TestSetMMLTextData(t *testing.T) {
	defer memory.ClearSequences()
	if err := SetMMLTextData(testMML); err != nil {
		t.Fatalf("SetMMLTextData failed: %v", err)
	}
	seq, ok := memory.GetSequence("FileIOTest")
	if !ok {
		t.Fatalf("expected sequence FileIOTest to be stored in memory")
	}
	if seq.Tempo != 120 {
		t.Fatalf("unexpected tempo: %v", seq.Tempo)
	}
}

// TestSetFMVoiceData is SetMMLTextData's FM-voice-parameter counterpart.
func TestSetFMVoiceData(t *testing.T) {
	defer memory.ClearVoices()
	if err := SetFMVoiceData(testFMVoiceYAML); err != nil {
		t.Fatalf("SetFMVoiceData failed: %v", err)
	}
	v, ok := memory.GetVoice(42)
	if !ok {
		t.Fatalf("expected voice 42 to be stored in memory")
	}
	if v.Algorithm != 1 || v.Feedback != 5 {
		t.Fatalf("unexpected voice: %+v", v)
	}
}

// TestSetPCMVoiceDataAbsolutePath is SetMMLTextData's PCM-voice-parameter
// counterpart: since a Go string has no source file directory to resolve a
// bare fileName against, this only exercises an absolute-path fileName
// (see SetPCMVoiceData's own doc comment).
func TestSetPCMVoiceDataAbsolutePath(t *testing.T) {
	defer memory.ClearPCMVoices()
	defer memory.ClearPCMWaveCache()
	wavPath := filepath.Join(t.TempDir(), "kick.wav")
	writeTestWAV(t, wavPath, []int16{1, 2, 3})

	yaml := "\npcmVoice:\n  - voiceID: 7\n    voiceType: oneShot\n    loop: false\n    voiceSetting:\n      - fileName: " + wavPath + "\n        note: C1\n        volume: 100\n        pan: 0\n"
	if err := SetPCMVoiceData(yaml); err != nil {
		t.Fatalf("SetPCMVoiceData failed: %v", err)
	}
	voice, ok := memory.GetPCMVoice(7)
	if !ok {
		t.Fatalf("expected PCM voice 7 to be stored in memory")
	}
	if len(voice.Settings) != 1 || voice.Settings[0].Wave == nil {
		t.Fatalf("expected the kick.wav sample to be loaded, got %+v", voice.Settings)
	}
}

// TestPCMWaveCacheReusesAlreadyLoadedWAV confirms Phase 7's PCM-loading
// efficiency addendum: a WAV file already decoded by an earlier
// LoadPCMVoiceFile/SetPCMVoiceData call is reused (the exact same *WAVData
// instance) rather than decoded again, when a later call references the
// same resolved file path - even from a completely different voiceID/YAML
// document.
func TestPCMWaveCacheReusesAlreadyLoadedWAV(t *testing.T) {
	defer memory.ClearPCMVoices()
	defer memory.ClearPCMWaveCache()

	dir := t.TempDir()
	writeTestWAV(t, filepath.Join(dir, "kick.wav"), []int16{10, 20, 30})
	yamlPath := filepath.Join(dir, "voice.yaml")
	if err := os.WriteFile(yamlPath, []byte(testPCMVoiceYAML), 0o644); err != nil {
		t.Fatalf("failed to write yaml fixture: %v", err)
	}

	if err := LoadPCMVoiceFile(yamlPath); err != nil {
		t.Fatalf("first LoadPCMVoiceFile failed: %v", err)
	}
	first, ok := memory.GetPCMVoice(5)
	if !ok || len(first.Settings) == 0 {
		t.Fatalf("expected voice 5 to be loaded with a sample")
	}

	// A second voice, different voiceID, referencing the exact same WAV
	// file by its resolved absolute path.
	yaml2 := "\npcmVoice:\n  - voiceID: 8\n    voiceType: oneShot\n    loop: false\n    voiceSetting:\n      - fileName: " + filepath.Join(dir, "kick.wav") + "\n        note: C1\n        volume: 100\n        pan: 0\n"
	if err := SetPCMVoiceData(yaml2); err != nil {
		t.Fatalf("SetPCMVoiceData failed: %v", err)
	}
	second, ok := memory.GetPCMVoice(8)
	if !ok || len(second.Settings) == 0 {
		t.Fatalf("expected voice 8 to be loaded with a sample")
	}

	if first.Settings[0].Wave != second.Settings[0].Wave {
		t.Fatalf("expected the cached WAV data to be reused (same *WAVData instance), got two distinct decodes")
	}
}

// TestClearPCMWaveCacheForcesAReload confirms ClearPCMWaveCache actually
// drops the cache: a WAV load after clearing produces a fresh *WAVData
// instance rather than reusing the old one.
func TestClearPCMWaveCacheForcesAReload(t *testing.T) {
	defer memory.ClearPCMVoices()
	defer memory.ClearPCMWaveCache()

	dir := t.TempDir()
	wavPath := filepath.Join(dir, "kick.wav")
	writeTestWAV(t, wavPath, []int16{1, 2, 3})
	yamlPath := filepath.Join(dir, "voice.yaml")
	if err := os.WriteFile(yamlPath, []byte(testPCMVoiceYAML), 0o644); err != nil {
		t.Fatalf("failed to write yaml fixture: %v", err)
	}

	if err := LoadPCMVoiceFile(yamlPath); err != nil {
		t.Fatalf("first LoadPCMVoiceFile failed: %v", err)
	}
	first, _ := memory.GetPCMVoice(5)

	memory.ClearPCMWaveCache()

	if err := LoadPCMVoiceFile(yamlPath); err != nil {
		t.Fatalf("second LoadPCMVoiceFile failed: %v", err)
	}
	second, _ := memory.GetPCMVoice(5)

	if first.Settings[0].Wave == second.Settings[0].Wave {
		t.Fatalf("expected ClearPCMWaveCache to force a fresh decode, got the same *WAVData instance")
	}
}
