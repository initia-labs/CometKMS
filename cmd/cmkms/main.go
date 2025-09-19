package main

import (
	"os"

	"github.com/initia-labs/CometKMS/cmd/cmkms/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
