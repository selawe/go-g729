package g729

import (
	"sync"
)

// EncoderPool manages a concurrent-safe pool of recycled *Encoder instances
// configured with the same Config. Using an EncoderPool prevents heap allocations
// and Garbage Collection (GC) pauses in high-concurrency telephony systems
// (e.g. VoIP media servers, PBXs, IVRs) where sessions are frequently opened and closed.
type EncoderPool struct {
	cfg  Config
	pool sync.Pool
}

// NewEncoderPool creates an EncoderPool for the specified Config.
func NewEncoderPool(cfg Config) *EncoderPool {
	p := &EncoderPool{cfg: cfg}
	p.pool.New = func() any {
		return NewEncoder(cfg)
	}
	return p
}

// Get retrieves an Encoder from the pool.
// The returned Encoder is guaranteed to be in a freshly reset initial state.
func (p *EncoderPool) Get() *Encoder {
	enc := p.pool.Get().(*Encoder)
	enc.Reset()
	return enc
}

// Put returns an Encoder to the pool for reuse.
// If enc is nil, Put does nothing. Put automatically resets the internal state
// of the encoder before making it available for subsequent Get calls.
func (p *EncoderPool) Put(enc *Encoder) {
	if enc == nil {
		return
	}
	enc.Reset()
	p.pool.Put(enc)
}

// DecoderPool manages a concurrent-safe pool of recycled *Decoder instances.
// Using a DecoderPool eliminates heap allocations when handling high call turnover.
type DecoderPool struct {
	cfg  DecoderConfig
	pool sync.Pool
}

// NewDecoderPool creates a DecoderPool using default DecoderConfig.
func NewDecoderPool() *DecoderPool {
	return NewDecoderPoolWithConfig(DecoderConfig{})
}

// NewDecoderPoolWithConfig creates a DecoderPool using a custom DecoderConfig.
func NewDecoderPoolWithConfig(cfg DecoderConfig) *DecoderPool {
	p := &DecoderPool{cfg: cfg}
	p.pool.New = func() any {
		return NewDecoderWithConfig(cfg)
	}
	return p
}

// Get retrieves a Decoder from the pool.
// The returned Decoder is guaranteed to be in a freshly reset initial state.
func (p *DecoderPool) Get() *Decoder {
	dec := p.pool.Get().(*Decoder)
	dec.Reset()
	return dec
}

// Put returns a Decoder to the pool for reuse.
// If dec is nil, Put does nothing. Put automatically resets the internal state
// of the decoder before making it available for subsequent Get calls.
func (p *DecoderPool) Put(dec *Decoder) {
	if dec == nil {
		return
	}
	dec.Reset()
	p.pool.Put(dec)
}
