/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package player

import (
	"fmt"
	"log"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/megaak-soft/go-fmml/common"
	"github.com/megaak-soft/go-fmml/fmcore"
	"github.com/megaak-soft/go-fmml/memory"
	"github.com/megaak-soft/go-fmml/pcmcore"
)

// refillLookaheadSeconds is how far ahead (in seconds of audio, converted
// to samples) a looping sequence keeps its schedule topped up.
const refillLookaheadSeconds = 2.0

// refillPollInterval is how often the loop-refill watchdog checks whether
// more of a looping sequence needs to be scheduled. Its timing is not
// audio-critical: it only decides when to hand more events to the engine's
// sample-accurate scheduler, never fires a note itself, so jitter here
// can't make a note early, late, or dropped - at worst a slow refill risks
// briefly running out of scheduled material, which the next poll recovers
// from.
const refillPollInterval = 250 * time.Millisecond

// shortTransitionFadeSeconds is the brief master-volume glide Play/Pause/
// Resume/Stop each apply around their own transition (Phase 7), so
// starting, stopping or resuming playback never sounds like an abrupt
// on/off switch. It's layered on top of - not a replacement for - each
// note's own short force-mute fade (see fmcore/pcmcore's forceMuteFadeMs):
// this one covers the whole mix (including a reverb tail DuckReverb is
// separately fading), that one covers an individual retriggered note.
const shortTransitionFadeSeconds = 0.1

type playbackState int

const (
	statePlaying playbackState = iota
	statePaused
	stateStopped
)

// eventDomain distinguishes an FM timelineEvent (memory.SeqEvent, driven
// through fmcore's Schedule* methods) from a PCM one (memory.PCMSeqEvent,
// driven through fmcore's PCM-prefixed pass-through methods, see
// fmcore/pcm.go). Both domains are merged into one tick-ordered timeline
// (see newPlayback) so their scheduling, pause/resume and looping logic is
// shared rather than duplicated.
type eventDomain int

const (
	// domainTempo sorts before domainFM/domainPCM at the same tick (see
	// newPlayback's sort.SliceStable comparator), so a tempo change due at
	// the exact same tick as a note always takes effect before that note's
	// own duration is resolved.
	domainTempo eventDomain = iota
	domainFM
	domainPCM
)

// timelineEvent is one memory.SeqEvent, memory.PCMSeqEvent, or Phase 7's
// [conductor] tempo-map breakpoint, merged into a single, tick-ordered
// timeline across every FM and PCM part of a sequence.
type timelineEvent struct {
	AtTick    int
	Domain    eventDomain
	PartID    fmcore.PartID      // Domain == domainFM
	PCMPartID pcmcore.PartID     // Domain == domainPCM
	Event     memory.SeqEvent    // Domain == domainFM
	PCMEvent  memory.PCMSeqEvent // Domain == domainPCM
	TempoBPM  float64            // Domain == domainTempo
}

type runtimePart struct {
	voiceID fmcore.VoiceID
	volume  float64
	pan     float64
	muted   bool // Phase 5's [partSetting] mute; see effectiveVolume
}

// runtimePCMPart tracks a PCM part's current voice/pan across
// PCMEventSetVoice/PCMEventSetPan (Long parts only - see scheduleEvent).
type runtimePCMPart struct {
	voiceID pcmcore.VoiceID
	volume  float64
	pan     float64
	muted   bool // Phase 5's [pcmPartSetting] mute; see effectiveVolume
}

// effectiveVolume silences a muted part regardless of its configured
// volume, without the engine itself needing any concept of "mute" - a
// muted part is simply always played at zero volume, on every SetPart/
// ScheduleSetPart call for that part (see newPlayback/scheduleEvent/
// schedulePCMEvent).
func effectiveVolume(volume float64, muted bool) float64 {
	if muted {
		return 0
	}
	return volume
}

// effectiveMuteWithSolo folds the MML addendum's [partSetting]/[pcmPartSetting]
// "solo" debug aid into a part's own mute setting: once any FM or PCM part
// in the sequence has solo set (anySolo), every part without its own solo
// is muted regardless of its own mute setting, and a soloed part plays
// regardless of its own mute (mirrors a typical mixing console's solo
// button, which overrides mute both ways). With no solo set anywhere,
// this is simply mute unchanged.
func effectiveMuteWithSolo(mute, solo, anySolo bool) bool {
	if anySolo {
		return !solo
	}
	return mute
}

// playback drives one sequence: it pre-computes a tick-ordered timeline
// once, then repeatedly hands the engine's sample-accurate scheduler
// batches of "fire this at sample N" instructions computed directly from
// each event's absolute tick offset (never by accumulating already-rounded
// increments), so neither individual note timing nor loop-to-loop timing
// can drift.
type playback struct {
	engine    Engine
	seq       *memory.SequenceData
	timeline  []timelineEvent
	loopTicks int

	// loopSpanTicks/loopWrapIdx narrow a loop's *restart* point to
	// [global]'s startOffset (CLAUDE.md's addendum: looping back should
	// resume from the offset, not tick 0, once the offset has been passed
	// the first time) when the sequence both loops and has a positive
	// StartOffsetTicks smaller than loopTicks; otherwise loopSpanTicks
	// equals loopTicks and loopWrapIdx is 0, reproducing the original
	// (always-restart-from-tick-0) behavior exactly - see newPlayback and
	// scheduleMore's use of both.
	loopSpanTicks int
	loopWrapIdx   int

	// tempoMap is seq.TempoMap (Phase 7's [conductor] tempo automation),
	// defaulted in newPlayback to a single {0, seq.Tempo} breakpoint when
	// the sequence has no conductor tempo changes - so every tick<->sample
	// conversion below can unconditionally go through the tempo-map-aware
	// common.TicksToSamplesFromMap/SamplesToTicksFromMap and still behave
	// exactly like the fixed-tempo math it replaces in the common case.
	tempoMap []common.TempoPoint

	// loopSpanSamples is one repeating lap's duration in samples - from
	// loopStartTick (see loopSpanTicks's doc comment) to loopTicks,
	// computed once via the tempo map rather than derived by scaling
	// loopSpanTicks by a single tempo, since tempo may vary within the
	// lap. scheduleMore/resume multiply this by the lap count instead of
	// feeding an ever-growing "total ticks across every lap" through the
	// tempo map, which would incorrectly replay the map's tail tempo
	// forever instead of restarting it each lap.
	loopSpanSamples int64

	parts   map[fmcore.PartID]*runtimePart
	partIDs []fmcore.PartID // parts, for Engine.AnySounding

	pcmParts   map[pcmcore.PartID]*runtimePCMPart
	pcmPartIDs []pcmcore.PartID // pcmParts, for Engine.AnySoundingPCM

	onComplete func() // see Play; invoked once for a non-looping sequence when it naturally finishes

	mu           sync.Mutex
	state        playbackState
	startSample  int64 // sample position corresponding to absolute tick 0
	iterationIdx int   // which loop iteration the scheduling cursor is in (0 if not looping)
	timelineIdx  int   // index into timeline of the next event to schedule
	pausedAtTick int

	// savedMasterVolumeTarget captures the engine's master volume target
	// (see fmcore.Engine.MasterVolumeTarget) at the moment pause() fades it
	// to 0, so resume() knows what level to glide back up to - which may
	// not be seq.MasterVolume if the caller changed it mid-playback (see
	// SetMasterVolume/SetMasterVolumePercent).
	savedMasterVolumeTarget float64

	refillStop chan struct{}
	refillDone chan struct{}

	completionStop chan struct{}
	completionDone chan struct{}

	// pendingFadeTimer is a one-shot delayed fade action (Phase 7's
	// FadeOutPlay/FadeInOutPlay scheduling their fade-out to start
	// shortly before the sequence ends, or FadeOut scheduling its own
	// stopPlayback once its fade-out completes) - see scheduleFadeTimer/
	// cancelFadeTimer. nil when nothing is pending.
	pendingFadeTimer *time.Timer
}

