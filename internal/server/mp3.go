package server

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"time"
)

// ---- MP3 frames ----
//
// A channel is one endless MP3 stream made of songs one after the other.
// MP3 frames can simply follow each other, so the broadcaster reads each
// song frame by frame (to know how long every frame plays, and to send them
// in real time) and drops what is not sound: the ID3 tags and the
// Xing/Info frame at the start of a file.

var bitratesV1L3 = [16]int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 0}
var bitratesV2L3 = [16]int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0}
var sampleRates = [4][3]int{{11025, 12000, 8000}, {0, 0, 0}, {22050, 24000, 16000}, {44100, 48000, 32000}}

type mp3Header struct {
	length   int
	duration time.Duration
}

// parseMP3Header reads a 4-byte MPEG audio layer III frame header.
func parseMP3Header(h []byte) (mp3Header, bool) {
	if len(h) < 4 || h[0] != 0xFF || h[1]&0xE0 != 0xE0 {
		return mp3Header{}, false
	}
	version := (h[1] >> 3) & 3 // 3 = MPEG1, 2 = MPEG2, 0 = MPEG2.5
	layer := (h[1] >> 1) & 3   // 1 = layer III
	if version == 1 || layer != 1 {
		return mp3Header{}, false
	}
	bi := h[2] >> 4
	si := (h[2] >> 2) & 3
	pad := int((h[2] >> 1) & 1)
	if bi == 0 || bi == 15 || si == 3 {
		return mp3Header{}, false
	}
	sr := sampleRates[version][si]
	var br, samples, length int
	if version == 3 {
		br, samples = bitratesV1L3[bi], 1152
		length = 144*br*1000/sr + pad
	} else {
		br, samples = bitratesV2L3[bi], 576
		length = 72*br*1000/sr + pad
	}
	if length < 24 {
		return mp3Header{}, false
	}
	return mp3Header{length: length, duration: time.Duration(samples) * time.Second / time.Duration(sr)}, true
}

type mp3Reader struct {
	r     *bufio.Reader
	first bool
}

func newMP3Reader(r io.Reader) *mp3Reader {
	return &mp3Reader{r: bufio.NewReaderSize(r, 64<<10), first: true}
}

var errNoFrames = errors.New("no MP3 frames in the song")

// next returns the next audio frame and how long it plays.
func (m *mp3Reader) next() ([]byte, time.Duration, error) {
	skipped := 0
	for {
		if m.first {
			if hd, err := m.r.Peek(10); err == nil && bytes.HasPrefix(hd, []byte("ID3")) {
				size := int(hd[6]&0x7F)<<21 | int(hd[7]&0x7F)<<14 | int(hd[8]&0x7F)<<7 | int(hd[9]&0x7F)
				if hd[5]&0x10 != 0 {
					size += 10
				}
				if _, err := m.r.Discard(10 + size); err != nil {
					return nil, 0, err
				}
				continue
			}
		}
		hd, err := m.r.Peek(4)
		if err != nil {
			return nil, 0, err
		}
		h, ok := parseMP3Header(hd)
		if !ok {
			if _, err := m.r.Discard(1); err != nil {
				return nil, 0, err
			}
			if skipped++; skipped > 256<<10 {
				return nil, 0, errNoFrames
			}
			continue
		}
		frame := make([]byte, h.length)
		if _, err := io.ReadFull(m.r, frame); err != nil {
			return nil, 0, err
		}
		if m.first {
			m.first = false
			if end := min(len(frame), 48); bytes.Contains(frame[4:end], []byte("Xing")) || bytes.Contains(frame[4:end], []byte("Info")) {
				continue
			}
		}
		return frame, h.duration, nil
	}
}
