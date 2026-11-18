//go:build windows

package main

import (
	"errors"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

type hiddAttributes struct {
	Size      uint32
	VendorID  uint16
	ProductID uint16
	Version   uint16
}

type hidpCaps struct {
	Usage                     uint16
	UsagePage                 uint16
	InputReportByteLength     uint16
	OutputReportByteLength    uint16
	FeatureReportByteLength   uint16
	Reserved                  [17]uint16
	NumberLinkCollectionNodes uint16
	NumberInputButtonCaps     uint16
	NumberInputValueCaps      uint16
	NumberInputDataIndices    uint16
	NumberOutputButtonCaps    uint16
	NumberOutputValueCaps     uint16
	NumberOutputDataIndices   uint16
	NumberFeatureButtonCaps   uint16
	NumberFeatureValueCaps    uint16
	NumberFeatureDataIndices  uint16
}

type hidInfo struct {
	Path      string
	VID, PID  uint16
	UsagePage uint16
	Usage     uint16
	InLen     int
	OutLen    int
	Product   string
}

func listHIDPaths() []string {
	var size uint32
	r, _, _ := pCMGetIfaceListSizeW.Call(uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&hidGUID)), 0, 0)
	if r != 0 || size == 0 {
		return nil
	}
	buf := make([]uint16, size)
	r, _, _ = pCMGetIfaceListW.Call(uintptr(unsafe.Pointer(&hidGUID)), 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(size), 0)
	if r != 0 {
		return nil
	}
	var out []string
	start := 0
	for i, c := range buf {
		if c == 0 {
			if i > start {
				out = append(out, syscall.UTF16ToString(buf[start:i]))
			}
			start = i + 1
		}
	}
	return out
}

func openHandle(path string, access uint32, flags uint32) (syscall.Handle, error) {
	return syscall.CreateFile(u16(path), access, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING, flags, 0)
}

func queryHID(path string) (*hidInfo, error) {
	h, err := openHandle(path, 0, 0)
	if err != nil {
		return nil, err
	}
	defer syscall.CloseHandle(h)
	info := &hidInfo{Path: path}
	var a hiddAttributes
	a.Size = uint32(unsafe.Sizeof(a))
	if r, _, _ := pHidDGetAttributes.Call(uintptr(h), uintptr(unsafe.Pointer(&a))); r == 0 {
		return nil, errors.New("HidD_GetAttributes failed")
	}
	info.VID, info.PID = a.VendorID, a.ProductID
	var pp uintptr
	if r, _, _ := pHidDGetPreparsedData.Call(uintptr(h), uintptr(unsafe.Pointer(&pp))); r != 0 {
		var caps hidpCaps
		st, _, _ := pHidPGetCaps.Call(pp, uintptr(unsafe.Pointer(&caps)))
		if uint32(st) == 0x00110000 {
			info.UsagePage, info.Usage = caps.UsagePage, caps.Usage
			info.InLen, info.OutLen = int(caps.InputReportByteLength), int(caps.OutputReportByteLength)
		}
		pHidDFreePreparsedData.Call(pp)
	}
	pbuf := make([]uint16, 128)
	if r, _, _ := pHidDGetProductString.Call(uintptr(h), uintptr(unsafe.Pointer(&pbuf[0])), uintptr(len(pbuf)*2)); r != 0 {
		info.Product = syscall.UTF16ToString(pbuf)
	}
	return info, nil
}

// findHIDPP returns Logitech vendor-specific collections that carry 20-byte HID++ long reports.
func findHIDPP() []*hidInfo {
	var out []*hidInfo
	for _, p := range listHIDPaths() {
		lp := strings.ToLower(p)
		if !strings.Contains(lp, "vid&02046d") && !strings.Contains(lp, "vid_046d") {
			continue
		}
		info, err := queryHID(p)
		if err != nil || info.VID != 0x046D {
			continue
		}
		if info.UsagePage >= 0xFF00 && info.OutLen == 20 {
			out = append(out, info)
		}
	}
	return out
}

// ---------- overlapped HID device ----------

type hidDevice struct {
	info   *hidInfo
	h      syscall.Handle
	wmu    sync.Mutex
	closed bool
	cmu    sync.Mutex
}

func openHIDDevice(info *hidInfo) (*hidDevice, error) {
	h, err := openHandle(info.Path, syscall.GENERIC_READ|syscall.GENERIC_WRITE, syscall.FILE_FLAG_OVERLAPPED)
	if err != nil {
		return nil, err
	}
	return &hidDevice{info: info, h: h}, nil
}

func newEvent() syscall.Handle {
	h, _, _ := pCreateEventW.Call(0, 1, 0, 0)
	return syscall.Handle(h)
}

// Read blocks until one input report arrives (or the handle is closed).
func (d *hidDevice) Read(buf []byte) (int, error) {
	ev := newEvent()
	defer syscall.CloseHandle(ev)
	var ov syscall.Overlapped
	ov.HEvent = ev
	var n uint32
	err := syscall.ReadFile(d.h, buf, &n, &ov)
	if err != nil && err != syscall.ERROR_IO_PENDING {
		return 0, err
	}
	r, _, e := pGetOverlappedResult.Call(uintptr(d.h), uintptr(unsafe.Pointer(&ov)), uintptr(unsafe.Pointer(&n)), 1)
	if r == 0 {
		return 0, e
	}
	return int(n), nil
}

// Write sends one output report with a timeout in ms.
func (d *hidDevice) Write(buf []byte, timeoutMs uint32) error {
	d.wmu.Lock()
	defer d.wmu.Unlock()
	ev := newEvent()
	defer syscall.CloseHandle(ev)
	var ov syscall.Overlapped
	ov.HEvent = ev
	var n uint32
	err := syscall.WriteFile(d.h, buf, &n, &ov)
	if err != nil && err != syscall.ERROR_IO_PENDING {
		return err
	}
	w, _, _ := pWaitForSingleObject.Call(uintptr(ev), uintptr(timeoutMs))
	if w != 0 { // not WAIT_OBJECT_0
		pCancelIoEx.Call(uintptr(d.h), uintptr(unsafe.Pointer(&ov)))
		pGetOverlappedResult.Call(uintptr(d.h), uintptr(unsafe.Pointer(&ov)), uintptr(unsafe.Pointer(&n)), 1)
		return errors.New("write timeout")
	}
	r, _, e := pGetOverlappedResult.Call(uintptr(d.h), uintptr(unsafe.Pointer(&ov)), uintptr(unsafe.Pointer(&n)), 0)
	if r == 0 {
		return e
	}
	return nil
}

func (d *hidDevice) Close() {
	d.cmu.Lock()
	defer d.cmu.Unlock()
	if d.closed {
		return
	}
	d.closed = true
	pCancelIoEx.Call(uintptr(d.h), 0)
	syscall.CloseHandle(d.h)
}