var (
	mu      sync.Mutex
	current *playback

	// lastLoadedMasterVolume is the most recently Play'd (or FadeIn/
	// FadeOut-Play'd) sequence's own original [global] volume, in [0,1] -
	// the "100%" SetMasterVolumePercent measures against. Set once per
	// newPlayback call and, unlike current, deliberately not cleared by
	// Stop, so a percentage-based volume change still has a base to work
	// from even after playback stops.
	lastLoadedMasterVolume float64 = 1.0
)

// masterVolumeChangeSeconds is how long a mid-playback SetMasterVolume/
// SetMasterVolumePercent call takes to glide to its new value (CLAUDE.md's
// master-volume-change methods): long enough to mask a step in level as a
// change rather than a jump, short enough to feel responsive.
const masterVolumeChangeSeconds = 0.3

func clampVolume01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// SetMasterVolume overwrites the engine's master volume with volume, on
// [global]'s own 0-127 scale (CLAUDE.md's master-volume-change method (1)).
// Callable before or during playback: if a sequence is currently playing,
// the change glides in smoothly over masterVolumeChangeSeconds instead of
// jumping instantly; otherwise it takes effect immediately, ready for the
// next Play. engine must be the same Engine instance passed to Play.
func SetMasterVolume(engine Engine, volume float64) {
	applyMasterVolume(engine, clampVolume01(volume/127))
}

// SetMasterVolumePercent is SetMasterVolume's percentage-based counterpart
// (CLAUDE.md's master-volume-change method (2)): percent is relative to the
// most recently loaded sequence's own original [global] volume (100 = that
// value unchanged, 50 = half of it) rather than to full scale. Same
// before/during-playback and glide behavior as SetMasterVolume.
func SetMasterVolumePercent(engine Engine, percent float64) {
	mu.Lock()
	base := lastLoadedMasterVolume
	mu.Unlock()
	applyMasterVolume(engine, clampVolume01(base*percent/100))
}

func applyMasterVolume(engine Engine, normalized float64) {
	mu.Lock()
	p := current
	mu.Unlock()
	if p != nil && p.isPlaying() {
		engine.SetMasterVolumeSmooth(normalized, masterVolumeChangeSeconds)
		return
	}
	engine.SetMasterVolume(normalized)
}

// Play starts asynchronous playback of the previously loaded sequence
// identified by sequenceID (see go-fmml/fileio's MML loaders), driving up
// to 16 parts of engine in real time. It returns immediately; the sequence
// itself is scheduled on engine's own sample-accurate clock. Any sequence
// already playing is stopped first. If a part references a voice ID that
// isn't in go-fmml/memory, a default voice is used instead so playback
// still proceeds.
//
// If the sequence's [global] startOffset is set, playback begins that many
// whole notes into the sequence instead of at tick 0 (see
// playback.startWithOffset): every part's voice/pan change from before
// that point still takes effect, but no note that started before it is
// ever heard, even one still sounding across the offset point.
//
// onComplete, if non-nil, is called exactly once, on a background
// goroutine, when a non-looping sequence finishes playing through its
// whole written timeline on its own (not via Pause or Stop). Pass nil if
// you don't need to be notified; a looping sequence never calls it, since
// it never naturally finishes.
func Play(engine Engine, sequenceID string, onComplete func()) error {
	seq, ok := memory.GetSequence(sequenceID)
	if !ok {
		return fmt.Errorf("player: sequence %q is not loaded", sequenceID)
	}

	p := newPlayback(engine, seq)
	p.onComplete = onComplete

	mu.Lock()
	if current != nil {
		current.stopPlayback()
	}
	current = p
	mu.Unlock()

	p.startWithOffset(seq.StartOffsetTicks)
	return nil
}

// SkipPlay is Play, but begins playback seekSeconds seconds into the
// sequence instead of from the very beginning - every other argument
// (including onComplete) behaves exactly as in Play.
//
// seekSeconds <= 0 behaves exactly like Play (start from the beginning).
// A seekSeconds at or beyond the sequence's own length - one loop
// iteration's worth, for a looping sequence, since a looping sequence has
// no overall length - is treated as invalid the same way: playback starts
// from the beginning too, rather than erroring.
//
// If the requested position falls strictly inside a note/chord rather
// than exactly on its start, playback rewinds slightly to that note's own
// start so it never begins mid-note - the same rewind-to-note-start
// behavior Resume uses when a Pause landed mid-note.
func SkipPlay(engine Engine, sequenceID string, seekSeconds float64, onComplete func()) error {
	seq, ok := memory.GetSequence(sequenceID)
	if !ok {
		return fmt.Errorf("player: sequence %q is not loaded", sequenceID)
	}

	p := newPlayback(engine, seq)
	p.onComplete = onComplete

	mu.Lock()
	if current != nil {
		current.stopPlayback()
	}
	current = p
	mu.Unlock()

	p.start(p.seekTick(seekSeconds))
	return nil
}

// FadeInPlay is Play, but starts at volume 0 and glides smoothly up to the
// sequence's own [global] volume over fadeSeconds (CLAUDE.md's fade-in
// play method) instead of starting at full volume immediately.
func FadeInPlay(engine Engine, sequenceID string, fadeSeconds float64, onComplete func()) error {
	seq, ok := memory.GetSequence(sequenceID)
	if !ok {
		return fmt.Errorf("player: sequence %q is not loaded", sequenceID)
	}

	p := newPlayback(engine, seq)
	p.onComplete = onComplete
	engine.SetMasterVolume(0) // override newPlayback's own instant full-volume set

	mu.Lock()
	if current != nil {
		current.stopPlayback()
	}
	current = p
	mu.Unlock()

	p.startWithOffset(seq.StartOffsetTicks)
	engine.SetMasterVolumeSmooth(seq.MasterVolume, fadeSeconds)
	return nil
}

