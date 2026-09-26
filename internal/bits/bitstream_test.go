package bits

import (
	"math/rand"
	"sync"
	"testing"
)

func TestBitstreamRoundTrip(t *testing.T) {
	testCases := []struct {
		name string
		p    ParamSet
	}{
		{
			name: "all_zeros",
			p:    ParamSet{},
		},
		{
			name: "max_valid_values",
			p: ParamSet{
				L0:  1,
				L1:  127,
				L2:  31,
				L3:  31,
				P1:  255,
				P0:  1,
				C1:  8191,
				S1:  15,
				GA1: 7,
				GB1: 15,
				P2:  31,
				C2:  8191,
				S2:  15,
				GA2: 7,
				GB2: 15,
			},
		},
		{
			name: "alternating_bits",
			p: ParamSet{
				L0:  1,
				L1:  0x55,
				L2:  0x15,
				L3:  0x0A,
				P1:  0xAA,
				P0:  0,
				C1:  0x1555,
				S1:  0x0A,
				GA1: 0x05,
				GB1: 0x0A,
				P2:  0x15,
				C2:  0x0AAA,
				S2:  0x05,
				GA2: 0x02,
				GB2: 0x05,
			},
		},
		{
			name: "plan_test_vector",
			p: ParamSet{
				L0:  1,
				L1:  63,
				L2:  15,
				L3:  31,
				P1:  100,
				P0:  1,
				C1:  0x1FFF,
				S1:  0xF,
				GA1: 7,
				GB1: 15,
				P2:  31,
				C2:  0x1FFF,
				S2:  0xF,
				GA2: 7,
				GB2: 15,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			buf := make([]byte, 10)
			Pack(buf, &tc.p)

			var got ParamSet
			Unpack(&got, buf)

			if got != tc.p {
				t.Fatalf("ParamSet mismatch:\n got:  %+v\n want: %+v", got, tc.p)
			}
		})
	}
}

func TestBitstreamRandomRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(12345))
	buf := make([]byte, 10)

	for i := 0; i < 10000; i++ {
		p := ParamSet{
			L0:  uint8(rng.Intn(2)),
			L1:  uint8(rng.Intn(128)),
			L2:  uint8(rng.Intn(32)),
			L3:  uint8(rng.Intn(32)),
			P1:  uint8(rng.Intn(256)),
			P0:  uint8(rng.Intn(2)),
			C1:  uint16(rng.Intn(8192)),
			S1:  uint8(rng.Intn(16)),
			GA1: uint8(rng.Intn(8)),
			GB1: uint8(rng.Intn(16)),
			P2:  uint8(rng.Intn(32)),
			C2:  uint16(rng.Intn(8192)),
			S2:  uint8(rng.Intn(16)),
			GA2: uint8(rng.Intn(8)),
			GB2: uint8(rng.Intn(16)),
		}

		Pack(buf, &p)
		var got ParamSet
		Unpack(&got, buf)

		if got != p {
			t.Fatalf("iteration %d: mismatch:\n got:  %+v\n want: %+v\n buf:  %x", i, got, p, buf)
		}
	}
}

func TestBitstreamFieldBoundaries(t *testing.T) {
	// Verify that setting each field individually to its maximum value does not corrupt other fields
	fields := []struct {
		name string
		set  func(p *ParamSet)
	}{
		{"L0", func(p *ParamSet) { p.L0 = 1 }},
		{"L1", func(p *ParamSet) { p.L1 = 127 }},
		{"L2", func(p *ParamSet) { p.L2 = 31 }},
		{"L3", func(p *ParamSet) { p.L3 = 31 }},
		{"P1", func(p *ParamSet) { p.P1 = 255 }},
		{"P0", func(p *ParamSet) { p.P0 = 1 }},
		{"C1", func(p *ParamSet) { p.C1 = 8191 }},
		{"S1", func(p *ParamSet) { p.S1 = 15 }},
		{"GA1", func(p *ParamSet) { p.GA1 = 7 }},
		{"GB1", func(p *ParamSet) { p.GB1 = 15 }},
		{"P2", func(p *ParamSet) { p.P2 = 31 }},
		{"C2", func(p *ParamSet) { p.C2 = 8191 }},
		{"S2", func(p *ParamSet) { p.S2 = 15 }},
		{"GA2", func(p *ParamSet) { p.GA2 = 7 }},
		{"GB2", func(p *ParamSet) { p.GB2 = 15 }},
	}

	buf := make([]byte, 10)
	for _, f := range fields {
		t.Run(f.name, func(t *testing.T) {
			var want ParamSet
			f.set(&want)

			for i := range buf {
				buf[i] = 0
			}
			Pack(buf, &want)

			var got ParamSet
			Unpack(&got, buf)

			if got != want {
				t.Fatalf("Field %s boundary violation:\n got:  %+v\n want: %+v\n buf:  %x", f.name, got, want, buf)
			}
		})
	}
}

