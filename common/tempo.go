/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

// Package common holds process-wide playback settings shared across the
// go-fmml library, such as the tempo used to convert note types into
// millisecond durations.
package common

// Tempo is the global tempo in beats (quarter notes) per minute, used by
// fmcore.Engine.NoteOn to convert a note type (e.g. "NOTE4") into a
// duration in milliseconds. Defaults to 120 BPM.
var Tempo float64 = 120.0
