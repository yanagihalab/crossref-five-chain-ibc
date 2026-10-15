package main

import (
	"encoding/json"
	"os"
	"testing"
)

func TestEmptyBundleRejected(t *testing.T) {
	if verify(bundle{}) == nil {
		t.Fatal("empty bundle accepted")
	}
}

func TestCapturedProof(t *testing.T) {
	file := os.Getenv("CROSSREF_LOCAL_PROOF_FIXTURE")
	if file == "" {
		t.Skip("set CROSSREF_LOCAL_PROOF_FIXTURE to a captured checkpoint JSON")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	load := func() bundle {
		var b bundle
		if err := json.Unmarshal(data, &b); err != nil {
			t.Fatal(err)
		}
		return b
	}
	if err := verify(load()); err != nil {
		t.Fatalf("captured proof: %v", err)
	}
	cases := map[string]func(*bundle){
		"wrong chain":         func(b *bundle) { b.ChainID = "wrong-chain-1" },
		"wrong height":        func(b *bundle) { b.Proof.HeightRoot++ },
		"wrong domain":        func(b *bundle) { b.Proof.DomainID = "wrong-domain" },
		"missing public key":  func(b *bundle) { b.PublicKey = nil },
		"wrong signature key": func(b *bundle) { b.PublicKey[0] ^= 1 },
		"missing proof":       func(b *bundle) { b.Proof.Bytes = nil },
		"corrupted proof":     func(b *bundle) { b.Proof.Bytes[len(b.Proof.Bytes)/2] ^= 1 },
		"wrong store key":     func(b *bundle) { b.Proof.StoreKey[0] ^= 1 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			b := load()
			mutate(&b)
			if verify(b) == nil {
				t.Fatal("invalid bundle accepted")
			}
		})
	}
}
