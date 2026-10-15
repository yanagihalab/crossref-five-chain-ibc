package main

import (
	"encoding/json"
	"io"
	"os"
)

func main() {
	decoder := json.NewDecoder(io.LimitReader(os.Stdin, 64<<20))
	var messages []json.RawMessage
	for {
		var message json.RawMessage
		if err := decoder.Decode(&message); err == io.EOF {
			break
		} else if err != nil {
			os.Exit(1)
		}
		messages = append(messages, message)
	}
	if len(messages) == 0 || json.NewEncoder(os.Stdout).Encode(messages) != nil {
		os.Exit(1)
	}
}
