package cli

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/crossref/crossrefd/x/crossref/types"
	"github.com/stretchr/testify/require"
)

func TestHistoryProofFileAndInlineBounds(t *testing.T) {
	file := filepath.Join(t.TempDir(), "proof.bin")
	for _, size := range []int{0, 150000, types.MaxHistoryProofBytes, types.MaxHistoryProofBytes + 1} {
		data := make([]byte, size)
		require.NoError(t, os.WriteFile(file, data, 0600))
		for _, value := range []string{"@" + file, base64.StdEncoding.EncodeToString(data)} {
			got, err := readHistoryProof(value)
			if size == 0 || size > types.MaxHistoryProofBytes {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, data, got)
			}
		}
	}
	_, err := readHistoryProof("@" + t.TempDir())
	require.Error(t, err)
	_, err = readHistoryProof("not-base64")
	require.Error(t, err)
}
