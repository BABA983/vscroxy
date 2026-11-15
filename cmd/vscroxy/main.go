package main

import (
	"fmt"
	"log"
	"os"

	"github.com/baba983/vscroxy/internal/app"
	"github.com/baba983/vscroxy/internal/system"
	"github.com/baba983/vscroxy/internal/version"
)

func main() {
	if len(os.Args) == 2 && (os.Args[1] == "--version" || os.Args[1] == "-version") {
		fmt.Println("vscroxy " + version.String())
		return
	}
	if len(os.Args) == 5 && os.Args[1] == "--proxy-guard" {
		if err := system.GuardProxy(os.Args[2], os.Args[3], os.Args[4], os.Stdin, os.Stdout); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
