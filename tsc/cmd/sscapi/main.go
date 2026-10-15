package main

/*
#include <stdlib.h>
*/
import "C"

import (
	"context"
	"os"
	"unsafe"

	"github.com/microsoft/TypeScript/tsc/internal/api"
	"github.com/microsoft/TypeScript/tsc/internal/bundled"
	"github.com/microsoft/TypeScript/tsc/internal/json"
	"github.com/microsoft/TypeScript/tsc/internal/lsp/lsproto"
	"github.com/microsoft/TypeScript/tsc/internal/project"
	"github.com/microsoft/TypeScript/tsc/internal/tspath"
	"github.com/microsoft/TypeScript/tsc/internal/vfs/osvfs"
)

var session *api.Session

func main() {}

//export sscApiOpen
func sscApiOpen(cwd *C.char, cwdLength C.int) {
	directory := C.GoStringN(cwd, cwdLength)
	workingDirectory, _ := os.Getwd()
	session = api.NewStandaloneSession(&project.SessionInit{
		BackgroundCtx: context.Background(),
		FS:            bundled.WrapFS(osvfs.FS()),
		Options: &project.SessionOptions{
			CurrentDirectory:   tspath.ToRootedDirectoryPath(directory, tspath.ToRootedDirectoryPath(workingDirectory, "")),
			DefaultLibraryPath: bundled.LibPath(),
			PositionEncoding:   lsproto.PositionEncodingKindUTF8,
		},
	}, &api.SessionOptions{})
}

//export sscApiRequest
func sscApiRequest(method *C.char, methodLength C.int, params *C.char, paramsLength C.int, resultLength *C.int, isError *C.int) unsafe.Pointer {
	result, err := session.HandleRequest(context.Background(), C.GoStringN(method, methodLength), json.Value(C.GoBytes(unsafe.Pointer(params), paramsLength)))
	var payload []byte
	if err != nil {
		*isError = 1
		payload = []byte(err.Error())
	} else if raw, ok := result.(api.RawBinary); ok {
		payload = raw
	} else {
		payload, err = json.Marshal(result)
		if err != nil {
			*isError = 1
			payload = []byte(err.Error())
		}
	}
	*resultLength = C.int(len(payload))
	return C.CBytes(payload)
}
