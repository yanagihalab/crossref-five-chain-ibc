package main

import "testing"

func TestPinnedHistoryAgainstQuorumRewrite(t *testing.T) {
	c, tr, keys := synthetic()
	if err := verify(c, tr, true); err != nil {
		t.Fatal(err)
	}
	changed := clone(c)
	changed.Data[0].Txs[0][0] ^= 1
	if verify(changed, tr, false) == nil {
		t.Fatal("unrehashed Tx accepted")
	}
	repair(&changed)
	sign(&changed, tr, keys, 2)
	if verify(changed, tr, false) == nil {
		t.Fatal("2/4 quorum accepted")
	}
	sign(&changed, tr, keys, 3)
	if err := verify(changed, tr, false); err != nil {
		t.Fatal(err)
	}
	if verify(changed, tr, true) == nil {
		t.Fatal("rewritten history matches trusted anchor")
	}
	if verify(c, tr, true) != nil {
		t.Fatal("mutation changed original fixture")
	}
}
func TestEvidenceGapsFailClosed(t *testing.T) {
	c, tr, _ := synthetic()
	for _, name := range []string{"missing_anchor", "missing_commit", "missing_header", "omitted_target_block", "bad_signature", "different_chain", "transition", "later_than_anchor"} {
		t.Run(name, func(t *testing.T) {
			v := clone(c)
			trustCopy := tr
			switch name {
			case "missing_anchor":
				trustCopy.Anchor = anchor{}
			case "missing_commit":
				v.Commit = nil
			case "missing_header":
				v.Headers = v.Headers[1:]
			case "omitted_target_block":
				v.Headers = v.Headers[1:]
				v.Data = v.Data[1:]
			case "bad_signature":
				v.Commit.Signatures[0].Signature[0] ^= 1
			case "different_chain":
				trustCopy.ChainID = "other"
			case "transition":
				v.Headers[1].NextValidatorsHash = nil
			case "later_than_anchor":
				trustCopy.Anchor = anchor{Height: 1, Hash: v.Headers[0].Hash()}
			}
			if verify(v, trustCopy, true) == nil {
				t.Fatal("accepted incomplete or inconsistent evidence")
			}
		})
	}
}
