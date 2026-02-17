package main

// #cgo CFLAGS: -x objective-c
// #cgo LDFLAGS: -framework Cocoa
// #import <Cocoa/Cocoa.h>
//
// void setDockIcon(const unsigned char* data, int length) {
//     NSData* imgData = [NSData dataWithBytes:data length:length];
//     NSImage* img = [[NSImage alloc] initWithData:imgData];
//     if (img) {
//         [NSApp setApplicationIconImage:img];
//     }
// }
import "C"

import (
	_ "embed"
	"unsafe"
)

//go:embed gui/icon.png
var dockIconBytes []byte

func setDockIcon() {
	if len(dockIconBytes) == 0 {
		return
	}
	C.setDockIcon((*C.uchar)(unsafe.Pointer(&dockIconBytes[0])), C.int(len(dockIconBytes)))
}