// FadeOutPlay is Play, but - for a non-looping sequence - automatically
// glides the master volume down to 0 over fadeSeconds timed to finish right
// as the sequence's own written timeline ends (CLAUDE.md's fade-out play
// method), instead of ending abruptly at full volume. A looping sequence
// has no natural end to fade out before, so per CLAUDE.md this behaves
// exactly like Play for one.
func FadeOutPlay(engine Engine, sequenceID string, fadeSeconds float64, onComplete func()) error {
	seq, ok := memory.GetSequence(sequenceID)
	if !ok {
		return fmt.Errorf("player: sequence %q is not loaded", sequenceID)
	}

	p := newPlayback(engine, seq)
	p.onComplete = onComplete

	mu.Lock()
	if current != nil {
		current.stopPlayback()
	}
	current = p
	mu.Unlock()

	p.startWithOffset(seq.StartOffsetTicks)
	p.scheduleAutoFadeOut(fadeSeconds)
	return nil
}

// FadeInOutPlay combines FadeInPlay and FadeOutPlay (CLAUDE.md's combined
// fade-in/fade-out play method): starts at volume 0 and glides up over
// fadeSeconds, then - for a non-looping sequence - glides back down to 0
// over the same fadeSeconds timed to finish at the sequence's own end.
func FadeInOutPlay(engine Engine, sequenceID string, fadeSeconds float64, onComplete func()) error {
	seq, ok := memory.GetSequence(sequenceID)
	if !ok {
		return fmt.Errorf("player: sequence %q is not loaded", sequenceID)
	}

	p := newPlayback(engine, seq)
	p.onComplete = onComplete
	engine.SetMasterVolume(0)

	mu.Lock()
	if current != nil {
		current.stopPlayback()
	}
	current = p
	mu.Unlock()

	p.startWithOffset(seq.StartOffsetTicks)
	engine.SetMasterVolumeSmooth(seq.MasterVolume, fadeSeconds)
	p.scheduleAutoFadeOut(fadeSeconds)
	return nil
}

// FadeOut smoothly glides the currently playing sequence's master volume
// down to 0 over fadeSeconds and stops playback once it reaches 0
// (CLAUDE.md's standalone fade-out method) - unlike FadeOutPlay's
// automatic end-of-song fade, this can be called at any moment and applies
// even to a looping sequence, which FadeOutPlay/FadeInOutPlay's own
// automatic fade never does.
func FadeOut(fadeSeconds float64) error {
	mu.Lock()
	p := current
	mu.Unlock()
	if p == nil || !p.isPlaying() {
		return fmt.Errorf("player: no sequence is playing")
	}
	if fadeSeconds < 0 {
		fadeSeconds = 0
	}

	p.engine.SetMasterVolumeSmooth(0, fadeSeconds)
	p.scheduleFadeTimer(time.Duration(fadeSeconds*float64(time.Second)), func() {
		mu.Lock()
		isStillCurrent := current == p
		if isStillCurrent {
			current = nil
		}
		mu.Unlock()
		if isStillCurrent {
			p.stopPlayback()
		}
	})
	return nil
}

// Pause suspends the currently playing sequence in place, fading out
// (rather than abruptly cutting off) whatever was still sounding on every
// part; Resume continues it. It is a no-op if nothing is playing.
func Pause() error {
	mu.Lock()
	p := current
	mu.Unlock()
	if p == nil {
		return fmt.Errorf("player: no sequence is playing")
	}
	p.pause()
	return nil
}

// Resume continues a sequence previously suspended with Pause. If playback
// was paused mid-note, it rewinds slightly to the start of that note so
// resumption doesn't cut it off partway through.
func Resume() error {
	mu.Lock()
	p := current
	mu.Unlock()
	if p == nil {
		return fmt.Errorf("player: no sequence is paused")
	}
	p.resume()
	return nil
}

// Stop halts the currently playing (or paused) sequence entirely.
func Stop() error {
	mu.Lock()
	p := current
	current = nil
	mu.Unlock()
	if p == nil {
		return fmt.Errorf("player: no sequence is playing")
	}
	p.stopPlayback()
	return nil
}

// IsPlaying reports whether a sequence is currently actively playing - true
// only while playback is running, not while paused or stopped, and false
// if nothing has ever been played.
func IsPlaying() bool {
	mu.Lock()
	p := current
	mu.Unlock()
	if p == nil {
		return false
	}
	return p.isPlaying()
}

// Rewind resets the current sequence's playback position back to the very
// start (tick 0). If playback is currently active, it takes effect
// immediately: whatever was still sounding is silenced and playback
// restarts from the beginning right away. If playback is paused, Rewind
// only marks the rewound position - playback stays paused, and the next
// Resume call continues from the start rather than from where Pause left
// off. It is a no-op if nothing is playing or paused.
func Rewind() error {
	mu.Lock()
	p := current
	mu.Unlock()
	if p == nil {
		return fmt.Errorf("player: no sequence is loaded")
	}
	p.rewind()
	return nil
}

