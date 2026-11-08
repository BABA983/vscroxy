package app

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/imports"
	"github.com/baba983/vscroxy/internal/postman"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/egoist/mygo"
)

// importCaptureSession reads a Charles JSON/XML session or a Proxyman log
// archive and appends its exchanges to the capture table, the way Import HAR
// does. Native Charles .chls is refused with a conversion hint.
func (a *app) importCaptureSession() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Title: "Import capture session",
			Filters: []mygo.FileFilter{{
				Name:       "Charles or Proxyman capture",
				Extensions: []string{"chlsj", "chlsx", "chls", "proxymanlogv2", "proxymanlog"},
			}},
		})
		if err != nil || len(paths) == 0 {
			return
		}
		a.importCaptureFile(paths[0])
	}()
}

// importCaptureFolder reads the older Proxyman log, a directory rather than a
// single archive.
func (a *app) importCaptureFolder() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Title:       "Import Proxyman log folder",
			ButtonLabel: "Import",
			Directory:   true,
		})
		if err != nil || len(paths) == 0 {
			return
		}
		a.importCaptureDir(paths[0])
	}()
}

// importDropped routes a file or folder dropped on the window. Only the first
// path is read; the import dialogs also take one file at a time.
func (a *app) importDropped(paths []string) {
	if len(paths) == 0 {
		return
	}
	go func() {
		path := paths[0]
		info, err := os.Lstat(path)
		if err != nil {
			a.fail("Import", err)
			return
		}
		if info.Mode()&os.ModeSymlink != 0 {
			a.warn("Import: choose a real file or folder, not a symbolic link")
			return
		}
		switch droppedImportKind(path, info.IsDir()) {
		case droppedHAR:
			a.importHARPath(path)
		case droppedCapture:
			a.importCaptureFile(path)
		case droppedCaptureDir:
			a.importCaptureDir(path)
		case droppedCollectionDir:
			a.importCollectionDir(path)
		default:
			a.warn("Import: unsupported file type. Drop a HAR (.har), Charles (.chlsj, .chlsx), or Proxyman (.proxymanlogv2, .proxymanlog) file, or a Postman v3 or Bruno collection folder")
		}
	}()
}

type droppedKind int

const (
	droppedUnknown droppedKind = iota
	droppedHAR
	droppedCapture
	droppedCaptureDir
	droppedCollectionDir
)

// droppedImportKind decides which importer owns a dropped path. HAR files keep
// using the HAR importer; only the formats the capture importer knows go
// there, and a .proxymanlog folder is a capture folder. A Bruno collection
// wins over a v3 collection when both markers are present.
func droppedImportKind(path string, isDir bool) droppedKind {
	if isDir {
		if strings.EqualFold(filepath.Ext(path), ".proxymanlog") {
			return droppedCaptureDir
		}
		if collectionDirKindOf(path) != collectionDirUnknown {
			return droppedCollectionDir
		}
		return droppedUnknown
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".har", ".json":
		return droppedHAR
	case ".chlsj", ".chlsx", ".chls", ".proxymanlogv2", ".proxymanlog":
		return droppedCapture
	default:
		return droppedUnknown
	}
}

type collectionDirKind int

const (
	collectionDirUnknown collectionDirKind = iota
	collectionDirBruno
	collectionDirV3
)

// collectionDirKindOf names a collection directory. Bruno is checked first
// because a Bruno folder can contain a .resources directory, which would
// otherwise look like a Postman v3 collection.
func collectionDirKindOf(path string) collectionDirKind {
	if isBrunoCollectionDir(path) {
		return collectionDirBruno
	}
	if postman.LooksLikeCollectionDir(path) {
		return collectionDirV3
	}
	return collectionDirUnknown
}

// isBrunoCollectionDir reports whether dir holds a Bruno collection config.
func isBrunoCollectionDir(dir string) bool {
	info, err := os.Lstat(filepath.Join(dir, "bruno.json"))
	return err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0
}

// importCaptureFile reads and parses one path off the UI thread, then appends
// it on the UI thread.
func (a *app) importCaptureFile(path string) {
	result, err := readCapturePath(path)
	if err != nil {
		a.fail("Import capture", err)
		return
	}
	a.win.Update(func() { a.addCaptureImport(result, path) })
}

// reserveFlowIDs returns the first ID for a batch of imported flows. The live
// capture engine allocates from the same counter, so reserving the batch keeps
// later traffic from reusing an imported ID. Without a server (tests and early
// startup) the table's last ID is the only counter there is.
func (a *app) reserveFlowIDs(n int) int {
	if n <= 0 {
		return 0
	}
	if a.srv != nil {
		return a.srv.ReserveIDs(n)
	}
	next := 0
	if len(a.flows) > 0 {
		next = a.flows[len(a.flows)-1].id
	}
	return next + 1
}

func (a *app) importCaptureDir(path string) {
	result, err := imports.ReadProxymanDir(path)
	if err != nil {
		a.fail("Import capture", err)
		return
	}
	a.win.Update(func() { a.addCaptureImport(result, path) })
}

// importCaptureData parses bytes already read and appends them. Tests and
// callers that hold the bytes use it; the dialog path reads the file first.
func (a *app) importCaptureData(data []byte, name string) {
	result, err := imports.ReadCapture(data, name)
	if err != nil {
		a.fail("Import capture", err)
		return
	}
	a.addCaptureImport(result, name)
}

