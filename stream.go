package g729

import (
	"context"
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

// FlushMode defines how Writer.Close handles residual trailing PCM samples
// that do not fill a complete 10 ms (80 sample / 160 byte) frame.
type FlushMode int

const (
	// FlushZeroPad zero-pads the remaining samples to form a complete 80-sample frame (default).
	FlushZeroPad FlushMode = iota
	// FlushDrop discards any trailing incomplete frame without emitting a final padded frame.
	FlushDrop
	// FlushError returns ErrIncompleteFrame on Close if incomplete frame bytes remain.
	FlushError
)

// ErrIncompleteFrame is returned by Writer.Close when FlushError mode is set
// and trailing unencoded PCM samples remain in the buffer.
var ErrIncompleteFrame = errors.New("g729: stream closed with incomplete trailing frame")

// Writer implements an io.WriteCloser that encodes an incoming stream of
// 16-bit linear PCM audio (8 kHz, mono, little-endian) and writes G.729
// bitstream frames to an underlying io.Writer.
//
// Restriction: Writer only supports constant bit-rate (CBR) streams
// (Config.EnableVAD must be false). For Annex B VAD/DTX streams, use the
// rtp package, which correctly handles variable-length SID and suppressed frames.
//
// Context cancellation: call SetContext to attach a context. When set, Write
// checks ctx.Err() once per 10 ms frame boundary; cancellation is detected
// within one frame (~42 µs worst case). By default no context is attached and
// Write behaves as if context.Background() were used.
type Writer struct {
	w             io.Writer
	enc           Encoder
	ctx           context.Context
	buf           [160]byte // 80 int16 samples = 160 bytes
	bufLen        int
	dst           [10]byte
	flushMode     FlushMode
	paddedSamples int
}

// NewWriter creates a new streaming G.729 encoder writing to w.
// cfg.EnableVAD must be false; returns ErrVADNotSupportedInStream otherwise.
//
// Each encoded frame produces a 10-byte Write call to w. For network or file sinks
// where per-syscall overhead matters, wrap w in a bufio.Writer before passing it here:
//
//	bw := bufio.NewWriterSize(conn, 1024) // buffers ~100 frames per flush
//	sw, err := g729.NewWriter(bw, cfg)
//	...
//	bw.Flush() // flush at end of session or packet boundary
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
	if w.ctx != nil {
		if err := w.ctx.Err(); err != nil {
			return err
		}
	}

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

// SetContext attaches ctx to the Writer. Write will return ctx.Err() at the
// next frame boundary if the context is cancelled or its deadline exceeded.
// Pass context.Background() to detach a previously set context.
func (w *Writer) SetContext(ctx context.Context) {
	w.ctx = ctx
}

// SetFlushMode configures how Close handles trailing partial PCM samples.
func (w *Writer) SetFlushMode(mode FlushMode) {
	w.flushMode = mode
}

// PaddedSamples returns the number of zero-padded samples appended to the final
// frame when Close is called under FlushZeroPad mode.
func (w *Writer) PaddedSamples() int {
	return w.paddedSamples
}

// Close flushes any remaining buffered PCM samples according to the configured FlushMode
// (default FlushZeroPad: zero-pads to 80 samples) and closes the underlying writer
// if it implements io.Closer.
func (w *Writer) Close() error {
	if w.bufLen > 0 {
		switch w.flushMode {
		case FlushZeroPad:
			unpaddedBytes := w.bufLen
			for i := w.bufLen; i < 160; i++ {
				w.buf[i] = 0
			}
			w.paddedSamples = (160 - unpaddedBytes) / 2
			if err := w.encodeAndWrite(w.buf[:]); err != nil {
				return err
			}
			w.bufLen = 0
		case FlushDrop:
			w.bufLen = 0
		case FlushError:
			w.bufLen = 0
			return ErrIncompleteFrame
		}
	}

	if closer, ok := w.w.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

// Reader implements io.ReadCloser that reads a G.729 bitstream from an underlying
// io.Reader and decompresses it into an outgoing stream of 16-bit linear PCM
// audio (8 kHz, mono, little-endian).
//
// Restriction: Reader expects a constant bit-rate (CBR) bitstream where every
// frame is exactly 10 bytes. It is designed to consume streams produced by
// Writer (with EnableVAD=false). Annex B bitstreams containing 2-byte SID
// frames or suppressed frames will be misparsed; use the rtp package instead.
//
// Context cancellation: call SetContext to attach a context. When set, Read
// checks ctx.Err() once per 10 ms frame boundary; cancellation is detected
// within one frame (~9 µs worst case). By default no context is attached and
// Read behaves as if context.Background() were used.
type Reader struct {
	r       io.Reader
	dec     Decoder
	ctx     context.Context
	buf     [160]byte
	bufHead int
	bufTail int
	pcm     [80]int16
	// pendingErr holds an error captured after some PCM bytes have already been
	// returned to the caller. It is surfaced on the subsequent Read call so a
	// mid-stream decoder failure is never silently swallowed.
	pendingErr error
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

// SetContext attaches ctx to the Reader. Read will return ctx.Err() at the
// next frame boundary if the context is cancelled or its deadline exceeded.
// Pass context.Background() to detach a previously set context.
func (r *Reader) SetContext(ctx context.Context) {
	r.ctx = ctx
}

// Close closes the underlying reader if it implements io.Closer.
// Symmetric with Writer.Close: callers that created the underlying reader
// may prefer to close it themselves; this method exists for callers that
// hand ownership to Reader.
func (r *Reader) Close() error {
	if closer, ok := r.r.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

// Read decompresses G.729 bitstream frames from the underlying reader and copies
// 16-bit linear PCM bytes into p.
//
// If a decode or read error occurs mid-stream after some PCM bytes have already
// been delivered, Read returns (n, nil) for that call and surfaces the error on
// the next call once the buffered PCM has been drained. This preserves io.Reader
// semantics while ensuring no error is silently discarded.
func (r *Reader) Read(p []byte) (n int, err error) {
	if len(p) == 0 {
		return 0, nil
	}

	// Surface any error captured on the previous call once buffered PCM drained.
	if r.pendingErr != nil && r.bufHead >= r.bufTail {
		e := r.pendingErr
		r.pendingErr = nil
		return 0, e
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

		// If an error was captured earlier in this same call (after we already
		// filled some PCM), stash it and return the bytes accumulated so far.
		if r.pendingErr != nil {
			return totalRead, nil
		}

		// Check for context cancellation at each frame boundary.
		if r.ctx != nil {
			if err := r.ctx.Err(); err != nil {
				return totalRead, err
			}
		}

		// Read next frame from underlying bitstream
		// Standard speech frames are 10 bytes
		var frame [10]byte
		nRead, readErr := io.ReadFull(r.r, frame[:])
		if nRead == 0 {
			if totalRead > 0 {
				r.pendingErr = readErr
				return totalRead, nil
			}
			return 0, readErr
		}
		if nRead < 10 {
			errToReturn := readErr
			if errToReturn == nil {
				errToReturn = io.ErrUnexpectedEOF
			}
			if totalRead > 0 {
				r.pendingErr = errToReturn
				return totalRead, nil
			}
			return 0, errToReturn
		}

		// Decompress frame into 80 int16 samples (strictly 10-byte CBR frames)
		if err := r.dec.Decode(r.pcm[:], frame[:]); err != nil {
			if totalRead > 0 {
				r.pendingErr = err
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