func newPlayback(engine Engine, seq *memory.SequenceData) *playback {
	p := &playback{
		engine:   engine,
		seq:      seq,
		parts:    make(map[fmcore.PartID]*runtimePart, len(seq.Parts)),
		pcmParts: make(map[pcmcore.PartID]*runtimePCMPart, len(seq.PCMParts)),
	}

	engine.SetMasterVolume(seq.MasterVolume)
	engine.SetReverb(seq.ReverbEnabled, seq.ReverbType, seq.ReverbTimeSeconds, seq.ReverbLevel)

	mu.Lock()
	lastLoadedMasterVolume = seq.MasterVolume
	mu.Unlock()

	// anySolo (the MML addendum's "solo" setting) is computed once, across every FM and PCM part
	// together, before any part's effective mute is decided below - see
	// effectiveMuteWithSolo.
	anySolo := false
	for _, ps := range seq.Parts {
		if ps.Solo {
			anySolo = true
			break
		}
	}
	if !anySolo {
		for _, pps := range seq.PCMParts {
			if pps.Solo {
				anySolo = true
				break
			}
		}
	}

	for partID, ps := range seq.Parts {
		voice, ok := memory.GetVoice(ps.VoiceID)
		voiceID := ps.VoiceID
		if !ok {
			voice = defaultVoice()
			log.Printf("player: part %d references unknown voice %d, using default voice", partID, ps.VoiceID)
		}
		engine.RegisterVoice(voiceID, voice)
		muted := effectiveMuteWithSolo(ps.Mute, ps.Solo, anySolo)
		if err := engine.SetPart(partID, voiceID, effectiveVolume(ps.Volume, muted), ps.Pan); err != nil {
			log.Printf("player: failed to set up part %d: %v", partID, err)
		}
		engine.SetPartReverbSend(partID, ps.ReverbSend)
		p.parts[partID] = &runtimePart{voiceID: voiceID, volume: ps.Volume, pan: ps.Pan, muted: muted}
		p.partIDs = append(p.partIDs, partID)

		for _, ev := range ps.Events {
			p.timeline = append(p.timeline, timelineEvent{AtTick: ev.AtTick, Domain: domainFM, PartID: partID, Event: ev})
		}
		if ps.LengthTicks > p.loopTicks {
			p.loopTicks = ps.LengthTicks
		}
	}

	for partID, pps := range seq.PCMParts {
		voice, ok := memory.GetPCMVoice(pps.VoiceID)
		if !ok {
			// Unlike a missing FM voice, there's no sensible synthetic
			// fallback for a missing PCM sample (no WAV data to play), so
			// the part is left unconfigured and simply stays silent rather
			// than erroring the whole sequence.
			log.Printf("player: PCM part %c references unknown voice %d, part will stay silent", partID, pps.VoiceID)
			continue
		}
		engine.RegisterPCMVoice(pps.VoiceID, voice)

		// memory.PCMPartSequence.Pan is already normalized to [-1,1] (see
		// analyze/mml.go's normalizePan16); a OneShot part ignores its own
		// pan entirely regardless (each sample's own pan, set in the PCM
		// voice file, is used instead - see go-fmml/pcmcore's
		// TriggerOneShot).
		pan := 0.0
		if pps.PartType == pcmcore.Long {
			pan = pps.Pan
		}
		pcmMuted := effectiveMuteWithSolo(pps.Mute, pps.Solo, anySolo)
		if err := engine.SetPCMPart(partID, pps.VoiceID, effectiveVolume(pps.Volume, pcmMuted), pan); err != nil {
			log.Printf("player: failed to set up PCM part %c: %v", partID, err)
			continue
		}
		if pps.PartType == pcmcore.Long {
			engine.SetPCMPartReverbSend(partID, pps.ReverbSend)
		} else {
			engine.SetPCMPartOneShotReverbSend(partID, pps.OneShotReverbSend)
		}
		p.pcmParts[partID] = &runtimePCMPart{voiceID: pps.VoiceID, volume: pps.Volume, pan: pan, muted: pcmMuted}
		p.pcmPartIDs = append(p.pcmPartIDs, partID)

		for _, ev := range pps.Events {
			p.timeline = append(p.timeline, timelineEvent{AtTick: ev.AtTick, Domain: domainPCM, PCMPartID: partID, PCMEvent: ev})
		}
		if pps.LengthTicks > p.loopTicks {
			p.loopTicks = pps.LengthTicks
		}
	}

	p.tempoMap = seq.TempoMap
	if len(p.tempoMap) == 0 {
		p.tempoMap = []common.TempoPoint{{AtTick: 0, BPM: seq.Tempo}}
	}
	for _, tp := range p.tempoMap[1:] {
		p.timeline = append(p.timeline, timelineEvent{AtTick: tp.AtTick, Domain: domainTempo, TempoBPM: tp.BPM})
	}

	sort.SliceStable(p.timeline, func(i, j int) bool {
		a, b := p.timeline[i], p.timeline[j]
		if a.AtTick != b.AtTick {
			return a.AtTick < b.AtTick
		}
		if a.Domain != b.Domain {
			return a.Domain < b.Domain
		}
		if a.Domain == domainPCM {
			return a.PCMPartID < b.PCMPartID
		}
		return a.PartID < b.PartID
	})

	// loopStartTick is the loop *restart* point (see loopSpanTicks/
	// loopWrapIdx's doc comment): Phase 7's [conductor] jump point, if set,
	// takes priority over Phase 5's startOffset-based restart.
	loopStartTick := 0
	if seq.Loop && seq.HasJumpPoint && seq.JumpPointTick > 0 && seq.JumpPointTick < p.loopTicks {
		loopStartTick = seq.JumpPointTick
	} else if seq.Loop && seq.StartOffsetTicks > 0 && seq.StartOffsetTicks < p.loopTicks {
		loopStartTick = seq.StartOffsetTicks
	}
	p.loopSpanTicks = p.loopTicks - loopStartTick
	p.loopWrapIdx = sort.Search(len(p.timeline), func(i int) bool { return p.timeline[i].AtTick >= loopStartTick })

	sampleRate := engine.SampleRate()
	p.loopSpanSamples = common.TicksToSamplesFromMap(p.loopTicks, p.tempoMap, sampleRate) - common.TicksToSamplesFromMap(loopStartTick, p.tempoMap, sampleRate)

	return p
}

// start begins scheduling from fromTick (0 for a fresh Play; see
// SkipPlay's seekTick for a non-zero one). If fromTick falls strictly
// inside a note/chord rather than exactly on its start, it's rewound to
// that note's own start first (see resolveStartPosition), so playback
// never begins mid-note.
func (p *playback) start(fromTick int) {
	resolvedTick, timelineIdx := p.resolveStartPosition(fromTick)
	p.beginScheduling(resolvedTick, timelineIdx)
}

// startWithOffset is start's counterpart for [global]'s startOffset
// (CLAUDE.md's Phase 5 addendum): offsetTicks <= 0 (no startOffset
// configured) behaves exactly like start(0). A positive offsetTicks first
// applies (immediately, not on the sample-accurate scheduler) the last
// voice/pan change on every part from before offsetTicks - see
// applyPreOffsetState - since CLAUDE.md requires those still take effect
// even though the notes themselves are skipped, then begins scheduling at
// the first timeline entry at or after offsetTicks. Unlike
// resolveStartPosition, this never snaps backward into a note already in
// progress at offsetTicks: per CLAUDE.md, such a note simply never sounds.
func (p *playback) startWithOffset(offsetTicks int) {
	if offsetTicks <= 0 {
		p.start(0)
		return
	}
	p.applyPreOffsetState(offsetTicks)
	timelineIdx := sort.Search(len(p.timeline), func(i int) bool { return p.timeline[i].AtTick >= offsetTicks })
	p.beginScheduling(offsetTicks, timelineIdx)
}

