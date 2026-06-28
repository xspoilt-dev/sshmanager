package main

import (
	"fmt"
	"os"

	"sshmanager/tui"
)

func main() {
	if err := tui.Run(); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
}
