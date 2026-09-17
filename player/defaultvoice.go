/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package player

import "github.com/megaak-soft/go-fmml/fmcore"

// defaultVoice is used when a sequence references a voice ID that isn't
// present in go-fmml/memory, so a missing voice doesn't stop playback of
// the rest of the sequence.
func defaultVoice() fmcore.Voice {
	return fmcore.Voice{
		Algorithm: 0,
		Feedback:  0,
		Operators: [4]fmcore.OperatorParams{
			{Multiple: 1, TotalLevel: 20, Envelope: fmcore.EnvelopeParams{AttackRate: 31, Decay1Rate: 10, SustainLevel: 0.6, Decay2Rate: 4, ReleaseRate: 12}},
			{Multiple: 2, TotalLevel: 40, Envelope: fmcore.EnvelopeParams{AttackRate: 31, Decay1Rate: 12, SustainLevel: 0.3, Decay2Rate: 4, ReleaseRate: 12}},
			{Multiple: 1, TotalLevel: 30, Envelope: fmcore.EnvelopeParams{AttackRate: 31, Decay1Rate: 10, SustainLevel: 0.5, Decay2Rate: 4, ReleaseRate: 12}},
			{Multiple: 1, TotalLevel: 0, Envelope: fmcore.EnvelopeParams{AttackRate: 31, Decay1Rate: 8, SustainLevel: 0.7, Decay2Rate: 2, ReleaseRate: 30}},
		},
	}
}
