package fsm

import (
	"testing"

	"github.com/cometbft/cometbft/privval"
)

func TestFromFilePVCreatesDeepCopy(t *testing.T) {
	fileState := &privval.FilePVLastSignState{
		Height:    11,
		Round:     2,
		Step:      3,
		Signature: []byte{1, 2, 3, 4},
		SignBytes: []byte{5, 6, 7, 8},
	}

	last := FromFilePV(fileState)
	if last.Height != fileState.Height || last.Round != fileState.Round || last.Step != fileState.Step {
		t.Fatalf("unexpected conversion %+v", last)
	}

	fileState.Signature[0] = 9
	fileState.SignBytes[0] = 10
	if last.Signature[0] == fileState.Signature[0] {
		t.Fatalf("signature slice was not copied")
	}
	if last.SignBytes[0] == fileState.SignBytes[0] {
		t.Fatalf("sign bytes slice was not copied")
	}

	var nilDest *privval.FilePVLastSignState
	last.CopyToFilePV(nilDest) // should not panic

	dest := &privval.FilePVLastSignState{}
	last.CopyToFilePV(dest)
	if dest.Height != last.Height || dest.Round != last.Round || dest.Step != last.Step {
		t.Fatalf("copy mismatch dest=%+v", dest)
	}
	if len(dest.Signature) != len(last.Signature) || len(dest.SignBytes) != len(last.SignBytes) {
		t.Fatalf("expected signature lengths to match")
	}
	dest.Signature[0] = 99
	dest.SignBytes[0] = 100
	if dest.Signature[0] == last.Signature[0] {
		t.Fatalf("signature slice was shared")
	}
	if dest.SignBytes[0] == last.SignBytes[0] {
		t.Fatalf("sign bytes slice was shared")
	}
}

func TestLastSignStateCloneDeepCopy(t *testing.T) {
	original := &LastSignState{
		Height:    20,
		Round:     1,
		Step:      1,
		Signature: []byte{9, 9, 9},
		SignBytes: []byte{8, 8, 8},
	}

	clone := original.Clone()
	if clone == original {
		t.Fatalf("expected distinct clone pointer")
	}
	clone.Signature[0] = 0
	clone.SignBytes[0] = 0
	clone.Height = 1

	if original.Signature[0] == 0 || original.SignBytes[0] == 0 {
		t.Fatalf("clone modifications affected original")
	}
	if original.Height != 20 {
		t.Fatalf("expected original height unchanged, got %d", original.Height)
	}

	var nilState *LastSignState
	if nilState.Clone() != nil {
		t.Fatal("clone of nil should be nil")
	}
}

func TestLastSignStateLessOrdering(t *testing.T) {
	cases := []struct {
		name   string
		a, b   *LastSignState
		expect bool
	}{
		{"both nil", nil, nil, false},
		{"nil less", nil, &LastSignState{Height: 1}, true},
		{"other nil", &LastSignState{Height: 1}, nil, false},
		{"lower height", &LastSignState{Height: 1}, &LastSignState{Height: 2}, true},
		{"higher height", &LastSignState{Height: 3}, &LastSignState{Height: 2}, false},
		{"same height lower round", &LastSignState{Height: 10, Round: 1}, &LastSignState{Height: 10, Round: 2}, true},
		{"same height higher round", &LastSignState{Height: 10, Round: 3}, &LastSignState{Height: 10, Round: 2}, false},
		{"same hr lower step", &LastSignState{Height: 10, Round: 2, Step: 1}, &LastSignState{Height: 10, Round: 2, Step: 2}, true},
		{"same", &LastSignState{Height: 10, Round: 2, Step: 2}, &LastSignState{Height: 10, Round: 2, Step: 2}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if result := tc.a.Less(tc.b); result != tc.expect {
				t.Fatalf("Less returned %v, expected %v", result, tc.expect)
			}
		})
	}
}

func TestLastSignStateEqualHRS(t *testing.T) {
	state := &LastSignState{Height: 5, Round: 1, Step: 2}
	if !state.EqualHRS(&LastSignState{Height: 5, Round: 1, Step: 2}) {
		t.Fatal("expected matching HRS to be equal")
	}
	if state.EqualHRS(&LastSignState{Height: 5, Round: 2, Step: 2}) {
		t.Fatal("expected differing rounds to be unequal")
	}
	if !(*LastSignState)(nil).EqualHRS(nil) {
		t.Fatal("expected nil comparisons to be equal")
	}
	if (&LastSignState{}).EqualHRS(nil) {
		t.Fatal("expected non-nil vs nil to be unequal")
	}
}