// applyPreOffsetState walks every part's merged timeline event before
// offsetTicks and, for a voice or pan change (a note/trigger event is
// simply skipped - see startWithOffset), applies its effect immediately via
// the engine's non-scheduled Set*Part calls and updates the matching
// runtimePart/runtimePCMPart so later scheduled events build on the correct
// running state. p.timeline is sorted by AtTick (see newPlayback), so
// scanning stops at the first entry that isn't before offsetTicks.
func (p *playback) applyPreOffsetState(offsetTicks int) {
	for _, te := range p.timeline {
		if te.AtTick >= offsetTicks {
			return
		}
		if te.Domain == domainPCM {
			switch te.PCMEvent.Kind {
			case memory.PCMEventSetVoice:
				rp := p.pcmParts[te.PCMPartID]
				if rp == nil {
					continue
				}
				voice, ok := memory.GetPCMVoice(te.PCMEvent.VoiceID)
				if !ok {
					log.Printf("player: PCM part %c switched to unknown voice %d before startOffset, ignoring", te.PCMPartID, te.PCMEvent.VoiceID)
					continue
				}
				p.engine.RegisterPCMVoice(te.PCMEvent.VoiceID, voice)
				rp.voiceID = te.PCMEvent.VoiceID
				if err := p.engine.SetPCMPart(te.PCMPartID, rp.voiceID, effectiveVolume(rp.volume, rp.muted), rp.pan); err != nil {
					log.Printf("player: failed to pre-apply PCM part %c voice before startOffset: %v", te.PCMPartID, err)
				}
			case memory.PCMEventSetPan:
				rp := p.pcmParts[te.PCMPartID]
				if rp == nil {
					continue
				}
				rp.pan = te.PCMEvent.Pan
				if err := p.engine.SetPCMPart(te.PCMPartID, rp.voiceID, effectiveVolume(rp.volume, rp.muted), rp.pan); err != nil {
					log.Printf("player: failed to pre-apply PCM part %c pan before startOffset: %v", te.PCMPartID, err)
				}
			}
			continue
		}
		switch te.Event.Kind {
		case memory.EventSetVoice:
			rp := p.parts[te.PartID]
			if rp == nil {
				continue
			}
			voice, ok := memory.GetVoice(te.Event.VoiceID)
			if !ok {
				voice = defaultVoice()
				log.Printf("player: part %d switched to unknown voice %d before startOffset, using default voice", te.PartID, te.Event.VoiceID)
			}
			p.engine.RegisterVoice(te.Event.VoiceID, voice)
			rp.voiceID = te.Event.VoiceID
			if err := p.engine.SetPart(te.PartID, rp.voiceID, effectiveVolume(rp.volume, rp.muted), rp.pan); err != nil {
				log.Printf("player: failed to pre-apply part %d voice before startOffset: %v", te.PartID, err)
			}
		case memory.EventSetPan:
			rp := p.parts[te.PartID]
			if rp == nil {
				continue
			}
			rp.pan = te.Event.Pan
			if err := p.engine.SetPart(te.PartID, rp.voiceID, effectiveVolume(rp.volume, rp.muted), rp.pan); err != nil {
				log.Printf("player: failed to pre-apply part %d pan before startOffset: %v", te.PartID, err)
			}
		}
	}
}

// beginScheduling is start's/startWithOffset's shared core: it establishes
// the sample<->tick mapping for resolvedTick to be "now", positions the
// scheduling cursor at timelineIdx, and hands the engine everything due
// within the current horizon.
func (p *playback) beginScheduling(resolvedTick, timelineIdx int) {
	common.Tempo = p.seq.Tempo

	sampleRate := p.engine.SampleRate()
	now := p.engine.SampleClock()

	p.mu.Lock()
	p.startSample = now - common.TicksToSamplesFromMap(resolvedTick, p.tempoMap, sampleRate)
	p.iterationIdx = 0
	p.timelineIdx = timelineIdx
	p.state = statePlaying
	p.mu.Unlock()

	p.scheduleMore(p.horizon(now, sampleRate))
	if p.seq.Loop && p.loopTicks > 0 {
		p.startRefill()
	} else if p.onComplete != nil {
		p.startCompletionWatcher()
	}
}

// resolveStartPosition finds the timeline index of the first event to
// schedule when starting (or resuming/seeking) playback at tickInIter
// ticks into the current loop iteration, snapping backward to the start
// of whatever note/chord (or other event) is already "in progress" at
// that instant instead of beginning strictly mid-note. Returns the
// (possibly rewound) tick to actually start at, still expressed within
// the same iteration, and that event's index into p.timeline.
//
// The timeline merges every part's events into one tick-ordered list (see
// newPlayback), so several entries can legitimately share the same
// AtTick - e.g. every part's tick-0 event, or several parts' events at
// the instant being sought to. Once a snap-back tick is found, this looks
// up the *first* timeline entry at that tick (not just one entry back),
// so a rewind never silently drops an earlier part's simultaneous event.
func (p *playback) resolveStartPosition(tickInIter int) (resolvedTickInIter, timelineIdx int) {
	idx := sort.Search(len(p.timeline), func(i int) bool { return p.timeline[i].AtTick > tickInIter })
	if idx == 0 {
		return tickInIter, 0
	}
	snappedTick := p.timeline[idx-1].AtTick
	first := sort.Search(len(p.timeline), func(i int) bool { return p.timeline[i].AtTick >= snappedTick })
	return snappedTick, first
}

// seekTick converts SkipPlay's seekSeconds argument into a starting tick
// for start(): <= 0, or at/beyond the sequence's own length (one loop
// iteration's worth), both mean "start from the beginning" (tick 0).
func (p *playback) seekTick(seekSeconds float64) int {
	if seekSeconds <= 0 || p.loopTicks <= 0 {
		return 0
	}
	sampleRate := p.engine.SampleRate()
	tick := common.SamplesToTicksFromMap(int64(seekSeconds*float64(sampleRate)), p.tempoMap, sampleRate)
	if tick <= 0 || tick >= p.loopTicks {
		return 0
	}
	return tick
}

// samplesToPausedTick converts elapsedSamples (time elapsed since
// p.startSample) into the "iterationIdx*loopTicks + tickInIter" encoding
// pause()/resume() share (resume() decodes it back via division/modulo by
// loopTicks). A looping sequence can't just run elapsedSamples through the
// tempo map directly once it's past the first lap - the map is only
// defined over one lap's own ticks, so it would replay only its tail tempo
// forever instead of restarting it each lap (see loopSpanSamples's doc
// comment on the playback struct for the same reasoning applied to
// scheduling).
func (p *playback) samplesToPausedTick(elapsedSamples int64, sampleRate int) int {
	if !p.seq.Loop || p.loopTicks <= 0 {
		return common.SamplesToTicksFromMap(elapsedSamples, p.tempoMap, sampleRate)
	}
	firstLapSamples := common.TicksToSamplesFromMap(p.loopTicks, p.tempoMap, sampleRate)
	if elapsedSamples < firstLapSamples || p.loopSpanSamples <= 0 {
		tick := common.SamplesToTicksFromMap(elapsedSamples, p.tempoMap, sampleRate)
		if tick > p.loopTicks {
			tick = p.loopTicks
		}
		return tick
	}
	loopStartTick := p.loopTicks - p.loopSpanTicks
	loopStartSamples := common.TicksToSamplesFromMap(loopStartTick, p.tempoMap, sampleRate)
	remaining := elapsedSamples - firstLapSamples
	lapsCompleted := int(remaining / p.loopSpanSamples)
	withinLap := remaining % p.loopSpanSamples
	tickInIter := common.SamplesToTicksFromMap(loopStartSamples+withinLap, p.tempoMap, sampleRate)
	iterationIdx := 1 + lapsCompleted
	return iterationIdx*p.loopTicks + tickInIter
}

// horizon returns how far ahead (as an absolute sample position) to
// schedule from now. A non-looping sequence has a finite timeline, so
// there's no reason to cap it - schedule the whole thing in one go. Only a
// looping (unbounded) sequence needs the rolling lookahead window, topped
// up later by startRefill.
func (p *playback) horizon(now int64, sampleRate int) int64 {
	if p.seq.Loop && p.loopTicks > 0 {
		return now + int64(refillLookaheadSeconds*float64(sampleRate))
	}
	return math.MaxInt64
}

