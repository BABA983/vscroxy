package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/baba983/vscroxy/internal/app"
	"github.com/baba983/vscroxy/internal/applog"
	"github.com/baba983/vscroxy/internal/datadir"
	"github.com/baba983/vscroxy/internal/system"
	"github.com/baba983/vscroxy/internal/version"
)

func main() {
	guard, showVersion, logLevel, err := parseLaunchArgs(os.Args[1:])
	if err != nil {
		log.Fatal(err)
	}
	if showVersion {
		fmt.Println("vscroxy " + version.String())
		return
	}
	if logLevel != "" {
		// The flag wins over settings.json for this run and is never saved.
		if err := applog.SetFlagLevel(logLevel); err != nil {
			log.Fatal(err)
		}
	}
	if guard != nil {
		if len(guard) != 3 {
			log.Fatal("proxy guard needs a journal path, a target and a bypass list")
		}
		if err := system.GuardProxy(guard[0], guard[1], guard[2], os.Stdin, os.Stdout); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

// parseLaunchArgs reads the flags that the proxy guard and the desktop share.
// The guard's positional arguments start at --proxy-guard and belong to it.
func parseLaunchArgs(args []string) (guard []string, showVersion bool, logLevel string, err error) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--user-data-dir", "-user-data-dir":
			if i+1 == len(args) {
				return nil, false, "", errors.New("--user-data-dir needs a path")
			}
			i++
			datadir.SetFlag(args[i])
		case "--log-level", "-log-level":
			if i+1 == len(args) {
				return nil, false, "", errors.New("--log-level needs a level")
			}
			i++
			if args[i] == "" {
				return nil, false, "", errors.New("--log-level needs a level")
			}
			logLevel = args[i]
		case "--version", "-version":
			showVersion = true
		case "--proxy-guard":
			return args[i+1:], showVersion, logLevel, nil
		default:
			for _, prefix := range []string{"--log-level=", "-log-level="} {
				if value, ok := strings.CutPrefix(args[i], prefix); ok {
					if value == "" {
						return nil, false, "", errors.New("--log-level needs a level")
					}
					logLevel = value
					break
				}
			}
			if value, ok := datadir.FlagValue(args[i]); ok {
				if value == "" {
					return nil, false, "", errors.New("--user-data-dir needs a path")
				}
				datadir.SetFlag(value)
			}
		}
	}
	return nil, showVersion, logLevel, nil
}
