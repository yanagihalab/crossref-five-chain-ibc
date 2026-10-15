package securitydeps_test

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp/armor"
	sdkcrypto "github.com/cosmos/cosmos-sdk/crypto"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
)

func TestMaintainedArmorCompatibility(t *testing.T) {
	payload := []byte("public compatibility fixture, not a private key")
	encoded := sdkcrypto.EncodeArmor("TENDERMINT PUBLIC KEY", map[string]string{"version": "0.0.1", "type": "secp256k1"}, payload)
	block, err := armor.Decode(strings.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(block.Body)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("armor roundtrip failed: %v", err)
	}
	typeName, headers, got, err := sdkcrypto.DecodeArmor(encoded)
	if err != nil || typeName != "TENDERMINT PUBLIC KEY" || headers["version"] != "0.0.1" || !bytes.Equal(got, payload) {
		t.Fatalf("SDK compatibility failed: %v", err)
	}
}

func TestFreshKeyExportImportAndWrongPassword(t *testing.T) {
	key := secp256k1.GenPrivKey()
	encoded := sdkcrypto.EncryptArmorPrivKey(key, "synthetic-test-password", "secp256k1")
	decoded, algorithm, err := sdkcrypto.UnarmorDecryptPrivKey(encoded, "synthetic-test-password")
	if err != nil || algorithm != "secp256k1" || !key.Equals(decoded) {
		t.Fatalf("fresh key roundtrip failed: %v", err)
	}
	if _, _, err := sdkcrypto.UnarmorDecryptPrivKey(encoded, "wrong"); err == nil {
		t.Fatal("wrong password accepted")
	}
	if _, _, _, err := sdkcrypto.DecodeArmor("not valid armor"); err == nil {
		t.Fatal("malformed armor accepted")
	}
}
