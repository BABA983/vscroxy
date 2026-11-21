//go:build windows

package clientsetup

import (
	"slices"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// startDetached launches a long-lived client. A terminal needs a console of its
// own, so it is created without STARTF_USESTDHANDLES: os/exec always hands the
// child three explicit handles (os.DevNull when none are set), and cmd.exe /k
// reads EOF from such a stdin and exits at once, leaving only its console host
// behind. Without the flag Windows attaches the new console's own handles.
func startDetached(c Argv, bin string) error {
	if c.NewConsole {
		return startInNewConsole(c, bin)
	}
	cmd := c.command(bin)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func startInNewConsole(c Argv, bin string) error {
	app, err := windows.UTF16PtrFromString(bin)
	if err != nil {
		return err
	}
	line, err := windows.UTF16PtrFromString(WindowsCommandLine(bin, c.Args))
	if err != nil {
		return err
	}
	var env *uint16
	if c.Env != nil {
		block, err := environmentBlock(c.Env)
		if err != nil {
			return err
		}
		env = &block[0]
	}
	info := &windows.StartupInfo{
		Flags:      windows.STARTF_USESHOWWINDOW,
		ShowWindow: windows.SW_SHOWNORMAL,
	}
	info.Cb = uint32(unsafe.Sizeof(*info))
	var process windows.ProcessInformation
	if err := windows.CreateProcess(app, line, nil, nil, false,
		windows.CREATE_NEW_CONSOLE|windows.CREATE_UNICODE_ENVIRONMENT, env, nil, info, &process); err != nil {
		return err
	}
	// The app never waits for a terminal; Windows cleans the process up when it
	// exits, so its handles are closed here rather than leaked for the session.
	windows.CloseHandle(process.Thread)
	windows.CloseHandle(process.Process)
	return nil
}

// environmentBlock renders env as the UTF-16 block CreateProcess reads. Names
// are deduplicated case-insensitively with the last value winning, as exec does,
// so the proxy variables here replace the ones the app inherited; a raw block
// would otherwise leave the first value in place.
func environmentBlock(env []string) ([]uint16, error) {
	values := make(map[string]int, len(env))
	entries := make([]string, 0, len(env))
	for _, entry := range env {
		name, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if name == "" {
			// A drive's current directory is stored as "=C:=C:\dir".
			rest, _, ok := strings.Cut(entry[1:], "=")
			if !ok {
				continue
			}
			name = "=" + rest
		}
		key := strings.ToUpper(name)
		if index, ok := values[key]; ok {
			entries[index] = entry
			continue
		}
		values[key] = len(entries)
		entries = append(entries, entry)
	}
	slices.SortFunc(entries, func(a, b string) int {
		return strings.Compare(strings.ToUpper(a), strings.ToUpper(b))
	})
	var block []uint16
	for _, entry := range entries {
		word, err := windows.UTF16FromString(entry)
		if err != nil {
			return nil, err
		}
		block = append(block, word...)
	}
	return append(block, 0), nil
}
