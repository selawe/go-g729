package g729_test

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"testing"

	g729 "github.com/selawe/go-g729"
)

func TestStreamRoundTrip(t *testing.T) {
	// Generate 10 frames (100 ms = 800 samples = 1600 bytes) of 440 Hz tone
	const numSamples = 800
	pcmInput := make([]byte, numSamples*2)
	for i := 0; i < numSamples; i++ {
		s := int16(10000.0 * math.Sin(2*math.Pi*440.0*float64(i)/8000.0))
		binary.LittleEndian.PutUint16(pcmInput[i*2:(i+1)*2], uint16(s))
	}

	// Test with various arbitrary chunk sizes: 1B, 7B, 50B, 160B, 500B, 1600B
	chunkSizes := []int{1, 7, 50, 160, 500, 1600}

	for _, chunkSize := range chunkSizes {
		var bitstreamBuf bytes.Buffer
		writer := g729.NewWriter(&bitstreamBuf, g729.ProfileFast())

		// Stream write in chunks
		for offset := 0; offset < len(pcmInput); offset += chunkSize {
			end := offset + chunkSize
			if end > len(pcmInput) {
				end = len(pcmInput)
			}
			n, err := writer.Write(pcmInput[offset:end])
			if err != nil {
				t.Fatalf("chunkSize %d: Write failed: %v", chunkSize, err)
			}
			if n != end-offset {
				t.Fatalf("chunkSize %d: wrote %d bytes, want %d", chunkSize, n, end-offset)
			}
		}

		if err := writer.Close(); err != nil {
			t.Fatalf("chunkSize %d: Close failed: %v", chunkSize, err)
		}

		// 10 frames of G.729A CBR = 100 bytes of bitstream
		if bitstreamBuf.Len() != 100 {
			t.Fatalf("chunkSize %d: bitstream len %d, want 100", chunkSize, bitstreamBuf.Len())
		}

		// Stream read back into PCM
		reader := g729.NewReader(&bitstreamBuf)
		var decodedPCM bytes.Buffer
		readBuf := make([]byte, chunkSize)

		for {
			n, err := reader.Read(readBuf)
			if n > 0 {
				decodedPCM.Write(readBuf[:n])
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("chunkSize %d: Read failed: %v", chunkSize, err)
			}
		}

		if decodedPCM.Len() != len(pcmInput) {
			t.Fatalf("chunkSize %d: decoded len %d, want %d", chunkSize, decodedPCM.Len(), len(pcmInput))
		}
	}
}

func TestStreamPartialFramePadding(t *testing.T) {
	// Write 100 samples (200 bytes) = 1 full frame (160 bytes) + 40 bytes partial
	pcmInput := make([]byte, 200)
	var bitstreamBuf bytes.Buffer
	writer := g729.NewWriter(&bitstreamBuf, g729.ProfileFast())

	_, err := writer.Write(pcmInput)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Before close, only 1 frame (10 bytes) should be emitted
	if bitstreamBuf.Len() != 10 {
		t.Fatalf("expected 10 bytes before close, got %d", bitstreamBuf.Len())
	}

	// Close must flush the partial frame zero-padded, resulting in 20 bytes total (2 frames)
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if bitstreamBuf.Len() != 20 {
		t.Fatalf("expected 20 bytes after close (2 frames), got %d", bitstreamBuf.Len())
	}
}
