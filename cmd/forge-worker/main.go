package main

import (
	"context"
	"fmt"
	"os"

	"forge_worker/internal/app"
)

var (
	version   = "dev"
	buildTime = ""
)

func main() {
	if err := app.Run(context.Background(), os.Args[1:], version, buildTime, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(app.NewTimestampWriter(os.Stderr), err)
		os.Exit(1)
	}
}
