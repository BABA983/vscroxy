//go:build windows

package appicon

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// lookup reads the icon the shell shows for an executable. The shell's icon
// is a device-dependent bitmap plus a mask; GetDIBits converts both to 32-bit
// pixels, and the mask supplies transparency for icons older than 32-bit
// alpha. The result is a PNG the UI's bitmap decoder already accepts.
func lookup(name, path string) (Icon, bool) {
	// SHGetFileInfo needs COM on the calling thread, and Go may run this on
	// any thread. S_FALSE means COM was already initialized on this thread;
	// RPC_E_CHANGED_MODE means it is initialized in another apartment, which
	// is enough for the shell query, so only our own initialization is undone.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED); err == nil || errors.Is(err, syscall.Errno(1)) {
		defer windows.CoUninitialize()
	}
	return lookupLocked(path)
}

func lookupLocked(path string) (Icon, bool) {
	if path == "" {
		return Icon{}, false
	}
	target, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return Icon{}, false
	}
	var file shFileInfo
	ok, _, _ := procSHGetFileInfoW.Call(uintptr(unsafe.Pointer(target)), 0,
		uintptr(unsafe.Pointer(&file)), unsafe.Sizeof(file), shgfiIcon|shgfiLargeIcon)
	if ok == 0 || file.icon == 0 {
		return Icon{}, false
	}
	defer procDestroyIcon.Call(uintptr(file.icon))

	var info iconInfo
	if r, _, _ := procGetIconInfo.Call(uintptr(file.icon), uintptr(unsafe.Pointer(&info))); r == 0 {
		return Icon{}, false
	}
	if info.hbmColor != 0 {
		defer procDeleteObject.Call(uintptr(info.hbmColor))
	}
	if info.hbmMask != 0 {
		defer procDeleteObject.Call(uintptr(info.hbmMask))
	}
	if info.hbmColor == 0 {
		return Icon{}, false
	}
	var bm bitmap
	if r, _, _ := procGetObjectW.Call(uintptr(info.hbmColor), unsafe.Sizeof(bm), uintptr(unsafe.Pointer(&bm))); r == 0 {
		return Icon{}, false
	}
	width, height := int(bm.width), int(bm.height)
	if width <= 0 || height <= 0 || width > 512 || height > 512 {
		return Icon{}, false
	}
	dc, _, _ := procCreateCompatibleDC.Call(0)
	if dc == 0 {
		return Icon{}, false
	}
	defer procDeleteDC.Call(dc)

	header := bitmapInfoHeader{
		size: uint32(unsafe.Sizeof(bitmapInfoHeader{})), width: int32(width), height: -int32(height),
		planes: 1, bitCount: 32, compression: biRGB,
	}
	info32 := bitmapInfo{header: header}
	pixels := make([]byte, width*height*4)
	if r, _, _ := procGetDIBits.Call(dc, uintptr(info.hbmColor), 0, uintptr(height),
		uintptr(unsafe.Pointer(&pixels[0])), uintptr(unsafe.Pointer(&info32)), dibRGBColors); r == 0 {
		return Icon{}, false
	}
	hasAlpha := false
	for i := 3; i < len(pixels); i += 4 {
		if pixels[i] != 0 {
			hasAlpha = true
			break
		}
	}
	if !hasAlpha {
		mask := make([]byte, width*height*4)
		if r, _, _ := procGetDIBits.Call(dc, uintptr(info.hbmMask), 0, uintptr(height),
			uintptr(unsafe.Pointer(&mask[0])), uintptr(unsafe.Pointer(&info32)), dibRGBColors); r != 0 {
			for i := 0; i < width*height; i++ {
				if mask[i*4] != 0 {
					pixels[i*4+3] = 0
				} else {
					pixels[i*4+3] = 255
				}
			}
		} else {
			for i := 0; i < width*height; i++ {
				pixels[i*4+3] = 255
			}
		}
	}
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for i := 0; i < width*height; i++ {
		img.Pix[i*4+0] = pixels[i*4+2] // BGRA to RGBA
		img.Pix[i*4+1] = pixels[i*4+1]
		img.Pix[i*4+2] = pixels[i*4+0]
		img.Pix[i*4+3] = pixels[i*4+3]
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return Icon{}, false
	}
	return Icon{Data: buf.Bytes()}, true
}

const (
	shgfiIcon      = 0x00000100
	shgfiLargeIcon = 0x00000000
	biRGB          = 0
	dibRGBColors   = 0
)

var (
	shell32 = windows.NewLazySystemDLL("shell32.dll")
	user32  = windows.NewLazySystemDLL("user32.dll")
	gdi32   = windows.NewLazySystemDLL("gdi32.dll")

	procSHGetFileInfoW     = shell32.NewProc("SHGetFileInfoW")
	procDestroyIcon        = user32.NewProc("DestroyIcon")
	procGetIconInfo        = user32.NewProc("GetIconInfo")
	procGetObjectW         = gdi32.NewProc("GetObjectW")
	procCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	procGetDIBits          = gdi32.NewProc("GetDIBits")
	procDeleteObject       = gdi32.NewProc("DeleteObject")
	procDeleteDC           = gdi32.NewProc("DeleteDC")
)

type shFileInfo struct {
	icon        windows.Handle
	iconIndex   int32
	attributes  uint32
	displayName [260]uint16
	typeName    [80]uint16
}

type iconInfo struct {
	icon     int32
	xHotspot uint32
	yHotspot uint32
	hbmMask  windows.Handle
	hbmColor windows.Handle
}

type bitmap struct {
	typ        int32
	width      int32
	height     int32
	widthBytes int32
	planes     uint16
	bitCount   uint16
	bits       uintptr
}

type bitmapInfoHeader struct {
	size          uint32
	width         int32
	height        int32
	planes        uint16
	bitCount      uint16
	compression   uint32
	sizeImage     uint32
	xPelsPerMeter int32
	yPelsPerMeter int32
	clrUsed       uint32
	clrImportant  uint32
}

type bitmapInfo struct {
	header bitmapInfoHeader
	colors [1]uint32
}
