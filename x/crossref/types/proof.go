package types

import (
	"fmt"

	"cosmossdk.io/collections"
)

func CheckpointStorageKey(domainID string, height uint64) ([]byte, error) {
	return collections.EncodeKeyWithPrefix(CheckpointKeyPrefix.Bytes(), collections.StringKey, checkpointStorageID(domainID, height))
}

func checkpointStorageID(domainID string, height uint64) string {
	return fmt.Sprintf("%s/%020d", domainID, height)
}
