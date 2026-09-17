/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package fileio

import (
	"embed"
	"testing"

	"github.com/megaak-soft/go-fmml/memory"
	"github.com/megaak-soft/go-fmml/pcmcore"
)

//go:embed testdata/voice.yaml testdata/song.mml testdata/pcmembed
var testdataFS embed.FS

func TestLoadFMVoiceFileFS(t *testing.T) {
	defer memory.ClearVoices()
	if err := LoadFMVoiceFileFS(testdataFS, "testdata/voice.yaml"); err != nil {
		t.Fatalf("LoadFMVoiceFileFS failed: %v", err)
	}
	if _, ok := memory.GetVoice(7); !ok {
		t.Fatalf("expected voice 7 to be stored in memory")
	}
}

func TestLoadMMLFileFS(t *testing.T) {
	defer memory.ClearSequences()
	if err := LoadMMLFileFS(testdataFS, "testdata/song.mml"); err != nil {
		t.Fatalf("LoadMMLFileFS failed: %v", err)
	}
	if _, ok := memory.GetSequence("EmbedTest"); !ok {
		t.Fatalf("expected sequence EmbedTest to be stored in memory")
	}
}

func TestLoadFMVoiceFileFSMissingPathErrors(t *testing.T) {
	if err := LoadFMVoiceFileFS(testdataFS, "testdata/does-not-exist.yaml"); err == nil {
		t.Fatalf("expected an error for a missing embedded path")
	}
}

// TestLoadPCMVoiceFileFSFileNameResolution covers both fileName forms
// LoadPCMVoiceFileFS accepts: a full path from fsys's own root (voiceID
// 20, "pcmembed/sounds/kick.wav") used as-is, and a bare file name with no
// "/" (voiceID 21, "kick.wav") resolved relative to the YAML file's own
// directory within fsys - see testdata/pcmembed/voice.yaml.
func TestLoadPCMVoiceFileFSFileNameResolution(t *testing.T) {
	defer memory.ClearPCMVoices()
	if err := LoadPCMVoiceFileFS(testdataFS, "testdata/pcmembed/voice.yaml"); err != nil {
		t.Fatalf("LoadPCMVoiceFileFS failed: %v", err)
	}

	fullPath, ok := memory.GetPCMVoice(20)
	if !ok || fullPath.VoiceType != pcmcore.OneShot {
		t.Fatalf("expected PCM voice 20 to be stored in memory")
	}
	if len(fullPath.Settings) != 1 || fullPath.Settings[0].Wave == nil {
		t.Fatalf("expected the embed-root-path kick.wav to be loaded, got %+v", fullPath.Settings)
	}

	bareName, ok := memory.GetPCMVoice(21)
	if !ok {
		t.Fatalf("expected PCM voice 21 to be stored in memory")
	}
	if len(bareName.Settings) != 1 || bareName.Settings[0].Wave == nil {
		t.Fatalf("expected the bare-filename kick.wav to be loaded, got %+v", bareName.Settings)
	}
}
