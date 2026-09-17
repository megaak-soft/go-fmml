/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

// Package midiparse decodes a Standard MIDI File's raw bytes into the plain
// per-track note/metadata content smf2gofmml's convert package needs -
// nothing more (no general-purpose MIDI event model, no export back to
// bytes).
package midiparse

import (
	"encoding/binary"
	"fmt"
)

// NoteEvent is one decoded MIDI note (on tick -> off tick pair), in the raw
// tick resolution of the source SMF file's own division (not yet scaled to
// go-fmml's internal tick grid - that's convert's job).
type NoteEvent struct {
	OnTick  int
	OffTick int
	Note    uint8
}

// ParsedTrack is one SMF track's relevant content: its name (if any, for
// [FM]/[PCM long]/[PCM oneShot] classification), decoded notes, and the
// first channel-volume/pan controller values seen at its head (if any).
type ParsedTrack struct {
	Name      string
	Notes     []NoteEvent
	VolumeCC7 *int
	PanCC10   *int
}

// SMFResult is a whole parsed Standard MIDI File: header fields plus every
// track's decoded content, in original track order.
type SMFResult struct {
	Format   int
	Division int
	TempoBPM float64
	SongName string
	Tracks   []ParsedTrack
}

// readVarLen decodes one MIDI variable-length quantity starting at pos: up
// to 4 bytes, 7 payload bits each, continuing while the top bit is set.
func readVarLen(data []byte, pos int) (value int, newPos int, err error) {
	for {
		if pos >= len(data) {
			return 0, pos, fmt.Errorf("unexpected end of data while reading a variable-length value")
		}
		b := data[pos]
		pos++
		value = (value << 7) | int(b&0x7F)
		if b&0x80 == 0 {
			return value, pos, nil
		}
	}
}

// ParseSMF decodes a Standard MIDI File's bytes into an SMFResult: the
// header chunk (format/division), then every MTrk chunk's notes, track
// name, first tempo meta event found, and first CC7/CC10 controller values.
func ParseSMF(data []byte) (*SMFResult, error) {
	if len(data) < 14 || string(data[0:4]) != "MThd" {
		return nil, fmt.Errorf("not a Standard MIDI File (missing MThd header)")
	}
	headerLen := binary.BigEndian.Uint32(data[4:8])
	if headerLen < 6 || int(8+headerLen) > len(data) {
		return nil, fmt.Errorf("invalid MThd header length %d", headerLen)
	}
	format := int(binary.BigEndian.Uint16(data[8:10]))
	ntrks := int(binary.BigEndian.Uint16(data[10:12]))
	division := int(binary.BigEndian.Uint16(data[12:14]))
	if division&0x8000 != 0 {
		return nil, fmt.Errorf("SMPTE-based time division is not supported")
	}
	if division == 0 {
		return nil, fmt.Errorf("invalid time division 0")
	}

	result := &SMFResult{Format: format, Division: division}

	pos := 8 + int(headerLen)
	for t := 0; t < ntrks; t++ {
		if pos+8 > len(data) || string(data[pos:pos+4]) != "MTrk" {
			return nil, fmt.Errorf("track %d: missing MTrk chunk", t)
		}
		chunkLen := int(binary.BigEndian.Uint32(data[pos+4 : pos+8]))
		chunkStart := pos + 8
		chunkEnd := chunkStart + chunkLen
		if chunkLen < 0 || chunkEnd > len(data) {
			return nil, fmt.Errorf("track %d: chunk length exceeds file size", t)
		}

		track, tempo, err := parseTrackChunk(data[chunkStart:chunkEnd])
		if err != nil {
			return nil, fmt.Errorf("track %d: %w", t, err)
		}
		if tempo > 0 && result.TempoBPM == 0 {
			result.TempoBPM = tempo
		}
		if track.Name != "" && result.SongName == "" {
			result.SongName = track.Name
		}
		result.Tracks = append(result.Tracks, track)

		pos = chunkEnd
	}

	if result.TempoBPM <= 0 {
		result.TempoBPM = 120
	}
	if result.SongName == "" {
		result.SongName = "untitled"
	}
	return result, nil
}

