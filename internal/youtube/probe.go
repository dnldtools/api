package youtube

import (
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"strings"
)

// probeResult holds the real media properties extracted from the produced
// download file. A zero BitrateKbps means the bitrate could not be determined.
type probeResult struct {
	BitrateKbps int
	Codec       string
	SizeBytes   int64
}

// probeMaxBytes limits how much of the download we read to detect an MPEG
// audio frame header. A leading ID3v2 tag plus the first frames is far below
// this limit.
const probeMaxBytes = 512 << 10 // 512 KiB

// probe inspects the produced file without depending on ffprobe being
// installed. MP3 (MPEG audio frame header), WAV (PCM fmt chunk) and FLAC
// (STREAMINFO + file size) are parsed directly; other containers fall back to
// HTTP metadata (Content-Length and Content-Type). Probing is best-effort: on
// any failure the zero value is returned and the caller falls back to the
// worker's reported selection.
func (s *Service) probe(ctx context.Context, downloadURL, expectedFormat string) probeResult {
	res := probeResult{Codec: expectedFormat}

	reqCtx, cancel := context.WithTimeout(ctx, s.cfg.RequestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return res
	}
	req.Header.Set("User-Agent", s.cfg.UserAgent)

	client := s.client
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return res
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return res
	}

	res.SizeBytes = resp.ContentLength
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	res.Codec = codecFromContentType(contentType, expectedFormat)

	// MP3, WAV and FLAC can be probed without ffprobe. Other containers keep
	// only the Content-Length / Content-Type metadata gathered above.
	switch {
	case expectedFormat == "mp3" || strings.Contains(contentType, "mpeg"):
		data, _ := io.ReadAll(io.LimitReader(resp.Body, probeMaxBytes))
		if bitrate, _, ok := probeMP3(data); ok {
			res.BitrateKbps = bitrate
			res.Codec = "mp3"
		}
	case expectedFormat == "wav" || strings.Contains(contentType, "wav"):
		data, _ := io.ReadAll(io.LimitReader(resp.Body, probeMaxBytes))
		if bitrate, ok := probeWAV(data); ok {
			res.BitrateKbps = bitrate
			res.Codec = "wav"
		}
	case expectedFormat == "flac" || strings.Contains(contentType, "flac"):
		data, _ := io.ReadAll(io.LimitReader(resp.Body, probeMaxBytes))
		if bitrate, ok := probeFLAC(data, res.SizeBytes); ok {
			res.BitrateKbps = bitrate
			res.Codec = "flac"
		}
	}
	return res
}

// probeMP3 scans data for the first valid MPEG audio frame header and returns
// its bitrate (kbps) and sample rate (Hz). A leading ID3v2 tag is skipped.
func probeMP3(data []byte) (bitrate, sampleRate int, ok bool) {
	i := id3v2Size(data)
	for i+4 <= len(data) {
		if data[i] == 0xFF && data[i+1]&0xE0 == 0xE0 {
			header := binary.BigEndian.Uint32(data[i : i+4])
			if b, sr, ok := parseMPEGHeader(header); ok {
				return b, sr, true
			}
		}
		i++
	}
	return 0, 0, false
}

// id3v2Size returns the byte offset just past an ID3v2 tag at the start of
// data, or 0 when no tag is present.
func id3v2Size(data []byte) int {
	if len(data) < 10 || string(data[0:3]) != "ID3" {
		return 0
	}
	size := int(data[6])<<21 | int(data[7])<<14 | int(data[8])<<7 | int(data[9])
	return 10 + size
}

// parseMPEGHeader decodes a 32-bit MPEG audio frame header into bitrate and
// sample rate. It returns ok=false for reserved/invalid combinations so a
// random 0xFF sync byte does not produce a false positive.
func parseMPEGHeader(h uint32) (bitrate, sampleRate int, ok bool) {
	if h&0xFFE00000 != 0xFFE00000 { // 11-bit sync word
		return 0, 0, false
	}

	version := (h >> 19) & 0x3 // 0=MPEG 2.5, 2=MPEG 2, 3=MPEG 1
	layer := (h >> 17) & 0x3   // 1=Layer III, 2=Layer II, 3=Layer I
	bitrateIdx := (h >> 12) & 0xF
	sampleRateIdx := (h >> 10) & 0x3

	if version == 0x1 || layer == 0x0 { // reserved
		return 0, 0, false
	}
	if bitrateIdx == 0 || bitrateIdx == 0xF { // free or invalid
		return 0, 0, false
	}
	if sampleRateIdx == 0x3 { // reserved
		return 0, 0, false
	}

	var bitrates []int
	switch version {
	case 0x3: // MPEG 1
		switch layer {
		case 0x3: // Layer I
			bitrates = []int{0, 32, 64, 96, 128, 160, 192, 224, 256, 288, 320, 352, 384, 416, 448}
		case 0x2: // Layer II
			bitrates = []int{0, 32, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 384}
		case 0x1: // Layer III
			bitrates = []int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320}
		}
	default: // MPEG 2 or 2.5
		switch layer {
		case 0x3: // Layer I
			bitrates = []int{0, 32, 48, 56, 64, 80, 96, 112, 128, 144, 160, 176, 192, 224, 256}
		case 0x2: // Layer II
			bitrates = []int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160}
		case 0x1: // Layer III
			bitrates = []int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160}
		}
	}
	if int(bitrateIdx) >= len(bitrates) {
		return 0, 0, false
	}
	bitrate = bitrates[bitrateIdx]

	var rates []int
	switch version {
	case 0x3: // MPEG 1
		rates = []int{44100, 48000, 32000}
	case 0x2: // MPEG 2
		rates = []int{22050, 24000, 16000}
	case 0x0: // MPEG 2.5
		rates = []int{11025, 12000, 8000}
	}
	sampleRate = rates[sampleRateIdx]

	return bitrate, sampleRate, true
}