// scheduleMore hands the engine every not-yet-scheduled event whose
// absolute sample position is <= horizonSample, advancing the scheduling
// cursor (iterationIdx/timelineIdx) as it goes. For a looping sequence it
// wraps to the next iteration as needed; each iteration's sample positions
// are always computed fresh from that iteration's own absolute tick count,
// so looping many times over never accumulates drift.
func (p *playback) scheduleMore(horizonSample int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state != statePlaying || len(p.timeline) == 0 {
		return
	}

	sampleRate := p.engine.SampleRate()
	for {
		if p.timelineIdx >= len(p.timeline) {
			if !p.seq.Loop || p.loopTicks <= 0 {
				return
			}
			p.iterationIdx++
			// Every iteration after the first restarts at loopWrapIdx (the
			// startOffset's forward timeline index when one applies, else
			// 0) rather than always 0 - see loopSpanTicks/loopWrapIdx's own
			// doc comment on the playback struct.
			p.timelineIdx = p.loopWrapIdx
			continue
		}

		te := p.timeline[p.timelineIdx]
		// iterationIdx 0 always converts te.AtTick directly through the
		// tempo map, whatever tick it started at (0 for a fresh Play, or
		// anywhere else for SkipPlay/Resume); only a loop *restart*
		// (iterationIdx>=1) adds whole laps' worth of loopSpanSamples first
		// - each lap's own duration is derived from the tempo map once (see
		// loopSpanSamples's doc comment), not by feeding an ever-growing
		// tick count through it, so a lap's tempo automation replays
		// identically every time instead of extrapolating its tail tempo
		// forever. This collapses to the original
		// iterationIdx*loopTicks+te.AtTick arithmetic whenever there's a
		// single constant tempo and no startOffset/jump-point-based
		// restart applies.
		var iterBaseSamples int64
		if p.iterationIdx > 0 {
			iterBaseSamples = int64(p.iterationIdx) * p.loopSpanSamples
		}
		atSample := p.startSample + iterBaseSamples + common.TicksToSamplesFromMap(te.AtTick, p.tempoMap, sampleRate)
		if atSample > horizonSample {
			return
		}

		p.scheduleEvent(atSample, te)
		p.timelineIdx++
	}
}

// isPortamentoEvent reports whether a note event is a portamento ('&&')
// glide-in, which go-fmml/analyze only ever marks on a single-note event
// (never a chord - see analyze/mmlcommands.go's chord-portamento case) and
// applies identically to every note it contains, so checking the first is
// enough.
func isPortamentoEvent(notes []memory.NoteSpec) bool {
	return len(notes) > 0 && notes[0].Bend.Portamento
}

// scheduleEvent must be called with p.mu held.
func (p *playback) scheduleEvent(atSample int64, te timelineEvent) {
	if te.Domain == domainTempo {
		p.engine.ScheduleSetTempo(atSample, te.TempoBPM)
		return
	}
	if te.Domain == domainPCM {
		p.schedulePCMEvent(atSample, te)
		return
	}

	switch te.Event.Kind {
	case memory.EventNote:
		// Force-stop whatever was still sounding from the previous
		// note/chord on this part at the exact same sample the new one
		// starts, independent of that note's voice's own envelope/
		// ReleaseRate (see fmcore.Engine.ScheduleForceStopPart). Notes
		// within this same event (a chord) are unaffected since they're
		// scheduled at the same sample, after this call. Skipped for a
		// portamento ('&&') note: it's meant to glide the still-sounding
		// previous note in place (true legato - see fmcore.NoteBend's doc
		// comment), so force-stopping it here would defeat the point.
		if !isPortamentoEvent(te.Event.Notes) {
			p.engine.ScheduleForceStopPart(atSample, te.PartID)
		}
		for _, n := range te.Event.Notes {
			bend := fmcore.NoteBend{
				AttackOffsetSemitones:  n.Bend.AttackOffsetSemitones,
				AttackTicks:            n.Bend.AttackTicks,
				Portamento:             n.Bend.Portamento,
				ReleaseOffsetSemitones: n.Bend.ReleaseOffsetSemitones,
				ReleaseTicks:           n.Bend.ReleaseTicks,
				TieTicks:               n.Bend.TieTicks,
			}
			if err := p.engine.ScheduleNoteOnWithBend(atSample, te.PartID, n.NoteName, n.NoteType, n.Sustain, n.Velocity, bend); err != nil {
				log.Printf("player: failed to schedule NoteOn for part %d note %s: %v", te.PartID, n.NoteName, err)
			}
		}

	case memory.EventSetPan:
		rp := p.parts[te.PartID]
		if rp == nil {
			return
		}
		rp.pan = te.Event.Pan
		p.engine.ScheduleSetPart(atSample, te.PartID, rp.voiceID, effectiveVolume(rp.volume, rp.muted), rp.pan)

	case memory.EventSetVoice:
		rp := p.parts[te.PartID]
		if rp == nil {
			return
		}
		voice, ok := memory.GetVoice(te.Event.VoiceID)
		if !ok {
			voice = defaultVoice()
			log.Printf("player: part %d switched to unknown voice %d, using default voice", te.PartID, te.Event.VoiceID)
		}
		p.engine.RegisterVoice(te.Event.VoiceID, voice)
		rp.voiceID = te.Event.VoiceID
		p.engine.ScheduleSetPart(atSample, te.PartID, rp.voiceID, effectiveVolume(rp.volume, rp.muted), rp.pan)
	}
}

