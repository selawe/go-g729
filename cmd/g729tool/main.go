// Command g729tool is a command-line tool for encoding and decoding ITU-T G.729 / G.729A audio files.
// Supports both raw 16-bit linear PCM and standard WAV audio files.
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/selawe/go-g729"
)

func main() {
	var (
		flagEncode   = flag.Bool("e", false, "Encode mode (PCM/WAV -> G.729)")
		flagDecode   = flag.Bool("d", false, "Decode mode (G.729 -> PCM/WAV)")
		flagFull     = flag.Bool("full", false, "Use Full G.729 (nested search) instead of G.729A")
		flagVAD      = flag.Bool("vad", false, "Enable Annex B VAD / DTX / CNG")
		flagLoss     = flag.Float64("loss", 0.0, "Simulate packet loss rate during decode (0.0 to 1.0)")
		flagWavOut   = flag.Bool("wav", false, "Force output as WAV format when decoding")
		flagBenchmark = flag.Bool("bench", false, "Print benchmark execution speed and real-time factor")
	)

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options] <input_file> <output_file>\n\n", filepath.Base(os.Args[0]))
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  %s -e input.wav output.g729           # Encode WAV to G.729\n", filepath.Base(os.Args[0]))
		fmt.Fprintf(os.Stderr, "  %s -e -vad input.pcm output.g729      # Encode PCM with Annex B VAD/DTX\n", filepath.Base(os.Args[0]))
		fmt.Fprintf(os.Stderr, "  %s -d input.g729 output.wav           # Decode G.729 to WAV\n", filepath.Base(os.Args[0]))
		fmt.Fprintf(os.Stderr, "  %s -d -loss 0.05 input.g729 out.wav   # Decode with 5%% packet loss concealment\n", filepath.Base(os.Args[0]))
	}

	flag.Parse()

	args := flag.Args()
	if len(args) < 2 {
		flag.Usage()
		os.Exit(1)
	}

	inFile := args[0]
	outFile := args[1]

	// Determine operation mode
	mode := "encode"
	if *flagDecode {
		mode = "decode"
	} else if *flagEncode {
		mode = "encode"
	} else {
		// Auto-detect from file extension
		inExt := strings.ToLower(filepath.Ext(inFile))
		outExt := strings.ToLower(filepath.Ext(outFile))
		if inExt == ".g729" || inExt == ".bit" || inExt == ".bts" {
			mode = "decode"
		} else if outExt == ".g729" || outExt == ".bit" || outExt == ".bts" {
			mode = "encode"
		}
	}

	var err error
	if mode == "encode" {
		err = runEncode(inFile, outFile, *flagFull, *flagVAD, *flagBenchmark)
	} else {
		err = runDecode(inFile, outFile, *flagLoss, *flagWavOut, *flagBenchmark)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runEncode(inFile, outFile string, isFull bool, enableVAD bool, showBench bool) error {
	inData, err := os.ReadFile(inFile)
	if err != nil {
		return fmt.Errorf("reading input file: %w", err)
	}

	// Check for WAV header
	var pcmBytes []byte
	if isWav(inData) {
		pcmBytes, err = extractWavPCM(inData)
		if err != nil {
			return fmt.Errorf("parsing WAV file: %w", err)
		}
	} else {
		pcmBytes = inData
	}

	numSamples := len(pcmBytes) / 2
	numFrames := numSamples / 80
	if numFrames == 0 {
		return fmt.Errorf("input audio too short (minimum 80 samples / 10 ms)")
	}

	cfg := g729.DefaultConfig()
	if isFull {
		cfg.Variant = g729.VariantG729
	} else {
		cfg.Variant = g729.VariantG729A
	}
	cfg.EnableVAD = enableVAD

	enc := g729.NewEncoder(cfg)

	outBuf := make([]byte, 0, numFrames*10)
	var frameIn [80]int16
	var frameDst [10]byte

	var speechCount, sidCount, untransCount int
	start := time.Now()

	for f := 0; f < numFrames; f++ {
		offset := f * 80 * 2
		for i := 0; i < 80; i++ {
			frameIn[i] = int16(binary.LittleEndian.Uint16(pcmBytes[offset+i*2 : offset+i*2+2]))
		}

		n, fType, err := enc.Encode(frameDst[:], frameIn[:])
		if err != nil {
			return fmt.Errorf("frame %d encode failed: %w", f, err)
		}

		switch fType {
		case g729.FrameSpeech:
			speechCount++
			outBuf = append(outBuf, frameDst[:n]...)
		case g729.FrameSID:
			sidCount++
			outBuf = append(outBuf, frameDst[:n]...)
		case g729.FrameUntransmitted:
			untransCount++
		}
	}

	elapsed := time.Since(start)

	if err := os.WriteFile(outFile, outBuf, 0644); err != nil {
		return fmt.Errorf("writing output file: %w", err)
	}

	durationSec := float64(numFrames) * 0.010
	rtf := elapsed.Seconds() / durationSec

	fmt.Printf("Encoding completed successfully:\n")
	fmt.Printf("  Frames:         %d (%.2f s)\n", numFrames, durationSec)
	fmt.Printf("  Speech frames:  %d (10 bytes each)\n", speechCount)
	if enableVAD {
		fmt.Printf("  SID frames:     %d (2 bytes each)\n", sidCount)
		fmt.Printf("  Untransmitted:  %d (0 bytes)\n", untransCount)
	}
	fmt.Printf("  Output size:    %d bytes\n", len(outBuf))
	if showBench {
		fmt.Printf("  Elapsed time:   %v\n", elapsed)
		fmt.Printf("  RTF:            %.4f (%.1fx real-time)\n", rtf, 1.0/rtf)
	}

	return nil
}

func runDecode(inFile, outFile string, lossRate float64, forceWav bool, showBench bool) error {
	inData, err := os.ReadFile(inFile)
	if err != nil {
		return fmt.Errorf("reading input file: %w", err)
	}

	dec := g729.NewDecoder()
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	// Determine frames from byte stream (10 bytes for speech, 2 bytes for SID)
	var frames [][]byte
	idx := 0
	for idx < len(inData) {
		rem := len(inData) - idx
		if rem >= 10 {
			frames = append(frames, inData[idx:idx+10])
			idx += 10
		} else if rem >= 2 {
			frames = append(frames, inData[idx:idx+2])
			idx += 2
		} else {
			break
		}
	}

	if len(frames) == 0 {
		return fmt.Errorf("no valid G.729 frames found in input file")
	}

	pcmOut := make([]byte, len(frames)*80*2)
	var decodedFrame [80]int16
	var lostFrames int

	start := time.Now()

	for f, frame := range frames {
		isLost := false
		if lossRate > 0 && rng.Float64() < lossRate {
			isLost = true
			lostFrames++
		}

		if isLost {
			err = dec.Decode(decodedFrame[:], nil)
		} else {
			err = dec.Decode(decodedFrame[:], frame)
		}

		if err != nil {
			return fmt.Errorf("frame %d decode error: %w", f, err)
		}

		offset := f * 80 * 2
		for i := 0; i < 80; i++ {
			binary.LittleEndian.PutUint16(pcmOut[offset+i*2:offset+i*2+2], uint16(decodedFrame[i]))
		}
	}

	elapsed := time.Since(start)

	// Write output as WAV if requested or extension is .wav
	outExt := strings.ToLower(filepath.Ext(outFile))
	var finalOut []byte
	if forceWav || outExt == ".wav" {
		finalOut = createWavHeader(pcmOut, 8000, 1, 16)
		finalOut = append(finalOut, pcmOut...)
	} else {
		finalOut = pcmOut
	}

	if err := os.WriteFile(outFile, finalOut, 0644); err != nil {
		return fmt.Errorf("writing output file: %w", err)
	}

	durationSec := float64(len(frames)) * 0.010
	rtf := elapsed.Seconds() / durationSec

	fmt.Printf("Decoding completed successfully:\n")
	fmt.Printf("  Frames decoded: %d (%.2f s)\n", len(frames), durationSec)
	if lossRate > 0 {
		fmt.Printf("  Concealed loss: %d (%.1f%%)\n", lostFrames, float64(lostFrames)/float64(len(frames))*100.0)
	}
	fmt.Printf("  Output size:    %d bytes\n", len(finalOut))
	if showBench {
		fmt.Printf("  Elapsed time:   %v\n", elapsed)
		fmt.Printf("  RTF:            %.4f (%.1fx real-time)\n", rtf, 1.0/rtf)
	}

	return nil
}

// isWav checks if data starts with RIFF and WAVE magic headers.
func isWav(data []byte) bool {
	if len(data) < 12 {
		return false
	}
	return string(data[0:4]) == "RIFF" && string(data[8:12]) == "WAVE"
}

// extractWavPCM parses a RIFF/WAVE header and returns the raw PCM data.
func extractWavPCM(data []byte) ([]byte, error) {
	if len(data) < 44 {
		return nil, fmt.Errorf("WAV data too short")
	}

	channels := binary.LittleEndian.Uint16(data[22:24])
	sampleRate := binary.LittleEndian.Uint32(data[24:28])
	bitsPerSample := binary.LittleEndian.Uint16(data[34:36])

	if channels != 1 {
		return nil, fmt.Errorf("unsupported channel count %d (only mono 1-channel supported)", channels)
	}
	if sampleRate != 8000 {
		return nil, fmt.Errorf("unsupported sample rate %d Hz (G.729 requires 8000 Hz)", sampleRate)
	}
	if bitsPerSample != 16 {
		return nil, fmt.Errorf("unsupported bit depth %d (only 16-bit linear PCM supported)", bitsPerSample)
	}

	// Find "data" chunk
	offset := 12
	for offset+8 <= len(data) {
		chunkID := string(data[offset : offset+4])
		chunkSize := binary.LittleEndian.Uint32(data[offset+4 : offset+8])
		if chunkID == "data" {
			dataStart := offset + 8
			dataEnd := dataStart + int(chunkSize)
			if dataEnd > len(data) {
				dataEnd = len(data)
			}
			return data[dataStart:dataEnd], nil
		}
		offset += 8 + int(chunkSize)
	}

	// Fallback to byte 44
	return data[44:], nil
}

// createWavHeader generates a standard 44-byte canonical WAV header.
func createWavHeader(pcm []byte, sampleRate int, channels int, bitsPerSample int) []byte {
	h := make([]byte, 44)
	copy(h[0:4], "RIFF")
	binary.LittleEndian.PutUint32(h[4:8], uint32(36+len(pcm)))
	copy(h[8:12], "WAVE")
	copy(h[12:16], "fmt ")
	binary.LittleEndian.PutUint32(h[16:20], 16) // Subchunk1Size (16 for PCM)
	binary.LittleEndian.PutUint16(h[20:22], 1)  // AudioFormat (1 for PCM)
	binary.LittleEndian.PutUint16(h[22:24], uint16(channels))
	binary.LittleEndian.PutUint32(h[24:28], uint32(sampleRate))
	byteRate := sampleRate * channels * bitsPerSample / 8
	binary.LittleEndian.PutUint32(h[28:32], uint32(byteRate))
	blockAlign := channels * bitsPerSample / 8
	binary.LittleEndian.PutUint16(h[32:34], uint16(blockAlign))
	binary.LittleEndian.PutUint16(h[34:36], uint16(bitsPerSample))
	copy(h[36:40], "data")
	binary.LittleEndian.PutUint32(h[40:44], uint32(len(pcm)))
	return h
}

var _ = io.EOF
