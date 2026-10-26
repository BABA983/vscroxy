package main

import (
	"log"
	"os"

	"github.com/baba983/vscroxy/internal/app"
	"github.com/baba983/vscroxy/internal/system"
)

func main() {
	if len(os.Args) == 4 && os.Args[1] == "--proxy-guard" {
		if err := system.GuardProxy(os.Args[2], os.Args[3], os.Stdin, os.Stdout); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
