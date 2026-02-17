package main

// #cgo CFLAGS: -x objective-c
// #cgo LDFLAGS: -framework Cocoa
// #import <Cocoa/Cocoa.h>
//
// void setDockIcon(const unsigned char* data, int length) {
//     // Copy data before dispatching (Go GC may move it)
//     NSData* imgData = [NSData dataWithBytes:data length:length];
//     dispatch_async(dispatch_get_main_queue(), ^{
//         NSImage* img = [[NSImage alloc] initWithData:imgData];
//         if (img) {
//             [[NSApplication sharedApplication] setApplicationIconImage:img];
//         }
//     });
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
