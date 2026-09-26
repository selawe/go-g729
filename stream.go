package g729

import (
	"encoding/binary"
	"errors"
	"io"
)

// ErrVADNotSupportedInStream is returned by NewWriter when the caller supplies
// a Config with EnableVAD=true. The raw byte streaming format requires fixed
// 10-byte speech frames and cannot represent variable-length SID frames (2 bytes)
// or suppressed frames (0 bytes) without an additional framing layer.
//
// For Annex B (VAD/DTX/CNG) streams, use the rtp package which correctly
// packetises speech, SID, and untransmitted frames as separate RTP packets.
var ErrVADNotSupportedInStream = errors.New(
	"g729: NewWriter does not support EnableVAD=true; " +
		"use EnableVAD=false for raw CBR streaming, or use the rtp package for Annex B streams",
)

// Writer implements an io.WriteCloser that encodes an incoming stream of
// 16-bit linear PCM audio (8 kHz, mono, little-endian) and writes G.729
// bitstream frames to an underlying io.Writer.
//
// Restriction: Writer only supports constant bit-rate (CBR) streams
// (Config.EnableVAD must be false). For Annex B VAD/DTX streams, use the
// rtp package, which correctly handles variable-length SID and suppressed frames.
type Writer struct {
	w      io.Writer
	enc    Encoder
	buf    [160]byte // 80 int16 samples = 160 bytes
	bufLen int
	dst    [10]byte
}

// NewWriter creates a new streaming G.729 encoder writing to w.
// cfg.EnableVAD must be false; returns ErrVADNotSupportedInStream otherwise.
func NewWriter(w io.Writer, cfg Config) (*Writer, error) {
	if cfg.EnableVAD {
		return nil, ErrVADNotSupportedInStream
	}
	return &Writer{
		w:   w,
		enc: NewEncoder(cfg),
	}, nil
}

// Write writes arbitrary-sized chunks of 16-bit linear PCM audio to the encoder.
// Incoming bytes are buffered into 160-byte blocks (80 int16 samples / 10 ms),
// encoded, and written as G.729 bitstream frames to the underlying writer.
func (w *Writer) Write(p []byte) (n int, err error) {
	total := len(p)
	src := p

	// 1. If buffer has residual bytes, complete the block first
	if w.bufLen > 0 {
		needed := 160 - w.bufLen
		toCopy := len(src)
		if toCopy > needed {
			toCopy = needed
		}
		copy(w.buf[w.bufLen:], src[:toCopy])
		w.bufLen += toCopy
		src = src[toCopy:]

		if w.bufLen == 160 {
			if err := w.encodeAndWrite(w.buf[:]); err != nil {
				return total - len(src), err
			}
			w.bufLen = 0
		}
	}

	// 2. Process complete 160-byte blocks directly from src
	for len(src) >= 160 {
		if err := w.encodeAndWrite(src[:160]); err != nil {
			return total - len(src), err
		}
		src = src[160:]
	}

	// 3. Stash remaining trailing bytes in buffer
	if len(src) > 0 {
		copy(w.buf[w.bufLen:], src)
		w.bufLen += len(src)
	}

	return total, nil
}

func (w *Writer) encodeAndWrite(block []byte) error {
	var samples [80]int16
	for i := 0; i < 80; i++ {
		samples[i] = int16(binary.LittleEndian.Uint16(block[i*2 : (i+1)*2]))
	}

	n, _, err := w.enc.Encode(w.dst[:], samples[:])
	if err != nil {
		return err
	}
	if n > 0 {
		if _, err := w.w.Write(w.dst[:n]); err != nil {
			return err
		}
	}
	return nil
}

// Close flushes any remaining buffered PCM samples (zero-padding to 80 samples
// if a partial frame exists) and closes the underlying writer if it implements io.Closer.
func (w *Writer) Close() error {
	if w.bufLen > 0 {
		// Zero-pad remainder of partial frame
		for i := w.bufLen; i < 160; i++ {
			w.buf[i] = 0
		}
		if err := w.encodeAndWrite(w.buf[:]); err != nil {
			return err
		}
		w.bufLen = 0
	}

	if closer, ok := w.w.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

// Reader implements an io.Reader that reads a G.729 bitstream from an underlying
// io.Reader and decompresses it into an outgoing stream of 16-bit linear PCM
// audio (8 kHz, mono, little-endian).
//
// Restriction: Reader expects a constant bit-rate (CBR) bitstream where every
// frame is exactly 10 bytes. It is designed to consume streams produced by
// Writer (with EnableVAD=false). Annex B bitstreams containing 2-byte SID
// frames or suppressed frames will be misparsed; use the rtp package instead.
type Reader struct {
	r       io.Reader
	dec     Decoder
	buf     [160]byte
	bufHead int
	bufTail int
	pcm     [80]int16
}

// NewReader creates a new streaming G.729 decoder reading from r.
// The reader expects a CBR bitstream of 10-byte speech frames as produced
// by Writer. For Annex B streams, use the rtp package.
func NewReader(r io.Reader) *Reader {
	return &Reader{
		r:   r,
		dec: NewDecoder(),
	}
}

// Read decompresses G.729 bitstream frames from the underlying reader and copies
// 16-bit linear PCM bytes into p.
func (r *Reader) Read(p []byte) (n int, err error) {
	if len(p) == 0 {
		return 0, nil
	}

	totalRead := 0

	for totalRead < len(p) {
		// If we have buffered PCM bytes, serve them first
		if r.bufHead < r.bufTail {
			toCopy := r.bufTail - r.bufHead
			avail := len(p) - totalRead
			if toCopy > avail {
				toCopy = avail
			}
			copy(p[totalRead:totalRead+toCopy], r.buf[r.bufHead:r.bufHead+toCopy])
			r.bufHead += toCopy
			totalRead += toCopy
			continue
		}

		// Read next frame from underlying bitstream
		// Standard speech frames are 10 bytes
		var frame [10]byte
		nRead, readErr := io.ReadFull(r.r, frame[:])
		if nRead == 0 {
			if totalRead > 0 {
				return totalRead, nil
			}
			return 0, readErr
		}
		if nRead < 10 && readErr != nil && readErr != io.ErrUnexpectedEOF {
			if totalRead > 0 {
				return totalRead, nil
			}
			return 0, readErr
		}

		// Decompress frame into 80 int16 samples
		if err := r.dec.Decode(r.pcm[:], frame[:nRead]); err != nil {
			if totalRead > 0 {
				return totalRead, nil
			}
			return 0, err
		}

		// Serialize int16 samples to little-endian bytes in r.buf
		for i := 0; i < 80; i++ {
			binary.LittleEndian.PutUint16(r.buf[i*2:(i+1)*2], uint16(r.pcm[i]))
		}
		r.bufHead = 0
		r.bufTail = 160
	}

	return totalRead, nil
}
