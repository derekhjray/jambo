//go:build !linux && !windows

package jambo

import (
	"errors"
	"os"
)

func getProcessInfo(pid int) (uid, gid, nspid int, err error) {
	return 0, 0, pid, errors.New("platform not supported")
}

func enterNamespace(pid int, nsType string) error {
	return nil
}

func setCredentials(uid, gid int) error {
	myUID := os.Geteuid()
	myGID := os.Getegid()

	if myUID != uid || myGID != gid {
		return errors.New("credential switching not supported on this platform")
	}
	return nil
}

func getTempPath(pid int) (string, error) {
	return os.TempDir(), nil
}

// hotSpot implements JVM interface for HotSpot JVM
type hotSpot struct{}

func (h *hotSpot) Type() JVMType {
	return HotSpot
}

func (h *hotSpot) Attach(pid, nspid int, args []string, printOutput bool, tmpPath string) (string, error) {
	return "", errors.New("HotSpot attach not supported on this platform")
}

func (h *hotSpot) Detect(nspid int) bool {
	return true // Default to HotSpot
}

// openJ9 implements JVM interface for OpenJ9 JVM
type openJ9 struct{}

func (o *openJ9) Type() JVMType {
	return OpenJ9
}

func (o *openJ9) Attach(pid, nspid int, args []string, printOutput bool, tmpPath string) (string, error) {
	return "", errors.New("OpenJ9 attach not supported on this platform")
}

func (o *openJ9) Detect(nspid int) bool {
	return false // OpenJ9 detection not supported
}

// translateCommand is a stub for non-Linux platforms
func (o *openJ9) translateCommand(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return ""
}

// unescapeString is a stub for non-Linux platforms
func (o *openJ9) unescapeString(s string) string {
	return s
}
