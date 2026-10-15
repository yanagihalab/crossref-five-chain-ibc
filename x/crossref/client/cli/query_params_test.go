package cli

import "testing"

func TestParamsCommandRegistered(t *testing.T) {
	cmd, rest, err := GetQueryCmd().Find([]string{"params"})
	if err != nil || cmd.Name() != "params" || len(rest) != 0 {
		t.Fatalf("params not registered: %v %v", rest, err)
	}
	if cmd.Flags().Lookup("node") == nil || cmd.Flags().Lookup("output") == nil {
		t.Fatal("query flags missing")
	}
	if err := cmd.Args(cmd, []string{"unexpected"}); err == nil {
		t.Fatal("params must not accept positional arguments")
	}
}
