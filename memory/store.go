/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package memory

import (
	"sync"

	"github.com/megaak-soft/go-fmml/fmcore"
	"github.com/megaak-soft/go-fmml/pcmcore"
)

var (
	voiceMu   sync.RWMutex
	voices    = make(map[fmcore.VoiceID]fmcore.Voice)
	seqMu     sync.RWMutex
	sequences = make(map[string]*SequenceData)

	pcmVoiceMu sync.RWMutex
	pcmVoices  = make(map[pcmcore.VoiceID]pcmcore.Voice)

	wavCacheMu sync.RWMutex
	wavCache   = make(map[string]*pcmcore.WAVData)
)

// StoreVoice registers a parsed FM voice under id, overwriting any existing
// voice with the same id.
func StoreVoice(id fmcore.VoiceID, voice fmcore.Voice) {
	voiceMu.Lock()
	defer voiceMu.Unlock()
	voices[id] = voice
}

// GetVoice looks up a previously stored FM voice by id.
func GetVoice(id fmcore.VoiceID) (fmcore.Voice, bool) {
	voiceMu.RLock()
	defer voiceMu.RUnlock()
	v, ok := voices[id]
	return v, ok
}

// ClearVoices deletes every stored FM voice, freeing the voice bank.
func ClearVoices() {
	voiceMu.Lock()
	defer voiceMu.Unlock()
	voices = make(map[fmcore.VoiceID]fmcore.Voice)
}

// StoreSequence registers a parsed sequence under its SequenceID,
// overwriting any existing sequence with the same id.
func StoreSequence(seq *SequenceData) {
	seqMu.Lock()
	defer seqMu.Unlock()
	sequences[seq.SequenceID] = seq
}

// GetSequence looks up a previously stored sequence by SequenceID.
func GetSequence(id string) (*SequenceData, bool) {
	seqMu.RLock()
	defer seqMu.RUnlock()
	s, ok := sequences[id]
	return s, ok
}

// ClearSequences deletes every stored sequence, freeing the sequence store.
func ClearSequences() {
	seqMu.Lock()
	defer seqMu.Unlock()
	sequences = make(map[string]*SequenceData)
}

// StorePCMVoice registers a parsed PCM voice under id, overwriting any
// existing voice with the same id.
func StorePCMVoice(id pcmcore.VoiceID, voice pcmcore.Voice) {
	pcmVoiceMu.Lock()
	defer pcmVoiceMu.Unlock()
	pcmVoices[id] = voice
}

// GetPCMVoice looks up a previously stored PCM voice by id.
func GetPCMVoice(id pcmcore.VoiceID) (pcmcore.Voice, bool) {
	pcmVoiceMu.RLock()
	defer pcmVoiceMu.RUnlock()
	v, ok := pcmVoices[id]
	return v, ok
}

// ClearPCMVoices deletes every stored PCM voice, freeing the PCM voice bank.
func ClearPCMVoices() {
	pcmVoiceMu.Lock()
	defer pcmVoiceMu.Unlock()
	pcmVoices = make(map[pcmcore.VoiceID]pcmcore.Voice)
}

// CacheWAV stores a decoded WAV sample under key (its resolved file path),
// so a later LoadPCMVoiceFile/LoadPCMVoiceFileFS/SetPCMVoiceData call
// referencing the exact same file can reuse it instead of decoding the WAV
// data again (Phase 7's PCM-loading efficiency addendum - see
// go-fmml/fileio's loadPCMVoiceData).
func CacheWAV(key string, wav *pcmcore.WAVData) {
	wavCacheMu.Lock()
	defer wavCacheMu.Unlock()
	wavCache[key] = wav
}

// GetCachedWAV looks up a previously cached decoded WAV sample by key (see
// CacheWAV).
func GetCachedWAV(key string) (*pcmcore.WAVData, bool) {
	wavCacheMu.RLock()
	defer wavCacheMu.RUnlock()
	w, ok := wavCache[key]
	return w, ok
}

// ClearPCMWaveCache deletes every cached decoded WAV sample, freeing the
// memory they occupy. Separate from ClearPCMVoices: this clears the raw
// waveform data itself, not the voice (音色) definitions that reference it.
func ClearPCMWaveCache() {
	wavCacheMu.Lock()
	defer wavCacheMu.Unlock()
	wavCache = make(map[string]*pcmcore.WAVData)
}
