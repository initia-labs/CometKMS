package fsm

import (
	cmtbytes "github.com/cometbft/cometbft/libs/bytes"
	"github.com/cometbft/cometbft/privval"
)

// LastSignState captures the mutable state from a CometBFT FilePV instance.
type LastSignState struct {
	Height    int64             `json:"height"`
	Round     int32             `json:"round"`
	Step      int8              `json:"step"`
	Signature []byte            `json:"signature,omitempty"`
	SignBytes cmtbytes.HexBytes `json:"signbytes,omitempty"`
}

// FromFilePV create LastSignState from *FilePVLastSignState
func FromFilePV(pv *privval.FilePVLastSignState) *LastSignState {
	return &LastSignState{
		Height:    pv.Height,
		Round:     pv.Round,
		Step:      pv.Step,
		Signature: append([]byte{}, pv.Signature...),
		SignBytes: append([]byte{}, pv.SignBytes...),
	}
}

// CopyToFilePV copy the contents to *FilePVLastSignState
func (s *LastSignState) CopyToFilePV(pv *privval.FilePVLastSignState) {
	if pv == nil {
		return
	}

	pv.Height = s.Height
	pv.Round = s.Round
	pv.Step = s.Step
	pv.Signature = append([]byte{}, s.Signature...)
	pv.SignBytes = append([]byte{}, s.SignBytes...)
}

// Clone produces a deep copy of the sign state.
func (s *LastSignState) Clone() *LastSignState {
	if s == nil {
		return nil
	}
	copy := *s
	if len(s.Signature) > 0 {
		copy.Signature = append([]byte(nil), s.Signature...)
	}
	if len(s.SignBytes) > 0 {
		copy.SignBytes = append(cmtbytes.HexBytes(nil), s.SignBytes...)
	}
	return &copy
}

// Less reports whether s represents a lower H/R/S than other.
func (s *LastSignState) Less(other *LastSignState) bool {
	if s == nil && other == nil {
		return false
	}
	if s == nil {
		return true
	}
	if other == nil {
		return false
	}
	if s.Height != other.Height {
		return s.Height < other.Height
	}
	if s.Round != other.Round {
		return s.Round < other.Round
	}
	return s.Step < other.Step
}

// EqualHRS check s and other have same HRS
func (s *LastSignState) EqualHRS(other *LastSignState) bool {
	if s == nil && other == nil {
		return true
	} else if s == nil {
		return false
	} else if other == nil {
		return false
	}

	return s.Height == other.Height && s.Round == other.Round && s.Step == other.Step
}
