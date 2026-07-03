//go:build darwin && cgo

package secrets

/*
#cgo CFLAGS: -Wno-deprecated-declarations
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>
#include <stdlib.h>
*/
import "C"

import (
	"context"
	"fmt"
	"unsafe"
)

const (
	keychainStatusSuccess       = 0
	keychainStatusDuplicateItem = -25299
)

func defaultStoreType() string {
	return "keychain"
}

func setKeychainValue(ctx context.Context, service string, name string, value string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	serviceData, serviceLen := cKeychainBuffer(service)
	defer freeKeychainBuffer(serviceData)
	nameData, nameLen := cKeychainBuffer(name)
	defer freeKeychainBuffer(nameData)
	valueData, valueLen := cKeychainBuffer(value)
	defer freeKeychainBuffer(valueData)

	status := C.SecKeychainAddGenericPassword(
		C.SecKeychainRef(0),
		serviceLen,
		(*C.char)(serviceData),
		nameLen,
		(*C.char)(nameData),
		valueLen,
		valueData,
		nil,
	)
	if int(status) == keychainStatusSuccess {
		return nil
	}
	if int(status) != keychainStatusDuplicateItem {
		return keychainStatusError("write", service, name, status)
	}

	var item C.SecKeychainItemRef
	status = C.SecKeychainFindGenericPassword(
		C.CFTypeRef(0),
		serviceLen,
		(*C.char)(serviceData),
		nameLen,
		(*C.char)(nameData),
		nil,
		nil,
		&item,
	)
	if int(status) != keychainStatusSuccess {
		return keychainStatusError("find existing", service, name, status)
	}
	defer C.CFRelease(C.CFTypeRef(item))

	status = C.SecKeychainItemModifyAttributesAndData(item, nil, valueLen, valueData)
	if int(status) != keychainStatusSuccess {
		return keychainStatusError("update", service, name, status)
	}
	return nil
}

func cKeychainBuffer(value string) (unsafe.Pointer, C.UInt32) {
	if value == "" {
		return nil, 0
	}
	content := []byte(value)
	return C.CBytes(content), C.UInt32(len(content))
}

func freeKeychainBuffer(data unsafe.Pointer) {
	if data != nil {
		C.free(data)
	}
}

func keychainStatusError(operation string, service string, name string, status C.OSStatus) error {
	return fmt.Errorf("%s private value %q in keychain service %q: security framework status %d", operation, name, service, int(status))
}
