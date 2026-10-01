// Command oow is a Windows-native system maintenance CLI.
package main

import (
	"os"

	"github.com/Harshul1484/out-of-windows/internal/cli"
)

func main() {
	os.Exit(cli.Main())
}
