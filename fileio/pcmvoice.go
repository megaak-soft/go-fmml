/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package fileio

import (
	"embed"
	"fmt"
	"log"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"

	"github.com/megaak-soft/go-fmml/analyze"
	"github.com/megaak-soft/go-fmml/memory"
	"github.com/megaak-soft/go-fmml/pcmcore"
)

// LoadPCMVoiceFile reads and parses the PCM voice parameter YAML file at
// the given absolute path, then loads every WAV file its voiceSettings
// reference and stores each voice into go-fmml/memory. Each
// voiceSetting's fileName is expected to be an absolute path to its WAV
// file; a bare file name with no path (e.g. "kick.wav") is resolved
// relative to the YAML file's own directory instead, so samples that sit
// alongside it don't need to spell out their full path. Warnings (minor,
// recoverable parse issues) are logged; a fatal parse error, or any WAV
// file failing to load, is returned and nothing is stored.
func LoadPCMVoiceFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	return loadPCMVoiceData(data, func(fileName string) (*pcmcore.WAVData, error) {
		resolved := fileName
		if !filepath.IsAbs(fileName) {
			resolved = filepath.Join(dir, fileName)
		}
		return cachedLoadWAV(resolved, func() (*pcmcore.WAVData, error) { return pcmcore.LoadWAV(resolved) })
	})
}

// LoadPCMVoiceFileFS is LoadPCMVoiceFile's embed.FS counterpart, for PCM
// voice files (and their WAV samples) embedded into the host game binary
// via go:embed. Each voiceSetting's fileName is expected to be a full path
// from fsys's own root - i.e. relative to the directory the //go:embed
// directive itself lives in, not necessarily this YAML file's directory;
// a bare file name with no path is resolved relative to the YAML file's
// own directory within fsys instead, same as LoadPCMVoiceFile.
func LoadPCMVoiceFileFS(fsys embed.FS, path string) error {
	data, err := fsys.ReadFile(path)
	if err != nil {
		return err
	}
	dir := pathpkg.Dir(path)
	return loadPCMVoiceData(data, func(fileName string) (*pcmcore.WAVData, error) {
		resolved := fileName
		if !strings.Contains(fileName, "/") {
			resolved = pathpkg.Join(dir, fileName)
		}
		return cachedLoadWAV(resolved, func() (*pcmcore.WAVData, error) { return pcmcore.LoadWAVFS(fsys, resolved) })
	})
}

// SetPCMVoiceData parses PCM voice parameter data supplied directly as a Go
// string (Phase 7's addendum) instead of read from a file/embed.FS - e.g. a
// backtick-quoted multi-line literal laid out exactly like a voice YAML
// file's own contents. Since there's no source file location to resolve a
// bare "kick.wav"-style fileName against, every voiceSetting's fileName
// here is resolved exactly as os.ReadFile itself would: an absolute path
// is used as-is, and a relative one is resolved against the process's
// current working directory. Internal processing is otherwise identical to
// LoadPCMVoiceFile/LoadPCMVoiceFileFS: warnings are logged, a fatal parse
// error (including any WAV file failing to load) is returned and nothing
// is stored.
func SetPCMVoiceData(data string) error {
	return loadPCMVoiceData([]byte(data), func(fileName string) (*pcmcore.WAVData, error) {
		return cachedLoadWAV(fileName, func() (*pcmcore.WAVData, error) { return pcmcore.LoadWAV(fileName) })
	})
}

// cachedLoadWAV returns the WAV data previously cached under key (Phase
// 7's PCM-loading efficiency addendum: the same WAV file referenced again,
// even from a different voice or a different call to LoadPCMVoiceFile/
// LoadPCMVoiceFileFS/SetPCMVoiceData, is decoded only once), calling load
// and caching its result under key when nothing is cached yet.
func cachedLoadWAV(key string, load func() (*pcmcore.WAVData, error)) (*pcmcore.WAVData, error) {
	if wav, ok := memory.GetCachedWAV(key); ok {
		return wav, nil
	}
	wav, err := load()
	if err != nil {
		return nil, err
	}
	memory.CacheWAV(key, wav)
	return wav, nil
}

func loadPCMVoiceData(data []byte, loadWAV func(fileName string) (*pcmcore.WAVData, error)) error {
	defs, warnings, err := analyze.ParsePCMVoiceFile(data)
	for _, w := range warnings {
		log.Printf("fileio: PCM voice file warning: %s", w)
	}
	if err != nil {
		return err
	}
	for _, def := range defs {
		for i := range def.Voice.Settings {
			wav, err := loadWAV(def.Voice.Settings[i].FileName)
			if err != nil {
				return fmt.Errorf("fileio: failed to load WAV %q for PCM voiceID %d: %w", def.Voice.Settings[i].FileName, def.VoiceID, err)
			}
			def.Voice.Settings[i].Wave = wav
		}
		memory.StorePCMVoice(def.VoiceID, def.Voice)
	}
	return nil
}
