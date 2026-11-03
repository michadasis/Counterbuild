//go:build !windows

// On Linux/macOS Counterbuild runs as a preview tool: it polls the Live Client
// API once and writes the overlay to a PNG. The real overlay is Windows-only,
// because League itself only runs there.
package main

import (
	"flag"
	"fmt"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"time"

	"counterbuild/app"
)

func main() {
	out := flag.String("out", "overlay.png", "where to write the preview")
	wait := flag.Duration("wait", 4*time.Second, "how long to wait for data and icons")
	dir := flag.String("data", "", "data folder (default: user cache dir)")
	flag.Parse()

	if *dir == "" {
		c, err := os.UserCacheDir()
		if err != nil {
			c = "."
		}
		*dir = filepath.Join(c, "Counterbuild")
	}
	cfg, err := app.LoadConfig(filepath.Join(*dir, "config.json"))
	if err != nil {
		log.Printf("config.json: %v (using defaults)", err)
	}
	a := app.New(cfg, *dir, []string{"/usr/share/fonts/truetype/dejavu", "/usr/share/fonts/TTF"}, log.New(os.Stderr, "", log.LstdFlags))
	a.Data.Refresh()
	deadline := time.Now().Add(*wait)
	var f app.Frame
	for {
		f = a.Step()
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !f.Show || f.Img == nil {
		fmt.Fprintln(os.Stderr, "no game detected (is a match or tools/mockgame running?)")
		os.Exit(1)
	}
	file, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()
	if err := png.Encode(file, f.Img); err != nil {
		log.Fatal(err)
	}
	fmt.Println("wrote", *out)
}
