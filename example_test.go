package g729_test

import (
	"bytes"
	"fmt"
	"log"
	"time"

	g729 "github.com/selawe/go-g729"
	"github.com/selawe/go-g729/jitter"
	"github.com/selawe/go-g729/rtp"
	"github.com/selawe/go-g729/sdp"
)

func ExampleEncoder() {
	enc := g729.NewEncoder(g729.ProfileFast())
	pcm := make([]int16, 80) // 10 ms audio frame @ 8 kHz
	dst := make([]byte, 10)

	n, frameType, err := enc.Encode(dst, pcm)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Encoded %d bytes, frameType: %d\n", n, frameType)
	// Output:
	// Encoded 10 bytes, frameType: 0
}

func ExampleDecoder() {
	dec := g729.NewDecoder()
	speechFrame := make([]byte, 10)
	pcm := make([]int16, 80)

	// Decode normal speech frame
	err := dec.Decode(pcm, speechFrame)
	if err != nil {
		log.Fatal(err)
	}

	// Packet loss concealment: pass nil frame
	err = dec.Decode(pcm, nil)
	if err != nil {
		log.Fatal(err)
	}

	stats := dec.Stats()
	fmt.Printf("Decoded speech frames: %d, PLC erasures: %d\n", stats.SpeechFrames, stats.ConcealedFrames)
	// Output:
	// Decoded speech frames: 1, PLC erasures: 1
}

func ExampleNewWriter() {
	var bitstream bytes.Buffer
	writer, err := g729.NewWriter(&bitstream, g729.ProfileFast())
	if err != nil {
		log.Fatal(err)
	}

	pcmBytes := make([]byte, 160) // 80 16-bit samples
	if _, err := writer.Write(pcmBytes); err != nil {
		log.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Bitstream output size: %d bytes\n", bitstream.Len())
	// Output:
	// Bitstream output size: 10 bytes
}

func ExampleEncoder_EncodeBatch() {
	enc := g729.NewEncoder(g729.ProfileFast())
	pcm := make([]int16, 160) // 20 ms audio (2 frames)
	dst := make([]byte, 20)

	n, frameTypes, err := enc.EncodeBatch(dst, pcm)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Batch encoded %d bytes across %d frames\n", n, len(frameTypes))
	// Output:
	// Batch encoded 20 bytes across 2 frames
}

func Example_rtpPackInto() {
	frame1 := make([]byte, 10)
	frame2 := make([]byte, 10)
	packetDst := make([]byte, 20)

	n, err := rtp.PackInto(packetDst, [][]byte{frame1, frame2})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Packed RTP payload: %d bytes\n", n)
	// Output:
	// Packed RTP payload: 20 bytes
}

func Example_sdpNegotiation() {
	offer := "annexb=no;annexa=yes"
	answer := "annexb=no;annexa=no"

	annexa, annexb, err := sdp.ParseFMTPParams(offer)
	if err != nil {
		log.Fatal(err)
	}

	agreedAnnexA, err := sdp.NegotiateAnnexA(offer, answer)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Offer: annexa=%v, annexb=%v | Agreed annexa=%v\n", annexa, annexb, agreedAnnexA)
	// Output:
	// Offer: annexa=true, annexb=false | Agreed annexa=false
}

func Example_jitterBuffer() {
	jb := jitter.New(jitter.Config{
		TargetDelay: 20 * time.Millisecond,
	})

	// Simulate incoming RTP packets (10-byte payload each)
	payload := make([]byte, 10)
	_ = jb.Push(100, 0, payload)
	_ = jb.Push(101, 80, payload)

	var frame [10]byte
	n, isLoss, ok := jb.PopInto(frame[:])
	fmt.Printf("Playout: n=%d isLoss=%v ok=%v\n", n, isLoss, ok)
	// Output:
	// Playout: n=10 isLoss=false ok=true
}