// probeWAV parses the RIFF/WAVE "fmt " chunk and derives the PCM bitrate from
// sample rate, channel count and bits per sample (e.g. 44.1 kHz stereo 16-bit
// → 1411 kbps). PCM (format 1) and IEEE float (format 3) streams are accepted.
func probeWAV(data []byte) (int, bool) {
	if len(data) < 12 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return 0, false
	}

	off := 12
	for off+8 <= len(data) {
		chunkID := string(data[off : off+4])
		chunkSize := int(binary.LittleEndian.Uint32(data[off+4 : off+8]))
		body := off + 8
		if body+chunkSize > len(data) {
			return 0, false
		}

		if chunkID == "fmt " && chunkSize >= 16 {
			audioFormat := binary.LittleEndian.Uint16(data[body : body+2])
			channels := int(binary.LittleEndian.Uint16(data[body+2 : body+4]))
			sampleRate := int(binary.LittleEndian.Uint32(data[body+4 : body+8]))
			bitsPerSample := int(binary.LittleEndian.Uint16(data[body+14 : body+16]))
			if (audioFormat == 1 || audioFormat == 3) && sampleRate > 0 && channels > 0 && bitsPerSample > 0 {
				return sampleRate * channels * bitsPerSample / 1000, true
			}
			return 0, false
		}

		off = body + chunkSize
		if chunkSize%2 == 1 {
			off++ // chunks are word-aligned
		}
	}
	return 0, false
}

// probeFLAC parses the FLAC STREAMINFO metadata block to obtain the total
// sample count and sample rate, then derives the average compressed bitrate
// from the full file size (Content-Length). A streaming response without a
// known Content-Length returns ok=false.
func probeFLAC(data []byte, fileSize int64) (int, bool) {
	if fileSize <= 0 || len(data) < 42 || string(data[0:4]) != "fLaC" {
		return 0, false
	}

	// The first metadata block is always STREAMINFO (type 0).
	blockType := data[4] & 0x7F
	blockLen := int(data[5])<<16 | int(data[6])<<8 | int(data[7])
	if blockType != 0 || blockLen < 34 || 8+34 > len(data) {
		return 0, false
	}

	st := data[8:42]
	sampleRate := int(st[10])<<12 | int(st[11])<<4 | int(st[12])>>4
	totalSamples := int64(st[13]&0x0F)<<32 | int64(st[14])<<24 | int64(st[15])<<16 | int64(st[16])<<8 | int64(st[17])
	if sampleRate <= 0 || totalSamples <= 0 {
		return 0, false
	}

	durationSec := float64(totalSamples) / float64(sampleRate)
	if durationSec <= 0 {
		return 0, false
	}
	kbps := int(float64(fileSize) * 8 / durationSec / 1000)
	if kbps <= 0 {
		return 0, false
	}
	return kbps, true
}

func codecFromContentType(contentType, fallback string) string {
	ct := strings.ToLower(contentType)
	switch {
	case strings.Contains(ct, "audio/mpeg"), strings.Contains(ct, "audio/mp3"):
		return "mp3"
	case strings.Contains(ct, "audio/mp4"), strings.Contains(ct, "audio/x-m4a"):
		return "m4a"
	case strings.Contains(ct, "video/mp4"):
		return "mp4"
	case strings.Contains(ct, "video/webm"):
		return "webm"
	case strings.Contains(ct, "audio/ogg"):
		return "ogg"
	case strings.Contains(ct, "audio/flac"), strings.Contains(ct, "audio/x-flac"):
		return "flac"
	case strings.Contains(ct, "audio/aac"):
		return "aac"
	case strings.Contains(ct, "audio/wav"), strings.Contains(ct, "audio/x-wav"):
		return "wav"
	case strings.Contains(ct, "audio/opus"):
		return "opus"
	}
	return fallback
}
