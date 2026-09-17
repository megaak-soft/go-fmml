/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package fmcore

// maxPolyphonyPerPart caps how many notes may sound simultaneously within a
// single part; a new note beyond this steals the oldest/quietest voice.
const maxPolyphonyPerPart = 8

// part is a multi-timbral sounding channel: a voice assignment plus its own
// volume, pan and set of currently-sounding notes.
type part struct {
	voiceID VoiceID
	volume  float64
	pan     float64
	notes   []*runningNote

	// reverbSend is this part's reverb send level (Phase 4), [0,1],
	// applied uniformly to the part's combined dry output each sample
	// (see Engine.mixSample). Deliberately not touched by SetPart/
	// ScheduleSetPart (a mid-sequence '@'/'P' voice/pan change must not
	// reset it) - see Engine.SetPartReverbSend.
	reverbSend float64
}

// stopNote force-mutes (see runningNote.forceMute) any note in this part
// currently playing the given note number, so a re-triggered note starts
// clean instead of overlapping itself. It force-mutes rather than dropping
// the note outright: a retriggered note is frequently still at a
// substantial amplitude (e.g. mid-sustain), and simply removing it from
// p.notes made that amplitude vanish in a single sample - an audible
// click, most noticeable on a voice with a high sustain level. The muted
// note keeps rendering its fixed ~3ms fade-out (mixSample already drops it
// once that completes, same as ScheduleForceStopPart's identical fade) and
// stealVoiceIfFull below already prefers a released (which forceMute also
// sets) note as its steal victim, so this doesn't pressure polyphony
// unless the part is genuinely full.
func (p *part) stopNote(noteNumber uint8, nowSample int64, sampleRate int) {
	for _, n := range p.notes {
		if n.noteNumber == noteNumber {
			n.forceMute(nowSample, sampleRate)
		}
	}
}

// activeNoteForPortamento returns the most recently started note on this
// part that is neither force-muted nor already releasing, for a
// portamento ('&&') to glide in place - true legato, continuing that
// note's own envelope rather than retriggering it (see
// Engine.glideNoteOn). nil if there's nothing left to continue (e.g. it
// already finished its own natural release before the portamento note
// fired), in which case the caller falls back to a fresh attack.
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

// stealVoiceIfFull removes one note when the part is at max polyphony,
// preferring a note already releasing, otherwise the oldest note.
func (p *part) stealVoiceIfFull() {
	if len(p.notes) < maxPolyphonyPerPart {
		return
	}
	victim := -1
	for i, n := range p.notes {
		if n.released {
			victim = i
			break
		}
	}
	if victim < 0 {
		victim = 0
		for i, n := range p.notes {
			if n.startedAtSample < p.notes[victim].startedAtSample {
				victim = i
			}
		}
	}
	p.notes = append(p.notes[:victim], p.notes[victim+1:]...)
}