// schedulePCMEvent is scheduleEvent's PCM counterpart. Per CLAUDE.md, a
// OneShot part's samples are meant to ring independently (multiple
// concurrent hits, e.g. kick+snare+hat), so unlike a Note event it never
// force-stops the part first; a Long part reuses the exact same MML
// grammar (and so the exact same non-overlap behavior) as an FM part.
func (p *playback) schedulePCMEvent(atSample int64, te timelineEvent) {
	switch te.PCMEvent.Kind {
	case memory.PCMEventOneShotTrigger:
		p.engine.ScheduleTriggerPCMOneShot(atSample, te.PCMPartID, te.PCMEvent.OneShot.NoteNumber, te.PCMEvent.OneShot.Velocity)

	case memory.PCMEventNote:
		if !isPortamentoEvent(te.PCMEvent.Notes) {
			p.engine.ScheduleForceStopPCMPart(atSample, te.PCMPartID)
		}
		for _, n := range te.PCMEvent.Notes {
			bend := pcmcore.NoteBend{
				AttackOffsetSemitones:  n.Bend.AttackOffsetSemitones,
				AttackTicks:            n.Bend.AttackTicks,
				Portamento:             n.Bend.Portamento,
				ReleaseOffsetSemitones: n.Bend.ReleaseOffsetSemitones,
				ReleaseTicks:           n.Bend.ReleaseTicks,
				TieTicks:               n.Bend.TieTicks,
			}
			if err := p.engine.ScheduleNoteOnPCMLongWithBend(atSample, te.PCMPartID, n.NoteName, n.NoteType, n.Sustain, n.Velocity, bend); err != nil {
				log.Printf("player: failed to schedule PCM long NoteOn for part %c note %s: %v", te.PCMPartID, n.NoteName, err)
			}
		}

	case memory.PCMEventSetPan:
		rp := p.pcmParts[te.PCMPartID]
		if rp == nil {
			return
		}
		rp.pan = te.PCMEvent.Pan
		p.engine.ScheduleSetPCMPart(atSample, te.PCMPartID, rp.voiceID, effectiveVolume(rp.volume, rp.muted), rp.pan)

	case memory.PCMEventSetVoice:
		rp := p.pcmParts[te.PCMPartID]
		if rp == nil {
			return
		}
		voice, ok := memory.GetPCMVoice(te.PCMEvent.VoiceID)
		if !ok {
			log.Printf("player: PCM part %c switched to unknown voice %d, ignoring", te.PCMPartID, te.PCMEvent.VoiceID)
			return
		}
		p.engine.RegisterPCMVoice(te.PCMEvent.VoiceID, voice)
		rp.voiceID = te.PCMEvent.VoiceID
		p.engine.ScheduleSetPCMPart(atSample, te.PCMPartID, rp.voiceID, effectiveVolume(rp.volume, rp.muted), rp.pan)
	}
}

func (p *playback) pause() {
	p.mu.Lock()
	if p.state != statePlaying {
		p.mu.Unlock()
		return
	}
	p.state = statePaused
	elapsedSamples := p.engine.SampleClock() - p.startSample
	p.pausedAtTick = p.samplesToPausedTick(elapsedSamples, p.engine.SampleRate())
	partIDs := append([]fmcore.PartID(nil), p.partIDs...)
	pcmPartIDs := append([]pcmcore.PartID(nil), p.pcmPartIDs...)
	p.mu.Unlock()

	p.stopRefill()
	p.stopCompletionWatcher()
	p.cancelFadeTimer()
	// Fade the whole mix down over shortTransitionFadeSeconds (Phase 7) on
	// top of the per-note force-mute below - see shortTransitionFadeSeconds's
	// doc comment. savedMasterVolumeTarget remembers what to glide back up
	// to on resume(), since the caller may have changed the master volume
	// mid-playback (see SetMasterVolume/SetMasterVolumePercent).
	p.savedMasterVolumeTarget = p.engine.MasterVolumeTarget()
	p.engine.SetMasterVolumeSmooth(0, shortTransitionFadeSeconds)
	now := p.engine.SampleClock()
	p.engine.CancelScheduled()
	p.engine.DuckReverb()
	// Force-mute whatever was still sounding instead of leaving it to ring
	// out through the pause - see stopPlayback's identical call for why
	// this fades out over a few milliseconds rather than cutting the
	// waveform off. resume() already rewinds to a note's start if the
	// pause landed strictly inside it, so muting here never leaves the
	// resumed audio picking up mid-note either.
	for _, id := range partIDs {
		p.engine.ScheduleForceStopPart(now, id)
	}
	for _, id := range pcmPartIDs {
		p.engine.ScheduleForceStopPCMPart(now, id)
	}
}

// resume continues a paused sequence. If the pause landed strictly inside a
// note (rather than exactly on one's start), it rewinds to that note's
// start so the resumed audio doesn't begin mid-note.
func (p *playback) resume() {
	p.mu.Lock()
	if p.state != statePaused {
		p.mu.Unlock()
		return
	}
	pausedAtTick := p.pausedAtTick
	p.mu.Unlock()

	iterationIdx := 0
	tickInIter := pausedAtTick
	if p.seq.Loop && p.loopTicks > 0 {
		iterationIdx = pausedAtTick / p.loopTicks
		tickInIter = pausedAtTick % p.loopTicks
	}
	resolvedTickInIter, timelineIdx := p.resolveStartPosition(tickInIter)

	sampleRate := p.engine.SampleRate()
	now := p.engine.SampleClock()

	var iterBaseSamples int64
	if iterationIdx > 0 {
		iterBaseSamples = int64(iterationIdx) * p.loopSpanSamples
	}

	p.mu.Lock()
	p.startSample = now - (iterBaseSamples + common.TicksToSamplesFromMap(resolvedTickInIter, p.tempoMap, sampleRate))
	p.iterationIdx = iterationIdx
	p.timelineIdx = timelineIdx
	p.state = statePlaying
	p.mu.Unlock()

	// Glide back up from the pause's fade-to-0 (see pause's identical call)
	// to whatever level was actually in effect before pausing, rather than
	// snapping straight back to full volume.
	p.engine.SetMasterVolumeSmooth(p.savedMasterVolumeTarget, shortTransitionFadeSeconds)

	p.scheduleMore(p.horizon(now, sampleRate))
	if p.seq.Loop && p.loopTicks > 0 {
		p.startRefill()
	} else if p.onComplete != nil {
		p.startCompletionWatcher()
	}
}

func (p *playback) stopPlayback() {
	p.mu.Lock()
	if p.state == stateStopped {
		p.mu.Unlock()
		return
	}
	p.state = stateStopped
	partIDs := append([]fmcore.PartID(nil), p.partIDs...)
	pcmPartIDs := append([]pcmcore.PartID(nil), p.pcmPartIDs...)
	p.mu.Unlock()

	p.stopRefill()
	p.stopCompletionWatcher()
	p.cancelFadeTimer()
	// Fade the whole mix down over shortTransitionFadeSeconds (Phase 7) on
	// top of the per-note force-mute below - see shortTransitionFadeSeconds's
	// doc comment.
	p.engine.SetMasterVolumeSmooth(0, shortTransitionFadeSeconds)
	now := p.engine.SampleClock()
	p.engine.CancelScheduled()
	p.engine.DuckReverb()
	// Force-mute whatever was still sounding (including any release tail)
	// instead of leaving it to ring out on its own - see
	// runningNote.forceMute (fmcore and pcmcore) for why this fades out
	// over a few milliseconds rather than cutting the waveform off, which
	// would otherwise produce an audible click.
	for _, id := range partIDs {
		p.engine.ScheduleForceStopPart(now, id)
	}
	for _, id := range pcmPartIDs {
		p.engine.ScheduleForceStopPCMPart(now, id)
	}
}

// isPlaying reports whether playback is actively running (as opposed to
// paused or stopped).
func (p *playback) isPlaying() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state == statePlaying
}