func readCapturePath(path string) (imports.Capture, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return imports.Capture{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return imports.Capture{}, fmt.Errorf("%s: choose a real file or folder, not a symbolic link", filepath.Base(path))
	}
	if info.IsDir() {
		if strings.EqualFold(filepath.Ext(path), ".proxymanlog") {
			return imports.ReadProxymanDir(path)
		}
		return imports.Capture{}, fmt.Errorf("%s: choose a capture file, or a .proxymanlog folder", filepath.Base(path))
	}
	if !info.Mode().IsRegular() {
		return imports.Capture{}, fmt.Errorf("%s: not a regular file", filepath.Base(path))
	}
	if info.Size() > imports.MaxCaptureFileSize {
		return imports.Capture{}, fmt.Errorf("%s: the file is larger than %d MiB", filepath.Base(path), imports.MaxCaptureFileSize>>20)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return imports.Capture{}, err
	}
	return imports.ReadCapture(data, filepath.Base(path))
}

// addCaptureImport appends the parsed exchanges and their stream messages to
// the table. It is a UI-thread action, like importHAR.
func (a *app) addCaptureImport(result imports.Capture, source string) {
	if len(result.Flows) == 0 {
		a.warn("Import capture: nothing to import from " + filepath.Base(source) + "; " + strings.Join(result.Warnings, "; "))
		return
	}
	start := len(a.flows)
	first := a.reserveFlowIDs(len(result.Flows))
	if a.frames == nil {
		a.frames = make(map[int][]frame)
	}
	for i, captured := range result.Flows {
		f, frames := captureFlow(captured)
		f.id = first + i
		a.flows = append(a.flows, f)
		if len(frames) > 0 {
			a.frames[f.id] = frames
		}
	}
	a.pickedID = a.flows[start].id
	a.recount()
	a.applyFilter()
	a.reportImport(fmt.Sprintf("Imported “%s” · %d %s", filepath.Base(source), len(result.Flows), plural(len(result.Flows), "exchange", "exchanges")), result.Warnings)
}

// captureFlow turns a parsed exchange into the table's row. Bodies stay byte
// strings, so binary payloads reach the inspector and session export intact.
func captureFlow(captured imports.CaptureFlow) (flow, []frame) {
	f := flow{
		method:     strings.ToUpper(cmp.Or(captured.Method, "GET")),
		scheme:     captured.Scheme,
		host:       captured.Host,
		path:       captured.Path,
		url:        captured.URL,
		status:     captured.Status,
		bytes:      captured.Bytes,
		millis:     captured.Millis,
		wait:       captured.Wait,
		download:   captured.Download,
		reqHeaders: request.HeaderBlock(captured.ReqHeader),
		resHeaders: request.HeaderBlock(captured.ResHeader),
		reqHeader:  captured.ReqHeader.Clone(),
		respHeader: captured.ResHeader.Clone(),
		reqBody:    string(captured.ReqBody),
		resBody:    string(captured.ResBody),
		reqSize:    captured.ReqSize,
		resSize:    captured.ResSize,
		resProto:   cmp.Or(captured.ResProto, captured.ReqProto),
		at:         captured.At,
		kind:       captured.Kind,
		process:    captured.Process,
		note:       captured.Note,
		conn: conn{
			proto:      cmp.Or(captured.ReqProto, captured.ResProto),
			client:     captured.Client,
			remote:     captured.Remote,
			tls:        captured.TLS,
			cipher:     captured.Cipher,
			certCN:     captured.CertCN,
			issuer:     captured.Issuer,
			certUntil:  captured.CertUntil,
			error:      captured.Error,
			errorStage: "",
		},
	}
	if captured.Error != "" {
		f.conn.errorStage = "HTTP"
		if f.kind == "connect" {
			f.conn.errorStage = "CONNECT"
		}
	}
	if f.reqSize < len(f.reqBody) {
		f.reqSize = len(f.reqBody)
	}
	if f.resSize < len(f.resBody) {
		f.resSize = len(f.resBody)
	}
	if f.bytes <= 0 {
		f.bytes = len(f.reqBody) + len(f.resBody)
	}
	if captured.Timing != nil {
		f.timing = importedTiming(captured.Timing)
	}
	frames := make([]frame, 0, len(captured.Frames))
	for i, message := range captured.Frames {
		at := message.At
		if at.IsZero() {
			at = f.at
		}
		frames = append(frames, frame{
			seq:       i + 1,
			direction: message.Direction,
			kind:      message.Kind,
			data:      string(message.Data),
			size:      len(message.Data),
			kept:      len(message.Data),
			at:        at,
		})
	}
	return f, frames
}

func importedTiming(timing *imports.CaptureTiming) *capture.Timing {
	phases := make([]capture.TimingPhase, 0, len(timing.Phases))
	for _, phase := range timing.Phases {
		phases = append(phases, capture.TimingPhase{
			Name:       phase.Name,
			StartNS:    int64(phase.Start),
			DurationNS: int64(phase.Duration),
		})
	}
	return &capture.Timing{TotalNS: int64(timing.Total), Phases: phases}
}
