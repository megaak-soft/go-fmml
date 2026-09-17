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

const testFMVoiceYAML = `
fmVoice:
  - voiceID: 42
    algorithm: 1
    feedback: 5
    operators:
      - multiple: 1
        totallevel: 20
        detuneCents: 0
        envelope: "31, 10, 0.6, 4, 12"
      - multiple: 2
        totallevel: 40
        detuneCents: 0
        envelope: "31, 12, 0.3, 4, 12"
      - multiple: 1
        totallevel: 30
        detuneCents: 0
        envelope: "31, 10, 0.5, 4, 12"
      - multiple: 1
        totallevel: 0
        detuneCents: 0
        envelope: "31, 8, 0.7, 2, 10"
`

func TestLoadFMVoiceFile(t *testing.T) {
	defer memory.ClearVoices()
	dir := t.TempDir()
	path := filepath.Join(dir, "voice.yaml")
	if err := os.WriteFile(path, []byte(testFMVoiceYAML), 0o644); err != nil {
		t.Fatalf("failed to write test fixture: %v", err)
	}

	if err := LoadFMVoiceFile(path); err != nil {
		t.Fatalf("LoadFMVoiceFile failed: %v", err)
	}
	v, ok := memory.GetVoice(42)
	if !ok {
		t.Fatalf("expected voice 42 to be stored in memory")
	}
	if v.Algorithm != 1 || v.Feedback != 5 {
		t.Fatalf("unexpected voice: %+v", v)
	}
}

func TestLoadFMVoiceFileMissingFileErrors(t *testing.T) {
	if err := LoadFMVoiceFile(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Fatalf("expected an error for a missing file")
	}
}

const testMML = `[GoFMML File]
[global]
  tempo: 120
  sequenceID: FileIOTest
  loop: false
[partSetting]
part:
  - partNo: 1
    voiceID: 0
    volume: 127
    pan: 0
[part1]
C4D4E4
[part1End]
`

func TestLoadMMLFile(t *testing.T) {
	defer memory.ClearSequences()
	dir := t.TempDir()
	path := filepath.Join(dir, "song.mml")
	if err := os.WriteFile(path, []byte(testMML), 0o644); err != nil {
		t.Fatalf("failed to write test fixture: %v", err)
	}

	if err := LoadMMLFile(path); err != nil {
		t.Fatalf("LoadMMLFile failed: %v", err)
	}
	seq, ok := memory.GetSequence("FileIOTest")
	if !ok {
		t.Fatalf("expected sequence FileIOTest to be stored in memory")
	}
	if seq.Tempo != 120 {
		t.Fatalf("unexpected tempo: %v", seq.Tempo)
	}
}