func TestSIDFrameRoundTrip(t *testing.T) {
	testCases := []struct {
		name string
		s    SIDParamSet
	}{
		{"zeros", SIDParamSet{}},
		{"max", SIDParamSet{Predictor: 1, Stage1: 31, Stage2: 15, Energy: 31}},
		{"alt", SIDParamSet{Predictor: 1, Stage1: 21, Stage2: 10, Energy: 18}},
	}

	buf := make([]byte, 2)
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			PackSID(buf, &tc.s)

			// The padding bit (bit 0 of byte 1) must be 0
			if buf[1]&0x01 != 0 {
				t.Errorf("SID padding bit not zero: %02x", buf[1])
			}

			var got SIDParamSet
			UnpackSID(&got, buf)

			if got != tc.s {
				t.Fatalf("SID mismatch:\n got:  %+v\n want: %+v", got, tc.s)
			}
		})
	}
}

func TestSIDFrameRandomRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(54321))
	buf := make([]byte, 2)

	for i := 0; i < 5000; i++ {
		s := SIDParamSet{
			Predictor: uint8(rng.Intn(2)),
			Stage1:    uint8(rng.Intn(32)),
			Stage2:    uint8(rng.Intn(16)),
			Energy:    uint8(rng.Intn(32)),
		}

		PackSID(buf, &s)
		var got SIDParamSet
		UnpackSID(&got, buf)

		if got != s {
			t.Fatalf("iteration %d: mismatch:\n got:  %+v\n want: %+v\n buf:  %x", i, got, s, buf)
		}
	}
}

func TestBitstreamConcurrent(t *testing.T) {
	const goroutines = 16
	const iterations = 1000

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(id * 1000)))
			buf := make([]byte, 10)
			sidBuf := make([]byte, 2)

			for i := 0; i < iterations; i++ {
				p := ParamSet{
					L0:  uint8(rng.Intn(2)),
					L1:  uint8(rng.Intn(128)),
					L2:  uint8(rng.Intn(32)),
					L3:  uint8(rng.Intn(32)),
					P1:  uint8(rng.Intn(256)),
					P0:  uint8(rng.Intn(2)),
					C1:  uint16(rng.Intn(8192)),
					S1:  uint8(rng.Intn(16)),
					GA1: uint8(rng.Intn(8)),
					GB1: uint8(rng.Intn(16)),
					P2:  uint8(rng.Intn(32)),
					C2:  uint16(rng.Intn(8192)),
					S2:  uint8(rng.Intn(16)),
					GA2: uint8(rng.Intn(8)),
					GB2: uint8(rng.Intn(16)),
				}
				Pack(buf, &p)
				var pGot ParamSet
				Unpack(&pGot, buf)
				if pGot != p {
					t.Errorf("concurrent mismatch: %+v vs %+v", pGot, p)
					return
				}

				s := SIDParamSet{
					Predictor: uint8(rng.Intn(2)),
					Stage1:    uint8(rng.Intn(32)),
					Stage2:    uint8(rng.Intn(16)),
					Energy:    uint8(rng.Intn(32)),
				}
				PackSID(sidBuf, &s)
				var sGot SIDParamSet
				UnpackSID(&sGot, sidBuf)
				if sGot != s {
					t.Errorf("concurrent SID mismatch: %+v vs %+v", sGot, s)
					return
				}
			}
		}(g)
	}

	wg.Wait()
}

func BenchmarkBitstreamPack(b *testing.B) {
	p := ParamSet{
		L0:  1,
		L1:  63,
		L2:  15,
		L3:  31,
		P1:  100,
		P0:  1,
		C1:  0x1FFF,
		S1:  0xF,
		GA1: 7,
		GB1: 15,
		P2:  31,
		C2:  0x1FFF,
		S2:  0xF,
		GA2: 7,
		GB2: 15,
	}
	buf := make([]byte, 10)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Pack(buf, &p)
	}
}

func BenchmarkBitstreamUnpack(b *testing.B) {
	buf := []byte{0xFF, 0xFE, 0xDC, 0xBA, 0x98, 0x76, 0x54, 0x32, 0x10, 0xF0}
	var p ParamSet
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Unpack(&p, buf)
	}
}