// parseTrackChunk decodes one MTrk chunk's event stream (delta-time +
// event, repeated), tracking running status per the SMF spec, and pairs
// note-on/note-off events (a note-on with velocity 0 counts as a note-off)
// via a per-note-number onset stack so overlapping same-pitch notes resolve
// in the order they were struck.
func parseTrackChunk(data []byte) (ParsedTrack, float64, error) {
	var track ParsedTrack
	tempoBPM := 0.0
	pos := 0
	tick := 0
	var runningStatus byte
	pending := make(map[uint8][]int)

	readByte := func() (byte, error) {
		if pos >= len(data) {
			return 0, fmt.Errorf("unexpected end of track data")
		}
		v := data[pos]
		pos++
		return v, nil
	}

	for pos < len(data) {
		delta, newPos, err := readVarLen(data, pos)
		if err != nil {
			return track, tempoBPM, err
		}
		pos = newPos
		tick += delta

		if pos >= len(data) {
			break
		}
		b := data[pos]

		if b == 0xFF {
			pos++
			metaType, err := readByte()
			if err != nil {
				return track, tempoBPM, err
			}
			length, newPos2, err := readVarLen(data, pos)
			if err != nil {
				return track, tempoBPM, err
			}
			pos = newPos2
			if length < 0 || pos+length > len(data) {
				return track, tempoBPM, fmt.Errorf("meta event length overruns track")
			}
			payload := data[pos : pos+length]
			pos += length

			switch metaType {
			case 0x03:
				if track.Name == "" {
					track.Name = string(payload)
				}
			case 0x51:
				if length == 3 {
					micros := int(payload[0])<<16 | int(payload[1])<<8 | int(payload[2])
					if micros > 0 {
						tempoBPM = 60000000.0 / float64(micros)
					}
				}
			case 0x2F:
				pos = len(data)
			}
			continue
		}

		if b == 0xF0 || b == 0xF7 {
			pos++
			length, newPos2, err := readVarLen(data, pos)
			if err != nil {
				return track, tempoBPM, err
			}
			pos = newPos2 + length
			if pos > len(data) {
				return track, tempoBPM, fmt.Errorf("sysex event length overruns track")
			}
			continue
		}

		var status byte
		if b&0x80 != 0 {
			status = b
			pos++
			runningStatus = status
		} else {
			status = runningStatus
			if status == 0 {
				return track, tempoBPM, fmt.Errorf("running status used before any status byte was seen")
			}
		}

		switch status & 0xF0 {
		case 0x80:
			note, err := readByte()
			if err != nil {
				return track, tempoBPM, err
			}
			if _, err := readByte(); err != nil {
				return track, tempoBPM, err
			}
			closeNote(pending, &track, note, tick)

		case 0x90:
			note, err := readByte()
			if err != nil {
				return track, tempoBPM, err
			}
			vel, err := readByte()
			if err != nil {
				return track, tempoBPM, err
			}
			if vel == 0 {
				closeNote(pending, &track, note, tick)
			} else {
				pending[note] = append(pending[note], tick)
			}

		case 0xA0, 0xB0, 0xE0:
			b1, err := readByte()
			if err != nil {
				return track, tempoBPM, err
			}
			b2, err := readByte()
			if err != nil {
				return track, tempoBPM, err
			}
			if status&0xF0 == 0xB0 {
				switch b1 {
				case 7:
					if track.VolumeCC7 == nil {
						v := int(b2)
						track.VolumeCC7 = &v
					}
				case 10:
					if track.PanCC10 == nil {
						v := int(b2)
						track.PanCC10 = &v
					}
				}
			}

		case 0xC0, 0xD0:
			if _, err := readByte(); err != nil {
				return track, tempoBPM, err
			}

		default:
			return track, tempoBPM, fmt.Errorf("unsupported status byte 0x%02X", status)
		}
	}

	// Any note-on left without a matching note-off is closed at the
	// track's final tick so it isn't silently dropped from conversion.
	for note, onsets := range pending {
		for _, on := range onsets {
			track.Notes = append(track.Notes, NoteEvent{OnTick: on, OffTick: tick, Note: note})
		}
	}

	return track, tempoBPM, nil
}

func closeNote(pending map[uint8][]int, track *ParsedTrack, note uint8, offTick int) {
	stack := pending[note]
	if len(stack) == 0 {
		return
	}
	onTick := stack[0]
	pending[note] = stack[1:]
	track.Notes = append(track.Notes, NoteEvent{OnTick: onTick, OffTick: offTick, Note: note})
}
