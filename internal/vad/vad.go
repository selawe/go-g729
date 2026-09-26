package vad

import (
	"math"

	"github.com/selawe/go-g729/internal/params"
	"github.com/selawe/go-g729/internal/tables"
)

const (
	// Voice indicates active speech detected.
	Voice = 1
	// Noise indicates silence / background noise detected.
	Noise = 0

	// InitFrame is the number of initial frames for noise initialization.
	InitFrame = 32
	// InitCount is the adaptation threshold count for background noise tracking.
	InitCount = 20
	// ZCStart is the start sample index in the speech buffer for zero crossing computation.
	ZCStart = 120
	// ZCEnd is the end sample index in the speech buffer for zero crossing computation.
	ZCEnd = 200
)

// VADState holds the memory and state for ITU-T G.729 Annex B Voice Activity Detection.
// It is fully encapsulated to ensure thread safety across parallel encoder instances.
type VADState struct {
	MeanLSF     [params.M]float32
	MinBuffer   [16]float32
	PrevMin     float32
	NextMin     float32
	Min         float32
	MeanE       float32
	MeanSE      float32
	MeanSLE     float32
	MeanSZC     float32
	PrevEnergy  float32
	CountSil    int
	CountUpdate int
	CountExt    int
	Flag        int
	VFlag       int
	LessCount   int
}

// NewVADState creates and initializes a new VAD state struct.
func NewVADState() *VADState {
	s := &VADState{}
	s.Reset()
	return s
}

// Reset re-initializes the VAD state according to ITU-T vad_init().
func (s *VADState) Reset() {
	for i := 0; i < params.M; i++ {
		s.MeanLSF[i] = 0.0
	}
	for i := 0; i < len(s.MinBuffer); i++ {
		s.MinBuffer[i] = 0.0
	}
	s.MeanSE = 0.0
	s.MeanSLE = 0.0
	s.MeanE = 0.0
	s.MeanSZC = 0.0
	s.PrevEnergy = 0.0
	s.PrevMin = 0.0
	s.NextMin = 0.0
	s.CountSil = 0
	s.CountUpdate = 0
	s.CountExt = 0
	s.LessCount = 0
	s.Flag = 1
	s.VFlag = 0
	s.Min = math.MaxFloat32
}

