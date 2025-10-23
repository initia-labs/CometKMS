package endpoint

import (
	"testing"

	privvalproto "github.com/cometbft/cometbft/proto/tendermint/privval"
	"github.com/stretchr/testify/require"
)

func TestPrivValProtoMessage(t *testing.T) {
	m := &privvalproto.Message{}

	_, ok := m.GetSum().(*privvalproto.Message_SignedProposalResponse)
	require.False(t, ok)

	m.Sum = &privvalproto.Message_SignedProposalResponse{SignedProposalResponse: &privvalproto.SignedProposalResponse{}}
	_, ok = m.GetSum().(*privvalproto.Message_SignedProposalResponse)
	require.True(t, ok)
}
