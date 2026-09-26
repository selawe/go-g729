package g729

import (
	"encoding/binary"
	"io"
)

// Writer implements an io.WriteCloser that encodes an incoming stream of
// 16-bit linear PCM audio (8 kHz, mono, little-endian) and writes G.729
// bitstream frames to an underlying io.Writer.
type Writer struct {
	w      io.Writer
	enc    Encoder
	buf    [160]byte // 80 int16 samples = 160 bytes
	bufLen int
	dst    [10]byte
}

// NewWriter creates a new streaming G.729 encoder writing to w.
func NewWriter(w io.Writer, cfg Config) *Writer {
	return &Writer{
		w:   w,
		enc: NewEncoder(cfg),
	}
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
type Reader struct {
	r       io.Reader
	dec     Decoder
	buf     [160]byte
	bufHead int
	bufTail int
	pcm     [80]int16
}

// NewReader creates a new streaming G.729 decoder reading from r.
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
