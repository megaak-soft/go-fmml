/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
*/

package fmcore

// algorithmDef describes how a voice's four operators are wired together:
// edges are modulator->carrier connections (always low index -> high index,
// so a single ascending pass can resolve them), and carriers are the
// operator indices summed into the note's audible output.
//
// The eight algorithms range from a fully serial FM chain (algorithm 0) to
// four independent carriers (algorithm 7), a spread chosen to cover the
// common general-purpose operator-routing shapes (serial chain, parallel
// pairs, one-to-many, all-independent). This is an original layout designed
// for this engine, not a reproduction of any specific chip's register map.
type algorithmDef struct {
	edges    [][2]int
	carriers []int
}

var algorithms = [8]algorithmDef{
	{edges: [][2]int{{0, 1}, {1, 2}, {2, 3}}, carriers: []int{3}},       // 0: 1->2->3->4
	{edges: [][2]int{{0, 2}, {1, 2}, {2, 3}}, carriers: []int{3}},       // 1: 1->3, 2->3->4
	{edges: [][2]int{{0, 3}, {1, 2}, {2, 3}}, carriers: []int{3}},       // 2: 1->4, 2->3->4
	{edges: [][2]int{{0, 1}, {1, 3}, {2, 3}}, carriers: []int{3}},       // 3: 1->2->4, 3->4
	{edges: [][2]int{{0, 1}, {2, 3}}, carriers: []int{1, 3}},            // 4: (1->2) + (3->4)
	{edges: [][2]int{{0, 1}, {0, 2}, {0, 3}}, carriers: []int{1, 2, 3}}, // 5: 1->2, 1->3, 1->4
	{edges: [][2]int{{0, 1}}, carriers: []int{1, 2, 3}},                 // 6: 1->2, 3 and 4 independent
	{edges: nil, carriers: []int{0, 1, 2, 3}},                           // 7: all operators are carriers
}

// feedbackScale converts a 0-7 feedback amount into a phase-offset scale for
// operator 1's self-modulation.
func feedbackScale(amount int) float64 {
	if amount <= 0 {
		return 0
	}
	if amount > 7 {
		amount = 7
	}
	return float64(amount) / 8
}

// renderOperators steps all four operators of a note by one sample according
// to its algorithm's connection graph and returns the summed carrier output.
func renderOperators(ops *[4]operator, alg algorithmDef, sampleRate int, noteFreq float64, feedbackAmount int) float64 {
	var modIn [4]float64
	var out [4]float64

	fb := ops[0].lastOut * feedbackScale(feedbackAmount)

	for i := 0; i < 4; i++ {
		var selfFB float64
		if i == 0 {
			selfFB = fb
		}
		out[i] = ops[i].step(sampleRate, noteFreq, modIn[i], selfFB)
		for _, e := range alg.edges {
			if e[0] == i {
				modIn[e[1]] += out[i]
			}
		}
	}

	if len(alg.carriers) == 0 {
		return 0
	}
	var sum float64
	for _, c := range alg.carriers {
		sum += out[c]
	}
	return sum / float64(len(alg.carriers))
}
