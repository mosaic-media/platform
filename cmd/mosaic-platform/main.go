// Command mosaic-platform is the Platform process entry point. Its
// responsibility is dependency bootstrap only (MEG-015 §02); it must stay
// free of business logic.
package main

import (
	"fmt"
	"os"

	"github.com/mosaic-media/mosaic-platform/internal/platform/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "mosaic-platform: config load failed:", err)
		os.Exit(1)
	}

	fmt.Printf("mosaic-platform: booting (environment=%s)\n", cfg.Environment)
	fmt.Println("mosaic-platform: exiting cleanly")
}
