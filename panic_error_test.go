package g729

import (
	"errors"
	"strings"
	"testing"
)

func TestBuildPanicErrorExcludeStack(t *testing.T) {
	fakeStack := []byte("goroutine 1 [running]:\ngithub.com/selawe/go-g729.someFunc(...)\n\t/tmp/build-xxxxx/pkg/thing.go:123\n")
	err := buildPanicError("boom", fakeStack, false)

	if !errors.Is(err, ErrInternalPanic) {
		t.Fatalf("expected error to wrap ErrInternalPanic, got %v", err)
	}
	msg := err.Error()
	if strings.Contains(msg, "stack:") {
		t.Errorf("error message must not contain 'stack:' when IncludePanicStack=false, got: %q", msg)
	}
	if strings.Contains(msg, "goroutine ") || strings.Contains(msg, "/tmp/build") {
		t.Errorf("error message must not leak stack details, got: %q", msg)
	}
	if !strings.Contains(msg, "boom") {
		t.Errorf("error message should contain panic value 'boom', got: %q", msg)
	}
}

func TestBuildPanicErrorIncludeStack(t *testing.T) {
	fakeStack := []byte("goroutine 42 [running]:\nfake.trace.line\n")
	err := buildPanicError("kablam", fakeStack, true)

	if !errors.Is(err, ErrInternalPanic) {
		t.Fatalf("expected error to wrap ErrInternalPanic, got %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "stack:") {
		t.Errorf("error message must contain 'stack:' when IncludePanicStack=true, got: %q", msg)
	}
	if !strings.Contains(msg, "goroutine 42") {
		t.Errorf("error message should contain provided stack, got: %q", msg)
	}
	if !strings.Contains(msg, "kablam") {
		t.Errorf("error message should contain panic value 'kablam', got: %q", msg)
	}
}

func TestNewDecoderWithConfigDefaults(t *testing.T) {
	dec := NewDecoderWithConfig(DecoderConfig{})
	if dec == nil {
		t.Fatal("NewDecoderWithConfig returned nil")
	}

	// Basic smoke test: a valid speech frame must decode without error.
	enc := NewEncoder(Config{Variant: VariantG729A, EnableVAD: false})
	frame := generateSine(440.0, 80, 8000.0)
	var bs [10]byte
	if n, _, err := enc.Encode(bs[:], frame); err != nil || n != 10 {
		t.Fatalf("encode setup failed: n=%d err=%v", n, err)
	}

	out := make([]int16, 80)
	if err := dec.Decode(out, bs[:]); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
}

func TestDecoderConfigPanicRecoveryFlags(t *testing.T) {
	// Ensure config fields are wired through: constructing with
	// IncludePanicStack=true should not affect normal decode results.
	dec := NewDecoderWithConfig(DecoderConfig{IncludePanicStack: true})
	enc := NewEncoder(Config{Variant: VariantG729A, EnableVAD: false})
	frame := generateSine(880.0, 80, 8000.0)
	var bs [10]byte
	if n, _, err := enc.Encode(bs[:], frame); err != nil || n != 10 {
		t.Fatalf("encode setup failed: n=%d err=%v", n, err)
	}
	out := make([]int16, 80)
	if err := dec.Decode(out, bs[:]); err != nil {
		t.Fatalf("decode with IncludePanicStack=true failed: %v", err)
	}
}