// rewind resets the sequence's playback position back to tick 0. If
// currently playing, whatever is still sounding on every part is silenced
// immediately and scheduling restarts from the very beginning right away
// (the same as a fresh Play, minus re-registering voices/parts); if
// currently paused, only the position Resume will continue from is reset -
// playback itself stays paused. A no-op once playback has been stopped.
func (p *playback) rewind() {
	p.mu.Lock()
	state := p.state
	partIDs := append([]fmcore.PartID(nil), p.partIDs...)
	pcmPartIDs := append([]pcmcore.PartID(nil), p.pcmPartIDs...)
	p.mu.Unlock()

	switch state {
	case statePlaying:
		p.stopRefill()
		p.stopCompletionWatcher()
		p.cancelFadeTimer()
		now := p.engine.SampleClock()
		p.engine.CancelScheduled()
		p.engine.DuckReverb()
		for _, id := range partIDs {
			p.engine.ScheduleForceStopPart(now, id)
		}
		for _, id := range pcmPartIDs {
			p.engine.ScheduleForceStopPCMPart(now, id)
		}
		p.start(0)
	case statePaused:
		p.mu.Lock()
		p.pausedAtTick = 0
		p.mu.Unlock()
	}
}

// startRefill launches the background watchdog that keeps a looping
// sequence's schedule topped up (see refillLookaheadSeconds/
// refillPollInterval). No-op timing-wise for non-looping sequences, which
// are scheduled to completion in a single scheduleMore call and never need
// one.
func (p *playback) startRefill() {
	stop := make(chan struct{})
	done := make(chan struct{})
	p.mu.Lock()
	p.refillStop = stop
	p.refillDone = done
	p.mu.Unlock()

	go func() {
		defer close(done)
		ticker := time.NewTicker(refillPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				sampleRate := p.engine.SampleRate()
				now := p.engine.SampleClock()
				p.scheduleMore(now + int64(refillLookaheadSeconds*float64(sampleRate)))
			}
		}
	}()
}

// scheduleAutoFadeOut arranges (for a non-looping sequence only - see
// FadeOutPlay/FadeInOutPlay) for the master volume to glide down to 0 over
// fadeSeconds, timed to finish right as the sequence's own written timeline
// ends. A no-op for a looping sequence (CLAUDE.md: "loopがtrueの曲の場合は
// フェードアウトはしない") or a non-positive fadeSeconds.
func (p *playback) scheduleAutoFadeOut(fadeSeconds float64) {
	if p.seq.Loop || fadeSeconds <= 0 || p.loopTicks <= 0 {
		return
	}
	sampleRate := p.engine.SampleRate()
	totalSeconds := float64(common.TicksToSamplesFromMap(p.loopTicks, p.tempoMap, sampleRate)) / float64(sampleRate)
	if fadeSeconds > totalSeconds {
		fadeSeconds = totalSeconds
	}
	delay := totalSeconds - fadeSeconds
	p.scheduleFadeTimer(time.Duration(delay*float64(time.Second)), func() {
		if p.isPlaying() {
			p.engine.SetMasterVolumeSmooth(0, fadeSeconds)
		}
	})
}

// scheduleFadeTimer arranges for fn to run once, after delay - cancelling
// any previously pending one first, since only one such delayed fade
// action makes sense per playback at a time (see cancelFadeTimer).
func (p *playback) scheduleFadeTimer(delay time.Duration, fn func()) {
	p.mu.Lock()
	if p.pendingFadeTimer != nil {
		p.pendingFadeTimer.Stop()
	}
	p.pendingFadeTimer = time.AfterFunc(delay, fn)
	p.mu.Unlock()
}

// cancelFadeTimer cancels any pending delayed fade action (see
// scheduleFadeTimer) without running it, so playback that has already
// stopped/moved on never has a stale fade fire later.
func (p *playback) cancelFadeTimer() {
	p.mu.Lock()
	if p.pendingFadeTimer != nil {
		p.pendingFadeTimer.Stop()
		p.pendingFadeTimer = nil
	}
	p.mu.Unlock()
}

func (p *playback) stopRefill() {
	p.mu.Lock()
	stop := p.refillStop
	done := p.refillDone
	p.refillStop = nil
	p.refillDone = nil
	p.mu.Unlock()

	if stop != nil {
		close(stop)
		<-done
	}
}

// maxReleaseGrace bounds how much longer than the written timeline
// startCompletionWatcher will wait for the last note(s) to actually finish
// ringing out (see Engine.AnySounding) before giving up and calling
// onComplete anyway. This only matters for a pathologically slow
// ReleaseRate; any normally-tuned voice finishes well within it.
const maxReleaseGrace = 2 * time.Second

// startCompletionWatcher launches the background watcher that calls
// p.onComplete once the sequence has both (a) finished its own written
// timeline and (b) gone quiet - i.e. every note, including whatever is
// still ringing out in its release tail, has actually stopped sounding
// (bounded by maxReleaseGrace) - so onComplete doesn't cut the last note
// off mid-release. Only meaningful for a non-looping sequence (a looping
// one has no natural end); callers must not call this if p.onComplete is
// nil.
//
// Its poll timing is not audio-critical - it only decides when to notice
// that the timeline has ended and the audio has gone quiet, never fires a
// note itself - so refillPollInterval's granularity is fine here too.
func (p *playback) startCompletionWatcher() {
	sampleRate := p.engine.SampleRate()

	p.mu.Lock()
	targetSample := p.startSample + common.TicksToSamplesFromMap(p.loopTicks, p.tempoMap, sampleRate)
	onComplete := p.onComplete
	partIDs := p.partIDs
	pcmPartIDs := p.pcmPartIDs
	stop := make(chan struct{})
	done := make(chan struct{})
	p.completionStop = stop
	p.completionDone = done
	p.mu.Unlock()

	go func() {
		ticker := time.NewTicker(refillPollInterval)
		defer ticker.Stop()
		var reachedEndAt time.Time
		for {
			select {
			case <-stop:
				close(done)
				return
			case <-ticker.C:
				if p.engine.SampleClock() < targetSample {
					continue
				}
				if reachedEndAt.IsZero() {
					reachedEndAt = time.Now()
				}
				stillSounding := p.engine.AnySounding(partIDs...) || p.engine.AnySoundingPCM(pcmPartIDs...)
				if stillSounding && time.Since(reachedEndAt) < maxReleaseGrace {
					continue // let the last note's release tail ring out
				}

				p.mu.Lock()
				stillPlaying := p.state == statePlaying
				p.state = stateStopped
				// Clear these before calling onComplete: if onComplete
				// calls Pause/Resume/Stop (a natural thing to do - e.g.
				// Stop the app), stopCompletionWatcher must not try to
				// wait on this same goroutine's own done channel from
				// inside itself.
				p.completionStop = nil
				p.completionDone = nil
				p.mu.Unlock()
				close(done)
				if stillPlaying && onComplete != nil {
					onComplete()
				}
				return
			}
		}
	}()
}

func (p *playback) stopCompletionWatcher() {
	p.mu.Lock()
	stop := p.completionStop
	done := p.completionDone
	p.completionStop = nil
	p.completionDone = nil
	p.mu.Unlock()

	if stop != nil {
		close(stop)
		<-done
	}
}
