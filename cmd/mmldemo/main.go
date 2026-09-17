/*
* Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)
* SPDX-License-Identifier: MIT
*
* Full license text is available in the LICENSE file at the root of this repository.
 */

// Command mmldemo is a minimal working example: it loads the sample FM
// voice/MML files from this directory and plays the resulting sequence
// through a real audio device. Run it from this directory:
//
//	cd cmd/mmldemo
//	go run .
package main

import (
	"log"

	"github.com/megaak-soft/go-fmml/fileio"
	"github.com/megaak-soft/go-fmml/fmcore"
	"github.com/megaak-soft/go-fmml/player"
)

func main() {
	if err := fileio.LoadFMVoiceFile("demo_fm.yaml"); err != nil {
		log.Fatalf("failed to load demo_fm.yaml: %v", err)
	}
	if err := fileio.LoadPCMVoiceFile("demo_pcm.yaml"); err != nil {
		log.Fatalf("failed to load demo_pcm.yaml: %v", err)
	}
	if err := fileio.LoadMMLFile("demo.mml"); err != nil {
		log.Fatalf("failed to load demo.mml: %v", err)
	}

	engine, err := fmcore.NewEngine(44100)
	if err != nil {
		log.Fatalf("failed to initialize audio: %v", err)
	}
	defer engine.Close()

	done := make(chan struct{})
	onComplete := func() {
		log.Println("playback finished")
		close(done)
	}

	if err := player.Play(engine, "demo", onComplete); err != nil {
		log.Fatalf("failed to start playback: %v", err)
	}

	<-done
}