// Process executes the VAD algorithm for one 10 ms frame.
//
// Arguments:
//   - rc: reflection coefficient of order 2 (rc[1])
//   - lsf: unquantized line spectral frequencies in radians (0 < lsf < pi), length >= params.M
//   - rxx: lag-windowed autocorrelation coefficients of order NP (12), length >= params.NP + 1 (13)
//   - sigpp: preprocessed speech buffer of length >= params.L_TOTAL (240), where [120...200] is analyzed
//   - frmCount: 1-indexed frame counter
//   - prevMarker: VAD decision of the previous frame (Voice or Noise)
//   - pprevMarker: VAD decision of the frame before last (Voice or Noise)
//
// Returns:
//   - marker: Voice (1) or Noise (0)
//   - energyDB: normalized full-band frame energy in dB
func (s *VADState) Process(
	rc float32,
	lsf []float32,
	rxx []float32,
	sigpp []float32,
	frmCount int,
	prevMarker int,
	pprevMarker int,
) (marker int, energyDB float32) {
	// 1. Compute normalized frame energy (full band)
	const epsi = 1.0e-38
	normEnergy := float32(10.0 * math.Log10(float64(rxx[0]/240.0+epsi)))
	energyDB = normEnergy

	// 2. Compute low band energy
	eLowSum := float32(0.0)
	for i := 1; i <= params.NP; i++ {
		eLowSum += rxx[i] * tables.LbfCorr[i]
	}
	eLowVal := rxx[0]*tables.LbfCorr[0] + 2.0*eLowSum
	if eLowVal < 0.0 {
		eLowVal = 0.0
	}
	eLow := float32(10.0 * math.Log10(float64(eLowVal/240.0+epsi)))

	// 3. Compute spectral distortion (SD)
	// Normalize LSFs by 2*pi to match C reference MeanLSF domain
	var lsfNorm [params.M]float32
	twoPiInv := float32(1.0 / (2.0 * math.Pi))
	for i := 0; i < params.M; i++ {
		lsfNorm[i] = lsf[i] * twoPiInv
	}
	var sd float32
	for i := 0; i < params.M; i++ {
		diff := lsfNorm[i] - s.MeanLSF[i]
		sd += diff * diff
	}

	// 4. Compute zero crossing rate (ZC) over samples [ZCStart...ZCEnd]
	var zc float32
	dtemp := sigpp[ZCStart]
	for i := ZCStart + 1; i <= ZCEnd; i++ {
		if dtemp*sigpp[i] < 0.0 {
			zc += 1.0
		}
		dtemp = sigpp[i]
	}
	zc /= 80.0

	// 5. Initialize and update minimum energy buffers
	if frmCount < 129 {
		if normEnergy < s.Min {
			s.Min = normEnergy
			s.PrevMin = normEnergy
		}
		if (frmCount % 8) == 0 {
			idx := frmCount/8 - 1
			if idx >= 0 && idx < 16 {
				s.MinBuffer[idx] = s.Min
			}
			s.Min = math.MaxFloat32
		}
	}
	if (frmCount % 8) == 0 {
		s.PrevMin = s.MinBuffer[0]
		for i := 1; i < 15; i++ {
			if s.MinBuffer[i] < s.PrevMin {
				s.PrevMin = s.MinBuffer[i]
			}
		}
	}

	if frmCount >= 129 {
		if (frmCount % 8) == 1 {
			s.Min = s.PrevMin
			s.NextMin = math.MaxFloat32
		}
		if normEnergy < s.Min {
			s.Min = normEnergy
		}
		if normEnergy < s.NextMin {
			s.NextMin = normEnergy
		}
		if (frmCount % 8) == 0 {
			for i := 0; i < 15; i++ {
				s.MinBuffer[i] = s.MinBuffer[i+1]
			}
			s.MinBuffer[15] = s.NextMin
			s.PrevMin = s.MinBuffer[0]
			for i := 1; i < 16; i++ {
				if s.MinBuffer[i] < s.PrevMin {
					s.PrevMin = s.MinBuffer[i]
				}
			}
		}
	}

	// 6. Initial frames handling (1..32)
	if frmCount <= InitFrame {
		if normEnergy < 21.0 {
			s.LessCount++
			marker = Noise
		} else {
			marker = Voice
			activeCount := float32(frmCount - s.LessCount)
			prevCount := float32(frmCount - s.LessCount - 1)
			s.MeanE = (s.MeanE*prevCount + normEnergy) / activeCount
			s.MeanSZC = (s.MeanSZC*prevCount + zc) / activeCount
			for i := 0; i < params.M; i++ {
				s.MeanLSF[i] = (s.MeanLSF[i]*prevCount + lsfNorm[i]) / activeCount
			}
		}
	}

	// 7. Regular frame processing (>= 32)
	if frmCount >= InitFrame {
		if frmCount == InitFrame {
			s.MeanSE = s.MeanE - 10.0
			s.MeanSLE = s.MeanE - 12.0
		}

		dSE := s.MeanSE - normEnergy
		dSLE := s.MeanSLE - eLow
		dSZC := s.MeanSZC - zc

		if normEnergy < 21.0 {
			marker = Noise
		} else {
			marker = makeDec(dSLE, dSE, sd, dSZC)
		}

		s.VFlag = 0
		if prevMarker == Voice && marker == Noise && normEnergy > s.MeanSE+2.0 && normEnergy > 21.0 {
			marker = Voice
			s.VFlag = 1
		}

		if s.Flag == 1 {
			diffEnergy := float32(math.Abs(float64(s.PrevEnergy - normEnergy)))
			if pprevMarker == Voice && prevMarker == Voice && marker == Noise && diffEnergy <= 3.0 {
				s.CountExt++
				marker = Voice
				s.VFlag = 1
				if s.CountExt <= 4 {
					s.Flag = 1
				} else {
					s.Flag = 0
					s.CountExt = 0
				}
			}
		} else {
			s.Flag = 1
		}

		if marker == Noise {
			s.CountSil++
		}
		if marker == Voice && s.CountSil > 10 && (normEnergy-s.PrevEnergy) <= 3.0 {
			marker = Noise
			s.CountSil = 0
		}
		if marker == Voice {
			s.CountSil = 0
		}

		if normEnergy < s.MeanSE+3.0 && frmCount > 128 && s.VFlag == 0 && rc < 0.6 {
			marker = Noise
		}

		if normEnergy < s.MeanSE+3.0 && rc < 0.75 && sd < 0.002532959 {
			s.CountUpdate++
			var coef, coefZC, coefSD float32
			if s.CountUpdate < InitCount {
				coef = 0.75
				coefZC = 0.80
				coefSD = 0.60
			} else if s.CountUpdate < InitCount+10 {
				coef = 0.95
				coefZC = 0.92
				coefSD = 0.65
			} else if s.CountUpdate < InitCount+20 {
				coef = 0.97
				coefZC = 0.94
				coefSD = 0.70
			} else if s.CountUpdate < InitCount+30 {
				coef = 0.99
				coefZC = 0.96
				coefSD = 0.75
			} else if s.CountUpdate < InitCount+40 {
				coef = 0.995
				coefZC = 0.99
				coefSD = 0.75
			} else {
				coef = 0.995
				coefZC = 0.998
				coefSD = 0.75
			}

			for i := 0; i < params.M; i++ {
				s.MeanLSF[i] = coefSD*s.MeanLSF[i] + (1.0-coefSD)*lsfNorm[i]
			}
			s.MeanSE = coef*s.MeanSE + (1.0-coef)*normEnergy
			s.MeanSLE = coef*s.MeanSLE + (1.0-coef)*eLow
			s.MeanSZC = coefZC*s.MeanSZC + (1.0-coefZC)*zc
		}

		// Reset MeanSE if noise floor shifted significantly after initial 128 frames
		if frmCount > 128 && (((s.MeanSE < s.Min) && (sd < 0.002532959)) || (s.MeanSE > s.Min+10.0)) {
			s.MeanSE = s.Min
			s.CountUpdate = 0
		}
	}

	s.PrevEnergy = normEnergy
	return marker, energyDB
}

