/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package constants

// Note-type (音符種別) names understood by fmcore.Engine.NoteOn. A "_DOT"
// suffix denotes a dotted note (1.5x the base note's duration). Note6/
// Note12/Note24 are triplet lengths (CLAUDE.md's MML length digits 6/12/24
// - a quarter/eighth/sixteenth-note triplet respectively).
const (
	Note1     = "NOTE1"      // whole note
	Note1Dot  = "NOTE1_DOT"  // dotted whole note
	Note2     = "NOTE2"      // half note
	Note2Dot  = "NOTE2_DOT"  // dotted half note
	Note4     = "NOTE4"      // quarter note
	Note4Dot  = "NOTE4_DOT"  // dotted quarter note
	Note6     = "NOTE6"      // quarter-note triplet
	Note6Dot  = "NOTE6_DOT"  // dotted quarter-note triplet
	Note8     = "NOTE8"      // eighth note
	Note8Dot  = "NOTE8_DOT"  // dotted eighth note
	Note12    = "NOTE12"     // eighth-note triplet
	Note12Dot = "NOTE12_DOT" // dotted eighth-note triplet
	Note16    = "NOTE16"     // sixteenth note
	Note16Dot = "NOTE16_DOT" // dotted sixteenth note
	Note24    = "NOTE24"     // sixteenth-note triplet
	Note24Dot = "NOTE24_DOT" // dotted sixteenth-note triplet
	Note32    = "NOTE32"     // thirty-second note
	Note32Dot = "NOTE32_DOT" // dotted thirty-second note
)
