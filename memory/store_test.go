/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package memory

import (
	"testing"

	"github.com/megaak-soft/go-fmml/fmcore"
)

func TestVoiceStoreRoundTrip(t *testing.T) {
	defer ClearVoices()
	StoreVoice(5, fmcore.Voice{Algorithm: 3})
	v, ok := GetVoice(5)
	if !ok || v.Algorithm != 3 {
		t.Fatalf("expected stored voice, got %+v ok=%v", v, ok)
	}
	if _, ok := GetVoice(6); ok {
		t.Fatalf("expected no voice registered under id 6")
	}
	ClearVoices()
	if _, ok := GetVoice(5); ok {
		t.Fatalf("expected ClearVoices to remove voice 5")
	}
}

func TestSequenceStoreRoundTrip(t *testing.T) {
	defer ClearSequences()
	StoreSequence(&SequenceData{SequenceID: "s1", Tempo: 100})
	s, ok := GetSequence("s1")
	if !ok || s.Tempo != 100 {
		t.Fatalf("expected stored sequence, got %+v ok=%v", s, ok)
	}
	ClearSequences()
	if _, ok := GetSequence("s1"); ok {
		t.Fatalf("expected ClearSequences to remove sequence s1")
	}
}
