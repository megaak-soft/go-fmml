/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package player

import (
	"testing"

	"github.com/megaak-soft/go-fmml/fmcore"
	"github.com/megaak-soft/go-fmml/memory"
	"github.com/megaak-soft/go-fmml/pcmcore"
)

func TestEffectiveMuteWithSolo(t *testing.T) {
	cases := []struct {
		name        string
		mute, solo  bool
		anySolo     bool
		wantEffMute bool
	}{
		{"no solo anywhere, unmuted part stays unmuted", false, false, false, false},
		{"no solo anywhere, muted part stays muted", true, false, false, true},
		{"solo elsewhere, this non-solo part is muted even though its own mute is false", false, false, true, true},
		{"soloed part plays even though its own mute is true", true, true, true, false},
		{"soloed part plays with mute false too", false, true, true, false},
	}
	for _, c := range cases {
		if got := effectiveMuteWithSolo(c.mute, c.solo, c.anySolo); got != c.wantEffMute {
			t.Errorf("%s: effectiveMuteWithSolo(mute=%v, solo=%v, anySolo=%v) = %v, want %v", c.name, c.mute, c.solo, c.anySolo, got, c.wantEffMute)
		}
	}
}

func soloTestSequence() *memory.SequenceData {
	return &memory.SequenceData{
		SequenceID: "solo-test-seq",
		Tempo:      600,
		Loop:       false,
		Parts: map[fmcore.PartID]*memory.PartSequence{
			1: {
				PartID:  1,
				VoiceID: 1,
				Volume:  1,
				Pan:     0,
				Mute:    true, // deliberately conflicting with Solo:true - solo must win
				Solo:    true,
				Events: []memory.SeqEvent{
					{AtTick: 0, Kind: memory.EventNote, Notes: []memory.NoteSpec{{NoteName: "C4", NoteType: "NOTE4", Sustain: 100, Velocity: 100}}},
				},
				LengthTicks: 48,
			},
			2: {
				PartID:  2,
				VoiceID: 1,
				Volume:  1,
				Pan:     0,
				// No solo, no mute: must still be silenced because part 1 is soloed.
				Events: []memory.SeqEvent{
					{AtTick: 0, Kind: memory.EventNote, Notes: []memory.NoteSpec{{NoteName: "D4", NoteType: "NOTE4", Sustain: 100, Velocity: 100}}},
				},
				LengthTicks: 48,
			},
		},
		PCMParts: map[pcmcore.PartID]*memory.PCMPartSequence{
			'A': {
				PartID:   'A',
				VoiceID:  1,
				PartType: pcmcore.Long,
				Volume:   1,
				Pan:      0,
				// No solo either: must also be silenced by the FM part's solo,
				// proving solo is scoped across FM and PCM together.
				Events: []memory.PCMSeqEvent{
					{AtTick: 0, Kind: memory.PCMEventNote, Notes: []memory.NoteSpec{{NoteName: "A4", NoteType: "NOTE4", Sustain: 100, Velocity: 100}}},
				},
				LengthTicks: 48,
			},
		},
	}
}

// TestPlaySoloMutesOtherPartsAcrossFMAndPCM confirms CLAUDE.md's "solo を
// trueにした場合は対象パートのみ再生し、他のパートは自動でミュートにする":
// once any part (here, FM part 1) is soloed, every other part - FM or PCM -
// is silenced regardless of its own mute setting, and the soloed part
// itself plays even though its own mute is also set to true.
func TestPlaySoloMutesOtherPartsAcrossFMAndPCM(t *testing.T) {
	memory.StoreSequence(soloTestSequence())
	defer memory.ClearSequences()
	memory.StorePCMVoice(1, pcmcore.Voice{VoiceType: pcmcore.Long})
	defer memory.ClearPCMVoices()

	fe := newFakeEngine(t)
	if err := Play(fe, "solo-test-seq", nil); err != nil {
		t.Fatalf("Play failed: %v", err)
	}
	defer Stop()

	_, setParts := fe.snapshot()
	var part1Volume, part2Volume float64
	for _, sp := range setParts {
		switch sp.partID {
		case 1:
			part1Volume = sp.volume
		case 2:
			part2Volume = sp.volume
		}
	}
	if part1Volume == 0 {
		t.Fatalf("expected the soloed part 1 to play (nonzero volume) despite its own mute:true, got volume %v", part1Volume)
	}
	if part2Volume != 0 {
		t.Fatalf("expected the non-soloed part 2 to be silenced because part 1 is soloed, got volume %v", part2Volume)
	}

	fe.mu.Lock()
	pcmSetParts := append([]fakePCMSetPart(nil), fe.pcmSetParts...)
	fe.mu.Unlock()
	if len(pcmSetParts) == 0 {
		t.Fatalf("expected PCM part A to have been configured")
	}
	if pcmSetParts[0].volume != 0 {
		t.Fatalf("expected PCM part A (no solo of its own) to be silenced by the FM part's solo, got volume %v", pcmSetParts[0].volume)
	}
}

// TestPlayNoSoloLeavesMuteUnaffected confirms that with no part soloed
// anywhere, every part's own mute setting behaves exactly as before this
// addendum.
func TestPlayNoSoloLeavesMuteUnaffected(t *testing.T) {
	seq := soloTestSequence()
	seq.Parts[1].Solo = false // no part soloed anywhere now
	memory.StoreSequence(seq)
	defer memory.ClearSequences()

	fe := newFakeEngine(t)
	if err := Play(fe, "solo-test-seq", nil); err != nil {
		t.Fatalf("Play failed: %v", err)
	}
	defer Stop()

	_, setParts := fe.snapshot()
	var part1Volume, part2Volume float64
	for _, sp := range setParts {
		switch sp.partID {
		case 1:
			part1Volume = sp.volume
		case 2:
			part2Volume = sp.volume
		}
	}
	if part1Volume != 0 {
		t.Fatalf("expected part 1's own mute:true to still silence it with no solo in play, got volume %v", part1Volume)
	}
	if part2Volume == 0 {
		t.Fatalf("expected part 2 (mute:false, no solo anywhere) to play normally, got volume %v", part2Volume)
	}
}