// makeDec evaluates multidimensional decision boundaries for VAD as specified in ITU-T G.729B.
func makeDec(dSLE, dSE, sd, dSZC float32) int {
	a := tables.VADA
	b := tables.VADB

	// SD vs dSZC
	if sd > a[0]*dSZC+b[0] {
		return Voice
	}
	if sd > a[1]*dSZC+b[1] {
		return Voice
	}

	// dE vs dSZC
	if dSE < a[2]*dSZC+b[2] {
		return Voice
	}
	if dSE < a[3]*dSZC+b[3] {
		return Voice
	}
	if dSE < b[4] {
		return Voice
	}

	// dE vs SD
	if dSE < a[5]*sd+b[5] {
		return Voice
	}
	if sd > b[6] {
		return Voice
	}

	// dEL vs dSZC
	if dSE < a[7]*dSZC+b[7] {
		return Voice
	}
	if dSE < a[8]*dSZC+b[8] {
		return Voice
	}
	if dSE < b[9] {
		return Voice
	}

	// dEL vs SD
	if dSLE < a[10]*sd+b[10] {
		return Voice
	}

	// dEL vs dE
	if dSLE > a[11]*dSE+b[11] {
		return Voice
	}
	if dSLE < a[12]*dSE+b[12] {
		return Voice
	}
	if dSLE < a[13]*dSE+b[13] {
		return Voice
	}

	return Noise
}
