/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package pcmcore

// maxPolyphonyPerPart caps how many PCM samples may sound simultaneously
// within a single part; a new trigger beyond this steals the oldest note.
// 16 matches the maximum voiceSetting count a OneShot voice can define, so
// a fully-populated kit can still have every one of its samples ringing at
// once without an unrelated one getting stolen early.
const maxPolyphonyPerPart = 16

// part is a PCM sounding channel: a voice assignment plus its own volume,
// pan and set of currently-sounding notes.
type part struct {
	voiceID   VoiceID
	voiceType VoiceType
	volume    float64
	pan       float64
	notes     []*runningNote

	// reverbSend is a Long part's reverb send level, [0,1], applied
	// uniformly to every note sounding on the part (see
	// Engine.SetPartReverbSend). Unused for a OneShot part.
	reverbSend float64
	// oneShotReverbSend is a OneShot part's per-note reverb send levels
	// ([0,1], keyed by MIDI note number; see Engine.SetPartOneShotReverbSend),
	// read at trigger time so each newly-sounding note carries its own send
	// (see runningNote.reverbSend). A note absent from this map gets no
	// reverb. Unused for a Long part.
	oneShotReverbSend map[uint8]float64
}

// stopNote force-mutes (see runningNote.forceMute) any note in this part
// triggered by the given note number, so a retriggered OneShot sample (or a
// monophonic Long note) starts clean instead of overlapping its own
// previous instance. It force-mutes rather than dropping the note outright:
// a retriggered note is frequently still at a substantial amplitude (e.g. a
// Long note mid-sustain), and simply removing it from p.notes made that
// amplitude vanish in a single sample - an audible click. The muted note
// keeps rendering its fixed ~3ms fade-out; mixSample already drops it once
// that completes, same as ScheduleForceStopPart's identical fade.
func (p *part) stopNote(noteNumber uint8, nowSample int64, sampleRate int) {
	for _, n := range p.notes {
		if n.triggerNote == noteNumber {
			n.forceMute(nowSample, sampleRate)
		}
	}
}

// activeNoteForPortamento returns the most recently started note on this
// part that is neither force-muted nor already releasing, for a
// portamento ('&&') to glide in place on a Long part - true legato,
// continuing that note's own envelope rather than retriggering it (see
// Engine.glideNoteOnLong). nil if there's nothing left to continue.
func (p *part) activeNoteForPortamento() *runningNote {
	var best *runningNote
	for _, n := range p.notes {
		if n.muted || n.released {
			continue
		}
		if best == nil || n.startedAtSample > best.startedAtSample {
			best = n
		}
	}
	return best
}

// stealVoiceIfFull removes the oldest note when the part is at max
// polyphony.
func (p *part) stealVoiceIfFull() {
	if len(p.notes) < maxPolyphonyPerPart {
		return
	}
	p.notes = p.notes[1:]
}
