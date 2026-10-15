package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/crossref/crossrefd/x/crossref/types"
	"github.com/stretchr/testify/require"
)

func responseFixture(t *testing.T) (options, ed25519.PrivateKey, types.QueryHistoryResponse) {
	c := types.HistoryConfig{Enabled: true, SessionId: "signer-test", LocalDomainId: "a", IntervalBlocks: 2, PacketTimeoutSeconds: 60}
	var private ed25519.PrivateKey
	r := types.HistoryRound{Number: 1, Status: types.HistorySigning}
	for _, id := range []string{"a", "b", "c"} {
		h := sha256.Sum256([]byte(id))
		key := ed25519.NewKeyFromSeed(h[:])
		if id == "a" {
			private = key
		}
		c.Members = append(c.Members, types.HistoryMember{DomainId: id, ChainId: id, PublicKey: key.Public().(ed25519.PublicKey)})
		r.Parts = append(r.Parts, types.HistoryPart{DomainId: id, ChainId: id, Height: 9, BlockHash: h[:], AppHash: h[:]})
	}
	b, err := types.HistorySignBytes(c, r, types.HistoryRound{}, "a")
	require.NoError(t, err)
	return options{chain: "a", session: hex.EncodeToString(types.HistorySessionHash(c))}, private, types.QueryHistoryResponse{State: types.HistoryState{Config: c, Current: r}, SignBytes: b}
}
func TestSignerPinsContextAndPersistsNoEquivocation(t *testing.T) {
	o, key, response := responseFixture(t)
	j := journal{}
	id, record, err := signResponse(o, key, response, j)
	require.NoError(t, err)
	require.NotEmpty(t, record.Signature)
	j[id] = record
	file := filepath.Join(t.TempDir(), "journal.json")
	require.NoError(t, persist(file, j))
	bz, err := os.ReadFile(file)
	require.NoError(t, err)
	var restored journal
	require.NoError(t, json.Unmarshal(bz, &restored))
	_, again, err := signResponse(o, key, response, restored)
	require.NoError(t, err)
	require.Equal(t, record, again)
	response.State.Current.Parts[1].Height++
	response.SignBytes, err = types.HistorySignBytes(response.State.Config, response.State.Current, types.HistoryRound{}, "a")
	require.NoError(t, err)
	_, _, err = signResponse(o, key, response, restored)
	require.ErrorContains(t, err, "equivocation")
}
func TestSignerRejectsRPCBytesAndKeySessionSubstitution(t *testing.T) {
	o, key, response := responseFixture(t)
	original := append([]byte(nil), response.SignBytes...)
	response.SignBytes = []byte("arbitrary bytes")
	_, _, err := signResponse(o, key, response, journal{})
	require.ErrorContains(t, err, "locally derived")
	response.SignBytes = original
	o.chain = "other"
	_, _, err = signResponse(o, key, response, journal{})
	require.ErrorContains(t, err, "chain")
	o.chain = "a"
	o.session = "other"
	_, _, err = signResponse(o, key, response, journal{})
	require.ErrorContains(t, err, "session")
}
func TestKeyFilePermissionsAndType(t *testing.T) {
	_, key, _ := responseFixture(t)
	bz, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	file := filepath.Join(t.TempDir(), "key.pem")
	require.NoError(t, os.WriteFile(file, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: bz}), 0600))
	loaded, err := loadKey(file)
	require.NoError(t, err)
	require.Equal(t, key, loaded)
	require.NoError(t, os.Chmod(file, 0644))
	_, err = loadKey(file)
	require.Error(t, err)
	require.NoError(t, os.Chmod(file, 0600))
	link := file + ".link"
	require.NoError(t, os.Symlink(file, link))
	_, err = loadKey(link)
	require.Error(t, err)
}
